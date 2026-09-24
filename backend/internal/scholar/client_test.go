package scholar

// Transport note: the client now runs its five operations in-process against
// the local Executor (see the client.go header) instead of POSTing envelopes
// to the private-network scholar worker. The retired HTTP-transport tests —
// bearer-token auth, 429/Retry-After, 500 outcome-unknown, mid-flight
// timeouts, request-id and input_hash echo mismatch, malformed or truncated
// JSON bodies, connection failures — asserted the old wire protocol and were
// dropped with it. Everything below is transport-independent and performs no
// network I/O: constructor guards, fail-fast client-side validation,
// envelope/request-id traceability, the rate-limiter hook, and the in-process
// parse path.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
)

const testToken = "go-test-token"

// testBaseURL is deliberately inert: NewClient keeps baseURL as the
// research-path enablement signal for wiring compatibility and never dials it.
const testBaseURL = "https://scholar.invalid"

// hashOf is a test helper mirroring HashPayload's canonicalisation.
func hashOf(t *testing.T, payload map[string]any) string {
	t.Helper()
	h, err := HashPayload(payload)
	if err != nil {
		t.Fatalf("HashPayload: %v", err)
	}
	return h
}

// newTestClient builds an in-process client with inert wiring values.
func newTestClient(t *testing.T, opts ...Option) *Client {
	t.Helper()
	c, err := NewClient(testBaseURL, testToken, opts...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestNewClientTokenGuard(t *testing.T) {
	if _, err := NewClient(testBaseURL, ""); err == nil {
		t.Fatal("empty token must be rejected (fail-closed service-auth guard)")
	}
}

func TestCallClientSideEnvelopeValidation(t *testing.T) {
	client := newTestClient(t)
	payload := map[string]any{}

	_, err := client.Call(context.Background(), Operation("teleport"), hashOf(t, payload), payload)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("non-whitelisted operation must be rejected client-side, got %v", err)
	}
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if se.Code != "client_unknown_operation" {
		t.Fatalf("code = %q, want client_unknown_operation", se.Code)
	}

	_, err = client.Call(context.Background(), OpDiscover, "deadbeef", payload)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("malformed input_hash must be rejected client-side, got %v", err)
	}
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if se.Code != "client_invalid_input_hash" {
		t.Fatalf("code = %q, want client_invalid_input_hash", se.Code)
	}
}

// Parse over text/plain is the one operation that runs entirely in-process
// (no LLM, no downloader, no provider egress), so it doubles as the Call
// envelope vehicle.
func TestCallEnvelopeEchoesRequestIdentity(t *testing.T) {
	client := newTestClient(t, WithRequestIDFactory(func() string { return "my-request" }))
	document := base64.StdEncoding.EncodeToString([]byte("first paragraph.\n\nsecond paragraph."))
	payload := map[string]any{"document": document, "media_type": MediaTypeTextPlain}
	inputHash := hashOf(t, payload)

	resp, err := client.Call(context.Background(), OpParse, inputHash, payload)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.RequestID != "my-request" {
		t.Fatalf("request_id = %q, want my-request", resp.RequestID)
	}
	if resp.InputHash != inputHash {
		t.Fatalf("input_hash = %q, want %q", resp.InputHash, inputHash)
	}
	if resp.Versions["operation_version"] != OperationVersion || resp.Versions["impl"] != "go-inprocess/1" {
		t.Fatalf("versions = %v", resp.Versions)
	}

	var outputs ParseOutputs
	if err := json.Unmarshal(resp.Outputs, &outputs); err != nil {
		t.Fatalf("decode outputs: %v", err)
	}
	if len(outputs.Blocks) != 2 {
		t.Fatalf("blocks = %+v", outputs.Blocks)
	}
	if outputs.Blocks[0].BlockHash != hashText("first paragraph.") {
		t.Fatalf("block hash = %q, want %q", outputs.Blocks[0].BlockHash, hashText("first paragraph."))
	}
	if outputs.Coverage.MediaType != MediaTypeTextPlain {
		t.Fatalf("coverage = %+v", outputs.Coverage)
	}

	// A pinned request id wins over the factory (callers re-driving a
	// reconciled task keep the original id).
	pinned, err := client.Call(context.Background(), OpParse, inputHash, payload, WithRequestID("pinned-request"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if pinned.RequestID != "pinned-request" {
		t.Fatalf("request_id = %q, want pinned-request", pinned.RequestID)
	}
}

func TestCallParseValidatesDocumentPayload(t *testing.T) {
	client := newTestClient(t)

	bad := map[string]any{"document": "%%not-base64%%", "media_type": MediaTypeTextPlain}
	_, err := client.Call(context.Background(), OpParse, hashOf(t, bad), bad)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("invalid base64 document must be rejected client-side, got %v", err)
	}
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if se.Code != "client_bad_document_encoding" {
		t.Fatalf("code = %q, want client_bad_document_encoding", se.Code)
	}

	oversized := map[string]any{
		"document":   base64.StdEncoding.EncodeToString(make([]byte, maxParseDocumentBytes+1)),
		"media_type": MediaTypeTextPlain,
	}
	_, err = client.Call(context.Background(), OpParse, hashOf(t, oversized), oversized)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("oversized document must be rejected client-side, got %v", err)
	}
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if se.Code != "client_document_too_large" {
		t.Fatalf("code = %q, want client_document_too_large", se.Code)
	}

	pdf := map[string]any{
		"document":   base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 fake")),
		"media_type": MediaTypePDF,
	}
	_, err = client.Call(context.Background(), OpParse, hashOf(t, pdf), pdf)
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if se.Code != "parse_extractor_unconfigured" {
		t.Fatalf("code = %q, want parse_extractor_unconfigured", se.Code)
	}
}

