package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/websocket"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

func (s *Server) handleCompileWritingPlan(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	var body struct {
		ContractID            string                     `json:"contract_id"`
		ContractVersion       int                        `json:"contract_version"`
		BaseVersionID         string                     `json:"base_version_id"`
		IntentPlan            writingplan.IntentPlan     `json:"intent_plan"`
		Budget                writingplan.PlanBudget     `json:"budget"`
		InitialArtifactTypes  []writingplan.ArtifactType `json:"initial_artifact_types"`
		RequiredValidators    []string                   `json:"required_validators"`
		RequiredFinalArtifact writingplan.ArtifactType   `json:"required_final_artifact"`
	}
	if err := decodeWritingJSON(w, r, &body); err != nil {
		s.writeWritingError(w, err)
		return
	}
	preview, err := s.writingAPI.CompilePlan(r.Context(), access, compileWritingPlanCommand{DocumentID: chi.URLParam(r, "documentId"), ContractID: body.ContractID, ContractVersion: body.ContractVersion, BaseVersionID: body.BaseVersionID, IntentPlan: body.IntentPlan, Budget: body.Budget, InitialArtifactTypes: body.InitialArtifactTypes, RequiredValidators: body.RequiredValidators, RequiredFinalArtifact: body.RequiredFinalArtifact})
	if err != nil {
		s.writeWritingErrorWithData(w, err, preview)
		return
	}
	response.OK(w, preview)
}

func (s *Server) handleCreateWritingRun(w http.ResponseWriter, r *http.Request) {
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
	var body struct {
		DocumentID      string                          `json:"document_id"`
		ContractID      string                          `json:"contract_id"`
		ContractVersion int                             `json:"contract_version"`
		ContractHash    string                          `json:"contract_hash"`
		BaseVersionID   string                          `json:"base_version_id"`
		Plan            writingplan.WritingPlanEnvelope `json:"plan"`
		Budget          writingplan.PlanBudget          `json:"budget"`
		Permissions     []writingplan.Permission        `json:"permissions"`
	}
	if err := decodeWritingJSON(w, r, &body); err != nil {
		s.writeWritingError(w, err)
		return
	}
	run, err := s.writingAPI.CreateRun(r.Context(), access, createWritingRunCommand{IdempotencyKey: key, DocumentID: body.DocumentID, ContractID: body.ContractID, ContractVersion: body.ContractVersion, ContractHash: body.ContractHash, BaseVersionID: body.BaseVersionID, Plan: body.Plan, Budget: body.Budget, Permissions: body.Permissions})
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.Created(w, run)
}

func (s *Server) handleGetWritingRun(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	run, err := s.writingAPI.GetRun(r.Context(), access, chi.URLParam(r, "runId"))
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.OK(w, run)
}

func (s *Server) handleApproveWritingRun(w http.ResponseWriter, r *http.Request) {
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
	var body struct {
		PlanID      string                   `json:"plan_id"`
		PlanVersion int                      `json:"plan_version"`
		PlanHash    string                   `json:"plan_hash"`
		Permissions []writingplan.Permission `json:"permissions"`
	}
	if err := decodeWritingJSON(w, r, &body); err != nil {
		s.writeWritingError(w, err)
		return
	}
	run, err := s.writingAPI.ApproveRun(r.Context(), access, approveWritingRunCommand{IdempotencyKey: key, RunID: chi.URLParam(r, "runId"), PlanID: body.PlanID, PlanVersion: body.PlanVersion, PlanHash: body.PlanHash, Permissions: body.Permissions})
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.OK(w, run)
}

func (s *Server) handleControlWritingRun(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		run, err := s.writingAPI.ControlRun(r.Context(), access, controlWritingRunCommand{IdempotencyKey: key, RunID: chi.URLParam(r, "runId"), Action: action})
		if err != nil {
			s.writeWritingError(w, err)
			return
		}
		response.OK(w, run)
	}
}

func (s *Server) handleWritingRunEvents(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	after := parseSequence(r.URL.Query().Get("after"))
	if header := parseSequence(r.Header.Get("Last-Event-ID")); header > after {
		after = header
	}
	runID := chi.URLParam(r, "runId")
	page, err := s.writingAPI.ListEvents(r.Context(), access, runID, after, 200)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		response.OK(w, page)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeWritingError(w, fmt.Errorf("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	writeWritingSSE(w, flusher, page.Events)
	after = page.NextSequence
	if r.URL.Query().Get("follow") == "false" {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			page, err = s.writingAPI.ListEvents(r.Context(), access, runID, after, 200)
			if err != nil {
				return
			}
			writeWritingSSE(w, flusher, page.Events)
			after = page.NextSequence
		}
	}
}

func writeWritingSSE(w http.ResponseWriter, flusher http.Flusher, events []websocket.WritingEvent) {
	for _, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			continue
		}
		_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, payload)
		flusher.Flush()
	}
}

func parseSequence(value string) int64 {
	sequence, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || sequence < 0 {
		return 0
	}
	return sequence
}
