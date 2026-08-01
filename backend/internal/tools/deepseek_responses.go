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
)

// responsesRequest is the request body for the Responses API.
// Unlike Chat Completions, it uses `instructions` (top-level, cacheable)
// and `input` (conversation messages) as separate fields.
type responsesRequest struct {
	Model           string        `json:"model"`
	Instructions    string        `json:"instructions,omitempty"`
	Input           []LLMMessage  `json:"input"`
	Stream          bool          `json:"stream"`
	Temperature     float64       `json:"temperature,omitempty"`
	MaxOutputTokens int           `json:"max_output_tokens,omitempty"`
	Thinking        *Thinking     `json:"thinking,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
	Tools           []responsesTool `json:"tools,omitempty"`
	Text            *responsesText `json:"text,omitempty"`
}

// responsesTool wraps a ToolDef in the Responses API format.
type responsesTool struct {
	Type     string                `json:"type"` // "function"
	Function ToolDefFunction       `json:"function"`
}

// responsesText controls output format (e.g., JSON mode).
type responsesText struct {
	Format *ResponseFormat `json:"format,omitempty"`
}

// responsesEvent represents a structured SSE event from the Responses API.
// Unlike Chat Completions (data-only), Responses API sends `event: <type>` + `data: <json>`.
type responsesEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// responsesChat sends a non-streaming request via the Responses API.
// It converts the LLMRequest to Responses API format, calls /responses,
// and returns the result in the same LLMResponse format for compatibility.
func (c *LLMClient) responsesChat(ctx context.Context, req *LLMRequest) (string, *LLMResponse, error) {
	rReq := c.toResponsesRequest(req, false)

	body, err := c.doResponsesRequest(ctx, rReq)
	if err != nil {
		return "", nil, err
	}

	// Parse the Responses API output format
	var result struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens          int `json:"input_tokens"`
			OutputTokens         int `json:"output_tokens"`
			TotalTokens          int `json:"total_tokens"`
			CachedTokens         int `json:"cached_tokens"`
			CompletionTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", nil, fmt.Errorf("failed to decode responses API result: %w", err)
	}

	// Extract text from output (find message type with text content)
	text := ""
	for _, item := range result.Output {
		if item.Type == "message" {
			for _, content := range item.Content {
				if content.Type == "output_text" {
					text += content.Text
				}
			}
		}
	}

	// Map to LLMResponse for compatibility
	resp := &LLMResponse{
		Usage: struct {
			PromptTokens          int `json:"prompt_tokens"`
			CompletionTokens      int `json:"completion_tokens"`
			TotalTokens           int `json:"total_tokens"`
			CacheHitTokens        int `json:"prompt_cache_hit_tokens"`
			CacheMissTokens       int `json:"prompt_cache_miss_tokens"`
			CompletionTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		}{
			PromptTokens:    result.Usage.InputTokens,
			CompletionTokens: result.Usage.OutputTokens,
			TotalTokens:     result.Usage.TotalTokens,
			CacheHitTokens:  result.Usage.CachedTokens,
			CacheMissTokens: result.Usage.InputTokens - result.Usage.CachedTokens,
			CompletionTokensDetails: struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			}{
				ReasoningTokens: result.Usage.CompletionTokensDetails.ReasoningTokens,
			},
		},
	}
	resp.Choices = append(resp.Choices, struct {
		Message      LLMMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
	}{
		Message:      LLMMessage{Role: "assistant", Content: text},
		FinishReason: "stop",
	})

	slog.Debug("Responses API chat completed",
		"model", req.Model,
		"prompt_tokens", resp.Usage.PromptTokens,
		"cache_hit_tokens", resp.Usage.CacheHitTokens,
		"completion_tokens", resp.Usage.CompletionTokens,
		"total_tokens", resp.Usage.TotalTokens,
	)

	return text, resp, nil
}

// responsesStream sends a streaming request via the Responses API.
// It parses structured SSE events (event: <type>\ndata: <json>) and
// calls onDelta for content and onReasoning for reasoning content.
func (c *LLMClient) responsesStream(ctx context.Context, req *LLMRequest, onDelta func(string), onReasoning func(string)) (string, int, error) {
	rReq := c.toResponsesRequest(req, true)

	resp, err := c.doResponsesStreamRequest(ctx, rReq)
	if err != nil {
		return "", 0, err
	}
	defer resp.Close()

	var fullText strings.Builder
	var reasoningText strings.Builder
	totalTokens := 0

	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	var currentEventType string // track event type across lines

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

				// Parse event type line
				if strings.HasPrefix(line, "event: ") {
					currentEventType = strings.TrimPrefix(line, "event: ")
					continue
				}

				// Parse data line
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				data := strings.TrimPrefix(line, "data: ")
				if data == "[DONE]" {
					goto done
				}

				// Process based on event type
				switch currentEventType {
				case "response.output_text.delta":
					var delta struct {
						Delta string `json:"delta"`
					}
					if json.Unmarshal([]byte(data), &delta) == nil && delta.Delta != "" {
						fullText.WriteString(delta.Delta)
						if onDelta != nil {
							onDelta(delta.Delta)
						}
					}

				case "response.reasoning.delta":
					var delta struct {
						Delta string `json:"delta"`
					}
					if json.Unmarshal([]byte(data), &delta) == nil && delta.Delta != "" {
						reasoningText.WriteString(delta.Delta)
						if onReasoning != nil {
							onReasoning(delta.Delta)
						}
					}

				case "response.completed":
					var completed struct {
						Response struct {
							Usage struct {
								InputTokens  int `json:"input_tokens"`
								OutputTokens int `json:"output_tokens"`
								TotalTokens  int `json:"total_tokens"`
								CachedTokens int `json:"cached_tokens"`
							} `json:"usage"`
						} `json:"response"`
					}
					if json.Unmarshal([]byte(data), &completed) == nil {
						totalTokens = completed.Response.Usage.TotalTokens
					}

				case "response.error":
					var errEvent struct {
						Error struct {
							Message string `json:"message"`
							Type    string `json:"type"`
						} `json:"error"`
					}
					if json.Unmarshal([]byte(data), &errEvent) == nil {
						slog.Error("Responses API error event",
							"error_type", errEvent.Error.Type,
							"error_message", errEvent.Error.Message)
					}
				}

				// Reset event type after processing data
				currentEventType = ""
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			slog.Error("responses stream read error", "error", err)
			break
		}
	}

done:
	slog.Debug("Responses API stream completed",
		"model", req.Model,
		"content_length", fullText.Len(),
		"reasoning_length", reasoningText.Len(),
		"total_tokens", totalTokens,
	)

	return fullText.String(), totalTokens, nil
}

// toResponsesRequest converts an LLMRequest (Chat Completions format) to a responsesRequest.
// - System messages are extracted into `instructions` (if not already set)
// - Remaining messages become `input`
// - max_tokens → max_output_tokens
// - response_format → text.format
func (c *LLMClient) toResponsesRequest(req *LLMRequest, stream bool) *responsesRequest {
	rReq := &responsesRequest{
		Model:           req.Model,
		Stream:          stream,
		Temperature:     req.Temperature,
		MaxOutputTokens: req.MaxTokens,
		Thinking:        req.Thinking,
		ReasoningEffort: req.ReasoningEffort,
	}

	// Use Instructions if set, otherwise extract from system messages
	if req.Instructions != "" {
		rReq.Instructions = req.Instructions
	}

	// Convert messages: extract system → instructions, rest → input
	for _, msg := range req.Messages {
		if msg.Role == "system" && rReq.Instructions == "" {
			rReq.Instructions = msg.Content
			continue
		}
		// Skip tool messages (handled differently in Responses API)
		if msg.Role == "tool" {
			// Convert tool result to a user message with context
			rReq.Input = append(rReq.Input, LLMMessage{
				Role:    "user",
				Content: fmt.Sprintf("[Tool Result: %s]", msg.Content),
			})
			continue
		}
		rReq.Input = append(rReq.Input, msg)
	}

	// Convert tools
	for _, t := range req.Tools {
		rReq.Tools = append(rReq.Tools, responsesTool{
			Type:     t.Type,
			Function: t.Function,
		})
	}

	// Convert response_format
	if req.ResponseFormat != nil {
		rReq.Text = &responsesText{Format: req.ResponseFormat}
	}

	return rReq
}

// doResponsesRequest sends a non-streaming request to the Responses API.
func (c *LLMClient) doResponsesRequest(ctx context.Context, req *responsesRequest) ([]byte, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal responses request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/responses", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Responses API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusPaymentRequired {
		return nil, fmt.Errorf("Responses API quota exceeded (402): %s", string(body))
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("Responses API rate limit (429): %s", string(body))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Responses API returned status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// doResponsesStreamRequest sends a streaming request to the Responses API.
func (c *LLMClient) doResponsesStreamRequest(ctx context.Context, req *responsesRequest) (io.ReadCloser, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal responses request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/responses", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Responses API stream request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("Responses API returned status %d: %s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}
