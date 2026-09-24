// Package scholar is the in-process Go scholar executor client
// (specs/research-review/contracts.md §4). The research runtime owns
// budgets, artifacts, and final transactions; this package performs the
// bounded operations the runtime addresses — in-process, with no separate
// worker service.
//
// Operation envelope (OperationResponse):
//
//	request:  {request_id, input_hash, operation_version, deadline_ms, payload}
//	success:  {request_id, input_hash, outputs, usage, warnings, versions}
//	failure:  {error: {code, retryable, retry_after_ms?, outcome_unknown}}
//
// deadline_ms is absolute Unix epoch milliseconds; the client derives it from
// the context deadline so executor-side checks and host-side timeouts agree.
package scholar

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/google/uuid"
)

// Operation is one of the whitelisted bounded operations.
type Operation string

const (
	OpDiscover      Operation = "discover"
	OpRank          Operation = "rank"
	OpFetchFullText Operation = "fetch_full_text"
	OpParse         Operation = "parse"
	OpRead          Operation = "read"
)

// OperationVersion is the envelope/payload contract version this client sends.
// It must match the worker's SUPPORTED_OPERATION_VERSION.
const OperationVersion = "1"

func (o Operation) valid() bool {
	switch o {
	case OpDiscover, OpRank, OpFetchFullText, OpParse, OpRead:
		return true
	}
	return false
}

// OperationRequest is the unified request envelope.
type OperationRequest struct {
	RequestID        string         `json:"request_id"`
	InputHash        string         `json:"input_hash"`
	OperationVersion string         `json:"operation_version"`
	DeadlineMs       int64          `json:"deadline_ms"`
	Payload          map[string]any `json:"payload"`
}

// Usage reports measured resource consumption for one operation. CostUSD is
// nil when the worker cannot attribute a cost (mock operations report null).
type Usage struct {
	Measured     bool     `json:"measured"`
	InputTokens  int64    `json:"input_tokens"`
	OutputTokens int64    `json:"output_tokens"`
	CostUSD      *float64 `json:"cost_usd"`
	Provider     string   `json:"provider"`
	Model        string   `json:"model"`
}

// OperationResponse is the unified success envelope. Outputs stays raw so each
// caller (T04/T05) decodes its own typed outputs after this client validates
// the envelope.
type OperationResponse struct {
	RequestID string            `json:"request_id"`
	InputHash string            `json:"input_hash"`
	Outputs   json.RawMessage   `json:"outputs"`
	Usage     Usage             `json:"usage"`
	Warnings  []string          `json:"warnings"`
	Versions  map[string]string `json:"versions"`
}

// ErrorBody is the failure envelope carried in the "error" field.
type ErrorBody struct {
	Code           string `json:"code"`
	Retryable      bool   `json:"retryable"`
	RetryAfterMs   *int64 `json:"retry_after_ms"`
	OutcomeUnknown bool   `json:"outcome_unknown"`
	Message        string `json:"message"`
}

// HashPayload computes the canonical input_hash for a payload: sha256 over
// the canonical JSON form defined in canonical.go — recursively key-sorted,
// UTF-8 without ASCII/HTML escaping, compact separators. The rule is frozen
// by the golden digest pins in canonical_test.go: persisted input_hash
// values must stay comparable across releases, so the rule never changes
// opportunistically. Floats are rejected (fail closed) — see canonical.go.
func HashPayload(payload map[string]any) (string, error) {
	encoded, err := CanonicalPayloadJSON(payload)
	if err != nil {
		return "", fmt.Errorf("scholar: hash payload: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// NewRequestID returns a fresh request identifier for idempotency tracking.
func NewRequestID() string {
	return uuid.NewString()
}

// operationsPath builds the private operations URL. The worker only serves
// this prefix; errors here are caller bugs, so they panic via url path rules
// instead of surfacing as runtime errors.
func operationURL(baseURL string, op Operation) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	parsed.Path = parsed.Path + "/internal/v1/operations/" + url.PathEscape(string(op))
	return parsed.String(), nil
}
