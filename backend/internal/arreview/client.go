// Package arreview adapts the AutoResearch AR-012 standalone scientific-review
// sidecar (Proprietary, deployed out-of-band from a workspace-local fork; see
// specs/research-review/ar012-sidecar.md) to the governed writing runtime.
//
// License discipline (requirements.md L56): this package contains only
// LuminBuddy-owned protocol code. The upstream Python service never enters
// the OSS or commercial repositories or images — the Go side talks to it over
// its published HTTP contract and a shared exchange volume.
//
// Design constraints (docs/plans/2026-09-07-research-review-integration.md
// T10, design.md §8):
//   - The sidecar is synchronous and has no cancel API, so any transport
//     failure after the request is sent is outcome-unknown by definition.
//   - Idempotency keys and surrogate project ids are derived deterministically
//     from frozen input hashes (idempotency.go), so a timed-out job reconciles
//     by re-POSTing the identical request instead of blind re-dispatch.
//   - The candidate manuscript this service produces is evaluation-only: it is
//     imported as an isolated Artifact and must never reach the document
//     delivery path (A18).
package arreview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	// DefaultRunTimeout bounds StartRun when the context carries no deadline.
	// The sidecar pipeline issues up to five LLM calls (plan, draft, whole
	// document revision, two bounded repairs); upstream's own client timeout
	// default is 650s. Five calls at 180s each is 900s worst case, so the
	// default sits above that with headroom.
	DefaultRunTimeout = 25 * time.Minute

	maxResponseBytes = 16 << 20 // 16 MiB: manuscript + receipts, never binaries

	contentTypeJSON = "application/json"
)

// Run modes supported by the sidecar. The LuminBuddy integration only
// dispatches GenerateOnly; CompareOnly and GenerateAndCompare exist upstream
// and are kept typed for future evaluation work.
type RunMode string

const (
	ModeGenerateOnly       RunMode = "generate_only"
	ModeCompareOnly        RunMode = "compare_only"
	ModeGenerateAndCompare RunMode = "generate_and_compare"
)

// RunStatus is the sidecar's terminal-aware record status. A 201 response can
// still carry StatusFailed — the HTTP status alone never means success.
type RunStatus string

const (
	StatusPending   RunStatus = "pending"
	StatusRunning   RunStatus = "running"
	StatusCompleted RunStatus = "completed"
	StatusFailed    RunStatus = "failed"
)

// Error classes for sidecar calls.
var (
	// ErrOutcomeUnknown: the request may have reached the sidecar and may
	// still be running (transport error, timeout, 5xx). Callers must
	// reconcile via ListRuns/GetRun instead of assuming failure.
	ErrOutcomeUnknown = errors.New("arreview: sidecar outcome unknown")
	// ErrProtocol: request/response shape violations detected before any
	// sidecar work can be trusted (bad path, malformed JSON).
	ErrProtocol = errors.New("arreview: sidecar protocol error")
	// ErrRemote: the sidecar definitively rejected or failed the run.
	ErrRemote = errors.New("arreview: sidecar run failed")
	// ErrNotFound: the requested run record does not exist.
	ErrNotFound = errors.New("arreview: review run not found")
)

// Error carries the failure classification for one sidecar interaction.
type Error struct {
	Kind       error
	Code       string
	Message    string
	HTTPStatus int
}

func (e *Error) Error() string {
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("arreview: %s (%s, http %d)", e.Message, e.Code, e.HTTPStatus)
	}
	return fmt.Sprintf("arreview: %s (%s)", e.Message, e.Code)
}

func (e *Error) Unwrap() error { return e.Kind }

// RunRequest mirrors the sidecar's ReviewRunRequest pydantic model.
type RunRequest struct {
	ProjectID            string  `json:"project_id"`
	Mode                 RunMode `json:"mode"`
	CorpusRelativePath   string  `json:"corpus_relative_path"`
	BaselineRelativePath string  `json:"baseline_relative_path"`
	IdempotencyKey       string  `json:"idempotency_key"`
}

