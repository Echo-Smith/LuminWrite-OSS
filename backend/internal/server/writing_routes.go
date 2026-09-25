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
		// Research contract draft (WP4 productization): the 深度研究 flow's
		// sealed lcp/1.1 contract is built server-side; the frontend posts
		// the returned versions straight to /contracts and /confirm.
		r.Post("/documents/{documentId}/research-contract-draft", s.handleCreateWritingResearchContractDraft)
		r.Post("/contracts/{contractId}/confirm", s.handleConfirmWritingContract)
		r.Post("/documents/{documentId}/plans", s.handleCompileWritingPlan)
		r.Post("/runs", s.handleCreateWritingRun)
		r.Get("/runs", s.handleListWritingRuns)
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
		// AR-012 candidate evaluation (T10): isolated comparison candidates,
		// never delivered into the document path (A18).
		r.Post("/runs/{runId}/research/ar012-candidate", s.handleRequestArReviewCandidate)
		r.Get("/runs/{runId}/research/ar012-candidate", s.handleGetArReviewCandidate)
		r.Post("/runs/{runId}/research/ar012-candidate/cancel", s.handleCancelArReviewCandidate)
		r.Get("/runs/{runId}/research/ar012-candidate/artifacts/{kind}", s.handleReadArReviewArtifact)
		r.Get("/documents/{documentId}/quality", s.handleWritingQuality)
		r.Get("/documents/{documentId}/audit-report", s.handleWritingAuditReport)
	})
}
