package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/mcp"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── MCP Tool Security Sandbox ──────────────────────────────
//
// The sandbox enforces per-tool security policies before MCP tool
// execution. Policies are stored in the database and cached in
// memory for fast lookup.
//
// Security dimensions:
//   1. Mode: allow / deny / conditional
//   2. Network: domain whitelist/blacklist (scanned in tool arguments)
//   3. Resource: max arg length, max result length, timeout
//   4. Rate limiting: calls per minute per tool
//
// When a violation is detected, the call is blocked and a violation
// record is persisted to mcp_tool_violations for audit.

// ─── Policy Types ─────────────────────────────────────────

// ToolPolicy is a security policy for an MCP tool.
type ToolPolicy struct {
	ID              string   `json:"id"`
	ServerName      string   `json:"server_name"`
	ToolName        string   `json:"tool_name"`
	Mode            string   `json:"mode"` // allow | deny | conditional
	AllowedDomains  []string `json:"allowed_domains"`
	BlockedDomains  []string `json:"blocked_domains"`
	MaxArgLength    int      `json:"max_arg_length"`
	MaxResultLength int      `json:"max_result_length"`
	TimeoutMs       int      `json:"timeout_ms"`
	RateLimitPerMin int      `json:"rate_limit_per_min"`
	Description     string   `json:"description"`
	IsActive        bool     `json:"is_active"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}

// ViolationRecord represents a logged policy violation.
type ViolationRecord struct {
	ID            string `json:"id"`
	PolicyID      string `json:"policy_id,omitempty"`
	ServerName    string `json:"server_name"`
	ToolName      string `json:"tool_name"`
	ViolationType string `json:"violation_type"` // blocked_domain | arg_too_large | timeout | rate_limit | denied
	Detail        string `json:"detail"`
	ArgsSummary   string `json:"args_summary"`
	TraceID       string `json:"trace_id"`
	UserID        string `json:"user_id"`
	CreatedAt     string `json:"created_at"`
}

// ─── Sandbox Engine ────────────────────────────────────────

// MCPSandbox evaluates tool calls against security policies.
type MCPSandbox struct {
	db *database.DB

	// In-memory policy cache: "server_name:tool_name" → *ToolPolicy
	// The wildcard "*" matches any server or tool.
	policiesMu sync.RWMutex
	policies   map[string]*ToolPolicy

	// Rate limiter: "server_name:tool_name" → []time.Time (call timestamps)
	rateMu  sync.Mutex
	rateLog map[string][]time.Time
}

// NewMCPSandbox creates a new sandbox instance.
func NewMCPSandbox(db *database.DB) *MCPSandbox {
	sb := &MCPSandbox{
		db:      db,
		policies: make(map[string]*ToolPolicy),
		rateLog: make(map[string][]time.Time),
	}
	if db != nil {
		sb.loadPoliciesFromDB()
	}
	return sb
}

// loadPoliciesFromDB loads all active policies into the in-memory cache.
func (sb *MCPSandbox) loadPoliciesFromDB() {
	if sb.db == nil {
		return
	}
	rows, err := sb.db.QueryContext(context.Background(), `
		SELECT id::text, server_name, tool_name, mode,
		       allowed_domains, blocked_domains,
		       max_arg_length, max_result_length, timeout_ms, rate_limit_per_min,
		       description, is_active, created_at::text, updated_at::text
		FROM mcp_tool_policies
		WHERE is_active = TRUE
	`)
	if err != nil {
		slog.Warn("sandbox: failed to load policies", "error", err)
		return
	}
	defer rows.Close()

	sb.policiesMu.Lock()
	defer sb.policiesMu.Unlock()

	for rows.Next() {
		var p ToolPolicy
		var allowedJSON, blockedJSON []byte
		if err := rows.Scan(&p.ID, &p.ServerName, &p.ToolName, &p.Mode,
			&allowedJSON, &blockedJSON,
			&p.MaxArgLength, &p.MaxResultLength, &p.TimeoutMs, &p.RateLimitPerMin,
			&p.Description, &p.IsActive, &p.CreatedAt, &p.UpdatedAt); err != nil {
			continue
		}
		if len(allowedJSON) > 0 {
			json.Unmarshal(allowedJSON, &p.AllowedDomains)
		}
		if len(blockedJSON) > 0 {
			json.Unmarshal(blockedJSON, &p.BlockedDomains)
		}
		key := sb.policyKey(p.ServerName, p.ToolName)
		sb.policies[key] = &p
	}

	slog.Info("sandbox: policies loaded", "count", len(sb.policies))
}

// policyKey generates a cache key for a policy.
func (sb *MCPSandbox) policyKey(serverName, toolName string) string {
	return serverName + ":" + toolName
}

// findPolicy finds the best matching policy for a server+tool combination.
// It first checks for an exact match, then falls back to server-level wildcard,
// then the global catch-all "*:*".
func (sb *MCPSandbox) findPolicy(serverName, toolName string) *ToolPolicy {
	sb.policiesMu.RLock()
	defer sb.policiesMu.RUnlock()

	// 1. Exact match
	if p, ok := sb.policies[serverName+":"+toolName]; ok {
		return p
	}
	// 2. Server wildcard for tool
	if p, ok := sb.policies[serverName+":*"]; ok {
		return p
	}
	// 3. Global catch-all
	if p, ok := sb.policies["*:*"]; ok {
		return p
	}
	return nil
}

// CheckResult is the outcome of a sandbox check.
type CheckResult struct {
	Allowed       bool
	Policy        *ToolPolicy
	ViolationType string
	Detail        string
}

// Check evaluates whether a tool call should be allowed.
// It checks: mode, domain restrictions, arg size, and rate limit.
func (sb *MCPSandbox) Check(serverName, toolName string, args map[string]any, traceID, userID string) CheckResult {
	policy := sb.findPolicy(serverName, toolName)
	if policy == nil {
		// No policy found — allow by default (fail-open for usability)
		return CheckResult{Allowed: true}
	}

	// 1. Mode check
	if policy.Mode == "deny" {
		sb.recordViolation(policy, serverName, toolName, "denied",
			"tool is denied by policy", args, traceID, userID)
		return CheckResult{Allowed: false, Policy: policy, ViolationType: "denied",
			Detail: fmt.Sprintf("tool %s/%s is denied by policy", serverName, toolName)}
	}

	// 2. Arg size check
	argsJSON, _ := json.Marshal(args)
	if len(argsJSON) > policy.MaxArgLength {
		detail := fmt.Sprintf("args length %d exceeds max %d", len(argsJSON), policy.MaxArgLength)
		sb.recordViolation(policy, serverName, toolName, "arg_too_large",
			detail, args, traceID, userID)
		return CheckResult{Allowed: false, Policy: policy, ViolationType: "arg_too_large", Detail: detail}
	}

	// 3. Domain check — scan args for URLs/domains
	argsStr := string(argsJSON)
	if violation := sb.checkDomains(policy, argsStr, serverName, toolName, args, traceID, userID); violation != nil {
		return *violation
	}

	// 4. Rate limit check
	if policy.RateLimitPerMin > 0 {
		if !sb.checkRateLimit(serverName, toolName, policy.RateLimitPerMin) {
			detail := fmt.Sprintf("rate limit %d/min exceeded for %s/%s", policy.RateLimitPerMin, serverName, toolName)
			sb.recordViolation(policy, serverName, toolName, "rate_limit",
				detail, args, traceID, userID)
			return CheckResult{Allowed: false, Policy: policy, ViolationType: "rate_limit", Detail: detail}
		}
	}

	return CheckResult{Allowed: true, Policy: policy}
}

// checkDomains scans the args string for blocked domains and enforces the allowed list.
func (sb *MCPSandbox) checkDomains(policy *ToolPolicy, argsStr, serverName, toolName string,
	args map[string]any, traceID, userID string) *CheckResult {
	// Check blocked domains first
	for _, domain := range policy.BlockedDomains {
		if domain != "" && strings.Contains(strings.ToLower(argsStr), strings.ToLower(domain)) {
			detail := fmt.Sprintf("blocked domain '%s' found in arguments", domain)
			sb.recordViolation(policy, serverName, toolName, "blocked_domain",
				detail, args, traceID, userID)
			return &CheckResult{Allowed: false, Policy: policy, ViolationType: "blocked_domain", Detail: detail}
		}
	}

	// If allowed_domains is non-empty, check that any URL/domain in args is in the allowlist
	if len(policy.AllowedDomains) > 0 {
		// Extract domain-like patterns from args (simple heuristic: look for URLs)
		foundDomains := extractDomains(argsStr)
		for _, d := range foundDomains {
			matched := false
			for _, allowed := range policy.AllowedDomains {
				if allowed != "" && strings.Contains(strings.ToLower(d), strings.ToLower(allowed)) {
					matched = true
					break
				}
			}
			if !matched {
				detail := fmt.Sprintf("domain '%s' not in allowed list", d)
				sb.recordViolation(policy, serverName, toolName, "blocked_domain",
					detail, args, traceID, userID)
				return &CheckResult{Allowed: false, Policy: policy, ViolationType: "blocked_domain", Detail: detail}
			}
		}
	}

	return nil
}

// checkRateLimit returns true if the call is within the rate limit.
func (sb *MCPSandbox) checkRateLimit(serverName, toolName string, limitPerMin int) bool {
	key := serverName + ":" + toolName
	now := time.Now()
	cutoff := now.Add(-time.Minute)

	sb.rateMu.Lock()
	defer sb.rateMu.Unlock()

	// Filter to only recent calls
	times := sb.rateLog[key]
	filtered := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}

	if len(filtered) >= limitPerMin {
		sb.rateLog[key] = filtered
		return false
	}

	filtered = append(filtered, now)
	sb.rateLog[key] = filtered
	return true
}

// recordViolation persists a violation record to the database.
func (sb *MCPSandbox) recordViolation(policy *ToolPolicy, serverName, toolName, violationType, detail string,
	args map[string]any, traceID, userID string) {
	if sb.db == nil {
		return
	}

	// Truncate args for audit storage
	argsJSON, _ := json.Marshal(args)
	argsSummary := string(argsJSON)
	if len(argsSummary) > 500 {
		argsSummary = argsSummary[:500] + "...(truncated)"
	}

	var policyID any
	if policy != nil {
		policyID = policy.ID
	}

	_, err := sb.db.ExecContext(context.Background(), `
		INSERT INTO mcp_tool_violations
			(policy_id, server_name, tool_name, violation_type, detail, args_summary, trace_id, user_id)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8)
	`, policyID, serverName, toolName, violationType, detail, argsSummary, traceID, userID)
	if err != nil {
		slog.Warn("sandbox: failed to record violation", "error", err)
	}
}

// TruncateResult truncates the tool output according to the policy.
func (sb *MCPSandbox) TruncateResult(serverName, toolName, output string) string {
	policy := sb.findPolicy(serverName, toolName)
	if policy == nil {
		// Default: truncate at 2000
		if len(output) > 2000 {
			return output[:2000] + "...(截断)"
		}
		return output
	}
	maxLen := policy.MaxResultLength
	if maxLen <= 0 {
		maxLen = 2000
	}
	if len(output) > maxLen {
		return output[:maxLen] + "...(截断)"
	}
	return output
}

// GetTimeout returns the timeout duration for a tool.
func (sb *MCPSandbox) GetTimeout(serverName, toolName string) time.Duration {
	policy := sb.findPolicy(serverName, toolName)
	if policy == nil || policy.TimeoutMs <= 0 {
		return 30 * time.Second
	}
	return time.Duration(policy.TimeoutMs) * time.Millisecond
}

// Reload refreshes the in-memory policy cache from the database.
func (sb *MCPSandbox) Reload() {
	sb.loadPoliciesFromDB()
}

// ─── Helpers ──────────────────────────────────────────────

// extractDomains tries to find domain-like patterns in a string.
// This is a simple heuristic — it looks for http(s):// URLs.
func extractDomains(s string) []string {
	var domains []string
	lower := strings.ToLower(s)
	// Simple URL extraction
	for {
		idx := strings.Index(lower, "http")
		if idx == -1 {
			break
		}
		rest := s[idx:]
		// Find end of URL (space, quote, or end)
		endIdx := len(rest)
		for _, delim := range []string{" ", "\"", "'", "\n", "\t", "}"} {
			if i := strings.Index(rest, delim); i > 0 && i < endIdx {
				endIdx = i
			}
		}
		url := rest[:endIdx]
		// Extract domain from URL
		domain := extractDomainFromURL(url)
		if domain != "" {
			domains = append(domains, domain)
		}
		// Move past this URL
		cut := idx + endIdx
		if cut >= len(s) {
			break
		}
		s = s[cut:]
		lower = lower[cut:]
	}
	return domains
}

// extractDomainFromURL extracts the host from a URL string.
func extractDomainFromURL(url string) string {
	// Strip scheme
	if i := strings.Index(url, "://"); i >= 0 {
		url = url[i+3:]
	}
	// Strip path
	if i := strings.Index(url, "/"); i >= 0 {
		url = url[:i]
	}
	// Strip port
	if i := strings.LastIndex(url, ":"); i >= 0 {
		url = url[:i]
	}
	return strings.ToLower(url)
}

// ─── Admin API: Policy CRUD ────────────────────────────────

// handleAdminListMCPToolPolicies returns all sandbox policies.
//
// GET /api/v2/admin/mcp/sandbox/policies
func (s *Server) handleAdminListMCPToolPolicies(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.OK(w, map[string]any{"policies": []any{}, "total": 0})
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id::text, server_name, tool_name, mode,
		       allowed_domains, blocked_domains,
		       max_arg_length, max_result_length, timeout_ms, rate_limit_per_min,
		       description, is_active, created_at::text, updated_at::text
		FROM mcp_tool_policies
		ORDER BY server_name, tool_name
	`)
	if err != nil {
		response.OK(w, map[string]any{"policies": []any{}, "total": 0})
		return
	}
	defer rows.Close()

	var policies []ToolPolicy
	for rows.Next() {
		var p ToolPolicy
		var allowedJSON, blockedJSON []byte
		if err := rows.Scan(&p.ID, &p.ServerName, &p.ToolName, &p.Mode,
			&allowedJSON, &blockedJSON,
			&p.MaxArgLength, &p.MaxResultLength, &p.TimeoutMs, &p.RateLimitPerMin,
			&p.Description, &p.IsActive, &p.CreatedAt, &p.UpdatedAt); err != nil {
			continue
		}
		if len(allowedJSON) > 0 {
			json.Unmarshal(allowedJSON, &p.AllowedDomains)
		}
		if len(blockedJSON) > 0 {
			json.Unmarshal(blockedJSON, &p.BlockedDomains)
		}
		policies = append(policies, p)
	}

	response.OK(w, map[string]any{"policies": policies, "total": len(policies)})
}

