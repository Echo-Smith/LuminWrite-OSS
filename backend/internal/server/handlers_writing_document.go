package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

func (s *Server) handleCreateWritingDocument(w http.ResponseWriter, r *http.Request) {
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
		Title    string         `json:"title"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := decodeWritingJSON(w, r, &body); err != nil {
		s.writeWritingError(w, err)
		return
	}
	record, err := s.writingAPI.CreateDocument(r.Context(), access, createWritingDocumentCommand{IdempotencyKey: key, Title: body.Title, Metadata: body.Metadata})
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.Created(w, record)
}

func (s *Server) handleGetWritingDocument(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	record, err := s.writingAPI.GetDocument(r.Context(), access, chi.URLParam(r, "documentId"))
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.OK(w, record)
}

func (s *Server) handleListWritingDocumentVersions(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	versions, err := s.writingAPI.ListDocumentVersions(r.Context(), access, chi.URLParam(r, "documentId"))
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	if versions == nil {
		versions = []writingstore.StoredDocumentVersion{}
	}
	response.OK(w, map[string]any{"versions": versions})
}

func writingIdempotencyKey(r *http.Request) (string, error) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 128 || strings.Contains(key, ":") {
		return "", errWritingIdempotencyKeyRequired
	}
	return key, nil
}