// RunRecord mirrors the sidecar's ReviewRunRecord response model.
type RunRecord struct {
	ReviewRunID    string            `json:"review_run_id"`
	ProjectID      string            `json:"project_id"`
	Mode           RunMode           `json:"mode"`
	Status         RunStatus         `json:"status"`
	IdempotencyKey string            `json:"idempotency_key"`
	RequestedAt    *time.Time        `json:"requested_at"`
	StartedAt      *time.Time        `json:"started_at"`
	CompletedAt    *time.Time        `json:"completed_at"`
	Artifacts      map[string]string `json:"artifacts"`
	Quality        json.RawMessage   `json:"quality"`
	Model          json.RawMessage   `json:"model"`
	ErrorType      *string           `json:"error_type"`
	ErrorMessage   *string           `json:"error_message"`
}

// Health is a decoded GET /health payload (subset; unknown fields ignored).
type Health struct {
	OK        bool   `json:"ok"`
	Service   string `json:"service"`
	Version   string `json:"version"`
	Execution string `json:"execution"`
	Provider  struct {
		State      string `json:"state"`
		Configured bool   `json:"configured"`
		Name       string `json:"name"`
		Model      string `json:"model"`
	} `json:"provider"`
}

// Client calls the AR-012 sidecar over HTTP. The sidecar has no
// authentication; isolation comes from private-network placement (compose
// internal network, no published ports), which NewClient documents by
// rejecting non-http(s) base URLs.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the underlying *http.Client (tests inject
// transports).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// NewClient builds a client for a private-network sidecar base URL.
func NewClient(baseURL string, opts ...Option) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("%w: invalid base URL %q", ErrProtocol, baseURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: base URL must be http(s), got %q", ErrProtocol, parsed.Scheme)
	}
	c := &Client{baseURL: baseURL, httpClient: &http.Client{}}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// StartRun POSTs one review run. The sidecar executes synchronously; the
// context deadline (or DefaultRunTimeout) bounds the whole call. The
// returned record is evaluated before it is returned: a 201/200 body whose
// record status is failed surfaces as ErrRemote carrying the sidecar's own
// error fields — the HTTP status alone never means success — and a
// non-terminal record surfaces as ErrOutcomeUnknown.
func (c *Client) StartRun(ctx context.Context, req RunRequest) (*RunRecord, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal request: %v", ErrProtocol, err)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultRunTimeout)
		defer cancel()
	}
	record, err := c.do(ctx, http.MethodPost, "/review-runs", body, http.StatusCreated, http.StatusOK)
	if err != nil {
		return nil, err
	}
	if err := Evaluate(record); err != nil {
		return record, err
	}
	return record, nil
}

// GetRun fetches one persisted record by id.
func (c *Client) GetRun(ctx context.Context, reviewRunID string) (*RunRecord, error) {
	if reviewRunID == "" {
		return nil, fmt.Errorf("%w: empty review run id", ErrProtocol)
	}
	return c.do(ctx, http.MethodGet, "/review-runs/"+url.PathEscape(reviewRunID), nil, http.StatusOK)
}

// ListRuns lists the records of one surrogate project — the reconciliation
// path for jobs whose synchronous response was lost (timeout, restart).
func (c *Client) ListRuns(ctx context.Context, projectID string) ([]RunRecord, error) {
	if projectID == "" {
		return nil, fmt.Errorf("%w: empty project id", ErrProtocol)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/projects/"+url.PathEscape(projectID)+"/review-runs", nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrProtocol, err)
	}
	req.Header.Set("Accept", contentTypeJSON)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, transportError(err)
	}
	defer resp.Body.Close()
	payload, err := readAll(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, classifyStatus(resp.StatusCode, payload)
	}
	var records []RunRecord
	if err := json.Unmarshal(payload, &records); err != nil {
		return nil, fmt.Errorf("%w: list response is not a JSON array: %v", ErrProtocol, err)
	}
	return records, nil
}

