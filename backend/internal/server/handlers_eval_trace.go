package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Merged Trace Handlers (Eval Center) ────────────────────────────────────
//
// Registered under /api/v2/admin/evaluation/wabench/... so the trace view reads
// the same agent_traces rows that WABench outputs point at via trace_ref. This
// is the Phase-1 unification: one join (wabench_runs -> wabench_outputs.trace_ref
// -> agent_traces.{llm_calls,tool_calls,pipeline_metadata,estimated_cost}) serves
// the Eval Center, benchmark and trace API alike.

// handleGetWABenchRunTrace returns the enhanced trace for one WABench run.
// GET /api/v2/admin/evaluation/wabench/runs/{runId}/trace
func (s *Server) handleGetWABenchRunTrace(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runId")
	if runID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "runId is required")
		return
	}
	if s.evalRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "evaluation repository not available")
		return
	}

	summary, analyses, err := s.evalRepo.GetWABenchRunTrace(r.Context(), runID)
	if err != nil {
		if strings.Contains(err.Error(), "no enhanced trace data") {
			response.Err(w, http.StatusNotFound, "no_trace_data", "该运行暂无 Trace 数据")
			return
		}
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	response.OK(w, map[string]interface{}{
		"summary":  summary,
		"analyses": analyses,
	})
}

// handleCompareWABenchTraces compares two runs' trace summaries.
// GET /api/v2/admin/evaluation/wabench/trace/compare?baseline=&candidate=
func (s *Server) handleCompareWABenchTraces(w http.ResponseWriter, r *http.Request) {
	baselineID := r.URL.Query().Get("baseline")
	candidateID := r.URL.Query().Get("candidate")
	if baselineID == "" || candidateID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "baseline and candidate are required")
		return
	}
	if s.evalRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "evaluation repository not available")
		return
	}

	comparison, err := s.evalRepo.CompareWABenchTraces(r.Context(), baselineID, candidateID)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	response.OK(w, comparison)
}
