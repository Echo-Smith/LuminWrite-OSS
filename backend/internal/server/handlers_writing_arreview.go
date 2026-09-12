package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// AR-012 candidate evaluation endpoints (T10). Owner-first 404 semantics;
// the sidecar service is nil when disabled → 503 AR_REVIEW_UNAVAILABLE.
//
// A18 note: these endpoints only ever read from the job row and the
// content-addressed blob store. No handler in this file can produce a
// document version, and no candidate is ever registered as a run artifact.

type arReviewArtifactRefView struct {
	Kind        string `json:"kind"`
	ContentHash string `json:"content_hash"`
	MediaType   string `json:"media_type"`
	Size        int    `json:"size"`
}

type arReviewJobView struct {
	JobID              string                    `json:"job_id"`
	RunID              string                    `json:"run_id"`
	Status             string                    `json:"status"`
	CancelRequested    bool                      `json:"cancel_requested"`
	SurrogateProjectID string                    `json:"surrogate_project_id"`
	RemoteRunID        string                    `json:"remote_run_id,omitempty"`
	ArtifactRefs       []arReviewArtifactRefView `json:"artifact_refs"`
	Usage              map[string]any            `json:"usage"`
	CorpusWarnings     []string                  `json:"corpus_warnings"`
	ErrorCode          string                    `json:"error_code,omitempty"`
	ErrorMessage       string                    `json:"error_message,omitempty"`
	CreatedAt          time.Time                 `json:"created_at"`
	UpdatedAt          time.Time                 `json:"updated_at"`
	CompletedAt        *time.Time                `json:"completed_at,omitempty"`
}

func arReviewJobViewOf(job writingstore.ArReviewJob) arReviewJobView {
	refs := make([]arReviewArtifactRefView, 0, len(job.ArtifactRefs))
	for _, ref := range job.ArtifactRefs {
		refs = append(refs, arReviewArtifactRefView{Kind: ref.Kind, ContentHash: ref.ContentHash,
			MediaType: ref.MediaType, Size: ref.Size})
	}
	if job.Usage == nil {
		job.Usage = map[string]any{}
	}
	if job.CorpusWarnings == nil {
		job.CorpusWarnings = []string{}
	}
	return arReviewJobView{
		JobID: job.ID, RunID: job.RunID, Status: job.Status, CancelRequested: job.CancelRequested,
		SurrogateProjectID: job.SurrogateProjectID, RemoteRunID: job.RemoteRunID,
		ArtifactRefs: refs, Usage: job.Usage, CorpusWarnings: job.CorpusWarnings,
		ErrorCode: job.ErrorCode, ErrorMessage: job.ErrorMessage,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, CompletedAt: job.CompletedAt,
	}
}

func (s *Server) arReviewServiceFor(w http.ResponseWriter, r *http.Request) (*arReviewService, bool) {
	if s.arReview == nil {
		s.writeWritingError(w, errArReviewDisabled)
		return nil, false
	}
	return s.arReview, true
}

