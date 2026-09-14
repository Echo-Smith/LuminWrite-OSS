package claimverify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// llmClient is a minimal OpenAI-compatible chat client for the verification
// model. Deliberately separate from the generation client: the whole point
// of this package is that verification runs on a different vendor.
type llmClient struct {
	baseURL     string
	apiKey      string
	model       string
	timeout     time.Duration
	maxAttempts int
	backoff     time.Duration
	client      *http.Client
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// httpStatusError marks retryable transport-level failures (429/5xx from the
// upstream relay — the GLM relay in particular rate-limits in bursts).
type httpStatusError struct {
	status int
	body   string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("verifier HTTP %d: %s", e.status, truncate([]byte(e.body), 300))
}

func (c *llmClient) completeJSON(ctx context.Context, system, user string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"model":       c.model,
		"messages":    []chatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}},
		"temperature": 0,
	})
	if err != nil {
		return "", err
	}
	var lastErr error
	for attempt := 0; attempt < c.maxAttempts; attempt++ {
		if attempt > 0 {
			wait := c.backoff * time.Duration(attempt)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(wait):
			}
		}
		content, err := c.attempt(ctx, payload)
		if err == nil {
			return extractJSONObject(content)
		}
		lastErr = err
		var statusErr *httpStatusError
		if !errors.As(err, &statusErr) || (statusErr.status != http.StatusTooManyRequests && statusErr.status < 500) {
			return "", err
		}
	}
	return "", lastErr
}

func (c *llmClient) attempt(ctx context.Context, payload []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("claimverify: verifier call: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("claimverify: read verifier response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", &httpStatusError{status: resp.StatusCode, body: string(body)}
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("claimverify: decode verifier response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("claimverify: verifier returned no choices")
	}
	content := parsed.Choices[0].Message.Content
	if content == "" {
		return "", fmt.Errorf("claimverify: verifier returned empty content")
	}
	return content, nil
}

func extractJSONObject(content string) (string, error) {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "```") {
		if end := strings.LastIndex(trimmed, "```"); end > 3 {
			trimmed = strings.TrimSpace(trimmed[3:end])
			trimmed = strings.TrimPrefix(trimmed, "json")
		}
	}
	first := strings.Index(trimmed, "{")
	last := strings.LastIndex(trimmed, "}")
	if first < 0 || last <= first {
		return "", fmt.Errorf("claimverify: no JSON object in verifier response")
	}
	return trimmed[first : last+1], nil
}

func truncate(body []byte, n int) string {
	if len(body) <= n {
		return string(body)
	}
	return string(body[:n]) + "…"
}

var reservedHostPattern = regexp.MustCompile(`(?i)^(localhost|.*\.local|.*\.internal)$`)

// ValidateBaseURL enforces the SSRF guard for the operator-provided
// verification endpoint: http/https only, no localhost, reserved, or
// private-IP hosts. DNS-level checks are out of scope for a deployment-time
// operator setting.
func ValidateBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("claimverify: base_url must be an http(s) URL, got %q", raw)
	}
	host := parsed.Hostname()
	if host == "" || reservedHostPattern.MatchString(host) {
		return fmt.Errorf("claimverify: base_url host %q is not allowed", host)
	}
	if ip := net.ParseIP(host); ip != nil &&
		(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified()) {
		return fmt.Errorf("claimverify: base_url host %q is a reserved address", host)
	}
	return nil
}
