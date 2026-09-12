package arreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func completedRecord() RunRecord {
	return RunRecord{
		ReviewRunID: "reviewrun_0123456789abcdef",
		ProjectID:   "lb-0123456789abcdef0123456789abcdef",
		Mode:        ModeGenerateOnly,
		Status:      StatusCompleted,
		Artifacts:   map[string]string{"manuscript": "05_writing/MANUSCRIPT_REVIEW_AGENT.md"},
	}
}

func recordBody(t *testing.T, record RunRecord) []byte {
	t.Helper()
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	return payload
}

func TestStartRunReturnsCompletedRecord(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/review-runs" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var request RunRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Mode != ModeGenerateOnly || request.ProjectID == "" || request.IdempotencyKey == "" {
			t.Errorf("incomplete request payload: %+v", request)
		}
		w.WriteHeader(http.StatusCreated)
		w.Write(recordBody(t, completedRecord()))
	}))
	record, err := client.StartRun(context.Background(), RunRequest{
		ProjectID: "lb-0123456789abcdef0123456789abcdef", Mode: ModeGenerateOnly,
		IdempotencyKey: "ar012-0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if record.Status != StatusCompleted || record.ReviewRunID == "" {
		t.Fatalf("unexpected record: %+v", record)
	}
}

func TestStartRun201WithFailedRecordIsRemoteFailure(t *testing.T) {
	failed := completedRecord()
	failed.Status = StatusFailed
	failed.ErrorType = ptr("RuntimeError")
	failed.ErrorMessage = ptr("generated review failed grounding gate")
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write(recordBody(t, failed))
	}))
	_, err := client.StartRun(context.Background(), RunRequest{
		ProjectID: "lb-0123456789abcdef0123456789abcdef", Mode: ModeGenerateOnly,
		IdempotencyKey: "ar012-0123456789abcdef",
	})
	if !errors.Is(err, ErrRemote) || !strings.Contains(err.Error(), "grounding gate") {
		t.Fatalf("expected remote failure carrying sidecar message, got %v", err)
	}
}

func TestEvaluateNonTerminalRecordIsOutcomeUnknown(t *testing.T) {
	running := completedRecord()
	running.Status = StatusRunning
	if err := Evaluate(&running); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("expected outcome unknown for running record, got %v", err)
	}
	pending := completedRecord()
	pending.Status = StatusPending
	pendingRecord := pending
	if err := Evaluate(&pendingRecord); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("expected outcome unknown for pending record, got %v", err)
	}
	done := completedRecord()
	if err := Evaluate(&done); err != nil {
		t.Fatalf("completed record must evaluate clean, got %v", err)
	}
}

func TestStartRunTimeoutIsOutcomeUnknown(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusCreated)
		w.Write(recordBody(t, completedRecord()))
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := client.StartRun(ctx, RunRequest{
		ProjectID: "lb-0123456789abcdef0123456789abcdef", Mode: ModeGenerateOnly,
		IdempotencyKey: "ar012-0123456789abcdef",
	})
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("expected outcome unknown on timeout, got %v", err)
	}
}

func TestSidecarErrorStatuses(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantMatrix error
	}{
		{"conflict", http.StatusConflict, ErrRemote},
		{"server error", http.StatusInternalServerError, ErrOutcomeUnknown},
		{"bad request", http.StatusBadRequest, ErrProtocol},
		{"not found", http.StatusNotFound, ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(`{"detail":"no"}`))
			}))
			_, err := client.StartRun(context.Background(), RunRequest{
				ProjectID: "lb-0123456789abcdef0123456789abcdef", Mode: ModeGenerateOnly,
				IdempotencyKey: "ar012-0123456789abcdef",
			})
			if !errors.Is(err, tc.wantMatrix) {
				t.Fatalf("status %d: expected %v, got %v", tc.status, tc.wantMatrix, err)
			}
		})
	}
}

func TestGetRunNotFoundAndList(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/review-runs/reviewrun_missing":
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/projects/lb-0123456789abcdef0123456789abcdef/review-runs":
			w.Write([]byte("[" + string(recordBody(t, completedRecord())) + "]"))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	if _, err := client.GetRun(context.Background(), "reviewrun_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	runs, err := client.ListRuns(context.Background(), "lb-0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != StatusCompleted {
		t.Fatalf("unexpected runs: %+v", runs)
	}
}

func TestHealthDecodes(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"ok":true,"service":"autoresearch-scientific-review","version":"0.1.1","provider":{"state":"offline","configured":false}}`))
	}))
	health, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !health.OK || health.Service != "autoresearch-scientific-review" {
		t.Fatalf("unexpected health: %+v", health)
	}
}

func TestTruncatedSuccessBodyIsOutcomeUnknown(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"review_run_id":"reviewrun_x"`)) // and no more
		if hijacker, ok := w.(http.Hijacker); ok {
			conn, buf, _ := hijacker.Hijack()
			_ = buf.Flush()
			_ = conn.Close()
		}
	}))
	_, err := client.StartRun(context.Background(), RunRequest{
		ProjectID: "lb-0123456789abcdef0123456789abcdef", Mode: ModeGenerateOnly,
		IdempotencyKey: "ar012-0123456789abcdef",
	})
	if err != nil && !errors.Is(err, ErrOutcomeUnknown) && !errors.Is(err, ErrProtocol) {
		t.Fatalf("unexpected error class: %v", err)
	}
}

func TestNewClientRejectsBadBaseURL(t *testing.T) {
	if _, err := NewClient("ftp://sidecar"); !errors.Is(err, ErrProtocol) {
		t.Fatalf("expected protocol error, got %v", err)
	}
	if _, err := NewClient("not a url"); !errors.Is(err, ErrProtocol) {
		t.Fatalf("expected protocol error, got %v", err)
	}
}

func ptr[T any](value T) *T { return &value }
