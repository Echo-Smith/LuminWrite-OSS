package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// Research contract draft (WP4 productization): the composer's 深度研究 flow
// collects the user's choices + a research-spec/1 and asks the server to
// construct and seal the lcp/1.1 research_review contract. Field order, the
// source attributions and both contract hashes are computed here from the Go
// struct definitions — the frontend never hand-writes field-ordered JSON
// again, so a struct-field reorder can no longer silently break client
// sealing (the failure mode the client-side builder guarded against).
//
// One launch = one fresh document (the composer creates a document per
// start), so the derived contract_id is collision-free in the product flow
// while staying stable per (document, input) — a repeated draft call for the
// same document with identical choices returns the identical sealed contract.

// researchContractDraftCommand is the request body of
// POST /api/v2/documents/{documentId}/research-contract-draft. It carries the
// user-facing choices of the research settings form (central question,
// audience, language, length, external-research switch) plus the validated
// research-spec/1 the frontend already builds with buildResearchSpec.
type researchContractDraftCommand struct {
	// DocumentID comes from the URL, never from the body.
	DocumentID string `json:"-"`
	// CentralQuestion doubles as content.topic / content.central_question /
	// intent.purpose; trimmed, must not be blank.
	CentralQuestion string `json:"central_question"`
	// Audience maps to audience.role (server requires non-empty).
	Audience string `json:"audience"`
	// Language maps to delivery.language (server requires non-empty).
	Language string `json:"language"`
	// Length bounds map to delivery.length (positive ints, min <= max).
	LengthMin int `json:"length_min"`
	LengthMax int `json:"length_max"`
	// AllowExternalResearch maps to material_policy.allow_external_research;
	// false compiles the F5 user-material branch and requires mounted
	// materials at plan time (RESEARCH_MATERIALS_REQUIRED otherwise).
	AllowExternalResearch bool `json:"allow_external_research"`
	// Research is the research-spec/1 section; decoded strictly (unknown
	// fields rejected) and validated before sealing.
	Research writingkernel.ResearchSpec `json:"research"`
}

// researchContractDraftView returns both contract versions the launch chain
// needs: contract goes to POST /documents/{id}/contracts (draft v1) and
// confirmed_contract to POST /contracts/{id}/confirm (v2). Both carry a
// canonical server-computed contract_hash, ready to POST verbatim.
type researchContractDraftView struct {
	DocumentID        string                        `json:"document_id"`
	Contract          writingkernel.WritingContract `json:"contract"`
	ConfirmedContract writingkernel.WritingContract `json:"confirmed_contract"`
}

