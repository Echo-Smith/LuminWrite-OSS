package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database/dbtest"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
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

// ── 5. pilot entitlement + persisted idempotent replay (wp-pilot-launch) ───
//
// The real persistentWritingAPI over a throwaway dbtest database: the pilot
// gate refuses subjects without an unexpired research_pilot_entitlements row
// (even on replay), and repeated identical drafts replay the stored seal so
// contract_hash/confirmed_hash stay byte-identical across seconds.

// pilotDraftHarness rides dbtest (per-process throwaway database, skip
// without TEST_DATABASE_URL) and wires the REAL persistentWritingAPI — store,
// entitlement policy, and replay store all share one pool, exactly like the
// server.go assembly.
type pilotDraftHarness struct {
	api   *persistentWritingAPI
	db    *database.DB
	store *writingstore.Store
}

func newPilotDraftHarness(t *testing.T) *pilotDraftHarness {
	t.Helper()
	db, cleanup, err := dbtest.Open(os.Getenv("TEST_DATABASE_URL"), 5, 2)
	if err != nil {
		if err == dbtest.ErrNoDatabaseURL {
			t.Skip("TEST_DATABASE_URL not set")
		}
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	store, err := writingstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	api := newPersistentWritingAPI(store, db)
	api.researchReviewEnabled = true
	// api.now stays nil: the REAL wall clock, so the cross-second replay
	// acceptance below cannot pass by clock freezing alone.
	return &pilotDraftHarness{api: api, db: db, store: store}
}

func (harness *pilotDraftHarness) provisionUser(t *testing.T, uid string) string {
	t.Helper()
	var id string
	if err := harness.db.QueryRow(`INSERT INTO users (uid, name) VALUES ($1, 'pilot draft user')
		ON CONFLICT (uid) DO UPDATE SET name = EXCLUDED.name RETURNING id::text`, uid).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (harness *pilotDraftHarness) provisionDocument(t *testing.T, ownerID, documentID string) {
	t.Helper()
	if err := harness.store.CreateDocument(context.Background(), writingstore.DocumentRecord{
		DocumentID: documentID, OwnerUserID: ownerID, Title: "深度研究试点草稿",
		Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: ownerID}}); err != nil {
		t.Fatal(err)
	}
}

func (harness *pilotDraftHarness) grant(subjectID string) error {
	_, err := harness.db.Exec(`INSERT INTO research_pilot_entitlements (subject_id, scope, granted_by)
		VALUES ($1::uuid, $2, 'wp-pilot-launch-tests') ON CONFLICT (subject_id, scope) DO NOTHING`,
		subjectID, ResearchPilotScopeResearchReview)
	return err
}

func (harness *pilotDraftHarness) grantExpiring(subjectID string, expiresAtSQL string) error {
	_, err := harness.db.Exec(`INSERT INTO research_pilot_entitlements (subject_id, scope, granted_by, expires_at)
		VALUES ($1::uuid, $2, 'wp-pilot-launch-tests', `+expiresAtSQL+`)
		ON CONFLICT (subject_id, scope) DO UPDATE SET expires_at = EXCLUDED.expires_at`,
		subjectID, ResearchPilotScopeResearchReview)
	return err
}

func (harness *pilotDraftHarness) draft(ctx context.Context, userID, documentID string) (researchContractDraftView, error) {
	command := draftTestCommand(documentID)
	return harness.api.DraftResearchContract(ctx, writingAccess{UserID: userID}, command)
}

func assertSealedPairsByteIdentical(t *testing.T, first, second researchContractDraftView) {
	t.Helper()
	if second.Contract.ContractID != first.Contract.ContractID {
		t.Fatalf("replay contract id drifted: %s vs %s", first.Contract.ContractID, second.Contract.ContractID)
	}
	if second.Contract.ContractHash != first.Contract.ContractHash {
		t.Fatalf("replay draft hash drifted across seconds: %s vs %s", first.Contract.ContractHash, second.Contract.ContractHash)
	}
	if second.ConfirmedContract.ContractHash != first.ConfirmedContract.ContractHash {
		t.Fatalf("replay confirmed hash drifted across seconds: %s vs %s", first.ConfirmedContract.ContractHash, second.ConfirmedContract.ContractHash)
	}
	for name, pair := range map[string][2]writingkernel.WritingContract{"draft": {first.Contract, second.Contract}, "confirmed": {first.ConfirmedContract, second.ConfirmedContract}} {
		firstBytes, err := json.Marshal(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		secondBytes, err := json.Marshal(pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if string(firstBytes) != string(secondBytes) {
			t.Fatalf("replayed %s contract body drifted from the first seal", name)
		}
	}
}

// TestResearchContractDraftReplayAcrossRealSeconds is the hard acceptance:
// same request >1s apart over the real wall clock must return the byte
// -identical sealed pair served from the persisted row (replayed=true).
func TestResearchContractDraftReplayAcrossRealSeconds(t *testing.T) {
	harness := newPilotDraftHarness(t)
	pilot := harness.provisionUser(t, "00000000-0000-0000-0000-00000000p201")
	harness.provisionDocument(t, pilot, "doc_pilot_replay_1")
	if err := harness.grant(pilot); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	first, err := harness.draft(ctx, pilot, "doc_pilot_replay_1")
	if err != nil {
		t.Fatalf("first draft: %v", err)
	}
	if first.Replayed {
		t.Fatal("the first seal must not be reported as a replay")
	}
	time.Sleep(1100 * time.Millisecond)
	second, err := harness.draft(ctx, pilot, "doc_pilot_replay_1")
	if err != nil {
		t.Fatalf("second draft: %v", err)
	}
	if !second.Replayed {
		t.Fatal("second draft must be served from the persisted replay row")
	}
	assertSealedPairsByteIdentical(t, first, second)

	// A different input on the same document seals fresh (no replay).
	command := draftTestCommand("doc_pilot_replay_1")
	command.LengthMax = 8000
	other, err := harness.api.DraftResearchContract(ctx, writingAccess{UserID: pilot}, command)
	if err != nil {
		t.Fatalf("changed-input draft: %v", err)
	}
	if other.Replayed {
		t.Fatal("a changed input must seal fresh, not replay")
	}
	if other.Contract.ContractHash == first.Contract.ContractHash {
		t.Fatal("changed input unexpectedly produced the first seal's hash")
	}
}

// TestResearchContractDraftReplayWithInjectedClockAdvance pins the same
// guarantee with a fake clock: the injected now moves 2s between the two
// calls, and the persisted row still answers with the FIRST seal verbatim.
func TestResearchContractDraftReplayWithInjectedClockAdvance(t *testing.T) {
	harness := newPilotDraftHarness(t)
	pilot := harness.provisionUser(t, "00000000-0000-0000-0000-00000000p202")
	harness.provisionDocument(t, pilot, "doc_pilot_replay_2")
	if err := harness.grant(pilot); err != nil {
		t.Fatal(err)
	}
	clock := draftTestTime()
	harness.api.now = func() time.Time { return clock }
	ctx := context.Background()

	first, err := harness.draft(ctx, pilot, "doc_pilot_replay_2")
	if err != nil {
		t.Fatalf("first draft: %v", err)
	}
	clock = clock.Add(2 * time.Second)
	second, err := harness.draft(ctx, pilot, "doc_pilot_replay_2")
	if err != nil {
		t.Fatalf("second draft: %v", err)
	}
	if !second.Replayed {
		t.Fatal("second draft must replay the stored row despite the clock advance")
	}
	assertSealedPairsByteIdentical(t, first, second)
}

func TestResearchContractDraftRequiresPilotEntitlement(t *testing.T) {
	harness := newPilotDraftHarness(t)
	outsider := harness.provisionUser(t, "00000000-0000-0000-0000-00000000p203")
	harness.provisionDocument(t, outsider, "doc_pilot_gate_1")
	ctx := context.Background()

	// Document owner without a pilot grant: 403 RESEARCH_PILOT_REQUIRED.
	_, err := harness.draft(ctx, outsider, "doc_pilot_gate_1")
	if !errors.Is(err, errResearchPilotRequired) {
		t.Fatalf("unentitled owner draft error = %v, want errResearchPilotRequired", err)
	}
	if strings.Contains(err.Error(), "FORBIDDEN") {
		t.Fatalf("pilot refusal must carry its own code, not the generic forbidden: %v", err)
	}

	// Granting flips the answer to a fresh seal.
	if err := harness.grant(outsider); err != nil {
		t.Fatal(err)
	}
	view, err := harness.draft(ctx, outsider, "doc_pilot_gate_1")
	if err != nil {
		t.Fatalf("entitled owner draft: %v", err)
	}
	if view.Replayed || !strings.HasPrefix(view.Contract.ContractHash, "sha256:") {
		t.Fatalf("entitled draft = replayed %v hash %s", view.Replayed, view.Contract.ContractHash)
	}
}

func TestResearchContractDraftRejectsExpiredEntitlement(t *testing.T) {
	harness := newPilotDraftHarness(t)
	expired := harness.provisionUser(t, "00000000-0000-0000-0000-00000000p204")
	harness.provisionDocument(t, expired, "doc_pilot_gate_2")
	if err := harness.grantExpiring(expired, "now() - interval '1 hour'"); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.draft(context.Background(), expired, "doc_pilot_gate_2"); !errors.Is(err, errResearchPilotRequired) {
		t.Fatalf("expired grant draft error = %v, want errResearchPilotRequired", err)
	}
}

func TestResearchContractDraftFailsClosedWhenPilotUnwired(t *testing.T) {
	harness := newPilotDraftHarness(t)
	pilot := harness.provisionUser(t, "00000000-0000-0000-0000-00000000p205")
	harness.provisionDocument(t, pilot, "doc_pilot_gate_3")
	if err := harness.grant(pilot); err != nil {
		t.Fatal(err)
	}
	// An API without a wired policy can never answer the entitlement
	// question, so the draft refuses with the 503 family instead of sealing.
	unwired := &persistentWritingAPI{store: harness.store, researchReviewEnabled: true}
	if _, err := unwired.DraftResearchContract(context.Background(), writingAccess{UserID: pilot}, draftTestCommand("doc_pilot_gate_3")); !errors.Is(err, errResearchPilotUnavailable) {
		t.Fatalf("unwired pilot draft error = %v, want errResearchPilotUnavailable", err)
	}
}

// TestResearchContractDraftConcurrentFirstSealAgrees pins the insert-race
// contract: concurrent first seals of the same (document, input) end on ONE
// stored seal — identical hashes for every caller, and at most one of them
// reports a fresh seal.
func TestResearchContractDraftConcurrentFirstSealAgrees(t *testing.T) {
	harness := newPilotDraftHarness(t)
	pilot := harness.provisionUser(t, "00000000-0000-0000-0000-00000000p206")
	harness.provisionDocument(t, pilot, "doc_pilot_race_1")
	if err := harness.grant(pilot); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const racers = 4
	views := make([]researchContractDraftView, racers)
	errs := make([]error, racers)
	start := sync.WaitGroup{}
	for index := 0; index < racers; index++ {
		start.Add(1)
		go func(index int) {
			defer start.Done()
			views[index], errs[index] = harness.draft(ctx, pilot, "doc_pilot_race_1")
		}(index)
	}
	start.Wait()
	freshSeals := 0
	for index := 0; index < racers; index++ {
		if errs[index] != nil {
			t.Fatalf("racer %d failed: %v", index, errs[index])
		}
		if views[index].Contract.ContractHash != views[0].Contract.ContractHash ||
			views[index].ConfirmedContract.ContractHash != views[0].ConfirmedContract.ContractHash {
			t.Fatalf("racer %d sealed a different pair: %s vs %s", index, views[index].Contract.ContractHash, views[0].Contract.ContractHash)
		}
		if !views[index].Replayed {
			freshSeals++
		}
	}
	if freshSeals != 1 {
		t.Fatalf("fresh seals across %d concurrent first drafts = %d, want exactly 1", racers, freshSeals)
	}
}

// pilotDeniedDraftAPI serves the error-mapping assertions without a database:
// the real route stack writes whatever the service returns.
type pilotDeniedDraftAPI struct {
	researchDraftTestAPI
	err error
}

func (api *pilotDeniedDraftAPI) DraftResearchContract(context.Context, writingAccess, researchContractDraftCommand) (researchContractDraftView, error) {
	return researchContractDraftView{}, api.err
}

func TestResearchContractDraftPilotErrorMappingOverHTTP(t *testing.T) {
	router, token := draftTestRouterWithAPI(&pilotDeniedDraftAPI{err: errResearchPilotRequired})
	denied := writingRequest(t, router, token, http.MethodPost, "/api/v2/documents/doc_http_1/research-contract-draft", draftRequestBody(), "")
	assertWritingErrorCode(t, denied, http.StatusForbidden, "RESEARCH_PILOT_REQUIRED")

	router, token = draftTestRouterWithAPI(&pilotDeniedDraftAPI{err: errResearchPilotUnavailable})
	unavailable := writingRequest(t, router, token, http.MethodPost, "/api/v2/documents/doc_http_1/research-contract-draft", draftRequestBody(), "")
	assertWritingErrorCode(t, unavailable, http.StatusServiceUnavailable, "RESEARCH_UNAVAILABLE")
}

func draftTestRouterWithAPI(api *pilotDeniedDraftAPI) (http.Handler, string) {
	server := &Server{cfg: &config.Config{JWT: config.JWTConfig{Secret: "test-secret", Expiry: time.Hour}}, writingAPI: api}
	router := chi.NewRouter()
	router.Route("/api/v2", func(router chi.Router) { server.registerWritingRoutes(router) })
	token, err := server.GenerateJWT("00000000-0000-0000-0000-000000000001", "user", "session_test")
	if err != nil {
		panic(err)
	}
	return router, token
}
