package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// LLMClient is a client for the DeepSeek (OpenAI-compatible) API.
type LLMClient struct {
	baseURL    string
	apiKey     string
	model      string
	maxTokens  int
	temperature float64
	httpClient *http.Client
}

// LLMMessage represents a single message in the conversation.
type LLMMessage struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"` // thinking mode: must be preserved for tool-call turns
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string `json:"tool_call_id,omitempty"`
}

// ToolCall represents a function tool call returned by the model.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction holds the function name and arguments for a tool call.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// LLMRequest is the request body for the chat completions API.
type LLMRequest struct {
	Model           string          `json:"model"`
	Messages        []LLMMessage    `json:"messages"`
	Stream          bool            `json:"stream"`
	Temperature     float64         `json:"temperature,omitempty"`
	MaxTokens       int             `json:"max_tokens"`
	Thinking        *Thinking       `json:"thinking,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"` // "high" | "max" (thinking mode only)
	Tools           []ToolDef       `json:"tools,omitempty"`
	ResponseFormat  *ResponseFormat `json:"response_format,omitempty"`
}

// ResponseFormat controls the output format of the model.
type ResponseFormat struct {
	Type string `json:"type"` // "json_object"
}

// Thinking controls the thinking mode for V4 models.
type Thinking struct {
	Type string `json:"type"` // "enabled" | "disabled"
}

