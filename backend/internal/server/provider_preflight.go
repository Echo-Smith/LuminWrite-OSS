package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

const (
	preflightCodeAuthRejected      = "PROVIDER_AUTH_REJECTED"
	preflightCodeRateLimited       = "PROVIDER_RATE_LIMITED"
	preflightCodeTimeout           = "PROVIDER_TIMEOUT"
	preflightCodeMalformedResponse = "PROVIDER_MALFORMED_RESPONSE"
	preflightCodeUnavailable       = "PROVIDER_UNAVAILABLE"
)

type ProviderProbeResult struct {
	ProviderID string    `json:"provider_id"`
	Component  string    `json:"component"`
	Configured bool      `json:"configured"`
	Reachable  bool      `json:"reachable"`
	ErrorCode  string    `json:"error_code,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

type ProviderPreflightSnapshot struct {
	CheckedAt time.Time                      `json:"checked_at,omitempty"`
	Results   map[string]ProviderProbeResult `json:"results"`
}

// ProviderPreflight owns all credential-bearing readiness probes. Probe
// results contain only stable provider IDs/codes; URLs, keys, response bodies,
// and transport error strings never enter readiness or API responses.
type ProviderPreflight struct {
	config     config.ProviderPreflightConfig
	readiness  *ReadinessRegistry
	search     *tools.SearchClient
	llmBaseURL string
	llmAPIKey  string
	llmReady   bool
	embedURL   string
	embedKey   string
	embedReady bool
	client     *http.Client
	now        func() time.Time
	refreshMCP func(time.Time)

	mu   sync.RWMutex
	last ProviderPreflightSnapshot
}

func NewProviderPreflight(cfg *config.Config, readiness *ReadinessRegistry, search *tools.SearchClient, refreshMCP func(time.Time)) *ProviderPreflight {
	if cfg == nil || readiness == nil {
		return nil
	}
	timeout := cfg.ProviderPreflight.Timeout
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 8 * time.Second
	}
	llmConfigured := usableProviderSecret(cfg.DeepSeek.APIKey)
	embedConfigured := usableProviderSecret(cfg.Dashscope.APIKey)
	return &ProviderPreflight{config: cfg.ProviderPreflight, readiness: readiness, search: search,
		llmBaseURL: cfg.DeepSeek.BaseURL, llmAPIKey: cfg.DeepSeek.APIKey, llmReady: llmConfigured,
		embedURL: cfg.Dashscope.BaseURL, embedKey: cfg.Dashscope.APIKey, embedReady: embedConfigured,
		client: &http.Client{Timeout: timeout}, now: func() time.Time { return time.Now().UTC() }, refreshMCP: refreshMCP,
		last: ProviderPreflightSnapshot{Results: map[string]ProviderProbeResult{}},
	}
}

func usableProviderSecret(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return normalized != "" && normalized != "placeholder" && normalized != "your-api-key" && !strings.HasPrefix(normalized, "your-")
}

func (preflight *ProviderPreflight) Run(ctx context.Context) ProviderPreflightSnapshot {
	if preflight == nil {
		return ProviderPreflightSnapshot{Results: map[string]ProviderProbeResult{}}
	}
	checkedAt := preflight.now()
	results := map[string]ProviderProbeResult{}

	llm := preflight.probeJSON(ctx, "llm", "llm", preflight.llmBaseURL, preflight.llmAPIKey, preflight.llmReady, checkedAt)
	results[llm.ProviderID] = llm
	preflight.readiness.Set("llm", readinessFromProbe(true, true, llm))

	embedding := preflight.probeJSON(ctx, "embedding", "embedding", preflight.embedURL, preflight.embedKey, preflight.embedReady, checkedAt)
	results[embedding.ProviderID] = embedding
	preflight.readiness.Set("embedding", readinessFromProbe(false, true, embedding))

	searchResults := []tools.SearchProbeResult{}
	if preflight.search != nil {
		searchTimeout := preflight.config.Timeout
		if searchTimeout <= 0 || searchTimeout > 30*time.Second {
			searchTimeout = 8 * time.Second
		}
		searchCtx, cancelSearch := context.WithTimeout(ctx, searchTimeout)
		searchResults = preflight.search.ProbeExternalSources(searchCtx)
		cancelSearch()
	}
	configuredSearch := 0
	reachableSearch := 0
	for _, item := range searchResults {
		configuredSearch++
		if item.Reachable {
			reachableSearch++
		}
		results["search:"+item.ProviderID] = ProviderProbeResult{ProviderID: item.ProviderID,
			Component: "external_search", Configured: true, Reachable: item.Reachable,
			ErrorCode: item.ErrorCode, CheckedAt: checkedAt}
	}
	searchInstalled := preflight.search != nil && len(preflight.search.Capabilities()) > 0
	searchConfigured := configuredSearch > 0
	searchReachable := reachableSearch > 0
	searchCode := readinessCodeDisabled
	if searchConfigured {
		searchCode = preflightCodeUnavailable
	}
	if searchReachable {
		searchCode = ""
	}
	preflight.readiness.Set("external_search", CapabilityReadiness{Installed: searchInstalled,
		Configured: searchConfigured, Reachable: searchReachable,
		Degraded:  reachableSearch > 0 && reachableSearch < configuredSearch,
		ErrorCode: searchCode, LastCheckedAt: checkedAt})

	if preflight.refreshMCP != nil {
		preflight.refreshMCP(checkedAt)
	}
	snapshot := ProviderPreflightSnapshot{CheckedAt: checkedAt, Results: results}
	preflight.mu.Lock()
	preflight.last = snapshot
	preflight.mu.Unlock()
	return cloneProviderPreflightSnapshot(snapshot)
}

func readinessFromProbe(required, installed bool, result ProviderProbeResult) CapabilityReadiness {
	return CapabilityReadiness{Required: required, Installed: installed, Configured: result.Configured,
		Reachable: result.Reachable, ErrorCode: result.ErrorCode, LastCheckedAt: result.CheckedAt}
}

func (preflight *ProviderPreflight) probeJSON(ctx context.Context, providerID, component, baseURL, apiKey string, configured bool, checkedAt time.Time) ProviderProbeResult {
	result := ProviderProbeResult{ProviderID: providerID, Component: component, Configured: configured, CheckedAt: checkedAt}
	if !configured {
		result.ErrorCode = readinessCodeNotConfigured
		return result
	}
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/models"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		result.ErrorCode = preflightCodeUnavailable
		return result
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+apiKey)
	providerResponse, err := preflight.client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.ErrorCode = preflightCodeTimeout
		} else {
			result.ErrorCode = preflightCodeUnavailable
		}
		return result
	}
	defer providerResponse.Body.Close()
	switch providerResponse.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		result.ErrorCode = preflightCodeAuthRejected
		return result
	case http.StatusTooManyRequests:
		result.ErrorCode = preflightCodeRateLimited
		return result
	}
	if providerResponse.StatusCode < 200 || providerResponse.StatusCode >= 300 {
		result.ErrorCode = preflightCodeUnavailable
		return result
	}
	var payload any
	decoder := json.NewDecoder(io.LimitReader(providerResponse.Body, 1<<20))
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		result.ErrorCode = preflightCodeMalformedResponse
		return result
	}
	result.Reachable = true
	return result
}

func (preflight *ProviderPreflight) Snapshot() ProviderPreflightSnapshot {
	if preflight == nil {
		return ProviderPreflightSnapshot{Results: map[string]ProviderProbeResult{}}
	}
	preflight.mu.RLock()
	defer preflight.mu.RUnlock()
	return cloneProviderPreflightSnapshot(preflight.last)
}

func cloneProviderPreflightSnapshot(snapshot ProviderPreflightSnapshot) ProviderPreflightSnapshot {
	clone := ProviderPreflightSnapshot{CheckedAt: snapshot.CheckedAt, Results: make(map[string]ProviderProbeResult, len(snapshot.Results))}
	for key, value := range snapshot.Results {
		clone.Results[key] = value
	}
	return clone
}

func (preflight *ProviderPreflight) Start(ctx context.Context) {
	if preflight == nil || !preflight.config.Enabled {
		return
	}
	interval := preflight.config.Interval
	if interval < 5*time.Minute {
		interval = 5 * time.Minute
	}
	go func() {
		preflight.Run(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				preflight.Run(ctx)
			}
		}
	}()
}

func (s *Server) handleAdminRunProviderPreflight(w http.ResponseWriter, r *http.Request) {
	if s.providerPreflight == nil {
		response.Err(w, http.StatusServiceUnavailable, "PROVIDER_PREFLIGHT_UNAVAILABLE", "provider preflight is not configured")
		return
	}
	response.OK(w, s.providerPreflight.Run(r.Context()))
}

func (s *Server) handleAdminGetProviderPreflight(w http.ResponseWriter, _ *http.Request) {
	if s.providerPreflight == nil {
		response.Err(w, http.StatusServiceUnavailable, "PROVIDER_PREFLIGHT_UNAVAILABLE", "provider preflight is not configured")
		return
	}
	response.OK(w, s.providerPreflight.Snapshot())
}
