package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

// WP4 research contract draft tests: the 深度研究 flow's lcp/1.1 contract is
// sealed server-side (canonical Go struct marshal + sha256) so the frontend
// no longer hand-writes field-ordered JSON. Covered here:
//  1. sealing determinism — same input + same clock => byte-identical
//     contract_id and both hashes; only the attribution clock may move them;
//  2. the sealed pair is valid and confirmable through the exact decode path
//     PutContract/ConfirmContract/CompilePlan use (strict research decode);
//  3. invalid specs fail closed with INVALID_RESEARCH_SPEC and name the
//     offending field; the flag-off gate refuses with the R14 sentinel;
//  4. httptest level: route/auth/decode/error-mapping plus hash determinism
//     across two identical POSTs.

const draftTestClock = "2026-09-25T08:00:00Z"

func draftTestTime() time.Time {
	parsed, err := time.Parse(time.RFC3339, draftTestClock)
	if err != nil {
		panic(err)
	}
	return parsed
}

func draftTestCommand(documentID string) researchContractDraftCommand {
	papers := 10
	candidates := 60
	yearFrom, yearTo := 2020, 2026
	return researchContractDraftCommand{
		DocumentID:            documentID,
		CentralQuestion:       "固态电解质界面稳定性目前的共识与分歧是什么？",
		Audience:              "材料学方向的研究生",
		Language:              "中文",
		LengthMin:             3000,
		LengthMax:             6000,
		AllowExternalResearch: true,
		Research: writingkernel.ResearchSpec{
			Version:                writingkernel.ResearchSpecVersionV1,
			ReviewKind:             writingkernel.ReviewKindNarrative,
			YearFrom:               &yearFrom,
			YearTo:                 &yearTo,
			ExclusionTerms:         []string{"燃料电池"},
			MaxQueries:             3,
			MaxCandidates:          candidates,
			MaxPapers:              papers,
			MinCitableSources:      5,
			EvidenceRequirement:    writingkernel.EvidenceRequirementAbstractAllowed,
			SelectionPolicyVersion: "selection/1",
			ReaderPolicyVersion:    "reader/1",
			Generator:              writingkernel.ResearchGeneratorLuminWriter,
			CitationStyle:          writingkernel.CitationStyleNumeric,
		},
	}
}

// ── 1. sealing determinism ─────────────────────────────────────────────────

func TestResearchContractDraftSealingIsDeterministic(t *testing.T) {
	first, err := buildResearchContractDrafts(draftTestCommand("doc_draft_1"), draftTestTime())
	if err != nil {
		t.Fatalf("build draft: %v", err)
	}
	second, err := buildResearchContractDrafts(draftTestCommand("doc_draft_1"), draftTestTime())
	if err != nil {
		t.Fatalf("rebuild draft: %v", err)
	}
	if first.Contract.ContractID != second.Contract.ContractID {
		t.Fatalf("contract id drifted: %s vs %s", first.Contract.ContractID, second.Contract.ContractID)
	}
	if first.Contract.ContractHash != second.Contract.ContractHash {
		t.Fatalf("draft hash drifted for identical input: %s vs %s", first.Contract.ContractHash, second.Contract.ContractHash)
	}
	if first.ConfirmedContract.ContractHash != second.ConfirmedContract.ContractHash {
		t.Fatalf("confirmed hash drifted for identical input: %s vs %s", first.ConfirmedContract.ContractHash, second.ConfirmedContract.ContractHash)
	}

	// The same input sealed at a later time only moves the attribution
	// recorded_at — still a valid contract, but a different seal.
	later, err := buildResearchContractDrafts(draftTestCommand("doc_draft_1"), draftTestTime().Add(time.Hour))
	if err != nil {
		t.Fatalf("build later draft: %v", err)
	}
	if later.Contract.ContractID != first.Contract.ContractID {
		t.Fatalf("contract id must not depend on the clock: %s vs %s", later.Contract.ContractID, first.Contract.ContractID)
	}
	if later.Contract.ContractHash == first.Contract.ContractHash {
		t.Fatal("hash unexpectedly identical across different attribution times")
	}

	// A different document derives a different contract id (the document id
	// is part of the seed), so two launches never collide on the store's
	// (contract_id, version) primary key.
	other, err := buildResearchContractDrafts(draftTestCommand("doc_draft_2"), draftTestTime())
	if err != nil {
		t.Fatalf("build other-document draft: %v", err)
	}
	if other.Contract.ContractID == first.Contract.ContractID {
		t.Fatal("contract id collided across documents")
	}
}