// LLMResponse is the response from the chat completions API.
type LLMResponse struct {
	Choices []struct {
		Message         LLMMessage `json:"message"`
		FinishReason    string     `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens          int `json:"prompt_tokens"`
		CompletionTokens      int `json:"completion_tokens"`
		TotalTokens           int `json:"total_tokens"`
		CompletionTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
}

// ToolDef defines a tool that the model can call.
type ToolDef struct {
	Type     string         `json:"type"` // always "function"
	Function ToolDefFunction `json:"function"`
}

// ToolDefFunction holds the function definition for a tool.
type ToolDefFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// StreamDelta is a single chunk from the streaming API.
type StreamDelta struct {
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content"`
}

// StreamChunk represents a single SSE chunk from the streaming API.
type StreamChunk struct {
	Choices []struct {
		Delta        StreamDelta `json:"delta"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens          int `json:"prompt_tokens"`
		CompletionTokens      int `json:"completion_tokens"`
		TotalTokens           int `json:"total_tokens"`
		CompletionTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage,omitempty"`
}

// NewLLMClient creates a new LLM client.
func NewLLMClient(baseURL, apiKey, model string, maxTokens int, temperature float64, timeout time.Duration) *LLMClient {
	return &LLMClient{
		baseURL:     baseURL,
		apiKey:      apiKey,
		model:       model,
		maxTokens:   maxTokens,
		temperature: temperature,
		httpClient:  &http.Client{Timeout: timeout},
	}
}

// Chat calls the LLM API and returns the full response text.
func (c *LLMClient) Chat(ctx context.Context, messages []LLMMessage, opts ...ChatOption) (string, *LLMResponse, error) {
	req := c.buildRequest(messages, false, opts...)

	body, err := c.doRequest(ctx, req)
	if err != nil {
		return "", nil, err
	}

	var resp LLMResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", &resp, fmt.Errorf("no choices in response")
	}

	slog.Debug("LLM chat completed",
		"model", req.Model,
		"prompt_tokens", resp.Usage.PromptTokens,
		"completion_tokens", resp.Usage.CompletionTokens,
		"total_tokens", resp.Usage.TotalTokens,
	)

	return resp.Choices[0].Message.Content, &resp, nil
}

// ChatStream calls the LLM API with streaming and calls onDelta for each content chunk.
// When thinking mode is active, onReasoning is called for reasoning_content chunks
// (before the final content).
// Returns the full text and total token count.
func (c *LLMClient) ChatStream(ctx context.Context, messages []LLMMessage, onDelta func(string), opts ...ChatOption) (string, int, error) {
	return c.ChatStreamWithReasoning(ctx, messages, onDelta, nil, opts...)
}

// ChatStreamWithReasoning is like ChatStream but also accepts an onReasoning callback
// for thinking-mode reasoning_content chunks.
func (c *LLMClient) ChatStreamWithReasoning(ctx context.Context, messages []LLMMessage, onDelta func(string), onReasoning func(string), opts ...ChatOption) (string, int, error) {
	req := c.buildRequest(messages, true, opts...)

	resp, err := c.doStreamRequest(ctx, req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Close()

	var fullText strings.Builder
	var reasoningText strings.Builder
	totalTokens := 0

	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := resp.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			// Process complete lines
			for {
				idx := bytes.IndexByte(buf, '\n')
				if idx < 0 {
					break
				}
				line := string(buf[:idx])
				buf = buf[idx+1:]

				line = strings.TrimSpace(line)
				if line == "" || !strings.HasPrefix(line, "data: ") {
					continue
				}
				data := strings.TrimPrefix(line, "data: ")
				if data == "[DONE]" {
					goto done
				}

				var chunk StreamChunk
				if err := json.Unmarshal([]byte(data), &chunk); err != nil {
					continue
				}

				for _, choice := range chunk.Choices {
					if choice.Delta.ReasoningContent != "" {
						reasoningText.WriteString(choice.Delta.ReasoningContent)
						if onReasoning != nil {
							onReasoning(choice.Delta.ReasoningContent)
						}
					}
					if choice.Delta.Content != "" {
						fullText.WriteString(choice.Delta.Content)
						if onDelta != nil {
							onDelta(choice.Delta.Content)
						}
					}
				}

				if chunk.Usage != nil {
					totalTokens = chunk.Usage.TotalTokens
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			slog.Error("stream read error", "error", err)
			break
		}
	}

done:
	slog.Debug("LLM stream completed",
		"model", req.Model,
		"content_length", fullText.Len(),
		"reasoning_length", reasoningText.Len(),
		"total_tokens", totalTokens,
	)

	return fullText.String(), totalTokens, nil
}

// ChatOption configures a chat request.
type ChatOption func(*LLMRequest)

// WithModel overrides the default model.
func WithModel(model string) ChatOption {
	return func(r *LLMRequest) { r.Model = model }
}

// WithTemperature overrides the default temperature.
func WithTemperature(t float64) ChatOption {
	return func(r *LLMRequest) { r.Temperature = t }
}

// WithThinking enables thinking mode.
func WithThinking(enabled bool) ChatOption {
	return func(r *LLMRequest) {
		if enabled {
			r.Thinking = &Thinking{Type: "enabled"}
			r.Temperature = 0 // thinking mode ignores temperature
		} else {
			r.Thinking = &Thinking{Type: "disabled"}
		}
	}
}

// WithReasoningEffort sets the reasoning effort level (thinking mode only).
// Valid values: "high" (default), "max".
func WithReasoningEffort(effort string) ChatOption {
	return func(r *LLMRequest) {
		r.ReasoningEffort = effort
	}
}

// WithTools adds tool definitions to the request.
func WithTools(tools []ToolDef) ChatOption {
	return func(r *LLMRequest) {
		r.Tools = tools
	}
}

// WithJSONResponse forces the model to return valid JSON.
func WithJSONResponse() ChatOption {
	return func(r *LLMRequest) {
		r.ResponseFormat = &ResponseFormat{Type: "json_object"}
	}
}

// ToolExecutor is a function that executes a tool call and returns the result.
// The agent loop uses this to resolve tool calls returned by the model.
// NOTE: JSON mode (response_format: json_object) is incompatible with tools;
// the DeepSeek API will reject requests containing both. Do not combine them.
type ToolExecutor func(name string, arguments string) (string, error)

// ChatWithTools performs an agent loop: it sends the request with tools,
// and if the model returns tool_calls, it executes them via the executor,
// appends the results to the conversation, and re-requests until the model
// produces a final content response (no more tool_calls).
//
// When thinking mode is active, reasoning_content from each turn is passed
// to onReasoning. The final content turn is streamed via onDelta.
//
// maxIterations limits the number of tool-call rounds (default 5).
func (c *LLMClient) ChatWithTools(
	ctx context.Context,
	messages []LLMMessage,
	onDelta func(string),
	onReasoning func(string),
	tools []ToolDef,
	executor ToolExecutor,
	opts ...ChatOption,
) (string, int, error) {
	const maxIterations = 5
	totalTokens := 0

	// Work on a copy of messages so we can append tool call/result turns
	conversation := make([]LLMMessage, len(messages))
	copy(conversation, messages)

	// Build tool-enabled opts (without streaming — tool rounds are non-streamed)
	toolOpts := append([]ChatOption{}, opts...)
	toolOpts = append(toolOpts, WithTools(tools))

	for iter := 0; iter < maxIterations; iter++ {
		// Non-streaming request for tool-call rounds
		resp, llmResp, err := c.Chat(ctx, conversation, toolOpts...)
		if err != nil {
			return "", totalTokens, fmt.Errorf("agent loop iteration %d failed: %w", iter, err)
		}
		if llmResp != nil {
			totalTokens += llmResp.Usage.TotalTokens
		}

		// Check if the model wants to call tools
		var lastMessage LLMMessage
		if len(llmResp.Choices) > 0 {
			lastMessage = llmResp.Choices[0].Message
		} else {
			lastMessage = LLMMessage{Role: "assistant", Content: resp}
		}

		if len(lastMessage.ToolCalls) == 0 {
			// No tool calls — this is the final answer.
			// If we've done at least one tool round, stream the final answer.
			if onDelta != nil && resp != "" {
				onDelta(resp)
			}
			return resp, totalTokens, nil
		}

		// Append the assistant's tool-call message to the conversation
		// (must preserve reasoning_content for thinking mode + tool calls)
		conversation = append(conversation, lastMessage)

		// Execute each tool call and append the results
		for _, tc := range lastMessage.ToolCalls {
			slog.Debug("agent loop: executing tool",
				"iteration", iter,
				"tool", tc.Function.Name,
				"arguments", tc.Function.Arguments)

			result := ""
			if executor != nil {
				result, err = executor(tc.Function.Name, tc.Function.Arguments)
				if err != nil {
					result = fmt.Sprintf("Error: %v", err)
				}
			} else {
				result = "Tool executor not available"
			}

			conversation = append(conversation, LLMMessage{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	// Exhausted iterations — do a final streaming request without tools
	slog.Warn("agent loop: max iterations reached, doing final stream", "iterations", maxIterations)
	finalOpts := append([]ChatOption{}, opts...)
	return c.ChatStreamWithReasoning(ctx, conversation, onDelta, onReasoning, finalOpts...)
}

func (c *LLMClient) buildRequest(messages []LLMMessage, stream bool, opts ...ChatOption) *LLMRequest {
	req := &LLMRequest{
		Model:       c.model,
		Messages:    messages,
		Stream:      stream,
		Temperature: c.temperature,
		MaxTokens:   c.maxTokens,
	}
	for _, opt := range opts {
		opt(req)
	}
	return req
}

func (c *LLMClient) doRequest(ctx context.Context, req *LLMRequest) ([]byte, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Retry with exponential backoff for rate limit (429) errors
	maxRetries := 3
	baseDelay := 500 * time.Millisecond

	for attempt := 0; attempt <= maxRetries; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, "POST",
			c.baseURL+"/chat/completions", bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			if attempt < maxRetries {
				delay := baseDelay * (1 << attempt) // 500ms, 1s, 2s
				slog.Warn("LLM request failed, retrying", "attempt", attempt+1, "delay", delay, "error", err)
				select {
				case <-time.After(delay):
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return nil, fmt.Errorf("LLM API request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}

		// Retry on 429 (rate limit) or 503 (service unavailable)
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) && attempt < maxRetries {
			delay := baseDelay * (1 << attempt)
			slog.Warn("LLM API rate limited, retrying",
				"status", resp.StatusCode,
				"attempt", attempt+1,
				"delay", delay)
			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("LLM API returned status %d: %s", resp.StatusCode, string(body))
		}

		return body, nil
	}

	return nil, fmt.Errorf("LLM API request failed after %d retries", maxRetries)
}

func (c *LLMClient) doStreamRequest(ctx context.Context, req *LLMRequest) (io.ReadCloser, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("LLM API stream request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("LLM API returned status %d: %s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}

// ExtractJSONObject extracts a JSON object from a text that may contain markdown fences.
func ExtractJSONObject(text string) string {
	raw := strings.TrimSpace(text)

	// Try to extract from markdown code fence
	if idx := strings.Index(raw, "```"); idx >= 0 {
		rest := raw[idx+3:]
		if strings.HasPrefix(rest, "json") {
			rest = rest[4:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			raw = strings.TrimSpace(rest[:end])
		}
	}

	// Find the JSON object
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return ""
	}

	return raw[start : end+1]
}

// ExtractJSONArray extracts a JSON array from a text that may contain markdown fences.
func ExtractJSONArray(text string) string {
	raw := strings.TrimSpace(text)

	// Try to extract from markdown code fence
	if idx := strings.Index(raw, "```"); idx >= 0 {
		rest := raw[idx+3:]
		if strings.HasPrefix(rest, "json") {
			rest = rest[4:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			raw = strings.TrimSpace(rest[:end])
		}
	}

	// Find the JSON array
	start := strings.Index(raw, "[")
	end := strings.LastIndex(raw, "]")
	if start < 0 || end <= start {
		return ""
	}

	return raw[start : end+1]
}
