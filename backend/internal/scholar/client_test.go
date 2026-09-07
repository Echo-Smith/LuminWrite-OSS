package scholar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "go-test-token"

// hashOf is a test helper mirroring HashPayload's canonicalisation.
func hashOf(t *testing.T, payload map[string]any) string {
	t.Helper()
	h, err := HashPayload(payload)
	if err != nil {
		t.Fatalf("HashPayload: %v", err)
	}
	return h
}

// newTestClient returns a client pointed at an httptest server plus the
// server itself for URL registration.
func newTestClient(t *testing.T, server *httptest.Server, opts ...Option) *Client {
	t.Helper()
	c, err := NewClient(server.URL, testToken, append([]Option{WithHTTPClient(server.Client())}, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// mockWorkerHandler implements the success envelope with strict echo.
func mockWorkerHandler(t *testing.T, status int, respond func(*OperationRequest) map[string]any) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","retryable":false,"outcome_unknown":false}}`))
			return
		}
		var req OperationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_json","retryable":false,"outcome_unknown":false}}`))
			return
		}
		if req.OperationVersion != OperationVersion {
			t.Errorf("worker saw operation_version %q, want %q", req.OperationVersion, OperationVersion)
		}
		if req.DeadlineMs <= time.Now().UnixMilli() {
			t.Errorf("worker saw deadline_ms %d in the past", req.DeadlineMs)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(respond(&req))
	})
}

func successEnvelope(req *OperationRequest) map[string]any {
	return map[string]any{
		"request_id": req.RequestID,
		"input_hash": req.InputHash,
		"outputs":    map[string]any{"value": 42},
		"usage": map[string]any{
			"measured": true, "input_tokens": 10, "output_tokens": 5,
			"cost_usd": nil, "provider": "mock", "model": "mock-scholar-v0",
		},
		"warnings": []string{"mock"},
		"versions": map[string]string{"operation_version": "1"},
	}
}

func TestCallSuccess(t *testing.T) {
	srv := httptest.NewServer(mockWorkerHandler(t, http.StatusOK, successEnvelope))
	defer srv.Close()
	client := newTestClient(t, srv)

	payload := map[string]any{"query": "test"}
	resp, err := client.Call(context.Background(), OpDiscover, hashOf(t, payload), payload)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.RequestID == "" {
		t.Fatal("expected non-empty request_id")
	}
	var outputs struct {
		Value int `json:"value"`
	}
	if err := json.Unmarshal(resp.Outputs, &outputs); err != nil {
		t.Fatalf("decode outputs: %v", err)
	}
	if outputs.Value != 42 {
		t.Fatalf("outputs.value = %d, want 42", outputs.Value)
	}
	if resp.Usage.Measured != true || resp.Usage.Provider != "mock" {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
	if resp.Usage.CostUSD != nil {
		t.Fatalf("expected null cost_usd, got %v", *resp.Usage.CostUSD)
	}
}

func TestCallSendsBearerToken(t *testing.T) {
	var seenToken atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenToken.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(successEnvelope(&OperationRequest{RequestID: "x", InputHash: hashOf(t, nil)}))
	}))
	defer srv.Close()
	client := newTestClient(t, srv)

	payload := map[string]any{"a": 1}
	_, _ = client.Call(context.Background(), OpDiscover, hashOf(t, payload), payload)
	if got, _ := seenToken.Load().(string); got != "Bearer "+testToken {
		t.Fatalf("Authorization = %q, want bearer test token", got)
	}
}

func TestCall429ParsesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(mockWorkerHandler(t, http.StatusTooManyRequests, func(*OperationRequest) map[string]any {
		return map[string]any{"error": map[string]any{
			"code": "rate_limited", "retryable": true, "retry_after_ms": 1500,
			"outcome_unknown": false,
		}}
	}))
	defer srv.Close()
	client := newTestClient(t, srv)

	payload := map[string]any{"query": "x"}
	_, err := client.Call(context.Background(), OpDiscover, hashOf(t, payload), payload)
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if !errors.Is(err, ErrRemote) {
		t.Fatalf("want ErrRemote kind, got %v", err)
	}
	if !se.Retryable {
		t.Fatal("expected Retryable=true")
	}
	if se.RetryAfter != 1500*time.Millisecond {
		t.Fatalf("RetryAfter = %v, want 1.5s", se.RetryAfter)
	}
	if se.OutcomeUnknown() {
		t.Fatal("429 with outcome_unknown=false must not be outcome-unknown")
	}
}

