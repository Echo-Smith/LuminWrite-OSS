package writingruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/scholar"
)

// parseEnvelope echoes the parse request back with the parse outputs shape
// (one block), so a successful call exercises the full adapter path.
func parseEnvelope() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req scholar.OperationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return // the strict echo check on the client side fails the test
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"request_id": req.RequestID,
			"input_hash": req.InputHash,
			"outputs": map[string]any{
				"blocks": []map[string]any{
					{"block_id": "blk-0001-aaaaaaaa", "text": "t", "page": 1,
						"block_hash": "sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
				},
				"coverage": map[string]any{
					"media_type": "application/pdf", "parser_version": ResearchReadParserVersion,
					"total_blocks": 1, "total_codepoints": 1,
					"complete": true, "truncated": false, "likely_scanned": false,
				},
			},
			"usage":    map[string]any{"measured": false, "input_tokens": 0, "output_tokens": 0, "cost_usd": nil, "provider": "parser", "model": "none"},
			"warnings": []string{},
			"versions": map[string]string{"operation_version": "1"},
		})
	}
}

func newAdapterTestClient(t *testing.T, srv *httptest.Server) *scholar.Client {
	t.Helper()
	client, err := scholar.NewClient(srv.URL, "test-token", scholar.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func TestParseDocumentSucceedsUnderCeiling(t *testing.T) {
	srv := httptest.NewServer(parseEnvelope())
	defer srv.Close()
	adapter := ScholarParseRead{Client: newAdapterTestClient(t, srv)}

	// 25 MiB exactly: the design ceiling itself must still parse (its base64
	// form ~33.4 MiB + envelope fits the worker's 40 MiB transport cap).
	document := make([]byte, maxParsePayloadBytes)
	for i := range document {
		document[i] = byte(i % 251)
	}
	outputs, resp, err := adapter.ParseDocument(context.Background(), document, "application/pdf", ResearchReadParserVersion)
	if err != nil {
		t.Fatalf("ParseDocument at 25 MiB: %v", err)
	}
	if resp == nil || len(outputs.Blocks) != 1 {
		t.Fatalf("unexpected outputs: %+v", outputs)
	}
}

func TestParseDocumentRejectsOverCeilingBeforeSend(t *testing.T) {
	// The HTTP handler must never be reached: a document over the 25 MiB
	// design cap is rejected client-side (its request would not fit the
	// worker's 40 MiB body cap).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("worker was called with an over-ceiling document")
	}))
	defer srv.Close()
	adapter := ScholarParseRead{Client: newAdapterTestClient(t, srv)}

	document := make([]byte, maxParsePayloadBytes+1)
	_, _, err := adapter.ParseDocument(context.Background(), document, "application/pdf", ResearchReadParserVersion)
	var scholarErr *scholar.Error
	if !errors.As(err, &scholarErr) {
		t.Fatalf("expected *scholar.Error, got %v", err)
	}
	if scholarErr.Code != "client_document_too_large" {
		t.Fatalf("code = %q, want client_document_too_large", scholarErr.Code)
	}
	if scholarErr.OutcomeUnknown() {
		t.Fatal("pre-send rejection must not be outcome-unknown")
	}
}

func TestParsePayloadCeilingMatchesWorkerTransportCap(t *testing.T) {
	// F4 arithmetic lock: worst-case parse request for a 25 MiB document
	// (base64 4/3 + a generous JSON envelope allowance) must fit the
	// worker's 40 MiB request-body cap (api.DEFAULT_MAX_BODY_BYTES in
	// services/scholar-worker/src/lumin_scholar/api.py — update both sides
	// together if this test ever fails).
	const workerMaxBodyBytes = 40 << 20
	base64Len := ((maxParsePayloadBytes + 2) / 3) * 4
	const envelopeOverhead = 1 << 16 // 64 KiB of JSON envelope/escaping headroom
	if base64Len+envelopeOverhead > workerMaxBodyBytes {
		t.Fatalf("worst-case parse request %d exceeds worker 40 MiB body cap", base64Len+envelopeOverhead)
	}
	if base64Len+envelopeOverhead <= maxParsePayloadBytes {
		t.Fatal("sanity: base64 form must be larger than the raw ceiling")
	}
}
