package scholar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

const (
	// DefaultTimeout bounds a single Call when the context carries no
	// deadline. Callers doing model-bound work (T04/T05) should always set
	// their own, budget-aware deadline.
	DefaultTimeout = 60 * time.Second

	// maxResponseBytes caps how much of a response body is read before the
	// client declares a protocol error.
	maxResponseBytes = 64 << 20 // 64 MiB

	contentTypeJSON = "application/json"
)

var inputHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// RateLimiter is the injection point for provider call pacing and budget
// accounting (wired to the host's provider configuration in T04). Wait must
// block until the operation may start, or return an error (including
// context.Canceled) to abort before any bytes hit the wire.
type RateLimiter interface {
	Wait(ctx context.Context, op Operation) error
}

// Client calls the private Scholar Worker over HTTP.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	limiter    RateLimiter
	newID      func() string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the underlying *http.Client (tests inject
// transports; production may tune dial/TLS settings). A nil client keeps the
// default with DefaultTimeout applied per request.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// WithRateLimiter installs the pacing/budget hook.
func WithRateLimiter(l RateLimiter) Option { return func(c *Client) { c.limiter = l } }

// WithRequestIDFactory overrides request-id generation (tests pin ids).
func WithRequestIDFactory(f func() string) Option {
	return func(c *Client) {
		if f != nil {
			c.newID = f
		}
	}
}

// NewClient builds a client for a private-network worker base URL.
func NewClient(baseURL, token string, opts ...Option) (*Client, error) {
	if token == "" {
		return nil, errors.New("scholar: token must not be empty")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("scholar: invalid worker base URL %q", baseURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("scholar: worker base URL must be http(s), got %q", parsed.Scheme)
	}
	c := &Client{
		baseURL:    baseURL,
		token:      token,
		httpClient: &http.Client{},
		newID:      NewRequestID,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// CallOption adjusts a single Call.
type CallOption func(*callConfig)

type callConfig struct {
	requestID string
}

// WithRequestID pins the request id (callers re-driving a reconciled task in
// T04 reuse the original id so the worker-side outcome stays traceable).
func WithRequestID(id string) CallOption {
	return func(cfg *callConfig) { cfg.requestID = id }
}

// Call performs one bounded operation and validates the response envelope.
//
//   - inputHash must be "sha256:<64 hex>" (see HashPayload).
//   - The context deadline (or DefaultTimeout) becomes both the HTTP deadline
//     and the worker-side deadline_ms, so worker and host clocks bound the
//     same absolute instant.
//   - The response request_id and input_hash echoes must match the request,
//     otherwise a protocol error is returned.
//
// Errors are classified per the taxonomy in errors.go; check
// (*Error).OutcomeUnknown before deciding to retry.
func (c *Client) Call(
	ctx context.Context,
	op Operation,
	inputHash string,
	payload map[string]any,
	opts ...CallOption,
) (*OperationResponse, error) {
	if !op.valid() {
		return nil, &Error{Kind: ErrProtocol, Code: "client_unknown_operation",
			Message: fmt.Sprintf("operation %q is not whitelisted", op)}
	}
	if !inputHashPattern.MatchString(inputHash) {
		return nil, &Error{Kind: ErrProtocol, Code: "client_invalid_input_hash",
			Message: "input_hash must match sha256:<64 lowercase hex chars>"}
	}

	cfg := &callConfig{}
	for _, o := range opts {
		o(cfg)
	}
	requestID := cfg.requestID
	if requestID == "" {
		requestID = c.newID()
	}

	// Deadline propagation: derive deadline_ms from the context; without one,
	// apply DefaultTimeout to a child context so both sides agree.
	deadline, ok := ctx.Deadline()
	if !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
		deadline, _ = ctx.Deadline()
	}

	if c.limiter != nil {
		if err := c.limiter.Wait(ctx, op); err != nil {
			return nil, &Error{Kind: ErrRemote, Code: "client_rate_limited",
				Message: fmt.Sprintf("rate limiter rejected operation before send: %v", err)}
		}
	}

	envelope := OperationRequest{
		RequestID:        requestID,
		InputHash:        inputHash,
		OperationVersion: OperationVersion,
		DeadlineMs:       deadline.UnixMilli(),
		Payload:          payload,
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, &Error{Kind: ErrProtocol, Code: "client_marshal_request", Message: err.Error()}
	}

	endpoint, err := operationURL(c.baseURL, op)
	if err != nil {
		return nil, &Error{Kind: ErrProtocol, Code: "client_bad_base_url", Message: err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, &Error{Kind: ErrProtocol, Code: "client_build_request", Message: err.Error()}
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", contentTypeJSON)

	// From here on the request may reach the worker, so any failure is
	// outcome-unknown: never report "not executed".
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, transportError(err)
	}
	defer resp.Body.Close()

	return decodeResponse(resp, requestID, inputHash)
}

// Health probes GET /healthz (no model calls on the worker side).
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return &Error{Kind: ErrProtocol, Code: "client_build_request", Message: err.Error()}
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return transportError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return classifyUnparsedStatus(resp.StatusCode, "healthz returned non-200")
	}
	return nil
}

// decodeResponse validates status, JSON shape, and the request_id/input_hash
// echo contract.
func decodeResponse(resp *http.Response, requestID, inputHash string) (*OperationResponse, error) {
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if readErr == nil && len(body) > maxResponseBytes {
		readErr = errors.New("response exceeds size limit")
	}
	if readErr != nil {
		// Truncated 200: the operation ran, but the result never arrived.
		if resp.StatusCode == http.StatusOK {
			return nil, &Error{Kind: ErrOutcomeUnknown, HTTPStatus: resp.StatusCode,
				Code: "client_truncated_response", Message: readErr.Error()}
		}
		return nil, classifyUnparsedStatus(resp.StatusCode, readErr.Error())
	}

	if resp.StatusCode != http.StatusOK {
		var wrapped struct {
			Error ErrorBody `json:"error"`
		}
		if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Error.Code != "" {
			return nil, newRemoteError(resp.StatusCode, &wrapped.Error)
		}
		return nil, classifyUnparsedStatus(resp.StatusCode, fmt.Sprintf("unparsable error body: %.200s", body))
	}

	var decoded struct {
		OperationResponse
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&decoded); err != nil {
		return nil, &Error{Kind: ErrProtocol, HTTPStatus: resp.StatusCode,
			Code: "client_malformed_success_body",
			Message: fmt.Sprintf("success response is not valid JSON: %v", err)}
	}
	got := decoded.OperationResponse

	// Strict echo validation: a mismatched request_id means the response may
	// belong to another request; a mismatched input_hash means the worker did
	// not process the payload we sent. Both are protocol violations.
	if got.RequestID == "" || got.RequestID != requestID {
		return nil, &Error{Kind: ErrProtocol, HTTPStatus: resp.StatusCode,
			Code: "client_request_id_mismatch",
			Message: fmt.Sprintf("response request_id %q does not match request %q", got.RequestID, requestID)}
	}
	if !inputHashPattern.MatchString(got.InputHash) || got.InputHash != inputHash {
		return nil, &Error{Kind: ErrProtocol, HTTPStatus: resp.StatusCode,
			Code: "client_input_hash_mismatch",
			Message: fmt.Sprintf("response input_hash %q does not match request %q", got.InputHash, inputHash)}
	}
	if len(got.Outputs) == 0 {
		return nil, &Error{Kind: ErrProtocol, HTTPStatus: resp.StatusCode,
			Code: "client_missing_outputs", Message: "success response has no outputs"}
	}
	return &got, nil
}

// transportError converts a request-send/receive failure. After the request
// has left the client the outcome is unknown by definition; a host-side
// deadline is flagged additionally via ErrDeadline.
func transportError(err error) *Error {
	e := &Error{Kind: ErrOutcomeUnknown, Code: "client_transport_error", Message: err.Error()}
	if errors.Is(err, context.DeadlineExceeded) {
		e.Kind = errors.Join(ErrOutcomeUnknown, ErrDeadline)
	} else if errors.Is(err, context.Canceled) {
		e.Code = "client_canceled"
	}
	return e
}
