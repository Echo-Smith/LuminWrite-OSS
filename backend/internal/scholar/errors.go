package scholar

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Failure taxonomy (four kinds, per T03):
//
//   - ErrProtocol: envelope contract violation (malformed JSON, missing or
//     mismatched request_id/input_hash echo, unknown-operation skew). Not
//     transient; indicates a broken deployment or client bug.
//   - ErrAuth: service-to-service authentication failed (401/403).
//   - ErrRemote: the worker returned a well-formed business error without
//     executing the request (e.g. duplicate_paper_id, rate_limited,
//     deadline_exceeded). Retryable() and RetryAfter carry the worker's hint.
//   - ErrOutcomeUnknown: the request may have been accepted and executed
//     (transport failure after send, context deadline mid-flight, 5xx, or a
//     worker error body flagged outcome_unknown). Per contracts.md §4 the
//     caller must NOT assume the work did not happen; auto-retry only for
//     known idempotent units is decided by the host (T02/T04), never here.
var (
	ErrProtocol       = errors.New("scholar: protocol violation")
	ErrAuth           = errors.New("scholar: authentication failed")
	ErrRemote         = errors.New("scholar: remote operation error")
	ErrOutcomeUnknown = errors.New("scholar: operation outcome unknown")
	// ErrDeadline is joined with ErrOutcomeUnknown when the host-side context
	// deadline elapsed mid-flight; errors.Is matches either.
	ErrDeadline = errors.New("scholar: deadline exceeded")
)

// Error is the structured failure returned by client methods.
type Error struct {
	// Kind is one of the sentinel errors above (possibly errors.Join'ed).
	Kind error

	// Code is the worker-provided machine code ("duplicate_paper_id", ...) or
	// a client-side "client_*" code when the worker gave nothing usable.
	Code string

	// HTTPStatus is the raw status; 0 when no HTTP response was involved.
	HTTPStatus int

	// Retryable reports the worker's retryable hint (ErrRemote only).
	Retryable bool

	// RetryAfter carries retry_after_ms when the worker sent one.
	RetryAfter time.Duration

	// Message is human-readable detail.
	Message string
}

func (e *Error) Error() string {
	status := ""
	if e.HTTPStatus != 0 {
		status = fmt.Sprintf(" (http %d)", e.HTTPStatus)
	}
	detail := e.Message
	if detail == "" {
		detail = e.Code
	}
	if detail == "" {
		detail = "no detail"
	}
	return fmt.Sprintf("%s%s: %s", e.Kind, status, detail)
}

func (e *Error) Unwrap() error { return e.Kind }

// OutcomeUnknown reports whether the operation may have executed remotely.
func (e *Error) OutcomeUnknown() bool { return errors.Is(e.Kind, ErrOutcomeUnknown) }

// IsProtocol reports an envelope-contract violation.
func (e *Error) IsProtocol() bool { return errors.Is(e.Kind, ErrProtocol) }

// newRemoteError builds an ErrRemote/ErrOutcomeUnknown/ErrAuth error from a
// decoded worker error body.
func newRemoteError(status int, body *ErrorBody) *Error {
	e := &Error{HTTPStatus: status, Kind: ErrRemote}
	if body != nil {
		e.Code = body.Code
		e.Message = body.Message
		e.Retryable = body.Retryable
		if body.RetryAfterMs != nil && *body.RetryAfterMs > 0 {
			e.RetryAfter = time.Duration(*body.RetryAfterMs) * time.Millisecond
		}
		if body.OutcomeUnknown {
			e.Kind = ErrOutcomeUnknown
			e.Retryable = false
		}
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		e.Kind = ErrAuth
		e.Retryable = false
	}
	return e
}

// classifyUnparsedStatus maps statuses whose body carried no usable error
// envelope (e.g. an HTML error page from an intermediary).
func classifyUnparsedStatus(status int, detail string) *Error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &Error{Kind: ErrAuth, HTTPStatus: status, Code: "client_unparsable_error_body", Message: detail}
	case status == http.StatusNotFound:
		// unknown_operation or a missing route: version skew, not transient.
		return &Error{Kind: ErrProtocol, HTTPStatus: status, Code: "client_unparsable_error_body", Message: detail}
	case status >= 500:
		// The worker may have accepted the request before failing.
		return &Error{Kind: ErrOutcomeUnknown, HTTPStatus: status, Code: "client_unparsable_error_body", Message: detail}
	default:
		return &Error{Kind: ErrRemote, HTTPStatus: status, Code: "client_unparsable_error_body", Message: detail}
	}
}