// buildResearchContractDrafts is the pure sealing core: same command and same
// clock => byte-identical contracts (contract_id, both hashes). The only
// clock input is the attribution recorded_at, so tests pin determinism by
// freezing the clock.
func buildResearchContractDrafts(command researchContractDraftCommand, now time.Time) (researchContractDraftView, error) {
	spec, err := validatedResearchSpec(command)
	if err != nil {
		return researchContractDraftView{}, fmt.Errorf("%w: %v", errInvalidResearchSpec, err)
	}
	contractID, err := deriveResearchContractID(command)
	if err != nil {
		return researchContractDraftView{}, fmt.Errorf("%w: derive contract id: %v", errInvalidResearchSpec, err)
	}
	question := strings.TrimSpace(command.CentralQuestion)
	draft := writingkernel.WritingContract{
		SchemaVersion: writingkernel.SchemaVersionV11,
		ContractVersion: writingkernel.ContractVersion{
			ContractID: contractID,
			Version:    1,
		},
		Status: writingkernel.ContractStatusDraft,
		Intent: writingkernel.IntentSpec{
			Operation: writingkernel.OperationCreate,
			Genre:     "literature_review",
			Purpose:   question,
		},
		Audience: writingkernel.AudienceSpec{
			Role:           strings.TrimSpace(command.Audience),
			KnowledgeLevel: "professional",
		},
		Content: writingkernel.ContentSpec{
			Topic:            question,
			CentralQuestion:  question,
			RequiredPoints:   []string{},
			ProhibitedPoints: []string{},
		},
		Voice: writingkernel.VoiceSpec{
			Tone:              "professional",
			PreserveUserVoice: true,
		},
		MaterialPolicy: writingkernel.MaterialPolicy{
			UserMaterialPriority:  writingkernel.UserMaterialPriorityHighest,
			AllowExternalResearch: command.AllowExternalResearch,
			ConflictHandling:      writingkernel.ConflictHandlingAskUser,
		},
		EvidencePolicy: writingkernel.EvidencePolicy{
			Level:             writingkernel.EvidenceLevelSourced,
			UnsupportedClaims: writingkernel.UnsupportedClaimsProhibit,
		},
		Delivery: writingkernel.DeliverySpec{
			Format:   writingkernel.DeliveryFormatMarkdown,
			Language: strings.TrimSpace(command.Language),
			Length:   writingkernel.LengthRange{Min: command.LengthMin, Max: command.LengthMax},
		},
		Collaboration: writingkernel.ExecutionControl{
			TaskMode:          writingkernel.TaskModeGuided,
			OrchestrationMode: writingkernel.OrchestrationModeResearchReview,
			AssuranceLevel:    writingkernel.AssuranceLevelSourced,
			ApprovalMode:      writingkernel.ApprovalModeConditional,
		},
		Inferences: []writingkernel.Inference{},
		Research:   &spec,
	}
	attributions, err := collaborationAttributions(draft, now)
	if err != nil {
		return researchContractDraftView{}, fmt.Errorf("%w: %v", errInvalidResearchSpec, err)
	}
	draft.SourceAttributions = attributions
	sealedDraft, err := draft.WithComputedHash()
	if err != nil {
		return researchContractDraftView{}, fmt.Errorf("%w: seal draft: %v", errInvalidResearchSpec, err)
	}
	if err := sealedDraft.Validate(); err != nil {
		return researchContractDraftView{}, fmt.Errorf("%w: sealed draft is not valid: %v", errInvalidResearchSpec, err)
	}
	confirmed := sealedDraft
	confirmed.Version = 2
	confirmed.Status = writingkernel.ContractStatusConfirmed
	confirmed.ContractHash = ""
	sealedConfirmed, err := confirmed.WithComputedHash()
	if err != nil {
		return researchContractDraftView{}, fmt.Errorf("%w: seal confirmed: %v", errInvalidResearchSpec, err)
	}
	if err := writingkernel.ValidateTransition(sealedDraft, sealedConfirmed); err != nil {
		return researchContractDraftView{}, fmt.Errorf("%w: draft→confirm transition invalid: %v", errInvalidResearchSpec, err)
	}
	return researchContractDraftView{
		DocumentID:        command.DocumentID,
		Contract:          sealedDraft,
		ConfirmedContract: sealedConfirmed,
	}, nil
}

// validatedResearchSpec checks the delivery fields the ResearchSpec cannot
// see (they land outside /research) and returns the spec strictly decoded:
// unknown fields inside research fail closed here instead of vanishing into
// the sealed contract, exactly like the confirmed-contract decode path.
func validatedResearchSpec(command researchContractDraftCommand) (writingkernel.ResearchSpec, error) {
	if strings.TrimSpace(command.CentralQuestion) == "" {
		return writingkernel.ResearchSpec{}, fmt.Errorf("central_question must not be blank")
	}
	if strings.TrimSpace(command.Audience) == "" {
		return writingkernel.ResearchSpec{}, fmt.Errorf("audience must not be blank (contract audience.role)")
	}
	if strings.TrimSpace(command.Language) == "" {
		return writingkernel.ResearchSpec{}, fmt.Errorf("language must not be blank (contract delivery.language)")
	}
	if command.LengthMin < 1 || command.LengthMax < 1 {
		return writingkernel.ResearchSpec{}, fmt.Errorf("delivery length must be positive")
	}
	if command.LengthMin > command.LengthMax {
		return writingkernel.ResearchSpec{}, fmt.Errorf("delivery length min must not exceed max")
	}
	payload, err := json.Marshal(command.Research)
	if err != nil {
		return writingkernel.ResearchSpec{}, fmt.Errorf("research spec: %v", err)
	}
	spec, err := writingkernel.DecodeResearchSpecStrict(payload)
	if err != nil {
		return writingkernel.ResearchSpec{}, fmt.Errorf("research spec: %v", err)
	}
	if err := spec.Validate(); err != nil {
		return writingkernel.ResearchSpec{}, fmt.Errorf("research spec: %v", err)
	}
	return spec, nil
}