// handleAdminCreateMCPToolPolicy creates a new sandbox policy.
//
// POST /api/v2/admin/mcp/sandbox/policies
func (s *Server) handleAdminCreateMCPToolPolicy(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	var body struct {
		ServerName      string   `json:"server_name"`
		ToolName        string   `json:"tool_name"`
		Mode            string   `json:"mode"`
		AllowedDomains  []string `json:"allowed_domains"`
		BlockedDomains  []string `json:"blocked_domains"`
		MaxArgLength    *int     `json:"max_arg_length"`
		MaxResultLength *int     `json:"max_result_length"`
		TimeoutMs       *int     `json:"timeout_ms"`
		RateLimitPerMin *int     `json:"rate_limit_per_min"`
		Description     string   `json:"description"`
		IsActive        *bool    `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if body.ServerName == "" {
		body.ServerName = "*"
	}
	if body.ToolName == "" {
		body.ToolName = "*"
	}
	if body.Mode == "" {
		body.Mode = "allow"
	}

	allowedJSON, _ := json.Marshal(body.AllowedDomains)
	blockedJSON, _ := json.Marshal(body.BlockedDomains)

	var id string
	err := s.db.QueryRowContext(r.Context(), `
		INSERT INTO mcp_tool_policies
			(server_name, tool_name, mode, allowed_domains, blocked_domains,
			 max_arg_length, max_result_length, timeout_ms, rate_limit_per_min,
			 description, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (server_name, tool_name) DO UPDATE SET
			mode = EXCLUDED.mode,
			allowed_domains = EXCLUDED.allowed_domains,
			blocked_domains = EXCLUDED.blocked_domains,
			max_arg_length = EXCLUDED.max_arg_length,
			max_result_length = EXCLUDED.max_result_length,
			timeout_ms = EXCLUDED.timeout_ms,
			rate_limit_per_min = EXCLUDED.rate_limit_per_min,
			description = EXCLUDED.description,
			is_active = EXCLUDED.is_active,
			updated_at = NOW()
		RETURNING id::text
	`, body.ServerName, body.ToolName, body.Mode, allowedJSON, blockedJSON,
		coalesceInt(body.MaxArgLength, 10000),
		coalesceInt(body.MaxResultLength, 2000),
		coalesceInt(body.TimeoutMs, 30000),
		coalesceInt(body.RateLimitPerMin, 60),
		body.Description,
		coalesceBool(body.IsActive, true),
	).Scan(&id)
	if err != nil {
		slog.Warn("failed to create sandbox policy", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create policy")
		return
	}

	// Reload cache
	if s.sandbox != nil {
		s.sandbox.Reload()
	}

	s.writeAuditLog(r, "create_mcp_policy", "mcp_tool_policy", id,
		fmt.Sprintf("Created sandbox policy for %s/%s", body.ServerName, body.ToolName),
		map[string]any{"server_name": body.ServerName, "tool_name": body.ToolName, "mode": body.Mode})

	response.Created(w, map[string]any{"id": id, "message": "policy created"})
}

// handleAdminUpdateMCPToolPolicy updates a sandbox policy.
//
// PUT /api/v2/admin/mcp/sandbox/policies/{id}
func (s *Server) handleAdminUpdateMCPToolPolicy(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		ServerName      *string  `json:"server_name"`
		ToolName        *string  `json:"tool_name"`
		Mode            *string  `json:"mode"`
		AllowedDomains  []string `json:"allowed_domains"`
		BlockedDomains  []string  `json:"blocked_domains"`
		MaxArgLength    *int     `json:"max_arg_length"`
		MaxResultLength *int     `json:"max_result_length"`
		TimeoutMs       *int     `json:"timeout_ms"`
		RateLimitPerMin *int     `json:"rate_limit_per_min"`
		Description     *string  `json:"description"`
		IsActive        *bool    `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	// Build dynamic update
	setParts := []string{}
	args := []any{}
	argIdx := 1

	addStr := func(col string, val *string) {
		if val != nil {
			setParts = append(setParts, fmt.Sprintf("%s = $%d", col, argIdx))
			args = append(args, *val)
			argIdx++
		}
	}
	addInt := func(col string, val *int) {
		if val != nil {
			setParts = append(setParts, fmt.Sprintf("%s = $%d", col, argIdx))
			args = append(args, *val)
			argIdx++
		}
	}
	addBool := func(col string, val *bool) {
		if val != nil {
			setParts = append(setParts, fmt.Sprintf("%s = $%d", col, argIdx))
			args = append(args, *val)
			argIdx++
		}
	}
	addJSON := func(col string, val []string) {
		if val != nil {
			j, _ := json.Marshal(val)
			setParts = append(setParts, fmt.Sprintf("%s = $%d", col, argIdx))
			args = append(args, j)
			argIdx++
		}
	}

	addStr("server_name", body.ServerName)
	addStr("tool_name", body.ToolName)
	addStr("mode", body.Mode)
	addJSON("allowed_domains", body.AllowedDomains)
	addJSON("blocked_domains", body.BlockedDomains)
	addInt("max_arg_length", body.MaxArgLength)
	addInt("max_result_length", body.MaxResultLength)
	addInt("timeout_ms", body.TimeoutMs)
	addInt("rate_limit_per_min", body.RateLimitPerMin)
	addStr("description", body.Description)
	addBool("is_active", body.IsActive)

	if len(setParts) == 0 {
		response.Err(w, http.StatusBadRequest, "bad_request", "no fields to update")
		return
	}

	setParts = append(setParts, fmt.Sprintf("updated_at = NOW()"))
	args = append(args, id)

	query := fmt.Sprintf(`
		UPDATE mcp_tool_policies SET %s WHERE id = $%d::uuid
	`, strings.Join(setParts, ", "), argIdx)

	_, err := s.db.ExecContext(r.Context(), query, args...)
	if err != nil {
		slog.Warn("failed to update sandbox policy", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update policy")
		return
	}

	// Reload cache
	if s.sandbox != nil {
		s.sandbox.Reload()
	}

	s.writeAuditLog(r, "update_mcp_policy", "mcp_tool_policy", id,
		"Updated sandbox policy", map[string]any{"id": id})

	response.OK(w, map[string]any{"id": id, "message": "policy updated"})
}

// handleAdminDeleteMCPToolPolicy deletes a sandbox policy.
//
// DELETE /api/v2/admin/mcp/sandbox/policies/{id}
func (s *Server) handleAdminDeleteMCPToolPolicy(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}
	id := chi.URLParam(r, "id")
	_, err := s.db.ExecContext(r.Context(), `DELETE FROM mcp_tool_policies WHERE id = $1::uuid`, id)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete policy")
		return
	}
	// Reload cache
	if s.sandbox != nil {
		s.sandbox.Reload()
	}
	s.writeAuditLog(r, "delete_mcp_policy", "mcp_tool_policy", id,
		"Deleted sandbox policy", map[string]any{"id": id})
	response.OK(w, map[string]any{"message": "policy deleted"})
}

// ─── Admin API: Violation Log ──────────────────────────────

// handleAdminListMCPViolations returns violation records.
//
// GET /api/v2/admin/mcp/sandbox/violations
func (s *Server) handleAdminListMCPViolations(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.OK(w, map[string]any{"violations": []any{}, "total": 0})
		return
	}

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := parseIntSafe(l); err == nil && v > 0 && v <= 500 {
			limit = v
		}
	}

	serverFilter := r.URL.Query().Get("server")
	typeFilter := r.URL.Query().Get("type")

	query := `
		SELECT id::text, COALESCE(policy_id::text, ''), server_name, tool_name,
		       violation_type, detail, args_summary, trace_id, user_id, created_at::text
		FROM mcp_tool_violations
	`
	conditions := []string{}
	args := []any{}
	argIdx := 1
	if serverFilter != "" {
		conditions = append(conditions, fmt.Sprintf("server_name = $%d", argIdx))
		args = append(args, serverFilter)
		argIdx++
	}
	if typeFilter != "" {
		conditions = append(conditions, fmt.Sprintf("violation_type = $%d", argIdx))
		args = append(args, typeFilter)
		argIdx++
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT %d", limit)

	rows, err := s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		response.OK(w, map[string]any{"violations": []any{}, "total": 0})
		return
	}
	defer rows.Close()

	var violations []ViolationRecord
	for rows.Next() {
		var v ViolationRecord
		if err := rows.Scan(&v.ID, &v.PolicyID, &v.ServerName, &v.ToolName,
			&v.ViolationType, &v.Detail, &v.ArgsSummary, &v.TraceID, &v.UserID, &v.CreatedAt); err != nil {
			continue
		}
		violations = append(violations, v)
	}

	response.OK(w, map[string]any{"violations": violations, "total": len(violations)})
}

