package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Workbuddy Adoption Callback ─────────────────────────

// WorkbuddyAdoptionRequest is the payload for the adoption callback.
type WorkbuddyAdoptionRequest struct {
	FeedbackID   string `json:"feedback_id"`
	TraceID      string `json:"trace_id"`
	UserID       string `json:"user_id"`
	Source       string `json:"source"`
	AdoptedText  string `json:"adopted_text"`
	Action       string `json:"action"` // adopt | reject
	Reason       string `json:"reason"`
}

func (s *Server) handleWorkbuddyAdoption(w http.ResponseWriter, r *http.Request) {
	var req WorkbuddyAdoptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.FeedbackID == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "feedback_id is required")
		return
	}

	if req.Source == "" {
		req.Source = "workbuddy"
	}

	if req.Action == "" {
		req.Action = "adopt"
	}

	if s.reputationSvc == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "reputation service not available")
		return
	}

	switch req.Action {
	case "adopt":
		err := s.reputationSvc.AdoptFeedback(r.Context(), req.FeedbackID, req.TraceID, req.UserID, req.Source, req.AdoptedText)
		if err != nil {
			slog.Warn("failed to adopt feedback", "error", err)
			response.Err(w, http.StatusInternalServerError, "internal_error", "failed to adopt feedback")
			return
		}
		response.OK(w, map[string]interface{}{
			"feedback_id": req.FeedbackID,
			"action":      "adopted",
			"source":      req.Source,
			"message":     "feedback adopted, reputation recalculated",
		})

	case "reject":
		err := s.reputationSvc.RejectFeedback(r.Context(), req.FeedbackID, req.TraceID, req.UserID, req.Source, req.Reason)
		if err != nil {
			slog.Warn("failed to reject feedback", "error", err)
			response.Err(w, http.StatusInternalServerError, "internal_error", "failed to reject feedback")
			return
		}
		response.OK(w, map[string]interface{}{
			"feedback_id": req.FeedbackID,
			"action":      "rejected",
			"source":      req.Source,
			"message":     "feedback rejected",
		})

	default:
		response.Err(w, http.StatusBadRequest, "bad_request", "action must be 'adopt' or 'reject'")
	}
}

// ─── Adoption History ────────────────────────────────────

func (s *Server) handleAdoptionHistory(w http.ResponseWriter, r *http.Request) {
	traceID := chi.URLParam(r, "traceId")

	if s.reputationSvc == nil {
		response.OK(w, map[string]interface{}{"adoptions": []interface{}{}})
		return
	}

	history, err := s.reputationSvc.GetAdoptionHistory(r.Context(), traceID)
	if err != nil {
		slog.Warn("failed to get adoption history", "error", err)
		response.OK(w, map[string]interface{}{"adoptions": []interface{}{}})
		return
	}

	response.OK(w, map[string]interface{}{"adoptions": history})
}

// ─── User Reputation ─────────────────────────────────────

func (s *Server) handleGetReputation(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")

	if s.reputationSvc == nil {
		response.OK(w, map[string]interface{}{
			"user_id":     userID,
			"reputation":  1.00,
			"message":     "reputation service not available",
		})
		return
	}

	rep, err := s.reputationSvc.CalculateReputation(r.Context(), userID)
	if err != nil {
		slog.Warn("failed to calculate reputation", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to calculate reputation")
		return
	}

	response.OK(w, rep)
}

func (s *Server) handleRecalculateReputation(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")

	if s.reputationSvc == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "reputation service not available")
		return
	}

	rep, err := s.reputationSvc.UpdateReputation(r.Context(), userID)
	if err != nil {
		slog.Warn("failed to update reputation", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update reputation")
		return
	}

	response.OK(w, map[string]interface{}{
		"user_id":     rep.UserID,
		"reputation":  rep.Reputation,
		"message":     "reputation recalculated",
	})
}

func (s *Server) handleReputationHistory(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")

	if s.reputationSvc == nil {
		response.OK(w, map[string]interface{}{"history": []interface{}{}})
		return
	}

	history, err := s.reputationSvc.GetReputationHistory(r.Context(), userID, 20)
	if err != nil {
		slog.Warn("failed to get reputation history", "error", err)
		response.OK(w, map[string]interface{}{"history": []interface{}{}})
		return
	}

	response.OK(w, map[string]interface{}{"history": history})
}
