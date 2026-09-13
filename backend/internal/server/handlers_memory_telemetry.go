package server

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// handleAdminMemoryTelemetry GET /api/v2/admin/memory/telemetry?days=7
//
// P3 注入遥测汇总：注入率、拒答分解、tier 分布、按来源（pipeline /
// harness / tool）分解、显式捕获/提取/关闭计数。记忆系统三类静默
// 失败（检索窗口、NULL 吞行、证据死锁）的长期观测面。
func (s *Server) handleAdminMemoryTelemetry(w http.ResponseWriter, r *http.Request) {
	if s.memorySvc == nil || !s.memorySvc.IsAvailable() {
		response.Err(w, http.StatusServiceUnavailable, "memory_unavailable", "memory service not available")
		return
	}
	ts := s.memorySvc.Telemetry()
	if ts == nil {
		response.Err(w, http.StatusServiceUnavailable, "telemetry_unavailable", "telemetry store not available")
		return
	}

	days := 7
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 90 {
			days = n
		}
	}

	summary, err := ts.Summary(r.Context(), days)
	if err != nil {
		slog.Warn("memory telemetry summary failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "summary query failed")
		return
	}

	response.OK(w, summary)
}
