package scholar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// T04 typed-operation tests: Discover/Rank/FetchFullText round trips through
// a fake worker, plus the MinIntervalRateLimiter spacing behaviour.

func fakeWorkerEnvelope(t *testing.T, outputs map[string]any) http.Handler {
	t.Helper()
	return mockWorkerHandler(t, http.StatusOK, func(req *OperationRequest) map[string]any {
		env := successEnvelope(req)
		env["outputs"] = outputs
		return env
	})
}

func TestDiscoverRoundTrip(t *testing.T) {
	outputs := map[string]any{
		"papers": []map[string]any{
			{
				"paper_id":           "p_abc123",
				"doi":                "10.1000/x.1",
				"title":              "A Study",
				"authors":            []string{"A. Author"},
				"year":               2024,
				"venue":              "J. Tests",
				"canonical_url":      nil,
				"aliases":            []string{"W1", "doi:10.1000/x.1"},
				"abstract":           "An abstract.",
				"oa_url":             "https://example.org/a.pdf",
				"providers":          []string{"openalex", "crossref"},
				"possible_duplicate": false,
			},
		},
		"provider_results": []map[string]any{
			{"provider": "openalex", "status": "ok", "returned": 1},
			{"provider": "crossref", "status": "error", "returned": 0, "error_code": "provider_timeout"},
		},
	}
	srv := httptest.NewServer(fakeWorkerEnvelope(t, outputs))
	defer srv.Close()
	client := newTestClient(t, srv)

	got, resp, err := client.Discover(context.Background(), "battery cathodes", []string{"openalex", "crossref"}, 5)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got.Papers) != 1 {
		t.Fatalf("papers = %+v", got.Papers)
	}
	paper := got.Papers[0]
	if paper.PaperID != "p_abc123" || paper.DOI == nil || *paper.DOI != "10.1000/x.1" {
		t.Fatalf("paper = %+v", paper)
	}
	if paper.Year == nil || *paper.Year != 2024 {
		t.Fatalf("year = %+v", paper.Year)
	}
	if len(got.ProviderResults) != 2 || got.ProviderResults[1].ErrorCode == nil {
		t.Fatalf("provider_results = %+v", got.ProviderResults)
	}
	if resp.Usage.CostUSD != nil {
		t.Fatalf("cost_usd should stay nil")
	}
}

func TestDiscoverClientSideAllowlistValidation(t *testing.T) {
	srv := httptest.NewServer(fakeWorkerEnvelope(t, map[string]any{}))
	defer srv.Close()
	client := newTestClient(t, srv)

	if _, _, err := client.Discover(context.Background(), "q", nil, 5); err == nil {
		t.Fatal("empty allowlist must be rejected client-side")
	}
}

func TestRankRoundTripAndValidation(t *testing.T) {
	outputs := map[string]any{
		"scores": []map[string]any{
			{"paper_id": "p1", "score": 3, "reason": "on topic"},
			{"paper_id": "p2", "score": 0, "reason": "unrelated"},
		},
	}
	srv := httptest.NewServer(fakeWorkerEnvelope(t, outputs))
	defer srv.Close()
	client := newTestClient(t, srv)

	got, _, err := client.Rank(context.Background(), "why do batteries age?", []RankCandidate{
		{PaperID: "p1", Abstract: "a1"},
		{PaperID: "p2", Abstract: "a2"},
	})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(got.Scores) != 2 || got.Scores[0].PaperID != "p1" || got.Scores[0].Score != 3 {
		t.Fatalf("scores = %+v", got.Scores)
	}

	// Duplicate IDs rejected before send.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("worker must not be reached for duplicate candidates")
	}))
	defer srv2.Close()
	client2 := newTestClient(t, srv2)
	if _, _, err := client2.Rank(context.Background(), "q", []RankCandidate{
		{PaperID: "p1"}, {PaperID: "p1"},
	}); err == nil {
		t.Fatal("duplicate ids must be rejected client-side")
	}
}

func TestFetchFullTextRoundTrip(t *testing.T) {
	content := []byte("fake pdf body for the typed client test")
	outputs := map[string]any{
		"paper_id":              "p_abc123",
		"acquisition_status":    "full_text_available",
		"content_hash":          "sha256:deadbeef",
		"size_bytes":            len(content),
		"media_type":            "application/pdf",
		"content_type_reported": "application/pdf",
		"looks_like_pdf":        true,
		"likely_scanned":        false,
		"final_url":             "https://example.org/a.pdf",
		"redirect_hops":         2,
		"content_base64":        base64.StdEncoding.EncodeToString(content),
		"blob":                  map[string]any{"transport": "inline_base64", "blob_url": nil},
	}
	srv := httptest.NewServer(fakeWorkerEnvelope(t, outputs))
	defer srv.Close()
	client := newTestClient(t, srv)

	got, _, err := client.FetchFullText(context.Background(), "p_abc123", "https://example.org/a.pdf", 10*1024*1024)
	if err != nil {
		t.Fatalf("FetchFullText: %v", err)
	}
	if string(got.Content()) != string(content) {
		t.Fatalf("content mismatch: %d bytes", len(got.Content()))
	}
	if got.MediaType != "application/pdf" || got.RedirectHops != 2 {
		t.Fatalf("outputs = %+v", got)
	}
	if got.Blob.Transport != "inline_base64" || got.Blob.BlobURL != nil {
		t.Fatalf("blob = %+v", got.Blob)
	}
}

func TestFetchFullTextClientSideSizeLimitValidation(t *testing.T) {
	srv := httptest.NewServer(fakeWorkerEnvelope(t, map[string]any{}))
	defer srv.Close()
	client := newTestClient(t, srv)
	if _, _, err := client.FetchFullText(context.Background(), "p", "https://x/y.pdf", 26*1024*1024); err == nil {
		t.Fatal("size_limit above the 25MiB design cap must be rejected client-side")
	}
	if _, _, err := client.FetchFullText(context.Background(), "p", "https://x/y.pdf", 0); err == nil {
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
