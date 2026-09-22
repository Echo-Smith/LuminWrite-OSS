package server

import (
	"net/http"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// handleAdminWABenchReviewCompare compares reviews of two runs (candidate - baseline).
// GET /api/v2/admin/evaluation/wabench/reviews/compare?baseline=&candidate=
func (s *Server) handleAdminWABenchReviewCompare(w http.ResponseWriter, r *http.Request) {
	if !s.requireWABenchRepo(w) {
		return
	}
	baseline := strings.TrimSpace(r.URL.Query().Get("baseline"))
	candidate := strings.TrimSpace(r.URL.Query().Get("candidate"))
	if baseline == "" || candidate == "" {
		response.Err(w, http.StatusBadRequest, "invalid_params", "baseline and candidate are required")
		return
	}
	if baseline == candidate {
		response.Err(w, http.StatusBadRequest, "invalid_params", "baseline and candidate must differ")
		return
	}
	comparison, err := s.wabenchRepo.CompareRunReviews(r.Context(), baseline, candidate)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "wabench_review_compare_failed", err.Error())
		return
	}
	response.OK(w, comparison)
}

// handleAdminWABenchRunReviewStats returns review stats for a single run.
// GET /api/v2/admin/evaluation/wabench/runs/{runId}/review-stats
func (s *Server) handleAdminWABenchRunReviewStats(w http.ResponseWriter, r *http.Request) {
	if !s.requireWABenchRepo(w) {
		return
	}
	runID := strings.TrimSpace(r.URL.Query().Get("runId"))
	if runID == "" {
		response.Err(w, http.StatusBadRequest, "invalid_params", "runId is required")
		return
	}
	stats, err := s.wabenchRepo.GetRunReviewStats(r.Context(), runID)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "wabench_review_stats_failed", err.Error())
		return
	}
	response.OK(w, stats)
}
