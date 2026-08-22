package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Security Event DB Recorder ──────────────────────────
//
// Implements engine.SecurityEventRecorder to persist prompt injection
// interceptions to the security_events table. Writes are non-blocking
// (buffered channel) to avoid slowing down the request path.

type securityEventDBRecorder struct {
	db  *database.DB
	buf chan engine.SecurityEvent
	wg  sync.WaitGroup
}

func newSecurityEventDBRecorder(db *database.DB) *securityEventDBRecorder {
	r := &securityEventDBRecorder{
		db:  db,
		buf: make(chan engine.SecurityEvent, 256),
	}
	r.wg.Add(1)
	go r.flushLoop()
	return r
}

func (r *securityEventDBRecorder) Record(event engine.SecurityEvent) {
	select {
	case r.buf <- event:
	default:
		slog.Warn("security event buffer full, dropping event",
			"source", event.Source, "pattern_count", event.PatternCount)
	}
}

func (r *securityEventDBRecorder) flushLoop() {
	defer r.wg.Done()
	batch := make([]engine.SecurityEvent, 0, 64)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := r.writeBatch(batch); err != nil {
			slog.Warn("failed to persist security events", "count", len(batch), "error", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case event, ok := <-r.buf:
			if !ok {
				flush()
				return
			}
			batch = append(batch, event)
			if len(batch) >= 64 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (r *securityEventDBRecorder) writeBatch(events []engine.SecurityEvent) error {
	ctx := context.Background()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO security_events
			(event_type, source, pattern_count, pattern_types, content_snippet, trace_id, user_id, session_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range events {
		typesJSON, _ := json.Marshal(e.PatternTypes)
		if len(typesJSON) == 0 {
			typesJSON = []byte("[]")
		}
		_, err := stmt.ExecContext(ctx, e.EventType, e.Source, e.PatternCount, typesJSON, e.ContentSnippet, e.TraceID, e.UserID, e.SessionID, e.Timestamp)
		if err != nil {
			slog.Warn("failed to insert security event", "error", err, "source", e.Source)
		}
	}

	return tx.Commit()
}

// ─── Admin: Security Audit Dashboard ──────────────────────
//
// GET /api/v2/admin/security/audit
//
// Returns comprehensive security audit data:
//   - In-memory real-time counters (since process start)
//   - DB-persisted historical stats (24h / 7d / 30d)
//   - Recent interceptions from both in-memory and DB
//   - Attack category breakdown
//   - MCP sandbox violation summary

func (s *Server) handleAdminSecurityAudit(w http.ResponseWriter, r *http.Request) {
	// ── Real-time in-memory stats ──
	externalCount, userCount, uniqueSources, recentInMemory := engine.GetInjectionStats()

	// Convert recent in-memory interceptions
	recentList := make([]map[string]any, 0, len(recentInMemory))
	for _, entry := range recentInMemory {
		recentList = append(recentList, map[string]any{
			"source":        entry.Source,
			"pattern_count": entry.PatternCount,
			"timestamp":     entry.Timestamp,
		})
	}

	// ── DB-persisted stats (if available) ──
	dbStats := s.getSecurityDBStats()
	dbRecent := s.getSecurityDBRecent(20)

	// Merge: DB recent first (older), then in-memory (newer)
	if len(dbRecent) > 0 {
		recentList = append(dbRecent, recentList...)
	}

	// ── MCP sandbox violation summary ──
	mcpSummary := s.getMCPSandboxSummary()

	response.OK(w, map[string]any{
		// Real-time counters (since process restart)
		"external_content_interceptions": externalCount,
		"user_input_interceptions":       userCount,
		"unique_sources":                 uniqueSources,
		"total_interceptions":            externalCount + userCount,
		"recent_interceptions":           recentList,

		// DB-persisted stats
		"db_stats": dbStats,

		// MCP sandbox
		"mcp_sandbox": mcpSummary,

		// Defense layers
		"defense_layers": []map[string]string{
			{"name": "input_sanitization", "desc": "SanitizeExternalContent + SanitizeUserInput", "status": "active"},
			{"name": "system_prompt_directive", "desc": "7 defense rules injected into LLM system prompt", "status": "active"},
			{"name": "world_state_security_section", "desc": "SecuritySection in WorldState context", "status": "active"},
			{"name": "mcp_sandbox", "desc": "Per-tool network/resource/rate-limit policies", "status": "active"},
			{"name": "red_team_evaluation", "desc": "20 adversarial test cases (PromptInjection + SearchInjection)", "status": "active"},
		},

		// Pattern category labels for frontend display
		"pattern_categories": map[string]string{
			"direct_override":    "指令覆盖",
			"identity_override":  "身份劫持",
			"prompt_extraction":  "提示词窃取",
			"fake_system_msg":    "伪造系统消息",
			"credential_extract": "凭据窃取",
			"instruction_chain":  "指令链注入",
		},
	})
}

// getSecurityDBStats returns aggregated stats from the security_events table.
func (s *Server) getSecurityDBStats() map[string]any {
	if s.db == nil {
		return map[string]any{"available": false}
	}
	ctx := context.Background()

	var totalAll, total24h, total7d int64
	var ext24h, user24h int64

	// Total events
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM security_events`).Scan(&totalAll)

	// 24h stats
	_ = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE event_type = 'external_content'),
		       COUNT(*) FILTER (WHERE event_type = 'user_input')
		FROM security_events
		WHERE created_at >= NOW() - INTERVAL '24 hours'
	`).Scan(&total24h, &ext24h, &user24h)

	// 7d stats
	_ = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM security_events
		WHERE created_at >= NOW() - INTERVAL '7 days'
	`).Scan(&total7d)

	// Hourly trend (last 24h, grouped by hour)
	trend := s.getSecurityTrend24h()

	// Category breakdown (last 7 days)
	categoryBreakdown := s.getSecurityCategoryBreakdown()

	// Top sources (last 7 days)
	topSources := s.getSecurityTopSources()

	return map[string]any{
		"available":          true,
		"total_all":          totalAll,
		"total_24h":          total24h,
		"total_7d":           total7d,
		"external_24h":       ext24h,
		"user_input_24h":    user24h,
		"trend_24h":          trend,
		"category_breakdown": categoryBreakdown,
		"top_sources":        topSources,
	}
}

// getSecurityTrend24h returns hourly interception counts for the last 24 hours.
func (s *Server) getSecurityTrend24h() []map[string]any {
	ctx := context.Background()
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			DATE_TRUNC('hour', created_at) AS hour,
			COUNT(*) AS count,
			COUNT(*) FILTER (WHERE event_type = 'external_content') AS ext_count,
			COUNT(*) FILTER (WHERE event_type = 'user_input') AS user_count
		FROM security_events
		WHERE created_at >= NOW() - INTERVAL '24 hours'
		GROUP BY hour
		ORDER BY hour
	`)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()

	result := []map[string]any{}
	for rows.Next() {
		var hour time.Time
		var count, extCount, userCount int64
		if err := rows.Scan(&hour, &count, &extCount, &userCount); err != nil {
			continue
		}
		result = append(result, map[string]any{
			"hour":          hour.Format("15:04"),
			"count":         count,
			"ext_count":     extCount,
			"user_count":    userCount,
		})
	}
	return result
}

// getSecurityCategoryBreakdown returns attack category counts for the last 7 days.
func (s *Server) getSecurityCategoryBreakdown() []map[string]any {
	ctx := context.Background()
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			jsonb_array_elements_text(pattern_types) AS category,
			COUNT(*) AS count
		FROM security_events
		WHERE created_at >= NOW() - INTERVAL '7 days'
		  AND jsonb_array_length(pattern_types) > 0
		GROUP BY category
		ORDER BY count DESC
	`)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()

	result := []map[string]any{}
	for rows.Next() {
		var category string
		var count int64
		if err := rows.Scan(&category, &count); err != nil {
			continue
		}
		result = append(result, map[string]any{
			"category": category,
			"count":    count,
		})
	}
	return result
}

// getSecurityTopSources returns the top interception sources (last 7 days).
func (s *Server) getSecurityTopSources() []map[string]any {
	ctx := context.Background()
	rows, err := s.db.QueryContext(ctx, `
		SELECT source, COUNT(*) AS count
		FROM security_events
		WHERE created_at >= NOW() - INTERVAL '7 days'
		GROUP BY source
		ORDER BY count DESC
		LIMIT 10
	`)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()

	result := []map[string]any{}
	for rows.Next() {
		var source string
		var count int64
		if err := rows.Scan(&source, &count); err != nil {
			continue
		}
		// Truncate long source names for display
		if len(source) > 60 {
			source = source[:57] + "..."
		}
		result = append(result, map[string]any{
			"source": source,
			"count":  count,
		})
	}
	return result
}

// getSecurityDBRecent returns recent security events from the DB.
func (s *Server) getSecurityDBRecent(limit int) []map[string]any {
	if s.db == nil {
		return []map[string]any{}
	}
	ctx := context.Background()

	rows, err := s.db.QueryContext(ctx, `
		SELECT event_type, source, pattern_count, pattern_types, content_snippet, created_at
		FROM security_events
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()

	result := []map[string]any{}
	for rows.Next() {
		var eventType, source string
		var patternCount int
		var patternTypes []byte
		var snippet string
		var createdAt time.Time
		if err := rows.Scan(&eventType, &source, &patternCount, &patternTypes, &snippet, &createdAt); err != nil {
			continue
		}

		var cats []string
		_ = json.Unmarshal(patternTypes, &cats)
		if cats == nil {
			cats = []string{}
		}

		// Truncate snippet for display
		if len(snippet) > 120 {
			snippet = snippet[:117] + "..."
		}

		result = append(result, map[string]any{
			"source":        source,
			"event_type":    eventType,
			"pattern_count": patternCount,
			"categories":    cats,
			"snippet":       snippet,
			"timestamp":     createdAt,
		})
	}
	return result
}

// getMCPSandboxSummary returns MCP sandbox violation stats for the security dashboard.
func (s *Server) getMCPSandboxSummary() map[string]any {
	if s.db == nil {
		return map[string]any{"available": false}
	}
	ctx := context.Background()

	var totalViolations, violations24h int64
	var activePolicies int

	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_tool_violations`).Scan(&totalViolations)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_tool_violations WHERE created_at >= NOW() - INTERVAL '24 hours'`).Scan(&violations24h)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_tool_policies WHERE is_active = true`).Scan(&activePolicies)

	// Violation breakdown by type
	typeBreakdown := []map[string]any{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT violation_type, COUNT(*) AS count
		FROM mcp_tool_violations
		WHERE created_at >= NOW() - INTERVAL '7 days'
		GROUP BY violation_type
		ORDER BY count DESC
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var vType string
			var count int64
			if err := rows.Scan(&vType, &count); err != nil {
				continue
			}
			typeBreakdown = append(typeBreakdown, map[string]any{
				"type":  vType,
				"count": count,
			})
		}
	}

	return map[string]any{
		"available":           true,
		"active_policies":      activePolicies,
		"total_violations":     totalViolations,
		"violations_24h":       violations24h,
		"violation_types_7d":   typeBreakdown,
	}
}

// ─── Security Events List Endpoint ────────────────────────
//
// GET /api/v2/admin/security/events?type=external_content&limit=50&offset=0
//
// Returns paginated security events from the DB with filtering.

func (s *Server) handleAdminSecurityEvents(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.OK(w, map[string]any{"events": []any{}, "total": 0})
		return
	}

	eventType := r.URL.Query().Get("type")
	limit := 50
	offset := 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := parseIntSafe(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := parseIntSafe(o); err == nil && n >= 0 {
			offset = n
		}
	}

	// Build query with optional type filter
	var query string
	var args []any
	if eventType != "" {
		query = `
			SELECT id, event_type, source, pattern_count, pattern_types, content_snippet, trace_id, user_id, created_at
			FROM security_events
			WHERE event_type = $1
			ORDER BY created_at DESC
			LIMIT $2 OFFSET $3
		`
		args = []any{eventType, limit, offset}
	} else {
		query = `
			SELECT id, event_type, source, pattern_count, pattern_types, content_snippet, trace_id, user_id, created_at
			FROM security_events
			ORDER BY created_at DESC
			LIMIT $1 OFFSET $2
		`
		args = []any{limit, offset}
	}

		rows, err := s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "db_error", "failed to query security events")
		return
	}
	defer rows.Close()

	events := []map[string]any{}
	for rows.Next() {
		var id int64
		var evType, source string
		var patternCount int
		var patternTypes []byte
		var snippet, traceID, userID string
		var createdAt time.Time
		if err := rows.Scan(&id, &evType, &source, &patternCount, &patternTypes, &snippet, &traceID, &userID, &createdAt); err != nil {
			continue
		}

		var cats []string
		_ = json.Unmarshal(patternTypes, &cats)
		if cats == nil {
			cats = []string{}
		}

		// Truncate snippet for display
		if len(snippet) > 200 {
			snippet = snippet[:197] + "..."
		}

		events = append(events, map[string]any{
			"id":            id,
			"event_type":    evType,
			"source":        source,
			"pattern_count": patternCount,
			"categories":    cats,
			"snippet":       snippet,
			"trace_id":      traceID,
			"user_id":       userID,
			"created_at":   createdAt,
		})
	}

	// Get total count
	var total int64
	countQuery := `SELECT COUNT(*) FROM security_events`
	if eventType != "" {
		countQuery += ` WHERE event_type = $1`
		_ = s.db.QueryRowContext(r.Context(), countQuery, eventType).Scan(&total)
	} else {
		_ = s.db.QueryRowContext(r.Context(), countQuery).Scan(&total)
	}

	response.OK(w, map[string]any{
		"events": events,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// ─── Helper: format security event source for display ────

func formatSecuritySource(source string) string {
	if strings.HasPrefix(source, "search_result[") {
		return "搜索结果"
	}
	if strings.HasPrefix(source, "compress_step:") {
		return "压缩步骤"
	}
	if source == "user_input" {
		return "用户输入"
	}
	return source
}
