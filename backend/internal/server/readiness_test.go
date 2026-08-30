package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReadinessDoesNotTreatConfiguredAsReachable(t *testing.T) {
	registry := NewReadinessRegistry(time.Minute)
	registry.Set("llm", CapabilityReadiness{Required: true, Installed: true, Configured: true})
	snapshot := registry.Snapshot(time.Now())
	if snapshot.Ready || snapshot.Components["llm"].Ready {
		t.Fatalf("configured but unprobed capability reported ready: %#v", snapshot)
	}
}

func TestReadinessRejectsStaleRequiredProbe(t *testing.T) {
	now := time.Date(2026, 8, 30, 2, 0, 0, 0, time.UTC)
	registry := NewReadinessRegistry(time.Minute)
	registry.Set("llm", CapabilityReadiness{Required: true, Installed: true, Configured: true,
		Reachable: true, LastCheckedAt: now.Add(-2 * time.Minute)})
	snapshot := registry.Snapshot(now)
	if snapshot.Ready || snapshot.Components["llm"].ErrorCode != readinessCodeStale {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestReadyHandlerReturnsServiceUnavailableWhenRequiredCapabilityIsBlocked(t *testing.T) {
	registry := NewReadinessRegistry(time.Minute)
	registry.Set("writing_runtime", CapabilityReadiness{Required: true, Installed: true,
		Configured: false, ErrorCode: readinessCodeNotWired})
	server := &Server{readiness: registry}
	recorder := httptest.NewRecorder()
	server.handleReady(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Success bool              `json:"success"`
		Data    ReadinessSnapshot `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Success || payload.Data.Ready {
		t.Fatalf("payload=%#v", payload)
	}
}