// handleAdminGetSandboxStats returns summary statistics for the sandbox.
//
// GET /api/v2/admin/mcp/sandbox/stats
func (s *Server) handleAdminGetSandboxStats(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.OK(w, map[string]any{"policy_count": 0, "violation_count_24h": 0, "violations_by_type": map[string]int{}})
		return
	}

	// Policy count
	var policyCount int
	s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM mcp_tool_policies WHERE is_active = TRUE`).Scan(&policyCount)

	// Violations in last 24h
	var violationCount24h int
	s.db.QueryRowContext(r.Context(), `
		SELECT COUNT(*) FROM mcp_tool_violations WHERE created_at > NOW() - INTERVAL '24 hours'
	`).Scan(&violationCount24h)

	// Violations by type in last 24h
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT violation_type, COUNT(*) as cnt
		FROM mcp_tool_violations
		WHERE created_at > NOW() - INTERVAL '24 hours'
		GROUP BY violation_type
	`)
	if err != nil {
		response.OK(w, map[string]any{
			"policy_count":       policyCount,
			"violation_count_24h": violationCount24h,
			"violations_by_type": map[string]int{},
		})
		return
	}
	defer rows.Close()

	violationsByType := make(map[string]int)
	for rows.Next() {
		var vType string
		var cnt int
		rows.Scan(&vType, &cnt)
		violationsByType[vType] = cnt
	}

	response.OK(w, map[string]any{
		"policy_count":        policyCount,
		"violation_count_24h": violationCount24h,
		"violations_by_type":  violationsByType,
	})
}

