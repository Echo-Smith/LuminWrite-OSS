package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

const (
	readinessCodeNotConfigured = "NOT_CONFIGURED"
	readinessCodeNotProbed     = "NOT_PROBED"
	readinessCodeStale         = "STALE"
	readinessCodeNotWired      = "NOT_WIRED"
	readinessCodeDisabled      = "DISABLED"
	readinessCodeUnreachable   = "UNREACHABLE"
)

// CapabilityReadiness separates installed code, deployment configuration,
// observed reachability, and the final traffic-readiness decision.
type CapabilityReadiness struct {
	Required      bool      `json:"required"`
	Installed     bool      `json:"installed"`
	Configured    bool      `json:"configured"`
	Reachable     bool      `json:"reachable"`
	Ready         bool      `json:"ready"`
	Degraded      bool      `json:"degraded"`
	ErrorCode     string    `json:"error_code,omitempty"`
	LastCheckedAt time.Time `json:"last_checked_at,omitempty"`
}

// ReadinessSnapshot is the stable deployment-facing readiness projection.
type ReadinessSnapshot struct {
	Ready      bool                           `json:"ready"`
	CheckedAt  time.Time                      `json:"checked_at"`
	Components map[string]CapabilityReadiness `json:"components"`
}

// ReadinessRegistry stores the latest bounded probe result for each runtime
// capability. It never converts configuration presence into reachability.
type ReadinessRegistry struct {
	mu         sync.RWMutex
	staleAfter time.Duration
	components map[string]CapabilityReadiness
}

func NewReadinessRegistry(staleAfter time.Duration) *ReadinessRegistry {
	if staleAfter <= 0 {
		staleAfter = time.Minute
	}
	return &ReadinessRegistry{
		staleAfter: staleAfter,
		components: make(map[string]CapabilityReadiness),
	}
}

func (r *ReadinessRegistry) Set(name string, status CapabilityReadiness) {
	if r == nil || name == "" {
		return
	}
	status.Ready = status.Installed && status.Configured && status.Reachable && status.ErrorCode == ""
	r.mu.Lock()
	r.components[name] = status
	r.mu.Unlock()
}

func (r *ReadinessRegistry) Snapshot(now time.Time) ReadinessSnapshot {
	snapshot := ReadinessSnapshot{
		Ready:      true,
		CheckedAt:  now,
		Components: make(map[string]CapabilityReadiness),
	}
	if r == nil {
		snapshot.Ready = false
		return snapshot
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.components) == 0 {
		snapshot.Ready = false
	}
	for name, original := range r.components {
		status := original
		status.Ready = status.Installed && status.Configured && status.Reachable && status.ErrorCode == ""
		if status.Reachable && status.LastCheckedAt.IsZero() {
			status.Ready = false
			status.ErrorCode = readinessCodeNotProbed
		}
		if status.Reachable && !status.LastCheckedAt.IsZero() && now.Sub(status.LastCheckedAt) > r.staleAfter {
			status.Ready = false
			status.ErrorCode = readinessCodeStale
		}
		if status.Required && !status.Ready {
			snapshot.Ready = false
		}
		snapshot.Components[name] = status
	}
	return snapshot
}

