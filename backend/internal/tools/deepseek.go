package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"
)

// ModelV4Pro is the DeepSeek V4 Pro model name, used for high-reasoning steps.
const ModelV4Pro = "deepseek-v4-pro"

// LLMClient is a client for the DeepSeek (OpenAI-compatible) API.
type LLMClient struct {
	baseURL           string
	apiKey            string
	model             string
	maxTokens         int
	temperature       float64
	httpClient        *http.Client
	responsesAPIRatio float64    // 0.0~1.0, proportion of traffic routed to Responses API
	abMetrics         *ABMetrics // A/B test metrics collector (nil if A/B disabled)
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
	Model            string          `json:"model"`
	Messages         []LLMMessage    `json:"messages"`
	Stream           bool            `json:"stream"`
	Temperature      float64         `json:"temperature,omitempty"`
	MaxTokens        int             `json:"max_tokens"`
	Thinking         *Thinking       `json:"thinking,omitempty"`
	ReasoningEffort  string          `json:"reasoning_effort,omitempty"` // "high" | "max" (thinking mode only)
	Tools            []ToolDef       `json:"tools,omitempty"`
	ResponseFormat   *ResponseFormat `json:"response_format,omitempty"`
	Instructions     string          `json:"-"` // Static system prompt for Responses API (higher cache hit rate)
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
		CacheHitTokens        int `json:"prompt_cache_hit_tokens"`
		CacheMissTokens       int `json:"prompt_cache_miss_tokens"`
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

// StreamToolCallDelta represents a tool call delta in a streaming response.
// The OpenAI-compatible API streams tool calls as fragments: the first delta
// contains id/type/function.name, subsequent deltas append to function.arguments.
type StreamToolCallDelta struct {
	Index    int              `json:"index"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function ToolCallFunction `json:"function,omitempty"`
}

// StreamDelta is a single chunk from the streaming API.
type StreamDelta struct {
	Content          string                `json:"content"`
	ReasoningContent string                `json:"reasoning_content"`
	ToolCalls        []StreamToolCallDelta `json:"tool_calls,omitempty"`
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
		CacheHitTokens        int `json:"prompt_cache_hit_tokens"`
		CacheMissTokens       int `json:"prompt_cache_miss_tokens"`
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

// SetResponsesAPIRatio configures the A/B test ratio for Responses API.
// 0.0 = all traffic goes to Chat Completions API (default)
// 1.0 = all traffic goes to Responses API
// 0.5 = 50/50 split
func (c *LLMClient) SetResponsesAPIRatio(ratio float64) {
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	c.responsesAPIRatio = ratio
	if ratio > 0 && c.abMetrics == nil {
		c.abMetrics = NewABMetrics()
	}
	slog.Info("A/B test configured", "responses_api_ratio", ratio)
}

// GetABMetrics returns a snapshot of A/B test metrics, or nil if A/B is disabled.
func (c *LLMClient) GetABMetrics() *ABMetricsSnapshot {
	if c.abMetrics == nil {
		return nil
	}
	return c.abMetrics.Snapshot()
}

// Chat calls the LLM API and returns the full response text.
// If A/B testing is enabled, traffic is split between Chat Completions and Responses API.
func (c *LLMClient) Chat(ctx context.Context, messages []LLMMessage, opts ...ChatOption) (string, *LLMResponse, error) {
	req := c.buildRequest(messages, false, opts...)

	// A/B routing: if ratio is set, randomly route to Responses API
	if c.responsesAPIRatio > 0 && rand.Float64() < c.responsesAPIRatio {
		start := time.Now()
		text, resp, err := c.responsesChat(ctx, req)
		if c.abMetrics != nil {
			c.abMetrics.RecordResponsesAPI(resp, time.Since(start))
		}
		return text, resp, err
	}

	start := time.Now()
	text, resp, err := c.chatCompletions(ctx, req)
	if c.abMetrics != nil {
		c.abMetrics.RecordChatCompletions(resp, time.Since(start))
	}
	return text, resp, err
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

	// A/B routing for streaming
	if c.responsesAPIRatio > 0 && rand.Float64() < c.responsesAPIRatio {
		start := time.Now()
		text, tokens, err := c.responsesStream(ctx, req, onDelta, onReasoning)
		if c.abMetrics != nil {
			c.abMetrics.RecordResponsesAPIStream(tokens, time.Since(start))
		}
		return text, tokens, err
	}

	start := time.Now()
	text, tokens, err := c.chatCompletionsStream(ctx, req, onDelta, onReasoning)
	if c.abMetrics != nil {
		c.abMetrics.RecordChatCompletionsStream(tokens, time.Since(start))
	}
	return text, tokens, err
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

// WithInstructions sets a static system prompt that is cached server-side.
// When using the Responses API, this is sent as the top-level `instructions` parameter
// (enabling prefix caching). When using Chat Completions, it is prepended as a system message.
// Static instructions should NOT contain dynamic content (dates, profile rules, etc.) —
// those belong in the user message to maximize cache hit rate.
func WithInstructions(instructions string) ChatOption {
	return func(r *LLMRequest) {
		r.Instructions = instructions
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
// All rounds use streaming — reasoning_content AND content are streamed in
// real-time via onReasoning and onDelta respectively.
//
// Safety mechanism (optimistic streaming + automatic rollback):
// Content is streamed to onDelta as soon as it arrives (for real-time UX in
// the final answer round). If a tool-call delta arrives after content was
// already streamed (meaning this is an intermediate round, not the final
// answer), onReset is called so the caller can discard the already-streamed
// content and reset its state machine. This handles the edge case where the
// model produces both content and tool_calls in the same response — something
// that API specs allow but thinking mode rarely triggers.
//
// maxIterations limits the number of tool-call rounds (default 5).
func (c *LLMClient) ChatWithTools(
	ctx context.Context,
	messages []LLMMessage,
	onDelta func(string),
	onReasoning func(string),
	onReset func(),
	tools []ToolDef,
	executor ToolExecutor,
	opts ...ChatOption,
) (string, int, error) {
	const maxIterations = 3
	totalTokens := 0

	// Work on a copy of messages so we can append tool call/result turns
	conversation := make([]LLMMessage, len(messages))
	copy(conversation, messages)

	// Build tool-enabled opts
	toolOpts := append([]ChatOption{}, opts...)
	toolOpts = append(toolOpts, WithTools(tools))

	for iter := 0; iter < maxIterations; iter++ {
		// Streaming request for all rounds.
		// reasoning_content is streamed via onReasoning in real-time.
		// content is streamed via onDelta in real-time (optimistic).
		// If tool_calls appear after content, onReset is called to roll back.
		assistantMsg, tokens, err := c.chatStreamRound(ctx, conversation, onDelta, onReasoning, onReset, toolOpts...)
		if err != nil {
			return "", totalTokens, fmt.Errorf("agent loop iteration %d failed: %w", iter, err)
		}
		totalTokens += tokens

		if len(assistantMsg.ToolCalls) == 0 {
			// No tool calls — this is the final answer.
			// Content was already streamed in real-time via onDelta.
			return assistantMsg.Content, totalTokens, nil
		}

		// Append the assistant's tool-call message to the conversation
		// (must preserve reasoning_content for thinking mode + tool calls)
		conversation = append(conversation, assistantMsg)

		// Execute each tool call and append the results
		for _, tc := range assistantMsg.ToolCalls {
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

	// Exhausted iterations — do a final streaming request without tools.
	// Use the ORIGINAL messages (not the tool-call-laden conversation) to avoid
	// confusing the model with a long history of failed/irrelevant tool results.
	slog.Warn("agent loop: max iterations reached, doing final stream", "iterations", maxIterations)
	finalOpts := append([]ChatOption{}, opts...)
	return c.ChatStreamWithReasoning(ctx, messages, onDelta, onReasoning, finalOpts...)
}

// flushChunked sends content to the callback in rune-sized chunks,
// simulating streaming output for buffered content.
// Kept for backward compatibility but no longer used by ChatWithTools.

// chatStreamRound performs a single streaming round with tool support.
// It streams reasoning_content via onReasoning AND content via onDelta in
// real-time, while also buffering both for the returned LLMMessage.
// Tool calls are accumulated from stream deltas.
//
// Optimistic streaming + rollback: content is pushed to onDelta as soon as
// it arrives. If a tool_call delta arrives after content was already pushed,
// onReset is called so the caller can discard the streamed content and reset
// its state. After onReset, no further content is pushed for this round.
func (c *LLMClient) chatStreamRound(
	ctx context.Context,
	messages []LLMMessage,
	onDelta func(string),
	onReasoning func(string),
	onReset func(),
	opts ...ChatOption,
) (LLMMessage, int, error) {
	req := c.buildRequest(messages, true, opts...)

	resp, err := c.doStreamRequest(ctx, req)
	if err != nil {
		return LLMMessage{}, 0, err
	}
	defer resp.Close()

	var contentBuf strings.Builder
	var reasoningBuf strings.Builder
	totalTokens := 0

	// Accumulate tool calls by index (OpenAI streams them as fragments)
	toolCallMap := make(map[int]*ToolCall)

	// Optimistic streaming state: track whether content has been pushed
	// and whether tool calls have started arriving.
	contentStreamed := false // true once we've pushed any content to onDelta
	toolCallStarted := false  // true once the first tool_call delta arrives
	resetCalled := false      // true after onReset has been invoked for this round

	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := resp.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
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
					// Stream reasoning_content in real-time
					if choice.Delta.ReasoningContent != "" {
						reasoningBuf.WriteString(choice.Delta.ReasoningContent)
						if onReasoning != nil {
							onReasoning(choice.Delta.ReasoningContent)
						}
					}
					// Stream content in real-time AND buffer it for the returned message.
					// Optimistic: push to onDelta immediately. If tool_calls arrive
					// later in this same round, onReset will be called to roll back.
					if choice.Delta.Content != "" {
						contentBuf.WriteString(choice.Delta.Content)
						if onDelta != nil && !toolCallStarted && !resetCalled {
							onDelta(choice.Delta.Content)
							contentStreamed = true
						}
					}
					// Accumulate tool call deltas by index
					for _, tcDelta := range choice.Delta.ToolCalls {
						if !toolCallStarted {
							toolCallStarted = true
							// If content was already streamed, this is an intermediate
							// round — invoke rollback so the caller can discard it.
							if contentStreamed && onReset != nil && !resetCalled {
								onReset()
								resetCalled = true
							}
						}
						tc, exists := toolCallMap[tcDelta.Index]
						if !exists {
							tc = &ToolCall{}
							toolCallMap[tcDelta.Index] = tc
						}
						if tcDelta.ID != "" {
							tc.ID = tcDelta.ID
						}
						if tcDelta.Type != "" {
							tc.Type = tcDelta.Type
						}
						if tcDelta.Function.Name != "" {
							tc.Function.Name = tcDelta.Function.Name
						}
						tc.Function.Arguments += tcDelta.Function.Arguments
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
	// Collect tool calls in index order
	var toolCalls []ToolCall
	for i := 0; i < len(toolCallMap); i++ {
		if tc, ok := toolCallMap[i]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}

	slog.Debug("LLM stream round completed",
		"model", req.Model,
		"content_length", contentBuf.Len(),
		"reasoning_length", reasoningBuf.Len(),
		"tool_calls", len(toolCalls),
		"content_streamed", contentStreamed,
		"reset_called", resetCalled,
		"total_tokens", totalTokens,
	)

	return LLMMessage{
		Role:             "assistant",
		Content:          contentBuf.String(),
		ReasoningContent: reasoningBuf.String(),
		ToolCalls:        toolCalls,
	}, totalTokens, nil
}

// flushChunked sends content to the callback in rune-sized chunks,
// simulating streaming output for buffered content.
func flushChunked(onDelta func(string), content string) {
	runes := []rune(content)
	chunkSize := 80
	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		onDelta(string(runes[i:end]))
	}
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
	// If Instructions is set and there's no system message yet, prepend it.
	// This enables cache-friendly prompting for both APIs:
	// - Chat Completions: instructions becomes the first system message
	// - Responses API: instructions is sent as the top-level parameter
	if req.Instructions != "" {
		hasSystem := false
		for _, m := range req.Messages {
			if m.Role == "system" {
				hasSystem = true
				break
			}
		}
		if !hasSystem {
			req.Messages = append([]LLMMessage{{Role: "system", Content: req.Instructions}}, req.Messages...)
		}
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

		// 402 Payment Required — quota exhausted, no point retrying
		if resp.StatusCode == http.StatusPaymentRequired {
			slog.Error("LLM API quota exceeded", "status", 402, "body", string(body))
			return nil, fmt.Errorf("LLM API quota exceeded (402): %s", string(body))
		}

		// 429 after exhausting retries — also likely a quota/billing issue
		if resp.StatusCode == http.StatusTooManyRequests {
			slog.Error("LLM API rate limit exhausted after retries", "status", 429, "body", string(body))
			return nil, fmt.Errorf("LLM API rate limit exhausted (429): %s", string(body))
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

		// 402 Payment Required — quota exhausted
		if resp.StatusCode == http.StatusPaymentRequired {
			slog.Error("LLM API quota exceeded (stream)", "status", 402, "body", string(body))
			return nil, fmt.Errorf("LLM API quota exceeded (402): %s", string(body))
		}

		// 429 rate limit (stream has no retry — treat as quota issue)
		if resp.StatusCode == http.StatusTooManyRequests {
			slog.Error("LLM API rate limited (stream)", "status", 429, "body", string(body))
			return nil, fmt.Errorf("LLM API rate limit exhausted (429): %s", string(body))
		}

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