func TestCall500OutcomeUnknown(t *testing.T) {
	srv := httptest.NewServer(mockWorkerHandler(t, http.StatusInternalServerError,
		func(*OperationRequest) map[string]any {
			return map[string]any{"error": map[string]any{
				"code": "internal_error", "retryable": false,
				"outcome_unknown": true,
			}}
		}))
	defer srv.Close()
	client := newTestClient(t, srv)

	payload := map[string]any{"query": "x"}
	_, err := client.Call(context.Background(), OpDiscover, hashOf(t, payload), payload)
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T", err)
	}
	if !se.OutcomeUnknown() || !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("want ErrOutcomeUnknown, got %v", err)
	}
	if se.Retryable {
		t.Fatal("outcome_unknown errors must not be retryable")
	}
}

func TestCallContextTimeoutMidFlight(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	client := newTestClient(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	payload := map[string]any{"query": "x"}
	_, err := client.Call(ctx, OpDiscover, hashOf(t, payload), payload)
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if !se.OutcomeUnknown() {
		t.Fatalf("mid-flight timeout must be outcome-unknown, got %v", err)
	}
	if !errors.Is(err, ErrDeadline) {
		t.Fatalf("want ErrDeadline joined, got %v", err)
	}
}

func TestCallDeadlineMsDerivedFromContext(t *testing.T) {
	var gotDeadline int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req OperationRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotDeadline = req.DeadlineMs
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(successEnvelope(&req))
	}))
	defer srv.Close()
	client := newTestClient(t, srv)

	at := time.Now().Add(3 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), at)
	defer cancel()
	payload := map[string]any{"query": "x"}
	if _, err := client.Call(ctx, OpDiscover, hashOf(t, payload), payload); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if gotDeadline != at.UnixMilli() {
		t.Fatalf("worker deadline_ms = %d, want %d", gotDeadline, at.UnixMilli())
	}
}

func TestCallMalformedJSON(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error // sentinel expected from a 200 body parse failure
	}{
		{"garbage-200", http.StatusOK, `not json at all`, ErrProtocol},
		{"wrong-shape-200", http.StatusOK, `{"unexpected":true}`, ErrProtocol},
		{"garbage-500", http.StatusInternalServerError, `<html>oops</html>`, ErrOutcomeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			client := newTestClient(t, srv)
			payload := map[string]any{"query": "x"}
			_, err := client.Call(context.Background(), OpDiscover, hashOf(t, payload), payload)
			if !errors.Is(err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, err)
			}
		})
	}
}

func TestCallRequestIDMismatch(t *testing.T) {
	srv := httptest.NewServer(mockWorkerHandler(t, http.StatusOK, func(req *OperationRequest) map[string]any {
		env := successEnvelope(req)
		env["request_id"] = "somebody-elses-request"
		return env
	}))
	defer srv.Close()
	client := newTestClient(t, srv, WithRequestIDFactory(func() string { return "my-request" }))

	payload := map[string]any{"query": "x"}
	_, err := client.Call(context.Background(), OpDiscover, hashOf(t, payload), payload)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("want ErrProtocol, got %v", err)
	}
	var se *Error
	if errors.As(err, &se) && se.Code != "client_request_id_mismatch" {
		t.Fatalf("code = %q, want client_request_id_mismatch", se.Code)
	}
}

func TestCallInputHashMismatch(t *testing.T) {
	srv := httptest.NewServer(mockWorkerHandler(t, http.StatusOK, func(req *OperationRequest) map[string]any {
		env := successEnvelope(req)
		env["input_hash"] = "sha256:" + strings.Repeat("ab", 32)
		return env
	}))
	defer srv.Close()
	client := newTestClient(t, srv)

	payload := map[string]any{"query": "x"}
	_, err := client.Call(context.Background(), OpDiscover, hashOf(t, payload), payload)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("want ErrProtocol, got %v", err)
	}
	var se *Error
	if errors.As(err, &se) && se.Code != "client_input_hash_mismatch" {
		t.Fatalf("code = %q, want client_input_hash_mismatch", se.Code)
	}
}

func TestCallDisconnectTruncatedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Emit a valid status and headers, then sever the connection before a
		// complete body: the worker may have executed the operation.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"request_id":`))
		controller := http.NewResponseController(w)
		if err := controller.Flush(); err != nil {
			t.Logf("flush: %v", err)
		}
		panic(http.ErrAbortHandler)
	}))
	defer srv.Close()
	client := newTestClient(t, srv)

	payload := map[string]any{"query": "x"}
	_, err := client.Call(context.Background(), OpDiscover, hashOf(t, payload), payload)
	if err == nil {
		t.Fatal("expected an error from a truncated response")
	}
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if !se.OutcomeUnknown() {
		t.Fatalf("disconnect after send must be outcome-unknown, got %v", err)
	}
}

func TestCallTransportErrorBeforeResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // guarantee connection refused

	client, err := NewClient(url, testToken)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	payload := map[string]any{"query": "x"}
	_, err = client.Call(context.Background(), OpDiscover, hashOf(t, payload), payload)
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if !se.OutcomeUnknown() {
		t.Fatalf("connection refused after send must be outcome-unknown, got %v", err)
	}
}

func TestCallUnauthorized(t *testing.T) {
	srv := httptest.NewServer(mockWorkerHandler(t, http.StatusOK, successEnvelope))
	defer srv.Close()
	client, err := NewClient(srv.URL, "wrong-token", WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	payload := map[string]any{"query": "x"}
	_, err = client.Call(context.Background(), OpDiscover, hashOf(t, payload), payload)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
}

func TestClientValidation(t *testing.T) {
	if _, err := NewClient("http://127.0.0.1:1", ""); err == nil {
		t.Fatal("empty token must be rejected")
	}
	if _, err := NewClient("not a url", testToken); err == nil {
		t.Fatal("invalid base URL must be rejected")
	}
	client, err := NewClient("http://127.0.0.1:1", testToken)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	payload := map[string]any{}
	if _, err := client.Call(context.Background(), Operation("teleport"), hashOf(t, payload), payload); !errors.Is(err, ErrProtocol) {
		t.Fatal("non-whitelisted operation must be rejected client-side")
	}
	if _, err := client.Call(context.Background(), OpDiscover, "deadbeef", payload); !errors.Is(err, ErrProtocol) {
		t.Fatal("malformed input_hash must be rejected client-side")
	}
}

func TestRateLimiterHook(t *testing.T) {
	var limitedOp Operation
	srv := httptest.NewServer(mockWorkerHandler(t, http.StatusOK, successEnvelope))
	defer srv.Close()
	client := newTestClient(t, srv, WithRateLimiter(stubLimiter{fn: func(ctx context.Context, op Operation) error {
		limitedOp = op
		return nil
	}}))
	payload := map[string]any{"query": "x"}
	if _, err := client.Call(context.Background(), OpDiscover, hashOf(t, payload), payload); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if limitedOp != OpDiscover {
		t.Fatalf("limiter saw %q, want discover", limitedOp)
	}
}

func TestRateLimiterRejectsBeforeSend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("worker must not be reached when the limiter rejects")
	}))
	defer srv.Close()
	client := newTestClient(t, srv, WithRateLimiter(stubLimiter{fn: func(ctx context.Context, op Operation) error {
		return fmt.Errorf("budget exhausted")
	}}))
	payload := map[string]any{"query": "x"}
	_, err := client.Call(context.Background(), OpDiscover, hashOf(t, payload), payload)
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if se.OutcomeUnknown() {
		t.Fatal("rejection before send must NOT be outcome-unknown")
	}
}

type stubLimiter struct {
	fn func(ctx context.Context, op Operation) error
}

func (s stubLimiter) Wait(ctx context.Context, op Operation) error { return s.fn(ctx, op) }

func TestHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	client := newTestClient(t, srv)
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
}
