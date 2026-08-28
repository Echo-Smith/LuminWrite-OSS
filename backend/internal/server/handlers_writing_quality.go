package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

func (s *Server) handleWritingQuality(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	summary, err := s.writingAPI.QualitySummary(r.Context(), access, chi.URLParam(r, "documentId"))
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.OK(w, summary)
}

func (s *Server) handleWritingAuditReport(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	report, err := s.writingAPI.AuditReport(r.Context(), access, chi.URLParam(r, "documentId"))
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.OK(w, report)
}