func (s *Server) initializeReadiness(now time.Time) {
	registry := NewReadinessRegistry(time.Minute)

	registry.Set("database", CapabilityReadiness{
		Required: true, Installed: true, Configured: s.dbAvail,
		Reachable: s.dbAvail, ErrorCode: readinessError(!s.dbAvail, readinessCodeNotConfigured),
		LastCheckedAt: checkedAt(s.dbAvail, now),
	})
	llmConfigured := s.llm != nil && s.llm.IsConfigured()
	registry.Set("llm", CapabilityReadiness{
		Required: true, Installed: true, Configured: llmConfigured,
		ErrorCode: readinessError(llmConfigured, readinessCodeNotProbed, readinessCodeNotConfigured),
	})

	externalSearchInstalled := s.search != nil && len(s.search.Capabilities()) > 0
	externalSearchConfigured := s.search != nil && s.search.HasExternalSources()
	registry.Set("external_search", CapabilityReadiness{
		Installed: externalSearchInstalled, Configured: externalSearchConfigured,
		ErrorCode: readinessError(externalSearchConfigured, readinessCodeNotProbed, readinessCodeDisabled),
	})
	registry.Set("crawler", CapabilityReadiness{
		Installed: true, Configured: true, Reachable: true, LastCheckedAt: now,
	})

	embeddingConfigured := s.embedding != nil && s.embedding.IsConfigured()
	registry.Set("embedding", CapabilityReadiness{
		Installed: true, Configured: embeddingConfigured,
		ErrorCode: readinessError(embeddingConfigured, readinessCodeNotProbed, readinessCodeDisabled),
	})
	registry.Set("knowledge", CapabilityReadiness{
		Installed: true, Configured: s.dbAvail, Reachable: s.dbAvail,
		Degraded: !embeddingConfigured, ErrorCode: readinessError(!s.dbAvail, readinessCodeNotConfigured),
		LastCheckedAt: checkedAt(s.dbAvail, now),
	})

	mcpConnected := s.mcpRegistry != nil && len(s.mcpRegistry.ServerNames()) > 0
	mcpConfigured := mcpConnected || s.mcpServer != nil
	mcpError := readinessCodeDisabled
	if mcpConfigured {
		mcpError = readinessCodeNotProbed
	}
	if mcpConnected {
		mcpError = ""
	}
	registry.Set("mcp", CapabilityReadiness{
		Installed: true, Configured: mcpConfigured, Reachable: mcpConnected,
		ErrorCode: mcpError, LastCheckedAt: checkedAt(mcpConnected, now),
	})

	// These three capabilities become ready only after Task13's production
	// composition root and durable shadow/evidence adapters are connected.
	registry.Set("writing_runtime", CapabilityReadiness{
		Required: true, Installed: s.writingAPI != nil, ErrorCode: readinessCodeNotWired,
	})
	registry.Set("evidence_store", CapabilityReadiness{
		Required: true, Installed: s.writingAPI != nil, ErrorCode: readinessCodeNotWired,
	})
	registry.Set("shadow_content", CapabilityReadiness{
		Required: true, ErrorCode: readinessCodeNotWired,
	})

	s.readiness = registry
}

// readinessError chooses a code without confusing an unprobed configured
// dependency with a missing or deliberately disabled dependency.
func readinessError(condition bool, codes ...string) string {
	if len(codes) == 0 {
		return ""
	}
	if condition {
		return codes[0]
	}
	if len(codes) > 1 {
		return codes[1]
	}
	return ""
}

func checkedAt(checked bool, now time.Time) time.Time {
	if checked {
		return now
	}
	return time.Time{}
}

func (s *Server) refreshLocalReadiness(now time.Time) {
	if s.readiness == nil {
		return
	}
	databaseReachable := false
	databaseError := readinessCodeNotConfigured
	if s.dbAvail && s.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := s.db.PingContext(ctx)
		cancel()
		databaseReachable = err == nil
		if databaseReachable {
			databaseError = ""
		} else {
			databaseError = readinessCodeUnreachable
		}
	}
	s.readiness.Set("database", CapabilityReadiness{
		Required: true, Installed: true, Configured: s.dbAvail,
		Reachable: databaseReachable, ErrorCode: databaseError,
		LastCheckedAt: checkedAt(databaseReachable, now),
	})
	s.readiness.Set("crawler", CapabilityReadiness{
		Installed: true, Configured: true, Reachable: true, LastCheckedAt: now,
	})
}

func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	s.refreshLocalReadiness(now)
	snapshot := s.readiness.Snapshot(now)
	status := http.StatusOK
	if !snapshot.Ready {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response.APIResponse{Success: snapshot.Ready, Data: snapshot})
}
