package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/writing-agent-v2/pkg/response"
)

// ─── Profile Publish ─────────────────────────────────────

func (s *Server) handlePublishStyle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	var req struct {
		Version int    `json:"version"`
		Detail  string `json:"detail"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Allow empty body with defaults
		req.Version = 0
	}
	if req.Version == 0 {
		p, ok := s.profiles.Get(slug)
		if ok {
			req.Version = p.Version + 1
		} else {
			req.Version = 1
		}
	}

	if err := s.profiles.Publish(slug, req.Version, req.Detail); err != nil {
		slog.Warn("profile publish encountered DB error", "slug", slug, "error", err)
	}

	response.OK(w, map[string]interface{}{
		"slug":    slug,
		"version": req.Version,
		"detail":  req.Detail,
		"message": "profile published, auto-evaluation triggered if eval sets exist",
	})
}

// ─── Evaluation Export ───────────────────────────────────

func (s *Server) handleExportEvalRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	format := chi.URLParam(r, "format")

	if format != "json" && format != "csv" {
		response.Err(w, http.StatusBadRequest, "bad_request", "format must be 'json' or 'csv'")
		return
	}

	if s.evalSvc == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "evaluation service not available")
		return
	}

	var data []byte
	var err error
	if format == "json" {
		data, err = s.evalSvc.ExportRunJSON(r.Context(), runID)
	} else {
		data, err = s.evalSvc.ExportRunCSV(r.Context(), runID)
	}
	if err != nil {
		slog.Warn("failed to export evaluation run", "run_id", runID, "format", format, "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to export evaluation run")
		return
	}

	// Set content type and disposition
	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "text/csv")
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=eval-run-%s.%s", runID, format))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

// ─── Feedback Aggregation ────────────────────────────────

func (s *Server) handleListAggregations(w http.ResponseWriter, r *http.Request) {
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	pageSize := parseIntDefault(r.URL.Query().Get("page_size"), 20)

	if s.feedback == nil {
		response.OK(w, map[string]interface{}{"aggregations": []interface{}{}, "total": 0})
		return
	}

	aggs, total, err := s.feedback.ListAggregations(r.Context(), page, pageSize)
	if err != nil {
		slog.Warn("failed to list aggregations", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list aggregations")
		return
	}

	response.OK(w, map[string]interface{}{"aggregations": aggs, "total": total})
}

func (s *Server) handleGetAggregation(w http.ResponseWriter, r *http.Request) {
	styleSlug := chi.URLParam(r, "style")
	version := parseIntDefault(chi.URLParam(r, "version"), 1)

	if s.feedback == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	agg, err := s.feedback.GetAggregation(r.Context(), styleSlug, version)
	if err != nil {
		slog.Warn("failed to get aggregation", "error", err)
		response.Err(w, http.StatusNotFound, "not_found", "aggregation not found")
		return
	}

	response.OK(w, agg)
}

func (s *Server) handleAggregateFeedback(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StyleSlug      string `json:"style_slug"`
		ProfileVersion int    `json:"profile_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.StyleSlug == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "style_slug is required")
		return
	}
	if req.ProfileVersion == 0 {
		req.ProfileVersion = 1
	}

	if s.feedback == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	agg, err := s.feedback.AggregateFeedback(r.Context(), req.StyleSlug, req.ProfileVersion)
	if err != nil {
		slog.Warn("failed to aggregate feedback", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to aggregate feedback")
		return
	}

	response.OK(w, agg)
}

func (s *Server) handleGenerateSuggestions(w http.ResponseWriter, r *http.Request) {
	styleSlug := chi.URLParam(r, "style")
	version := parseIntDefault(chi.URLParam(r, "version"), 1)

	if s.feedback == nil || s.evalSvc == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database or LLM not available")
		return
	}

	agg, err := s.feedback.GetAggregation(r.Context(), styleSlug, version)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "aggregation not found, run aggregate first")
		return
	}

	suggestions, err := s.evalSvc.GenerateImprovementSuggestions(r.Context(), agg)
	if err != nil {
		slog.Warn("failed to generate suggestions", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to generate suggestions")
		return
	}

	// Save suggestions
	s.feedback.SaveImprovementSuggestions(r.Context(), styleSlug, version, suggestions)

	response.OK(w, map[string]interface{}{
		"style_slug":   styleSlug,
		"version":      version,
		"suggestions":  suggestions,
	})
}

// ─── Evaluation Sets ─────────────────────────────────────

