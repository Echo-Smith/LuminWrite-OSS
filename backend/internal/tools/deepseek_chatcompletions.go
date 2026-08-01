package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// chatCompletions is the original Chat Completions API implementation,
// extracted from Chat() to allow A/B routing.
func (c *LLMClient) chatCompletions(ctx context.Context, req *LLMRequest) (string, *LLMResponse, error) {
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

// chatCompletionsStream is the original streaming implementation,
// extracted from ChatStreamWithReasoning() to allow A/B routing.
func (c *LLMClient) chatCompletionsStream(ctx context.Context, req *LLMRequest, onDelta func(string), onReasoning func(string)) (string, int, error) {
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
