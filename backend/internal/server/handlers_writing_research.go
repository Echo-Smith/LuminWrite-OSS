package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// Research-review endpoints (contracts.md §3). All handlers resolve the
// service lazily (mode=off reports RESEARCH_UNAVAILABLE), authorize the run
// owner first, and translate store sentinels through writeWritingError.

func (s *Server) researchService(w http.ResponseWriter, r *http.Request) (writingResearchService, bool) {
	research, err := researchAPIOf(s.writingAPI)
	if err != nil {
		s.writeWritingError(w, err)
		return nil, false
	}
	return research, true
}

// GET /runs/{runId}/research
func (s *Server) handleGetWritingResearch(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	research, ok := s.researchService(w, r)
	if !ok {
		return
	}
	view, err := research.GetResearchProgress(r.Context(), access, chi.URLParam(r, "runId"))
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.OK(w, view)
}

// GET /runs/{runId}/gates/{gateId}
func (s *Server) handleGetWritingGate(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	research, ok := s.researchService(w, r)
	if !ok {
		return
	}
	view, err := research.GetGate(r.Context(), access, chi.URLParam(r, "runId"), chi.URLParam(r, "gateId"))
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.OK(w, view)
}

// POST /runs/{runId}/gates/{gateId}/decisions — 202 for a fresh decision,
// 200 for an idempotent same-key/same-body replay (contracts.md §3).
func (s *Server) handleDecideWritingGate(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	key, err := writingIdempotencyKey(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	research, ok := s.researchService(w, r)
	if !ok {
		return
	}
	var body gateDecisionCommand
	if err := decodeWritingJSON(w, r, &body); err != nil {
		s.writeWritingError(w, err)
		return
	}
	body.IdempotencyKey = key
	view, replayed, err := research.DecideGate(r.Context(), access, chi.URLParam(r, "runId"), chi.URLParam(r, "gateId"), body)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	if replayed {
		response.OK(w, view)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(response.APIResponse{Success: true, Data: view})
}

// POST /runs/{runId}/gates/{gateId}/outline-revisions — 201 with the new
// gate revision and outline ref.
func (s *Server) handleSaveWritingOutlineRevision(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	key, err := writingIdempotencyKey(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	research, ok := s.researchService(w, r)
	if !ok {
		return
	}
	var body outlineRevisionCommand
	if err := decodeWritingJSON(w, r, &body); err != nil {
		s.writeWritingError(w, err)
		return
	}
	body.IdempotencyKey = key
	view, err := research.SaveOutlineRevision(r.Context(), access, chi.URLParam(r, "runId"), chi.URLParam(r, "gateId"), body)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.Created(w, view)
}

// GET /runs/{runId}/artifacts/{artifactId}/content — owner-authorized,
// referenced-by-run content read (raw body with its stored media type).
func (s *Server) handleReadWritingRunArtifact(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	research, ok := s.researchService(w, r)
	if !ok {
		return
	}
	view, err := research.ReadRunArtifact(r.Context(), access, chi.URLParam(r, "runId"), chi.URLParam(r, "artifactId"))
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	contentType := view.MediaType
	if contentType == "" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType+"; charset=utf-8")
	w.Header().Set("X-Content-Hash", view.ContentHash)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(view.Content)
}
