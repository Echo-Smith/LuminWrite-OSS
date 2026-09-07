package server

import (
	"github.com/go-chi/chi/v5"
)

func (s *Server) registerWritingRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(s.jwtAuthMiddleware, s.rejectGuestMiddleware, s.requireWritingAPI)
		r.Post("/documents", s.handleCreateWritingDocument)
		r.Get("/documents/{documentId}", s.handleGetWritingDocument)
		r.Get("/documents/{documentId}/versions", s.handleListWritingDocumentVersions)
		r.Post("/documents/{documentId}/contracts", s.handleCreateWritingContract)
		r.Post("/contracts/{contractId}/confirm", s.handleConfirmWritingContract)
		r.Post("/documents/{documentId}/plans", s.handleCompileWritingPlan)
		r.Post("/runs", s.handleCreateWritingRun)
		r.Get("/runs/{runId}", s.handleGetWritingRun)
		r.Get("/runs/{runId}/events", s.handleWritingRunEvents)
		r.Post("/runs/{runId}/approve", s.handleApproveWritingRun)
		r.Post("/runs/{runId}/pause", s.handleControlWritingRun("pause"))
		r.Post("/runs/{runId}/resume", s.handleControlWritingRun("resume"))
		r.Post("/runs/{runId}/cancel", s.handleControlWritingRun("cancel"))
		// Research-review surface (contracts.md §3, T02).
		r.Get("/runs/{runId}/research", s.handleGetWritingResearch)
		r.Get("/runs/{runId}/gates/{gateId}", s.handleGetWritingGate)
		r.Post("/runs/{runId}/gates/{gateId}/decisions", s.handleDecideWritingGate)
		r.Post("/runs/{runId}/gates/{gateId}/outline-revisions", s.handleSaveWritingOutlineRevision)
		r.Get("/runs/{runId}/artifacts/{artifactId}/content", s.handleReadWritingRunArtifact)
		r.Get("/documents/{documentId}/quality", s.handleWritingQuality)
		r.Get("/documents/{documentId}/audit-report", s.handleWritingAuditReport)
	})
}