// POST /runs/{runId}/research/ar012-candidate — 202 for a fresh job, 200 for
// an identical replay of an existing (owner, input hash) job.
func (s *Server) handleRequestArReviewCandidate(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	service, ok := s.arReviewServiceFor(w, r)
	if !ok {
		return
	}
	job, replayed, err := service.RequestCandidate(r.Context(), access.UserID, chi.URLParam(r, "runId"))
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	view := arReviewJobViewOf(job)
	if replayed {
		response.OK(w, view)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(response.APIResponse{Success: true, Data: view})
}

// GET /runs/{runId}/research/ar012-candidate — the run's newest job.
func (s *Server) handleGetArReviewCandidate(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	service, ok := s.arReviewServiceFor(w, r)
	if !ok {
		return
	}
	job, err := service.store.LatestArReviewJobForRun(r.Context(), access.UserID, chi.URLParam(r, "runId"))
	if err != nil {
		if errors.Is(err, writingstore.ErrNotFound) {
			s.writeWritingError(w, errResearchResourceNotFound)
			return
		}
		s.writeWritingError(w, err)
		return
	}
	response.OK(w, arReviewJobViewOf(job))
}

// POST /runs/{runId}/research/ar012-candidate/cancel — pending jobs cancel
// outright; running jobs are flagged so the worker discards the import when
// the synchronous sidecar call returns (the sidecar cannot be interrupted).
func (s *Server) handleCancelArReviewCandidate(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	service, ok := s.arReviewServiceFor(w, r)
	if !ok {
		return
	}
	job, err := service.store.LatestArReviewJobForRun(r.Context(), access.UserID, chi.URLParam(r, "runId"))
	if err != nil {
		if errors.Is(err, writingstore.ErrNotFound) {
			s.writeWritingError(w, errResearchResourceNotFound)
			return
		}
		s.writeWritingError(w, err)
		return
	}
	updated, err := service.store.RequestArReviewJobCancel(r.Context(), access.UserID, job.ID)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.OK(w, arReviewJobViewOf(updated))
}

// ─── Admin console (T10): /api/v2/admin/ar-review/*, eval.view RBAC ───

type arReviewAdminOverview struct {
	Enabled bool              `json:"enabled"`
	Jobs    []arReviewJobView `json:"jobs"`
}

// GET /api/v2/admin/ar-review/jobs — cross-owner job list for the internal
// evaluation console. Reports enabled=false as data (not 503) so the page
// can render the deployment hint instead of an error toast.
func (s *Server) handleAdminListArReviewJobs(w http.ResponseWriter, r *http.Request) {
	overview := arReviewAdminOverview{Enabled: false, Jobs: []arReviewJobView{}}
	if s.arReview == nil {
		response.OK(w, overview)
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	jobs, err := s.arReview.AdminListJobs(r.Context(), limit)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	overview.Enabled = true
	for _, job := range jobs {
		overview.Jobs = append(overview.Jobs, arReviewJobViewOf(job))
	}
	response.OK(w, overview)
}

// GET /api/v2/admin/ar-review/jobs/{jobId}/artifacts/{kind} — raw content of
// one imported sidecar output, admin-scoped (RBAC gates access; no owner
// check — the console must inspect any job).
func (s *Server) handleAdminReadArReviewArtifact(w http.ResponseWriter, r *http.Request) {
	if s.arReview == nil {
		s.writeWritingError(w, errArReviewDisabled)
		return
	}
	mediaType, contentHash, body, err := s.arReview.AdminReadArtifact(r.Context(),
		chi.URLParam(r, "jobId"), chi.URLParam(r, "kind"))
	if err != nil {
		if errors.Is(err, writingstore.ErrNotFound) {
			s.writeWritingError(w, errResearchResourceNotFound)
			return
		}
		s.writeWritingError(w, err)
		return
	}
	if mediaType == "" {
		mediaType = "application/json"
	}
	w.Header().Set("Content-Type", mediaType+"; charset=utf-8")
	w.Header().Set("X-Content-Hash", contentHash)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// GET /runs/{runId}/research/ar012-candidate/artifacts/{kind} — raw content
// of one imported sidecar output, resolved through the owner's job row.
func (s *Server) handleReadArReviewArtifact(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	service, ok := s.arReviewServiceFor(w, r)
	if !ok {
		return
	}
	job, err := service.store.LatestArReviewJobForRun(r.Context(), access.UserID, chi.URLParam(r, "runId"))
	if err != nil {
		if errors.Is(err, writingstore.ErrNotFound) {
			s.writeWritingError(w, errResearchResourceNotFound)
			return
		}
		s.writeWritingError(w, err)
		return
	}
	kind := chi.URLParam(r, "kind")
	var ref *writingstore.ArReviewArtifactRef
	for _, candidate := range job.ArtifactRefs {
		if candidate.Kind == kind {
			found := candidate
			ref = &found
			break
		}
	}
	if ref == nil {
		s.writeWritingError(w, errResearchResourceNotFound)
		return
	}
	_, body, err := service.store.GetArtifactContent(r.Context(), ref.ContentHash)
	if err != nil {
		if errors.Is(err, writingstore.ErrNotFound) {
			s.writeWritingError(w, errResearchResourceNotFound)
			return
		}
		s.writeWritingError(w, err)
		return
	}
	w.Header().Set("Content-Type", ref.MediaType+"; charset=utf-8")
	w.Header().Set("X-Content-Hash", ref.ContentHash)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
