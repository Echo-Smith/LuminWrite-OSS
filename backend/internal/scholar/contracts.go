// Package scholar is the Go host-side client for the private-network Python
// Scholar Worker (specs/research-review/contracts.md §4). The Go host owns
// budgets, artifacts, and final transactions; the worker only performs
// bounded operations addressed by this client.
//
// Wire contract (mirrors services/scholar-worker/src/lumin_scholar/contracts.py):
//
//	POST /internal/v1/operations/{operation}
//	Authorization: Bearer <SCHOLAR_WORKER_TOKEN>
//	request:  {request_id, input_hash, operation_version, deadline_ms, payload}
//	success:  {request_id, input_hash, outputs, usage, warnings, versions}
//	failure:  {error: {code, retryable, retry_after_ms?, outcome_unknown}}
//
// deadline_ms is absolute Unix epoch milliseconds; the client derives it from
// the context deadline so worker-side checks and host-side timeouts agree.
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
// compact JSON with sorted keys (encoding/json already sorts map keys and
// escapes HTML-sensitive bytes deterministically; the worker validates format
// only for now). The canonicalisation must be pinned jointly with the worker
// before real (T04+) operations depend on cross-language hash equality.
func HashPayload(payload map[string]any) (string, error) {
	encoded, err := json.Marshal(payload)
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