// ── 2. sealed pair is valid and confirmable ────────────────────────────────

func TestResearchContractDraftProducesValidConfirmablePair(t *testing.T) {
	view, err := buildResearchContractDrafts(draftTestCommand("doc_draft_3"), draftTestTime())
	if err != nil {
		t.Fatalf("build draft: %v", err)
	}
	draft, confirmed := view.Contract, view.ConfirmedContract

	if draft.SchemaVersion != writingkernel.SchemaVersionV11 {
		t.Fatalf("schema version %q, want lcp/1.1", draft.SchemaVersion)
	}
	if draft.Collaboration.OrchestrationMode != writingkernel.OrchestrationModeResearchReview {
		t.Fatalf("orchestration mode %q, want research_review", draft.Collaboration.OrchestrationMode)
	}
	if draft.Research == nil || draft.Research.Version != writingkernel.ResearchSpecVersionV1 {
		t.Fatal("draft must carry a research-spec/1 section")
	}
	if draft.Version != 1 || draft.Status != writingkernel.ContractStatusDraft {
		t.Fatalf("draft version/status = %d/%s", draft.Version, draft.Status)
	}
	if confirmed.Version != 2 || confirmed.Status != writingkernel.ContractStatusConfirmed {
		t.Fatalf("confirmed version/status = %d/%s", confirmed.Version, confirmed.Status)
	}
	if confirmed.ContractID != draft.ContractID {
		t.Fatal("confirmed contract must keep the draft's id")
	}
	if err := draft.Validate(); err != nil {
		t.Fatalf("sealed draft fails kernel validation: %v", err)
	}
	if err := confirmed.Validate(); err != nil {
		t.Fatalf("sealed confirmed contract fails kernel validation: %v", err)
	}
	if err := writingkernel.ValidateTransition(draft, confirmed); err != nil {
		t.Fatalf("draft→confirm transition rejected: %v", err)
	}

	// The exact decode path PutContract / ConfirmContract apply: strict
	// top-level decode plus the research-section unknown-field rejection.
	for name, contract := range map[string]writingkernel.WritingContract{"draft": draft, "confirmed": confirmed} {
		payload, err := json.Marshal(contract)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := writingkernel.DecodeWritingContractResearchStrict(payload)
		if err != nil {
			t.Fatalf("%s fails the strict contract decode: %v", name, err)
		}
		if decoded.ContractHash != contract.ContractHash {
			t.Fatalf("%s hash changed across the strict decode roundtrip", name)
		}
	}
}

// ── 3. fail-closed inputs ──────────────────────────────────────────────────

func TestResearchContractDraftRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*researchContractDraftCommand)
		message string
	}{
		{"blank central question", func(c *researchContractDraftCommand) { c.CentralQuestion = "  " }, "central_question"},
		{"blank audience", func(c *researchContractDraftCommand) { c.Audience = "" }, "audience"},
		{"blank language", func(c *researchContractDraftCommand) { c.Language = "" }, "language"},
		{"non-positive length", func(c *researchContractDraftCommand) { c.LengthMin = 0 }, "delivery length"},
		{"min above max", func(c *researchContractDraftCommand) { c.LengthMin, c.LengthMax = 6000, 3000 }, "delivery length"},
		{"max_papers over the protocol cap", func(c *researchContractDraftCommand) { c.Research.MaxPapers = 25 }, "research.max_papers"},
		{"invalid evidence requirement", func(c *researchContractDraftCommand) { c.Research.EvidenceRequirement = "summary_allowed" }, "evidence_requirement"},
		{"blank exclusion term", func(c *researchContractDraftCommand) { c.Research.ExclusionTerms = []string{"燃料电池", ""} }, "research"},
	}
	for _, testCase := range cases {
		command := draftTestCommand("doc_draft_4")
		testCase.mutate(&command)
		_, err := buildResearchContractDrafts(command, draftTestTime())
		if err == nil {
			t.Fatalf("%s: expected rejection", testCase.name)
		}
		if !strings.Contains(err.Error(), testCase.message) {
			t.Fatalf("%s: error %q does not name %q", testCase.name, err, testCase.message)
		}
	}
}

func TestResearchContractDraftFailsClosedWhileDisabled(t *testing.T) {
	_, err := draftResearchContractWhenEnabled(false, draftTestTime(), draftTestCommand("doc_draft_5"))
	if err == nil {
		t.Fatal("disabled flag must refuse the draft")
	}
	if !errors.Is(err, errResearchReviewDisabled) {
		t.Fatalf("disabled draft error %v, want the R14 sentinel (503 RESEARCH_UNAVAILABLE)", err)
	}
}

// ── 4. httptest level ──────────────────────────────────────────────────────

// researchDraftTestAPI rides the standard writing-routes harness: the fake
// writing API plus the REAL draft builder behind a fixed clock, so the HTTP
// assertions exercise the actual sealing.
type researchDraftTestAPI struct {
	fakeWritingAPI
}

func (api *researchDraftTestAPI) DraftResearchContract(_ context.Context, _ writingAccess, command researchContractDraftCommand) (researchContractDraftView, error) {
	return buildResearchContractDrafts(command, draftTestTime())
}

func (api *researchDraftTestAPI) GetResearchProgress(context.Context, writingAccess, string) (researchProgressView, error) {
	return researchProgressView{}, errResearchUnavailable
}
func (api *researchDraftTestAPI) GetGate(context.Context, writingAccess, string, string) (gateView, error) {
	return gateView{}, errResearchUnavailable
}
func (api *researchDraftTestAPI) DecideGate(context.Context, writingAccess, string, string, gateDecisionCommand) (gateDecisionView, bool, error) {
	return gateDecisionView{}, false, errResearchUnavailable
}
func (api *researchDraftTestAPI) SaveOutlineRevision(context.Context, writingAccess, string, string, outlineRevisionCommand) (outlineRevisionView, error) {
	return outlineRevisionView{}, errResearchUnavailable
}
func (api *researchDraftTestAPI) ReadRunArtifact(context.Context, writingAccess, string, string) (artifactContentView, error) {
	return artifactContentView{}, errResearchUnavailable
}

var (
	_ writingResearchService = (*researchDraftTestAPI)(nil)
	_ writingAPIService      = (*researchDraftTestAPI)(nil)
)

func draftTestRouter(t *testing.T) (http.Handler, string) {
	t.Helper()
	server := &Server{cfg: &config.Config{JWT: config.JWTConfig{Secret: "test-secret", Expiry: time.Hour}}, writingAPI: &researchDraftTestAPI{}}
	router := chi.NewRouter()
	router.Route("/api/v2", func(router chi.Router) { server.registerWritingRoutes(router) })
	token, err := server.GenerateJWT("00000000-0000-0000-0000-000000000001", "user", "session_test")
	if err != nil {
		t.Fatal(err)
	}
	return router, token
}

func draftRequestBody() string {
	command := draftTestCommand("doc_http_1")
	command.DocumentID = ""
	payload, err := json.Marshal(command)
	if err != nil {
		panic(err)
	}
	return string(payload)
}

