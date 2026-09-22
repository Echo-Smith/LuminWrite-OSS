package scholar

// Typed-operation tests for the in-process client (see the client.go header:
// the transport moved from HTTP to a local executor). The former fake-worker
// round trips for discover/rank/fetch_full_text asserted success envelopes
// fabricated over HTTP; in-process those results come from the real
// providers, the LLM and the constrained downloader, which speak HTTP through
// pinned clients to hardcoded external URLs — exercising them here would need
// live network egress or provider mocking that does not exist, so those round
// trips are dropped. What remains is the transport-independent contract:
// client-side validation that fails before any executor or network work, the
// pure-logic rate limiter, and canonical payload marshaling.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestDiscoverClientSideAllowlistValidation(t *testing.T) {
	client := newTestClient(t)

	_, _, err := client.Discover(context.Background(), "q", nil, 5)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("empty allowlist must be rejected client-side, got %v", err)
	}
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if se.Code != "client_empty_provider_allowlist" {
		t.Fatalf("code = %q, want client_empty_provider_allowlist", se.Code)
	}

	// Unknown providers are rejected before any provider network call.
	_, _, err = client.Discover(context.Background(), "q", []string{"not_a_provider"}, 5)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("unknown provider must be rejected client-side, got %v", err)
	}
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if se.Code != "client_unknown_provider" {
		t.Fatalf("code = %q, want client_unknown_provider", se.Code)
	}
}

func TestRankClientSideValidation(t *testing.T) {
	client := newTestClient(t)

	if _, _, err := client.Rank(context.Background(), "q", nil); err == nil {
		t.Fatal("empty rank batch must be rejected client-side")
	}
	if _, _, err := client.Rank(context.Background(), "q", make([]RankCandidate, 9)); err == nil {
		t.Fatal("rank batches above 8 candidates must be rejected client-side")
	}

	_, _, err := client.Rank(context.Background(), "q", []RankCandidate{{PaperID: "p1"}, {PaperID: "p1"}})
	if err == nil {
		t.Fatal("duplicate paper ids must be rejected client-side")
	}
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if se.Code != "client_duplicate_paper_id" {
		t.Fatalf("code = %q, want client_duplicate_paper_id", se.Code)
	}
}

// A well-formed batch must reach the executor intact: with the LLM env
// cleared it gets past the candidate-count check to the LLM guard instead of
// failing as a bad batch. Locks the payload-adapter plumbing (candidates
// travel as []map[string]any from the typed method). No network I/O: rank
// stops at llm_not_configured before any call.
func TestRankCandidatesReachExecutor(t *testing.T) {
	t.Setenv("SCHOLAR_LLM_BASE_URL", "")
	t.Setenv("SCHOLAR_LLM_MODEL", "")
	t.Setenv("SCHOLAR_LLM_API_KEY", "")
	client := newTestClient(t)

	_, _, err := client.Rank(context.Background(), "why do batteries age?", []RankCandidate{{PaperID: "p1", Abstract: "a1"}})
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if se.Code != "llm_not_configured" {
		t.Fatalf("code = %q, want llm_not_configured (candidates must flow to the executor)", se.Code)
	}
}

func TestFetchFullTextClientSideSizeLimitValidation(t *testing.T) {
	client := newTestClient(t)
	if _, _, err := client.FetchFullText(context.Background(), "p", "https://example.org/y.pdf", 26*1024*1024); err == nil {
		t.Fatal("size_limit above the 25MiB design cap must be rejected client-side")
	}
	if _, _, err := client.FetchFullText(context.Background(), "p", "https://example.org/y.pdf", 0); err == nil {
		t.Fatal("size_limit=0 must be rejected client-side")
	}
}

func TestMinIntervalRateLimiterSpacing(t *testing.T) {
	l := NewMinIntervalRateLimiter(map[Operation]time.Duration{
		OpDiscover: 80 * time.Millisecond,
	})
	ctx := context.Background()

	start := time.Now()
	if err := l.Wait(ctx, OpDiscover); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Millisecond {
		t.Fatalf("first Wait should be immediate, took %v", elapsed)
	}
	if err := l.Wait(ctx, OpDiscover); err != nil {
		t.Fatalf("second Wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 75*time.Millisecond {
		t.Fatalf("second Wait must honour the interval, elapsed %v", elapsed)
	}

	// A different operation key is not delayed by discover's interval.
	start = time.Now()
	if err := l.Wait(ctx, OpRank); err != nil {
		t.Fatalf("rank Wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Millisecond {
		t.Fatalf("rank Wait should be immediate, took %v", elapsed)
	}
}

func TestMinIntervalRateLimiterHonorsContextCancel(t *testing.T) {
	l := NewMinIntervalRateLimiter(map[Operation]time.Duration{
		OpDiscover: time.Hour,
	})
	ctx, cancel := context.WithCancel(context.Background())
	if err := l.Wait(ctx, OpDiscover); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	cancel()
	if err := l.Wait(ctx, OpDiscover); !errors.Is(err, context.Canceled) {
		t.Fatalf("second Wait = %v, want context.Canceled", err)
	}
}

func TestMinIntervalRateLimiterConcurrentWaits(t *testing.T) {
	l := NewMinIntervalRateLimiter(map[Operation]time.Duration{OpDiscover: 20 * time.Millisecond})
	var wg sync.WaitGroup
	var mu sync.Mutex
	count := 0
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.Wait(context.Background(), OpDiscover); err != nil {
				t.Errorf("Wait: %v", err)
			}
			mu.Lock()
			count++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if count != 5 {
		t.Fatalf("count = %d, want 5", count)
	}
}

// Compile-time guard: the envelope payload must round-trip json.Number
// verbatim through the canonical encoder.
func TestPayloadMarshalKeepsNumbers(t *testing.T) {
	payload := map[string]any{"limit": 5, "n": json.Number("1.5")}
	encoded, err := CanonicalPayloadJSON(payload)
	if err != nil {
		t.Fatalf("CanonicalPayloadJSON: %v", err)
	}
	if string(encoded) != `{"limit":5,"n":1.5}` {
		t.Fatalf("canonical = %s", encoded)
	}
}
