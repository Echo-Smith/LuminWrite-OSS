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
	Role    string `json:"role"`
	Content string `json:"content"`
}

// LLMRequest is the request body for the chat completions API.
type LLMRequest struct {
	Model       string       `json:"model"`
	Messages    []LLMMessage `json:"messages"`
	Stream      bool         `json:"stream"`
	Temperature float64      `json:"temperature"`
	MaxTokens   int          `json:"max_tokens"`
	Thinking    *Thinking    `json:"thinking,omitempty"`
}

// Thinking controls the thinking mode for V4 models.
type Thinking struct {
	Type string `json:"type"` // "enabled" | "disabled"
}

// LLMResponse is the response from the chat completions API.
type LLMResponse struct {
	Choices []struct {
		Message      LLMMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// StreamDelta is a single chunk from the streaming API.
type StreamDelta struct {
	Content string `json:"content"`
}

// StreamChunk represents a single SSE chunk from the streaming API.
type StreamChunk struct {
	Choices []struct {
		Delta      StreamDelta `json:"delta"`
		FinishReason string    `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
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

// ChatStream calls the LLM API with streaming and calls onDelta for each chunk.
// Returns the full text and total token count.
func (c *LLMClient) ChatStream(ctx context.Context, messages []LLMMessage, onDelta func(string), opts ...ChatOption) (string, int, error) {
	req := c.buildRequest(messages, true, opts...)

	resp, err := c.doStreamRequest(ctx, req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Close()

	var fullText strings.Builder
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
			r.Temperature = 0
		}
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
	return req
}

func (c *LLMClient) doRequest(ctx context.Context, req *LLMRequest) ([]byte, error) {
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

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("LLM API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM API returned status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
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
