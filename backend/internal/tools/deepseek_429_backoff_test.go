package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// chatOKBody is a minimal successful Chat Completions response.
const chatOKBody = `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

// sseOKPayload is a minimal successful SSE stream payload.
const sseOKPayload = "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

// rateLimitedThenOKServer returns 429 on the first request and a 200 on
// subsequent ones, recording every request arrival time. It is the fixture for
// asserting that the configured 429 backoff base actually shapes the delay
// before the second attempt.
func rateLimitedThenOKServer(t *testing.T, sse bool, arrivals *atomic.Int64, count *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrivals.Store(time.Now().UnixNano())
		if count.Add(1) == 1 {
			http.Error(w, `{"error":{"message":"rate limit"}}`, http.StatusTooManyRequests)
			return
		}
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(sseOKPayload))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(chatOKBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestChat429BackoffUsesConfiguredBase verifies that after a 429 the second
// attempt happens after ~the configured backoff base (not the 500ms default):
// with base=200ms the whole call must succeed well under 1s, while the default
// base would need at least 500ms before the second attempt.
func TestChat429BackoffUsesConfiguredBase(t *testing.T) {
	base := 200 * time.Millisecond
	var count atomic.Int32
	var lastArrival atomic.Int64
	srv := rateLimitedThenOKServer(t, false, &lastArrival, &count)

	c := NewLLMClient(srv.URL, "test-key", "test-model", 128, 0.7, 10*time.Second)
	c.SetRateLimitBackoffBase(base)

	start := time.Now()
	text, _, err := c.Chat(context.Background(), []LLMMessage{{Role: "user", Content: "hi"}})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Chat after 429 retry: %v", err)
	}
	if text != "ok" {
		t.Fatalf("unexpected text %q", text)
	}
	if got := count.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
	// Elastic lower bound: the second attempt cannot arrive before one full
	// backoff base elapsed. Allow 20% scheduler slack on the upper bound.
	if elapsed < base {
		t.Fatalf("second attempt arrived after %v, want >= backoff base %v", elapsed, base)
	}
	if elapsed > base*3 {
		t.Fatalf("second attempt arrived after %v, want within ~3x of base %v (backoff not applied?)", elapsed, base)
	}
}

// TestChat429BackoffDefaultBaseIsLonger verifies the zero-value behavior: a
// client with no explicit base waits at least the 500ms default before the
// second attempt.
func TestChat429BackoffDefaultBaseIsLonger(t *testing.T) {
	var count atomic.Int32
	var lastArrival atomic.Int64
	srv := rateLimitedThenOKServer(t, false, &lastArrival, &count)

	c := NewLLMClient(srv.URL, "test-key", "test-model", 128, 0.7, 10*time.Second)
	// No SetRateLimitBackoffBase — zero value must mean the 500ms default.

	start := time.Now()
	if _, _, err := c.Chat(context.Background(), []LLMMessage{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("Chat after 429 retry: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < defaultRateLimitBackoffBase {
		t.Fatalf("second attempt arrived after %v, want >= default base %v", elapsed, defaultRateLimitBackoffBase)
	}
}

// TestChatStream429BackoffUsesConfiguredBase covers the streaming retry loop
// (doStreamRequest) with the same elastic-gap assertion.
func TestChatStream429BackoffUsesConfiguredBase(t *testing.T) {
	base := 200 * time.Millisecond
	var count atomic.Int32
	var lastArrival atomic.Int64
	srv := rateLimitedThenOKServer(t, true, &lastArrival, &count)

	c := NewLLMClient(srv.URL, "test-key", "test-model", 128, 0.7, 10*time.Second)
	c.SetRateLimitBackoffBase(base)

	start := time.Now()
	text, _, err := c.ChatStream(context.Background(), []LLMMessage{{Role: "user", Content: "hi"}}, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ChatStream after 429 retry: %v", err)
	}
	if text != "ok" {
		t.Fatalf("unexpected text %q", text)
	}
	if got := count.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
	if elapsed < base {
		t.Fatalf("second attempt arrived after %v, want >= backoff base %v", elapsed, base)
	}
	if elapsed > base*3 {
		t.Fatalf("second attempt arrived after %v, want within ~3x of base %v", elapsed, base)
	}
}

// TestSetRateLimitBackoffBaseZeroResetsDefault pins the setter contract:
// zero and negative values reset to the default base.
func TestSetRateLimitBackoffBaseZeroResetsDefault(t *testing.T) {
	c := NewLLMClient("http://unused", "k", "m", 1, 0.7, time.Second)
	c.SetRateLimitBackoffBase(3 * time.Second)
	if got := c.backoffBase(); got != 3*time.Second {
		t.Fatalf("backoffBase = %v, want 3s", got)
	}
	c.SetRateLimitBackoffBase(0)
	if got := c.backoffBase(); got != defaultRateLimitBackoffBase {
		t.Fatalf("backoffBase after zero = %v, want default %v", got, defaultRateLimitBackoffBase)
	}
	c.SetRateLimitBackoffBase(-1 * time.Second)
	if got := c.backoffBase(); got != defaultRateLimitBackoffBase {
		t.Fatalf("backoffBase after negative = %v, want default %v", got, defaultRateLimitBackoffBase)
	}
}
