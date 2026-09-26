package scholar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Shared OpenAI-compatible chat/completions client for the rank and read
// operations (zero SDK, same as the former worker). Configuration comes from
// the environment at runtime — never from a request payload, never hardcoded,
// never logged. Fail-closed: operations that need an LLM return
// llm_not_configured when the configuration is missing; they must NEVER
// silently degrade (e.g. all-zero scores) because an unscored-but-passing
// batch would corrupt selection downstream.

// Environment variable names for the OpenAI-compatible endpoint. Declared
// via os.Getenv lookup keys; the secret itself lives only in the deployment
// environment (docker-compose env / secret store).
const (
	envLLMBaseURLKey = "SCHOLAR_LLM_BASE_URL"
	envLLMModelKey   = "SCHOLAR_LLM_MODEL"
	envLLMAPIKeyName = "SCHOLAR_LLM_API_KEY"

	// 480s：reasoning 模型（MiMo-V2.6-Flash）read 实测 217s→300s+ 波动且仍在
	// 恶化；300s 上限在第三轮 E2E 直接探针中被击穿（300.3s 无响应）。与
	// writingruntime 外层 researchCallTimeout（480s）对齐，节点 bounds 600s 兜底。
	llmTimeout     = 480 * time.Second
	llmMaxResponse = 8 << 20 // 8 MiB
)

// LLMConfig is the OpenAI-compatible endpoint configuration.
type LLMConfig struct {
	BaseURL string
	Model   string
	APIKey  string
}

// LLMConfigFromEnv reads the SCHOLAR_LLM_* environment variables; ok is false
// when any of the three is empty.
func LLMConfigFromEnv(getenv func(string) string) (LLMConfig, bool) {
	if getenv == nil {
		getenv = os.Getenv
	}
	cfg := LLMConfig{
		BaseURL: strings.TrimRight(strings.TrimSpace(getenv(envLLMBaseURLKey)), "/"),
		Model:   strings.TrimSpace(getenv(envLLMModelKey)),
		APIKey:  strings.TrimSpace(getenv(envLLMAPIKeyName)),
	}
	if cfg.BaseURL == "" || cfg.Model == "" || cfg.APIKey == "" {
		return LLMConfig{}, false
	}
	return cfg, true
}

// chatMessage is one OpenAI-compatible chat message.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatCaller posts one chat/completions request and returns the raw content
// string plus token usage. HTTP/transport failures map to typed *Error.
func chatCall(ctx context.Context, cfg LLMConfig, messages []chatMessage, timeout time.Duration) (string, Usage, *Error) {
	if timeout <= 0 {
		timeout = llmTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body := map[string]any{
		"model":       cfg.Model,
		"messages":    messages,
		"temperature": 0,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", Usage{}, &Error{Kind: ErrProtocol, Code: "op_marshal_request", Message: err.Error()}
	}
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, cfg.BaseURL+"/chat/completions", bytes.NewReader(encoded))
	if err != nil {
		return "", Usage{}, &Error{Kind: ErrProtocol, Code: "op_build_request", Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := llmHTTPClient.Do(req)
	if err != nil {
		if callCtx.Err() != nil || ctx.Err() != nil {
			return "", Usage{}, &Error{Kind: joinKinds(ErrOutcomeUnknown, ErrDeadline),
				Code: "op_llm_timeout", Message: "LLM call timed out", Retryable: true}
		}
		return "", Usage{}, &Error{Kind: ErrRemote, Code: "op_llm_unreachable",
			Message: "LLM unreachable: " + err.Error(), Retryable: true}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return "", Usage{}, &Error{Kind: ErrRemote, Code: "op_llm_rate_limited",
			Message: "LLM provider rate-limited the request", Retryable: true}
	}
	if resp.StatusCode >= 400 {
		return "", Usage{}, &Error{Kind: ErrRemote, Code: "op_llm_http_error",
			Message:   fmt.Sprintf("LLM returned HTTP %d", resp.StatusCode),
			Retryable: resp.StatusCode >= 500,
		}
	}
	payloadBytes, err := io.ReadAll(io.LimitReader(resp.Body, llmMaxResponse))
	if err != nil {
		// The call may have executed and billed; the response never arrived.
		return "", Usage{}, &Error{Kind: ErrOutcomeUnknown, Code: "op_llm_truncated",
			Message: "reading LLM response failed: " + err.Error()}
	}
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return "", Usage{}, &Error{Kind: ErrRemote, Code: "op_llm_bad_json",
			Message: "LLM response is not JSON", Retryable: true}
	}
	if len(payload.Choices) == 0 {
		return "", Usage{}, &Error{Kind: ErrRemote, Code: "op_llm_bad_response",
			Message: "LLM response lacks choices[0].message.content", Retryable: true}
	}
	usage := Usage{
		Measured:     true,
		InputTokens:  payload.Usage.PromptTokens,
		OutputTokens: payload.Usage.CompletionTokens,
		Provider:     "llm",
		Model:        cfg.Model,
	}
	return payload.Choices[0].Message.Content, usage, nil
}

// joinKinds combines two sentinel kinds so errors.Is matches either.
func joinKinds(a, b error) error {
	return joinedError{a, b}
}

type joinedError struct{ a, b error }

func (j joinedError) Error() string { return j.a.Error() + "; " + j.b.Error() }
func (j joinedError) Is(target error) bool {
	for _, part := range []error{j.a, j.b} {
		if part == target {
			return true
		}
	}
	return false
}

// stripMarkdownFence tolerates a ```json fence around an LLM JSON answer.
func stripMarkdownFence(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	// Drop the first fence line (``` or ```json/...).
	if nl := strings.IndexAny(trimmed, "\r\n"); nl >= 0 {
		trimmed = trimmed[nl+1:]
	} else {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
	}
	trimmed = strings.TrimSpace(trimmed)
	if idx := strings.LastIndex(trimmed, "```"); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	return trimmed
}
