package scholar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// In-process Client over the Go scholar executor. The public surface mirrors
// the former host-side HTTP client exactly — Client.Call + the typed
// operation methods + HashPayload/CallOption/OperationResponse — so the
// research runtime nodes (writingruntime/research_*.go) execute the five
// bounded operations in-process instead of over the private network with
// zero API adaptation.
//
// The baseURL/token constructor arguments are kept for wiring compatibility:
// baseURL (SCHOLAR_WORKER_URL) now acts purely as the research-path
// enablement signal and token (SCHOLAR_WORKER_TOKEN) stays a required
// configuration guard — both fail closed exactly as before.

const (
	// maxParseDocumentBytes is the parse-path document ceiling (the 25 MiB
	// design cap). The former base64-inlined HTTP transport also bounded the
	// encoded request; in-process the raw byte cap is authoritative.
	maxParseDocumentBytes = 25 * 1024 * 1024
)

var inputHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// RateLimiter is the injection point for provider call pacing and budget
// accounting. Wait must block until the operation may start, or return an
// error (including context.Canceled) to abort before any call is made.
type RateLimiter interface {
	Wait(ctx context.Context, op Operation) error
}

// Client executes the bounded scholar operations in-process.
type Client struct {
	exec       *Executor
	newID      func() string
	newIDKnown bool
}

// Option configures a Client.
type Option func(*Client)

// WithDocumentParser installs the binary-document text extractor (the
// docreader sidecar in production; a stub in tests). Required for the parse
// operation on application/pdf payloads.
func WithDocumentParser(p DocumentParser) Option {
	return func(c *Client) {
		if p != nil {
			c.exec.parser = p
		}
	}
}

// WithRateLimiter installs the pacing/budget hook.
func WithRateLimiter(l RateLimiter) Option {
	return func(c *Client) {
		if l != nil {
			c.exec.limiter = l
		}
	}
}

// WithLLMConfig overrides the environment-derived LLM configuration (tests
// inject stub endpoints; production derives from the SCHOLAR_LLM_* env).
func WithLLMConfig(cfg LLMConfig) Option {
	return func(c *Client) {
		if cfg.BaseURL != "" || cfg.Model != "" || cfg.APIKey != "" {
			c.exec.cfg = cfg
		}
	}
}

// WithRequestIDFactory overrides request-id generation (tests pin ids).
func WithRequestIDFactory(f func() string) Option {
	return func(c *Client) {
		if f != nil {
			c.newID = f
			c.newIDKnown = true
		}
	}
}

// NewClient builds the in-process client. The token must be non-empty (the
// deployment's service-auth guard, kept fail-closed); baseURL must be absent
// or a well-formed http(s) URL (it is no longer dialed).
func NewClient(baseURL, token string, opts ...Option) (*Client, error) {
	if token == "" {
		return nil, errors.New("scholar: token must not be empty")
	}
	c := &Client{exec: &Executor{}}
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

// WithRequestID pins the request id (callers re-driving a reconciled task
// reuse the original id so the outcome stays traceable).
func WithRequestID(id string) CallOption {
	return func(cfg *callConfig) { cfg.requestID = id }
}

// Call performs one bounded operation in-process.
//
//   - op must be whitelisted; inputHash must be "sha256:<64 hex>".
//   - The response request_id and input_hash echoes match the request (the
//     hash is recomputed from the payload by the caller's typed methods, so
//     the echo is exact by construction).
//   - Errors are classified per the taxonomy in errors.go; check
//     (*Error).OutcomeUnknown before deciding to retry (an LLM call may
//     have executed and billed even when its result never arrived).
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
	if requestID == "" && c.newIDKnown {
		requestID = c.newID()
	}

	var outputs any
	var usage Usage
	var warnings []string
	var err error
	switch op {
	case OpDiscover:
		outputs, usage, warnings, err = c.callDiscover(ctx, payload)
	case OpRank:
		outputs, usage, warnings, err = c.callRank(ctx, payload)
	case OpFetchFullText:
		outputs, usage, warnings, err = c.callFetchFullText(ctx, payload)
	case OpParse:
		outputs, usage, warnings, err = c.callParse(ctx, payload)
	case OpRead:
		outputs, usage, warnings, err = c.callRead(ctx, payload)
	}
	if err != nil {
		return nil, err
	}
	encoded, marshalErr := json.Marshal(outputs)
	if marshalErr != nil {
		return nil, &Error{Kind: ErrProtocol, Code: "client_marshal_outputs", Message: marshalErr.Error()}
	}
	return &OperationResponse{
		RequestID: requestID,
		InputHash: inputHash,
		Outputs:   encoded,
		Usage:     usage,
		Warnings:  warnings,
		Versions:  map[string]string{"operation_version": OperationVersion, "impl": "go-inprocess/1"},
	}, nil
}

// Health reports the in-process executor as always ready; kept for source
// compatibility with former worker probes (no model or network calls).
func (c *Client) Health(ctx context.Context) error { return nil }

