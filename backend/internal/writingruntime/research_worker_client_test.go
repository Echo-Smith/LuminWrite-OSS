package writingruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/scholar"
)

// The scholar operations now execute in-process (Go rewrite); the former
// HTTP envelope tests became configuration/ceiling tests against the same
// adapter surface.

func newAdapterTestClient(t *testing.T) *scholar.Client {
	t.Helper()
	client, err := scholar.NewClient("", "test-token")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func TestParseDocumentRejectsOverCeilingBeforeExtraction(t *testing.T) {
	// A document over the 25 MiB design cap is rejected before any
	// extraction work (it can never produce a bounded parse unit).
	adapter := ScholarParseRead{Client: newAdapterTestClient(t)}

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
		t.Fatal("pre-extraction rejection must not be outcome-unknown")
	}
}

func TestParsePayloadCeilingMatchesDesignCap(t *testing.T) {
	// F4 arithmetic lock: the 25 MiB design ceiling stays the parse-path
	// document cap; the base64 form of the boundary document fits within the
	// former worker transport cap the contract pinned.
	const workerMaxBodyBytes = 40 << 20
	base64Len := ((maxParsePayloadBytes + 2) / 3) * 4
	const envelopeOverhead = 1 << 16 // 64 KiB of JSON envelope/escaping headroom
	if base64Len+envelopeOverhead > workerMaxBodyBytes {
		t.Fatalf("worst-case parse request %d exceeds 40 MiB contract cap", base64Len+envelopeOverhead)
	}
	if base64Len+envelopeOverhead <= maxParsePayloadBytes {
		t.Fatal("sanity: base64 form must be larger than the raw ceiling")
	}
}