func (s *Server) handleListEvalSets(w http.ResponseWriter, r *http.Request) {
	styleSlug := r.URL.Query().Get("style")
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	pageSize := parseIntDefault(r.URL.Query().Get("page_size"), 20)

	if s.evalRepo == nil {
		response.OK(w, map[string]interface{}{"sets": []interface{}{}, "total": 0})
		return
	}

	sets, total, err := s.evalRepo.ListSets(r.Context(), styleSlug, page, pageSize)
	if err != nil {
		slog.Warn("failed to list eval sets", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list eval sets")
		return
	}

	response.OK(w, map[string]interface{}{"sets": sets, "total": total})
}

func (s *Server) handleCreateEvalSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		StyleSlug   string `json:"style_slug"`
		Description string `json:"description"`
		Samples     []map[string]interface{} `json:"samples,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.Name == "" || req.StyleSlug == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "name and style_slug are required")
		return
	}

	if s.evalRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	set, err := s.evalRepo.CreateSet(r.Context(), req.Name, req.StyleSlug, req.Description)
	if err != nil {
		slog.Warn("failed to create eval set", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create eval set")
		return
	}

	// Add samples if provided
	if len(req.Samples) > 0 {
		count, err := s.evalRepo.AddSamples(r.Context(), set.ID, req.Samples)
		if err != nil {
			slog.Warn("failed to add samples", "error", err)
		}
		// Refresh set to get updated sample_count
		set, _ = s.evalRepo.GetSet(r.Context(), set.ID)
		_ = count
	}

	response.Created(w, set)
}

func (s *Server) handleGetEvalSet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if s.evalRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	set, err := s.evalRepo.GetSet(r.Context(), id)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "eval set not found")
		return
	}

	response.OK(w, set)
}

func (s *Server) handleAddEvalSamples(w http.ResponseWriter, r *http.Request) {
	setID := chi.URLParam(r, "id")

	var req struct {
		Samples []map[string]interface{} `json:"samples"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if s.evalRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	count, err := s.evalRepo.AddSamples(r.Context(), setID, req.Samples)
	if err != nil {
		slog.Warn("failed to add samples", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to add samples")
		return
	}

	response.OK(w, map[string]interface{}{"added": count})
}

func (s *Server) handleListEvalSamples(w http.ResponseWriter, r *http.Request) {
	setID := chi.URLParam(r, "id")

	if s.evalRepo == nil {
		response.OK(w, map[string]interface{}{"samples": []interface{}{}})
		return
	}

	samples, err := s.evalRepo.ListSamples(r.Context(), setID)
	if err != nil {
		slog.Warn("failed to list samples", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list samples")
		return
	}

	response.OK(w, map[string]interface{}{"samples": samples})
}

// ─── Evaluation Runs ─────────────────────────────────────

func (s *Server) handleCreateEvalRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SetID          string `json:"set_id"`
		ProfileSlug    string `json:"profile_slug"`
		ProfileVersion int    `json:"profile_version"`
		TriggerType    string `json:"trigger_type"`
		TriggerDetail  string `json:"trigger_detail"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.SetID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "set_id is required")
		return
	}
	if req.TriggerType == "" {
		req.TriggerType = "manual"
	}
	if req.ProfileSlug == "" {
		req.ProfileSlug = "yinyue"
	}
	if req.ProfileVersion == 0 {
		req.ProfileVersion = 1
	}

	if s.evalRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	run, err := s.evalRepo.CreateRun(r.Context(), req.SetID, req.ProfileSlug, req.ProfileVersion, req.TriggerType, req.TriggerDetail)
	if err != nil {
		slog.Warn("failed to create eval run", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create eval run")
		return
	}

	// Run asynchronously
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*60*1e9) // 10 min
		defer cancel()
		if err := s.evalSvc.RunEvaluation(ctx, run.ID); err != nil {
			slog.Error("evaluation run failed", "run_id", run.ID, "error", err)
		}
	}()

	response.Created(w, run)
}

func (s *Server) handleListEvalRuns(w http.ResponseWriter, r *http.Request) {
	setID := r.URL.Query().Get("set_id")
	page := parseIntDefault(r.URL.Query().Get("page"), 1)
	pageSize := parseIntDefault(r.URL.Query().Get("page_size"), 20)

	if s.evalRepo == nil {
		response.OK(w, map[string]interface{}{"runs": []interface{}{}, "total": 0})
		return
	}

	runs, total, err := s.evalRepo.ListRuns(r.Context(), setID, page, pageSize)
	if err != nil {
		slog.Warn("failed to list eval runs", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list eval runs")
		return
	}

	response.OK(w, map[string]interface{}{"runs": runs, "total": total})
}

func (s *Server) handleGetEvalRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if s.evalRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	run, err := s.evalRepo.GetRun(r.Context(), id)
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "eval run not found")
		return
	}

	response.OK(w, run)
}

// ─── Helpers ─────────────────────────────────────────────

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