type stubParser struct{ text string }

func (s stubParser) ExtractText(ctx extractionContext) (string, *bool, error) {
	scanned := false
	return s.text, &scanned, nil
}

func TestCallParsePDFUsesInjectedParser(t *testing.T) {
	client := newTestClient(t, WithDocumentParser(stubParser{text: "page one text\fpage two text"}))
	payload := map[string]any{
		"document":   base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 fake")),
		"media_type": MediaTypePDF,
	}
	resp, err := client.Call(context.Background(), OpParse, hashOf(t, payload), payload)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var outputs ParseOutputs
	if err := json.Unmarshal(resp.Outputs, &outputs); err != nil {
		t.Fatalf("decode outputs: %v", err)
	}
	if len(outputs.Blocks) != 2 {
		t.Fatalf("blocks = %+v", outputs.Blocks)
	}
	if outputs.Blocks[0].Page == nil || *outputs.Blocks[0].Page != 1 {
		t.Fatalf("block 1 page = %+v, want 1", outputs.Blocks[0].Page)
	}
	if outputs.Blocks[1].Page == nil || *outputs.Blocks[1].Page != 2 {
		t.Fatalf("block 2 page = %+v, want 2", outputs.Blocks[1].Page)
	}
}

type stubLimiter struct {
	fn func(ctx context.Context, op Operation) error
}

func (s stubLimiter) Wait(ctx context.Context, op Operation) error { return s.fn(ctx, op) }

// The pacing hook runs before any executor egress: a rejection aborts the
// operation without side effects and is never outcome-unknown.
func TestRateLimiterRejectsBeforeAnyWork(t *testing.T) {
	var seen []Operation
	reject := stubLimiter{fn: func(ctx context.Context, op Operation) error {
		seen = append(seen, op)
		return errors.New("budget exhausted")
	}}
	// Rank needs an LLM config to reach the limiter; this inert config is
	// never dialed because the limiter rejects first.
	llm := LLMConfig{BaseURL: testBaseURL, Model: "test-model", APIKey: "test-key"}
	client := newTestClient(t, WithRateLimiter(reject), WithLLMConfig(llm))

	rankPayload := map[string]any{
		"research_question": "why do batteries age?",
		"candidates":        []any{map[string]any{"paper_id": "p1", "abstract": "a1"}},
	}
	_, err := client.Call(context.Background(), OpRank, hashOf(t, rankPayload), rankPayload)
	assertRateLimitRejection(t, err, OpRank, &seen)

	discoverPayload := map[string]any{
		"query":              "battery cathodes",
		"provider_allowlist": []any{"openalex"},
		"limit":              5,
	}
	_, err = client.Call(context.Background(), OpDiscover, hashOf(t, discoverPayload), discoverPayload)
	assertRateLimitRejection(t, err, OpDiscover, &seen)
}

func assertRateLimitRejection(t *testing.T, err error, wantOp Operation, seen *[]Operation) {
	t.Helper()
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if se.Code != "op_rate_limited" {
		t.Fatalf("code = %q, want op_rate_limited", se.Code)
	}
	if se.OutcomeUnknown() {
		t.Fatal("rejection before send must NOT be outcome-unknown")
	}
	if len(*seen) == 0 || (*seen)[len(*seen)-1] != wantOp {
		t.Fatalf("limiter saw %v, want %v last", *seen, wantOp)
	}
}

// Health is kept for source compatibility with the former worker probes and
// reports the in-process executor always ready (no model or network calls).
func TestHealthAlwaysReady(t *testing.T) {
	client := newTestClient(t)
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
}
