package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

func (s *Server) handleAdminUpsertWABenchCandidate(w http.ResponseWriter, r *http.Request) {
	if s.wabenchRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "wabench_unavailable", "WABench repository is unavailable")
		return
	}
	candidateID := chi.URLParam(r, "id")
	var req struct {
		Name          string                 `json:"name"`
		PromptHash    string                 `json:"promptHash"`
		MemoryHash    string                 `json:"memoryHash"`
		ModelManifest map[string]interface{} `json:"modelManifest"`
		CodeHash      string                 `json:"codeHash"`
		ToolManifest  map[string]interface{} `json:"toolManifest"`
		FeatureFlags  map[string]interface{} `json:"featureFlags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if candidateID == "" || req.Name == "" || req.PromptHash == "" || req.CodeHash == "" || len(req.ModelManifest) == 0 {
		response.Err(w, http.StatusBadRequest, "bad_request", "id, name, promptHash, codeHash and modelManifest are required")
		return
	}
	if req.ToolManifest == nil {
		req.ToolManifest = map[string]interface{}{}
	}
	if req.FeatureFlags == nil {
		req.FeatureFlags = map[string]interface{}{"memoryEnabled": false}
	} else if _, exists := req.FeatureFlags["memoryEnabled"]; !exists {
		req.FeatureFlags["memoryEnabled"] = false
	}
	candidate, err := s.wabenchRepo.UpsertCandidate(r.Context(), database.WABenchCandidateDraft{
		CandidateID:   candidateID,
		Name:          req.Name,
		PromptHash:    req.PromptHash,
		MemoryHash:    req.MemoryHash,
		ModelManifest: req.ModelManifest,
		CodeHash:      req.CodeHash,
		ToolManifest:  req.ToolManifest,
		FeatureFlags:  req.FeatureFlags,
	})
	if err != nil {
		slog.Warn("upsert WABench candidate failed", "candidate_id", candidateID, "error", err)
		response.Err(w, http.StatusBadRequest, "invalid_candidate", err.Error())
		return
	}
	response.OK(w, candidate)
}

func (s *Server) handleAdminCreateWABenchRun(w http.ResponseWriter, r *http.Request) {
	if s.wabenchSvc == nil {
		response.Err(w, http.StatusServiceUnavailable, "wabench_unavailable", "WABench V2 runner is unavailable")
		return
	}
	var req struct {
		SuiteID         string `json:"suiteId"`
		CandidateID     string `json:"candidateId"`
		Environment     string `json:"environment"`
		EvaluationRunID string `json:"evaluationRunId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.SuiteID == "" || req.CandidateID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "suiteId and candidateId are required")
		return
	}
	run, err := s.wabenchSvc.CreateRun(r.Context(), services.WABenchRunRequest{
		SuiteID:         req.SuiteID,
		CandidateID:     req.CandidateID,
		Environment:     req.Environment,
		EvaluationRunID: req.EvaluationRunID,
	})
	if err != nil {
		slog.Warn("create WABench run failed", "error", err)
		response.Err(w, http.StatusBadRequest, "invalid_run", err.Error())
		return
	}
	go func(runID string) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		if err := s.wabenchSvc.ExecuteRun(ctx, runID); err != nil {
			slog.Error("WABench V2 run failed", "run_id", runID, "error", err)
		}
	}(run.RunID)
	response.Created(w, run)
}

func (s *Server) handleAdminGetWABenchRun(w http.ResponseWriter, r *http.Request) {
	if s.wabenchRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "wabench_unavailable", "WABench repository is unavailable")
		return
	}
	report, err := s.wabenchRepo.GetRunReport(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		response.Err(w, http.StatusNotFound, "not_found", "WABench run not found")
		return
	}
	response.OK(w, report)
}

func (s *Server) handleAdminGetWABenchRunBundle(w http.ResponseWriter, r *http.Request) {
	if s.wabenchRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "wabench_unavailable", "WABench repository is unavailable")
		return
	}
	batchID := r.URL.Query().Get("batchId")
	batchContentHash := r.URL.Query().Get("batchContentHash")
	if batchID == "" || batchContentHash == "" {
		response.Err(w, http.StatusBadRequest, "batch_identity_required", "batchId and batchContentHash are required")
		return
	}
	bundle, err := s.wabenchRepo.GetNormalizedRunBundle(r.Context(), chi.URLParam(r, "id"), batchID, batchContentHash)
	if err != nil {
		slog.Warn("export normalized WABench bundle failed", "run_id", chi.URLParam(r, "id"), "error", err)
		response.Err(w, http.StatusBadRequest, "bundle_export_failed", err.Error())
		return
	}
	response.OK(w, bundle)
}

func (s *Server) handleAdminSeedWABenchRedTeam(w http.ResponseWriter, r *http.Request) {
	if s.wabenchSvc == nil {
		response.Err(w, http.StatusServiceUnavailable, "wabench_unavailable", "WABench V2 runner is unavailable")
		return
	}
	suite, err := s.wabenchSvc.EnsureDefaultRedTeamSuite(r.Context())
	if err != nil {
		slog.Warn("seed WABench red-team suite failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "red_team_seed_failed", err.Error())
		return
	}
	response.OK(w, suite)
}