// handleAdminTestSandbox simulates a sandbox check without executing the tool.
//
// POST /api/v2/admin/mcp/sandbox/test
func (s *Server) handleAdminTestSandbox(w http.ResponseWriter, r *http.Request) {
	if s.sandbox == nil {
		response.Err(w, http.StatusServiceUnavailable, "sandbox_unavailable", "sandbox not initialized")
		return
	}
	var body struct {
		ServerName string         `json:"server_name"`
		ToolName   string         `json:"tool_name"`
		Args       map[string]any `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	result := s.sandbox.Check(body.ServerName, body.ToolName, body.Args, "sandbox-test", "admin")
	response.OK(w, map[string]any{
		"allowed":         result.Allowed,
		"violation_type":  result.ViolationType,
		"detail":          result.Detail,
		"policy_applied":  result.Policy != nil,
	})
}

// handleAdminReloadSandbox forces a policy cache reload.
//
// POST /api/v2/admin/mcp/sandbox/reload
func (s *Server) handleAdminReloadSandbox(w http.ResponseWriter, r *http.Request) {
	if s.sandbox == nil {
		response.Err(w, http.StatusServiceUnavailable, "sandbox_unavailable", "sandbox not initialized")
		return
	}
	s.sandbox.Reload()
	s.writeAuditLog(r, "reload_mcp_sandbox", "mcp_sandbox", "", "Reloaded sandbox policies", nil)
	response.OK(w, map[string]any{"message": "sandbox policies reloaded"})
}

// ─── Helper ────────────────────────────────────────────────

func parseIntSafe(s string) (int, error) {
	var v int
	_, err := fmt.Sscanf(s, "%d", &v)
	return v, err
}

// ─── SandboxHook Adapter ──────────────────────────────────
//
// mcpSandboxAdapter adapts *MCPSandbox to the mcp.SandboxHook interface.
// This bridges the server-layer sandbox with the mcp-layer tool execution.

type mcpSandboxAdapter struct{ sb *MCPSandbox }

func (a *mcpSandboxAdapter) Check(serverName, toolName string, args map[string]any, traceID, userID string) mcp.SandboxCheckResult {
	r := a.sb.Check(serverName, toolName, args, traceID, userID)
	return mcp.SandboxCheckResult{
		Allowed:       r.Allowed,
		ViolationType: r.ViolationType,
		Detail:        r.Detail,
	}
}

func (a *mcpSandboxAdapter) TruncateResult(serverName, toolName, output string) string {
	return a.sb.TruncateResult(serverName, toolName, output)
}

func (a *mcpSandboxAdapter) GetTimeout(serverName, toolName string) time.Duration {
	return a.sb.GetTimeout(serverName, toolName)
}

// Ensure MCPSandbox can be adapted to mcp.SandboxHook
var _ mcp.SandboxHook = (*mcpSandboxAdapter)(nil)