func TestResearchContractDraftHTTPEndpoint(t *testing.T) {
	router, token := draftTestRouter(t)

	unauthenticated := writingRequest(t, router, "", http.MethodPost, "/api/v2/documents/doc_http_1/research-contract-draft", draftRequestBody(), "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated draft status=%d", unauthenticated.Code)
	}

	created := writingRequest(t, router, token, http.MethodPost, "/api/v2/documents/doc_http_1/research-contract-draft", draftRequestBody(), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("draft status=%d body=%s", created.Code, created.Body.String())
	}
	var payload struct {
		Data researchContractDraftView `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	view := payload.Data
	if view.DocumentID != "doc_http_1" {
		t.Fatalf("document id %q", view.DocumentID)
	}
	if view.Contract.Status != "draft" || view.Contract.Version != 1 || view.ConfirmedContract.Status != "confirmed" || view.ConfirmedContract.Version != 2 {
		t.Fatalf("unexpected contract versions: %+v", view)
	}
	if !strings.HasPrefix(view.Contract.ContractHash, "sha256:") || !strings.HasPrefix(view.ConfirmedContract.ContractHash, "sha256:") {
		t.Fatalf("hashes not sealed: %s / %s", view.Contract.ContractHash, view.ConfirmedContract.ContractHash)
	}
	if view.Contract.Research == nil || view.Contract.Research.MaxPapers != 10 {
		t.Fatal("draft response lost the research spec")
	}

	// Determinism over the wire: two identical POSTs seal identically.
	replay := writingRequest(t, router, token, http.MethodPost, "/api/v2/documents/doc_http_1/research-contract-draft", draftRequestBody(), "")
	if replay.Code != http.StatusCreated {
		t.Fatalf("replay status=%d", replay.Code)
	}
	var replayPayload struct {
		Data researchContractDraftView `json:"data"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayPayload); err != nil {
		t.Fatal(err)
	}
	if replayPayload.Data.Contract.ContractHash != view.Contract.ContractHash ||
		replayPayload.Data.Contract.ContractID != view.Contract.ContractID ||
		replayPayload.Data.ConfirmedContract.ContractHash != view.ConfirmedContract.ContractHash {
		t.Fatalf("replay drifted: %s/%s vs %s/%s",
			replayPayload.Data.Contract.ContractID, replayPayload.Data.Contract.ContractHash,
			view.Contract.ContractID, view.Contract.ContractHash)
	}

	// Invalid research spec: explicit 400 with the INVALID_RESEARCH_SPEC code
	// (never a silent 500 or a masked JSON error).
	invalid := strings.Replace(draftRequestBody(), `"max_papers":10`, `"max_papers":25`, 1)
	invalidResponse := writingRequest(t, router, token, http.MethodPost, "/api/v2/documents/doc_http_1/research-contract-draft", invalid, "")
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid spec status=%d body=%s", invalidResponse.Code, invalidResponse.Body.String())
	}
	if !strings.Contains(invalidResponse.Body.String(), "INVALID_RESEARCH_SPEC") ||
		!strings.Contains(invalidResponse.Body.String(), "research.max_papers") {
		t.Fatalf("invalid spec error not actionable: %s", invalidResponse.Body.String())
	}

	// Unknown top-level fields are rejected by the strict request decoder.
	unknown := writingRequest(t, router, token, http.MethodPost, "/api/v2/documents/doc_http_1/research-contract-draft",
		strings.Replace(draftRequestBody(), `{`, `{"surprise":1,`, 1), "")
	assertWritingErrorCode(t, unknown, http.StatusBadRequest, "INVALID_JSON")

	// Unknown fields inside the research section are rejected too — the
	// strict decoder applies recursively, so nothing can sneak past Go
	// validation by nesting.
	nested := writingRequest(t, router, token, http.MethodPost, "/api/v2/documents/doc_http_1/research-contract-draft",
		strings.Replace(draftRequestBody(), `"research":{`, `"research":{"surprise":1,`, 1), "")
	assertWritingErrorCode(t, nested, http.StatusBadRequest, "INVALID_JSON")
}
