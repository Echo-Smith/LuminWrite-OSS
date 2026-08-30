package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
)

func TestProviderPreflightClassifiesHTTPFailuresAndRecovers(t *testing.T) {
	status := http.StatusUnauthorized
	body := `{"error":"secret must never escape"}`
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer provider.Close()

	registry := NewReadinessRegistry(time.Hour)
	cfg := &config.Config{DeepSeek: config.DeepSeekConfig{BaseURL: provider.URL, APIKey: "real-test-key"},
		ProviderPreflight: config.ProviderPreflightConfig{Timeout: 50 * time.Millisecond}}
	preflight := NewProviderPreflight(cfg, registry, nil, nil)
	assertProbeCode(t, preflight.Run(context.Background()), "llm", preflightCodeAuthRejected)

	status, body = http.StatusTooManyRequests, `{}`
	assertProbeCode(t, preflight.Run(context.Background()), "llm", preflightCodeRateLimited)

	status, body = http.StatusOK, `not-json`
	assertProbeCode(t, preflight.Run(context.Background()), "llm", preflightCodeMalformedResponse)

	status, body = http.StatusOK, `{"data":[{"id":"model"}]}`
	snapshot := preflight.Run(context.Background())
	if !snapshot.Results["llm"].Reachable || !registry.Snapshot(time.Now()).Components["llm"].Ready {
		t.Fatalf("recovered snapshot=%#v readiness=%#v", snapshot, registry.Snapshot(time.Now()))
	}
	encoded := strings.ToLower(snapshot.Results["llm"].ErrorCode + snapshot.Results["llm"].ProviderID)
	if strings.Contains(encoded, "real-test-key") || strings.Contains(encoded, provider.URL) {
		t.Fatalf("probe output leaked credential or URL: %#v", snapshot)
	}
}

func TestProviderPreflightClassifiesTimeout(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer provider.Close()
	cfg := &config.Config{DeepSeek: config.DeepSeekConfig{BaseURL: provider.URL, APIKey: "real-test-key"},
		ProviderPreflight: config.ProviderPreflightConfig{Timeout: 10 * time.Millisecond}}
	preflight := NewProviderPreflight(cfg, NewReadinessRegistry(time.Hour), nil, nil)
	assertProbeCode(t, preflight.Run(context.Background()), "llm", preflightCodeTimeout)
}

func TestProviderPreflightDoesNotProbePlaceholderCredentials(t *testing.T) {
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer provider.Close()
	cfg := &config.Config{DeepSeek: config.DeepSeekConfig{BaseURL: provider.URL, APIKey: "your-deepseek-api-key"}}
	preflight := NewProviderPreflight(cfg, NewReadinessRegistry(time.Hour), nil, nil)
	assertProbeCode(t, preflight.Run(context.Background()), "llm", readinessCodeNotConfigured)
	if calls != 0 {
		t.Fatalf("placeholder credential triggered %d network calls", calls)
	}
}

func assertProbeCode(t *testing.T, snapshot ProviderPreflightSnapshot, providerID, code string) {
	t.Helper()
	if result := snapshot.Results[providerID]; result.ErrorCode != code || result.Reachable {
		t.Fatalf("result=%#v want code=%s", result, code)
	}
}