// Health probes GET /health (no model calls on the sidecar side).
func (c *Client) Health(ctx context.Context) (*Health, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrProtocol, err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, transportError(err)
	}
	defer resp.Body.Close()
	payload, err := readAll(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, classifyStatus(resp.StatusCode, payload)
	}
	var health Health
	if err := json.Unmarshal(payload, &health); err != nil {
		return nil, fmt.Errorf("%w: health response is not valid JSON: %v", ErrProtocol, err)
	}
	return &health, nil
}

// do performs one JSON round-trip and decodes a RunRecord body.
func (c *Client) do(ctx context.Context, method, path string, body []byte, wantStatus ...int) (*RunRecord, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrProtocol, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", contentTypeJSON)
	}
	req.Header.Set("Accept", contentTypeJSON)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, transportError(err)
	}
	defer resp.Body.Close()
	payload, err := readAll(resp)
	if err != nil {
		return nil, err
	}
	ok := len(wantStatus) == 0
	for _, status := range wantStatus {
		if resp.StatusCode == status {
			ok = true
			break
		}
	}
	if !ok {
		return nil, classifyStatus(resp.StatusCode, payload)
	}
	var record RunRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return nil, fmt.Errorf("%w: response is not valid JSON: %v", ErrProtocol, err)
	}
	if record.ReviewRunID == "" {
		return nil, fmt.Errorf("%w: response record has no review_run_id", ErrProtocol)
	}
	return &record, nil
}

// Evaluate records decodes the sidecar's terminal state into a Go-level
// verdict: completed records return normally; failed records become ErrRemote
// carrying the sidecar's own error fields; non-terminal records surface as
// ErrOutcomeUnknown so the caller keeps waiting instead of double-running.
func Evaluate(record *RunRecord) error {
	if record == nil {
		return fmt.Errorf("%w: nil record", ErrProtocol)
	}
	switch record.Status {
	case StatusCompleted:
		return nil
	case StatusFailed:
		message := ""
		if record.ErrorMessage != nil {
			message = *record.ErrorMessage
		}
		errorType := "unknown"
		if record.ErrorType != nil {
			errorType = *record.ErrorType
		}
		return &Error{Kind: ErrRemote, Code: "sidecar_" + errorType,
			Message: fmt.Sprintf("sidecar record %s failed: %s", record.ReviewRunID, message)}
	default:
		return fmt.Errorf("%w: record %s is %s", ErrOutcomeUnknown, record.ReviewRunID, record.Status)
	}
}

func readAll(resp *http.Response) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
			return nil, fmt.Errorf("%w: truncated response: %v", ErrOutcomeUnknown, err)
		}
		return nil, classifyStatus(resp.StatusCode, []byte(err.Error()))
	}
	if len(payload) > maxResponseBytes {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", ErrOutcomeUnknown, maxResponseBytes)
	}
	return payload, nil
}

func classifyStatus(status int, body []byte) error {
	switch {
	case status == http.StatusNotFound:
		return ErrNotFound
	case status == http.StatusBadRequest:
		return fmt.Errorf("%w: bad request: %.300s", ErrProtocol, body)
	case status == http.StatusConflict:
		// Upstream returns 409 for idempotency-signature conflicts; the
		// derived-key scheme makes this a caller bug, so it is definitive.
		return fmt.Errorf("%w: idempotency conflict: %.300s", ErrRemote, body)
	case status >= 500:
		return fmt.Errorf("%w: sidecar http %d: %.300s", ErrOutcomeUnknown, status, body)
	default:
		return fmt.Errorf("%w: unexpected http %d: %.300s", ErrProtocol, status, body)
	}
}

func transportError(err error) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: canceled before completion: %v", ErrOutcomeUnknown, err)
	}
	return fmt.Errorf("%w: transport: %v", ErrOutcomeUnknown, err)
}
