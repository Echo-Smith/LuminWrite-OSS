package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

func (s *Server) handleCreateWritingContract(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	var body struct {
		Contract writingkernel.WritingContract `json:"contract"`
	}
	if err := decodeWritingJSON(w, r, &body); err != nil {
		s.writeWritingError(w, err)
		return
	}
	record, err := s.writingAPI.PutContract(r.Context(), access, putWritingContractCommand{DocumentID: chi.URLParam(r, "documentId"), Contract: body.Contract})
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.Created(w, record)
}

func (s *Server) handleConfirmWritingContract(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	var body struct {
		PreviousVersion int                           `json:"previous_version"`
		Contract        writingkernel.WritingContract `json:"contract"`
	}
	if err := decodeWritingJSON(w, r, &body); err != nil {
		s.writeWritingError(w, err)
		return
	}
	record, err := s.writingAPI.ConfirmContract(r.Context(), access, confirmWritingContractCommand{ContractID: chi.URLParam(r, "contractId"), PreviousVersion: body.PreviousVersion, Contract: body.Contract})
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.OK(w, record)
}