// deriveResearchContractID pins the compiler's deterministic-id convention
// (writingplan.deterministicID): sha256 over the draft inputs including the
// owning document. Same document + same choices => same id; two documents
// never collide because document_id is part of the seed.
func deriveResearchContractID(command researchContractDraftCommand) (string, error) {
	seed := struct {
		DocumentID            string                     `json:"document_id"`
		CentralQuestion       string                     `json:"central_question"`
		Audience              string                     `json:"audience"`
		Language              string                     `json:"language"`
		LengthMin             int                        `json:"length_min"`
		LengthMax             int                        `json:"length_max"`
		AllowExternalResearch bool                       `json:"allow_external_research"`
		Research              writingkernel.ResearchSpec `json:"research"`
	}{
		DocumentID:            command.DocumentID,
		CentralQuestion:       strings.TrimSpace(command.CentralQuestion),
		Audience:              strings.TrimSpace(command.Audience),
		Language:              strings.TrimSpace(command.Language),
		LengthMin:             command.LengthMin,
		LengthMax:             command.LengthMax,
		AllowExternalResearch: command.AllowExternalResearch,
		Research:              command.Research,
	}
	payload, err := json.Marshal(seed)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "ctr_" + hex.EncodeToString(sum[:])[:24], nil
}

// collaborationAttributions builds the four mandatory /collaboration/*
// source attributions. Value hashes are computed through the contract's own
// FieldValueHash so they can never drift from the canonical bytes the
// validator recomputes.
func collaborationAttributions(contract writingkernel.WritingContract, now time.Time) ([]writingkernel.SourceAttribution, error) {
	paths := []string{
		"/collaboration/task_mode",
		"/collaboration/orchestration_mode",
		"/collaboration/assurance_level",
		"/collaboration/approval_mode",
	}
	recordedAt := now.UTC().Format(time.RFC3339)
	attributions := make([]writingkernel.SourceAttribution, 0, len(paths))
	for _, path := range paths {
		valueHash, err := contract.FieldValueHash(path)
		if err != nil {
			return nil, fmt.Errorf("attribute %s: %v", path, err)
		}
		attributions = append(attributions, writingkernel.SourceAttribution{
			FieldPath:  path,
			Source:     writingkernel.AttributionSourceUser,
			ValueHash:  valueHash,
			RecordedAt: recordedAt,
		})
	}
	return attributions, nil
}

// DraftResearchContract authorizes the document owner, honors the R14 flag
// (fail closed with errResearchReviewDisabled while off) and seals both
// contract versions. Document authorization first: a non-owner gets the same
// 404/403 it would get on any other document-scoped endpoint, flag on or off.
func (service *persistentWritingAPI) DraftResearchContract(ctx context.Context, access writingAccess, command researchContractDraftCommand) (researchContractDraftView, error) {
	if _, err := service.authorizeDocument(ctx, access, command.DocumentID); err != nil {
		return researchContractDraftView{}, err
	}
	if service.now != nil {
		return draftResearchContractWhenEnabled(service.researchReviewEnabled, service.now(), command)
	}
	return draftResearchContractWhenEnabled(service.researchReviewEnabled, time.Now().UTC(), command)
}

// draftResearchContractWhenEnabled splits the feature gate from the pure
// sealing so both halves are testable without a store.
func draftResearchContractWhenEnabled(enabled bool, now time.Time, command researchContractDraftCommand) (researchContractDraftView, error) {
	if !enabled {
		return researchContractDraftView{}, errResearchReviewDisabled
	}
	return buildResearchContractDrafts(command, now)
}

// handleCreateWritingResearchContractDraft serves
// POST /api/v2/documents/{documentId}/research-contract-draft.
func (s *Server) handleCreateWritingResearchContractDraft(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	research, ok := s.researchService(w, r)
	if !ok {
		return
	}
	var body researchContractDraftCommand
	if err := decodeWritingJSON(w, r, &body); err != nil {
		s.writeWritingError(w, err)
		return
	}
	body.DocumentID = chi.URLParam(r, "documentId")
	view, err := research.DraftResearchContract(r.Context(), access, body)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.Created(w, view)
}