// --- payload adapters (the envelope payloads formerly crossing the wire) --
//
// Payload values reach these adapters in two shapes: Go-native (what the typed
// methods and the writingruntime adapters build — []string, []map[string]any,
// int/int64) and JSON-decoded ([]any, float64). A typed assertion for only one
// shape silently drops the other, so every extraction accepts both.

// payloadStringSlice reads a payload string list ([]string or []any of string).
func payloadStringSlice(value any) []string {
	switch raw := value.(type) {
	case []string:
		return append([]string(nil), raw...)
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// payloadMapSlice reads a payload object list ([]map[string]any or []any of maps).
func payloadMapSlice(value any) []map[string]any {
	switch raw := value.(type) {
	case []map[string]any:
		return raw
	case []any:
		out := make([]map[string]any, 0, len(raw))
		for _, item := range raw {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// payloadInt64 reads a payload number (int, int64 or JSON-decoded float64).
func payloadInt64(value any) (int64, bool) {
	switch n := value.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	}
	return 0, false
}

func (c *Client) callDiscover(ctx context.Context, payload map[string]any) (any, Usage, []string, error) {
	query, _ := payload["query"].(string)
	allowlist := payloadStringSlice(payload["provider_allowlist"])
	limit := 10
	if n, ok := payloadInt64(payload["limit"]); ok {
		limit = int(n)
	}
	return c.exec.Discover(ctx, query, allowlist, limit)
}

func (c *Client) callRank(ctx context.Context, payload map[string]any) (any, Usage, []string, error) {
	question, _ := payload["research_question"].(string)
	raw := payloadMapSlice(payload["candidates"])
	candidates := make([]RankCandidate, 0, len(raw))
	for _, m := range raw {
		id, _ := m["paper_id"].(string)
		abstract, _ := m["abstract"].(string)
		candidates = append(candidates, RankCandidate{PaperID: id, Abstract: abstract})
	}
	return c.exec.Rank(ctx, question, candidates)
}

func (c *Client) callFetchFullText(ctx context.Context, payload map[string]any) (any, Usage, []string, error) {
	paperID, _ := payload["paper_id"].(string)
	oaURL, _ := payload["oa_url"].(string)
	var sizeLimit int64
	if n, ok := payloadInt64(payload["size_limit"]); ok {
		sizeLimit = n
	}
	return c.exec.FetchFullText(ctx, paperID, oaURL, sizeLimit)
}

func (c *Client) callParse(ctx context.Context, payload map[string]any) (any, Usage, []string, error) {
	encoded, _ := payload["document"].(string)
	mediaType, _ := payload["media_type"].(string)
	document, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, Usage{}, nil, &Error{Kind: ErrProtocol, Code: "client_bad_document_encoding",
			Message: "parse payload document is not valid base64: " + err.Error()}
	}
	if len(document) > maxParseDocumentBytes {
		return nil, Usage{}, nil, &Error{Kind: ErrProtocol, Code: "client_document_too_large",
			Message: fmt.Sprintf("document is %d bytes; the parse path allows at most %d bytes", len(document), maxParseDocumentBytes)}
	}
	if c.exec.parser == nil && mediaType == MediaTypePDF {
		return nil, Usage{}, nil, &Error{Kind: ErrRemote, Code: "parse_extractor_unconfigured",
			Message: "no document parser configured for application/pdf"}
	}
	outputs, err := ParseDocument(parsingContext{
		Content:     document,
		MediaType:   mediaType,
		ExtractText: c.exec.parser,
	})
	if err != nil {
		return nil, Usage{}, nil, err
	}
	return outputs, Usage{}, outputs.Warnings, nil
}

func (c *Client) callRead(ctx context.Context, payload map[string]any) (any, Usage, []string, error) {
	question, _ := payload["research_question"].(string)
	paperID, _ := payload["paper_id"].(string)
	cfg, ok := c.exec.llm()
	if !ok {
		return nil, Usage{}, nil, &ReaderError{Code: "llm_not_configured",
			Message: "paper reading requires SCHOLAR_LLM_BASE_URL, SCHOLAR_LLM_MODEL and SCHOLAR_LLM_API_KEY to be configured"}
	}
	rawBlocks := payloadMapSlice(payload["blocks"])
	blocks := make([]TextBlock, 0, len(rawBlocks))
	for _, m := range rawBlocks {
		id, _ := m["block_id"].(string)
		text, _ := m["text"].(string)
		hash, _ := m["block_hash"].(string)
		block := TextBlock{BlockID: id, Text: text, BlockHash: hash}
		if page, ok := payloadInt64(m["page"]); ok {
			block.Page = &page
		}
		blocks = append(blocks, block)
	}
	policyVersion := ""
	if policy, ok := payload["reader_policy"].(map[string]any); ok {
		policyVersion, _ = policy["reader_policy_version"].(string)
	}
	return ReadPaper(ctx, cfg, question, paperID, blocks, ReadPolicy{ReaderPolicyVersion: policyVersion})
}

// executorOf exposes the executor for tests.
func executorOf(c *Client) *Executor { return c.exec }

var _ = time.Second
