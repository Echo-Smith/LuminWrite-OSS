package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database/dbtest"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// Normal-flow writing contract draft tests (the research-contract-draft
// pattern extended to 长文创作/多材料综合/忠实改写): the composer posts only
// user choices, the server seals the lcp/1.0 contract pair (ctr_ id, version
// ≥1, WithComputedHash hashes) plus the intent plan. Covered here:
//  1. sealing determinism — same input + same clock => byte-identical ids and
//     every hash; only the attribution/created_at clock may move them;
//  2. the sealed pair + intent plan are valid and confirmable through the
//     exact decode/validate paths /contracts, /confirm and /plans apply, with
//     the plan's contract_ref bound to the sealed confirmed contract;
//  3. invalid inputs fail closed with INVALID_WRITING_SPEC naming the field,
//     and the research_review flow is refused with a pointer to its own
//     research-contract-draft endpoint;
//  4. httptest level: route, auth, decode, error mapping and wire-level
//     determinism across two identical POSTs;
//  5. service level (dbtest-gated): the real persistentWritingAPI authorizes
//     the document owner before sealing.

// writingDraftTestCommand is a valid long_form launch.
func writingDraftTestCommand(documentID string) writingContractDraftCommand {
	return writingContractDraftCommand{
		DocumentID:     documentID,
		Message:        "写一篇关于固态电解质产业化路径的行业分析",
		Style:          "default",
		Mode:           "auto",
		Flow:           "long_form",
		AssuranceLevel: "standard",
		ApprovalMode:   "conditional",
		Language:       "zh-CN",
	}
}

// ── 1. sealing determinism ─────────────────────────────────────────────────

func TestWritingContractDraftSealingIsDeterministic(t *testing.T) {
	first, err := buildWritingContractDrafts(writingDraftTestCommand("doc_wdraft_1"), draftTestTime())
	if err != nil {
		t.Fatalf("build draft: %v", err)
	}
	second, err := buildWritingContractDrafts(writingDraftTestCommand("doc_wdraft_1"), draftTestTime())
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
	if first.IntentPlan.IntentPlanID != second.IntentPlan.IntentPlanID || first.IntentPlan.IntentPlanHash != second.IntentPlan.IntentPlanHash {
		t.Fatalf("intent plan drifted: %s/%s vs %s/%s",
			first.IntentPlan.IntentPlanID, first.IntentPlan.IntentPlanHash,
			second.IntentPlan.IntentPlanID, second.IntentPlan.IntentPlanHash)
	}

	// A later clock only moves the attribution recorded_at and the intent
	// plan's created_at — still valid, but a different seal; ids never move.
	later, err := buildWritingContractDrafts(writingDraftTestCommand("doc_wdraft_1"), draftTestTime().Add(time.Hour))
	if err != nil {
		t.Fatalf("build later draft: %v", err)
	}
	if later.Contract.ContractID != first.Contract.ContractID || later.IntentPlan.IntentPlanID != first.IntentPlan.IntentPlanID {
		t.Fatal("ids must not depend on the clock")
	}
	if later.Contract.ContractHash == first.Contract.ContractHash || later.IntentPlan.IntentPlanHash == first.IntentPlan.IntentPlanHash {
		t.Fatal("hash unexpectedly identical across different seal times")
	}

	// A different document derives a different contract id (the document id
	// is part of the seed), so two launches never collide on the store's
	// (contract_id, version) primary key.
	other, err := buildWritingContractDrafts(writingDraftTestCommand("doc_wdraft_2"), draftTestTime())
	if err != nil {
		t.Fatalf("build other-document draft: %v", err)
	}
	if other.Contract.ContractID == first.Contract.ContractID {
		t.Fatal("contract id collided across documents")
	}

	// Omitting an optional field and sending its default seal identically:
	// the canonical input identity is the normalized seed.
	defaulted := writingContractDraftCommand{
		DocumentID: "doc_wdraft_1",
		Message:    writingDraftTestCommand("doc_wdraft_1").Message,
		Flow:       "long_form",
	}
	implicit, err := buildWritingContractDrafts(defaulted, draftTestTime())
	if err != nil {
		t.Fatalf("build defaulted draft: %v", err)
	}
	explicit := writingContractDraftCommand{
		DocumentID:     "doc_wdraft_1",
		Message:        defaulted.Message,
		Flow:           "long_form",
		Mode:           string(writingkernel.TaskModeAuto),
		AssuranceLevel: string(writingkernel.AssuranceLevelStandard),
		ApprovalMode:   string(writingkernel.ApprovalModeAuto),
		Language:       "zh-CN",
	}
	explicitView, err := buildWritingContractDrafts(explicit, draftTestTime())
	if err != nil {
		t.Fatalf("build explicit-defaults draft: %v", err)
	}
	if implicit.Contract.ContractHash != explicitView.Contract.ContractHash || implicit.IntentPlan.IntentPlanHash != explicitView.IntentPlan.IntentPlanHash {
		t.Fatal("omitted optionals must seal identically to their defaults")
	}
}

// ── 2. sealed pair + intent plan are valid and confirmable ─────────────────

func TestWritingContractDraftProducesValidConfirmablePair(t *testing.T) {
	command := writingDraftTestCommand("doc_wdraft_3")
	command.Message = strings.Repeat("固态电解质产业化路径分析。", 40) // well past the 100-rune topic cap
	view, err := buildWritingContractDrafts(command, draftTestTime())
	if err != nil {
		t.Fatalf("build draft: %v", err)
	}
	draft, confirmed, plan := view.Contract, view.ConfirmedContract, view.IntentPlan

	if draft.SchemaVersion != writingkernel.SchemaVersionV1 {
		t.Fatalf("schema version %q, want lcp/1.0", draft.SchemaVersion)
	}
	if !strings.HasPrefix(draft.ContractID, "ctr_") || !strings.HasPrefix(plan.IntentPlanID, "iplan_") {
		t.Fatalf("id prefixes wrong: %s / %s", draft.ContractID, plan.IntentPlanID)
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
	if view.ContractHash != confirmed.ContractHash {
		t.Fatalf("top-level contract_hash %s does not bind the confirmed contract %s", view.ContractHash, confirmed.ContractHash)
	}
	if view.IntentPlanHash != plan.IntentPlanHash {
		t.Fatalf("top-level intent_plan_hash %s does not bind the intent plan %s", view.IntentPlanHash, plan.IntentPlanHash)
	}

	// Semantic mapping (task contract): flow → operation/orchestration,
	// style → voice.tone, audience/content/voice/material/delivery defaults.
	if draft.Intent.Operation != writingkernel.OperationCreate || draft.Intent.Genre != "article" {
		t.Fatalf("intent = %+v", draft.Intent)
	}
	if draft.Intent.Purpose != command.Message || draft.Content.CentralQuestion != command.Message {
		t.Fatal("message must land in intent.purpose and content.central_question verbatim (trimmed)")
	}
	if got := []rune(draft.Content.Topic); len(got) != 100 {
		t.Fatalf("content.topic = %d runes, want the first 100", len(got))
	}
	if draft.Audience.Role != "reader" || draft.Audience.KnowledgeLevel != "intermediate" {
		t.Fatalf("audience = %+v", draft.Audience)
	}
	if draft.Voice.Tone != "default" || !draft.Voice.PreserveUserVoice {
		t.Fatalf("voice = %+v (style slug becomes tone)", draft.Voice)
	}
	if draft.MaterialPolicy.UserMaterialPriority != writingkernel.UserMaterialPriorityPreferred ||
		draft.MaterialPolicy.AllowExternalResearch ||
		draft.MaterialPolicy.ConflictHandling != writingkernel.ConflictHandlingPreferUserMaterial {
		t.Fatalf("material policy = %+v", draft.MaterialPolicy)
	}
	if draft.EvidencePolicy.Level != writingkernel.EvidenceLevelStandard || draft.EvidencePolicy.UnsupportedClaims != writingkernel.UnsupportedClaimsFlag {
		t.Fatalf("evidence policy = %+v", draft.EvidencePolicy)
	}
	if draft.Delivery.Format != writingkernel.DeliveryFormatMarkdown || draft.Delivery.Language != "zh-CN" ||
		draft.Delivery.Length.Min != 300 || draft.Delivery.Length.Max != 5000 {
		t.Fatalf("delivery = %+v", draft.Delivery)
	}
	if draft.Collaboration.OrchestrationMode != writingkernel.OrchestrationModeOutlineFirst {
		t.Fatalf("orchestration %q, want the long_form mapping", draft.Collaboration.OrchestrationMode)
	}
	if draft.Research != nil {
		t.Fatal("normal-flow contracts must not carry a research section")
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

	// The exact decode path the /contracts and /confirm handlers apply:
	// strict top-level decode (no research section to recurse into).
	for name, contract := range map[string]writingkernel.WritingContract{"draft": draft, "confirmed": confirmed} {
		payload, err := json.Marshal(contract)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := writingkernel.DecodeWritingContractStrict(payload)
		if err != nil {
			t.Fatalf("%s fails the strict contract decode: %v", name, err)
		}
		if decoded.ContractHash != contract.ContractHash {
			t.Fatalf("%s hash changed across the strict decode roundtrip", name)
		}
	}

	// The intent plan validates on its own (hash recompute included) and its
	// contract_ref binds the sealed confirmed contract — exactly what
	// CompilePlan and CreateRun check.
	if err := plan.Validate(); err != nil {
		t.Fatalf("sealed intent plan fails validation: %v", err)
	}
	if plan.ContractRef.ID != confirmed.ContractID || plan.ContractRef.Version != confirmed.Version || plan.ContractRef.Hash != confirmed.ContractHash {
		t.Fatalf("intent plan contract_ref %+v does not bind the confirmed contract", plan.ContractRef)
	}
	if plan.CreatedBy != writingplan.ActorUser {
		t.Fatalf("created_by %q", plan.CreatedBy)
	}
	steps := writingFlowContractPlans["long_form"].steps
	if len(plan.ProposedSteps) != len(steps) {
		t.Fatalf("proposed steps = %d, want the long_form chain", len(plan.ProposedSteps))
	}
	for index, step := range steps {
		if !reflect.DeepEqual(plan.ProposedSteps[index], step) {
			t.Fatalf("step %d = %+v, want %+v", index, plan.ProposedSteps[index], step)
		}
	}
}

// TestWritingContractDraftPerFlowMappings pins the three normal flows'
// semantic mapping (operation, orchestration, plan steps).
func TestWritingContractDraftPerFlowMappings(t *testing.T) {
	cases := []struct {
		flow          string
		operation     writingkernel.Operation
		orchestration writingkernel.OrchestrationMode
		firstStep     string
		initialTypes  int
	}{
		{"long_form", writingkernel.OperationCreate, writingkernel.OrchestrationModeOutlineFirst, "outline", 1},
		{"multi_material", writingkernel.OperationSynthesize, writingkernel.OrchestrationModeSourced, "synthesis", 2},
		{"faithful_rewrite", writingkernel.OperationRewrite, writingkernel.OrchestrationModeFast, "rewrite", 2},
	}
	for _, testCase := range cases {
		command := writingDraftTestCommand("doc_wdraft_flow")
		command.Flow = testCase.flow
		view, err := buildWritingContractDrafts(command, draftTestTime())
		if err != nil {
			t.Fatalf("%s: build draft: %v", testCase.flow, err)
		}
		contract := view.Contract
		if contract.Intent.Operation != testCase.operation || contract.Collaboration.OrchestrationMode != testCase.orchestration {
			t.Fatalf("%s: operation/orchestration = %s/%s", testCase.flow, contract.Intent.Operation, contract.Collaboration.OrchestrationMode)
		}
		if contract.Collaboration.OrchestrationMode == writingkernel.OrchestrationModeResearchReview {
			t.Fatalf("%s: normal flows must never seal research_review orchestration", testCase.flow)
		}
		if view.IntentPlan.ProposedSteps[0].StepID != testCase.firstStep {
			t.Fatalf("%s: first step %q", testCase.flow, view.IntentPlan.ProposedSteps[0].StepID)
		}
	}
}

// ── 3. fail-closed inputs ──────────────────────────────────────────────────

func TestWritingContractDraftRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*writingContractDraftCommand)
		message string
	}{
		{"blank message", func(c *writingContractDraftCommand) { c.Message = "   " }, "message must not be blank"},
		{"blank flow", func(c *writingContractDraftCommand) { c.Flow = "" }, "flow must not be blank"},
		{"unknown flow", func(c *writingContractDraftCommand) { c.Flow = "poetry" }, `flow "poetry" is not a writing flow`},
		{"invalid mode", func(c *writingContractDraftCommand) { c.Mode = "telepathy" }, "mode \"telepathy\" is invalid"},
		{"invalid assurance", func(c *writingContractDraftCommand) { c.AssuranceLevel = "paranoid" }, "assurance_level \"paranoid\" is invalid"},
		{"invalid approval", func(c *writingContractDraftCommand) { c.ApprovalMode = "never" }, "approval_mode \"never\" is invalid"},
	}
	for _, testCase := range cases {
		command := writingDraftTestCommand("doc_wdraft_4")
		testCase.mutate(&command)
		_, err := buildWritingContractDrafts(command, draftTestTime())
		if err == nil {
			t.Fatalf("%s: expected rejection", testCase.name)
		}
		if !errors.Is(err, errInvalidWritingSpec) {
			t.Fatalf("%s: error %v does not carry the INVALID_WRITING_SPEC sentinel", testCase.name, err)
		}
		if !strings.Contains(err.Error(), testCase.message) {
			t.Fatalf("%s: error %q does not name %q", testCase.name, err, testCase.message)
		}
	}
}

func TestWritingContractDraftRefusesResearchReview(t *testing.T) {
	command := writingDraftTestCommand("doc_wdraft_5")
	command.Flow = "research_review"
	_, err := buildWritingContractDrafts(command, draftTestTime())
	if err == nil {
		t.Fatal("research_review flow must not seal a normal-flow contract")
	}
	if !errors.Is(err, errInvalidWritingSpec) {
		t.Fatalf("refusal error %v, want the INVALID_WRITING_SPEC sentinel", err)
	}
	if !strings.Contains(err.Error(), "research-contract-draft") {
		t.Fatalf("refusal %q must point back to the research draft endpoint", err)
	}
}

// ── 4. httptest level ──────────────────────────────────────────────────────

// writingDraftTestAPI rides the standard writing-routes harness: the fake
// writing API plus the REAL draft builder behind a fixed clock, so the HTTP
// assertions exercise the actual sealing.
type writingDraftTestAPI struct {
	fakeWritingAPI
}

func (api *writingDraftTestAPI) DraftWritingContract(_ context.Context, _ writingAccess, command writingContractDraftCommand) (writingContractDraftView, error) {
	return buildWritingContractDrafts(command, draftTestTime())
}

var (
	_ writingContractDraftService = (*writingDraftTestAPI)(nil)
	_ writingAPIService           = (*writingDraftTestAPI)(nil)
)

func writingDraftTestRouter(t *testing.T) (http.Handler, string) {
	t.Helper()
	server := &Server{cfg: &config.Config{JWT: config.JWTConfig{Secret: "test-secret", Expiry: time.Hour}}, writingAPI: &writingDraftTestAPI{}}
	router := chi.NewRouter()
	router.Route("/api/v2", func(router chi.Router) { server.registerWritingRoutes(router) })
	token, err := server.GenerateJWT("00000000-0000-0000-0000-000000000001", "user", "session_test")
	if err != nil {
		t.Fatal(err)
	}
	return router, token
}

func writingDraftRequestBody() string {
	command := writingDraftTestCommand("doc_wdraft_http")
	command.DocumentID = ""
	payload, err := json.Marshal(command)
	if err != nil {
		panic(err)
	}
	return string(payload)
}

func TestWritingContractDraftHTTPEndpoint(t *testing.T) {
	router, token := writingDraftTestRouter(t)
	path := "/api/v2/documents/doc_wdraft_http/writing-contract-draft"

	unauthenticated := writingRequest(t, router, "", http.MethodPost, path, writingDraftRequestBody(), "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated draft status=%d", unauthenticated.Code)
	}

	created := writingRequest(t, router, token, http.MethodPost, path, writingDraftRequestBody(), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("draft status=%d body=%s", created.Code, created.Body.String())
	}
	var payload struct {
		Data writingContractDraftView `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	view := payload.Data
	if view.DocumentID != "doc_wdraft_http" {
		t.Fatalf("document id %q", view.DocumentID)
	}
	if view.Contract.SchemaVersion != writingkernel.SchemaVersionV1 {
		t.Fatalf("schema version %q", view.Contract.SchemaVersion)
	}
	if !strings.HasPrefix(view.Contract.ContractID, "ctr_") || view.Contract.Version != 1 || view.Contract.Status != "draft" {
		t.Fatalf("draft contract identity wrong: %+v", view.Contract.ContractVersion)
	}
	if view.ConfirmedContract.Version != 2 || view.ConfirmedContract.Status != "confirmed" {
		t.Fatalf("confirmed contract wrong: %+v", view.ConfirmedContract.ContractVersion)
	}
	if !strings.HasPrefix(view.Contract.ContractHash, "sha256:") || !strings.HasPrefix(view.ConfirmedContract.ContractHash, "sha256:") {
		t.Fatalf("hashes not sealed: %s / %s", view.Contract.ContractHash, view.ConfirmedContract.ContractHash)
	}
	if !strings.HasPrefix(view.IntentPlan.IntentPlanID, "iplan_") || view.IntentPlan.ContractRef.Hash != view.ConfirmedContract.ContractHash {
		t.Fatalf("intent plan not bound to the confirmed contract: %+v", view.IntentPlan)
	}

	// Wire-level determinism: two identical POSTs seal identically.
	replay := writingRequest(t, router, token, http.MethodPost, path, writingDraftRequestBody(), "")
	if replay.Code != http.StatusCreated {
		t.Fatalf("replay status=%d", replay.Code)
	}
	var replayPayload struct {
		Data writingContractDraftView `json:"data"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayPayload); err != nil {
		t.Fatal(err)
	}
	if replayPayload.Data.Contract.ContractHash != view.Contract.ContractHash ||
		replayPayload.Data.Contract.ContractID != view.Contract.ContractID ||
		replayPayload.Data.ConfirmedContract.ContractHash != view.ConfirmedContract.ContractHash ||
		replayPayload.Data.IntentPlan.IntentPlanHash != view.IntentPlan.IntentPlanHash {
		t.Fatal("replay drifted across two identical POSTs")
	}

	// Blank message: explicit 400 naming the field (never a masked 500).
	invalidCommand := writingDraftTestCommand("doc_wdraft_http")
	invalidCommand.DocumentID = ""
	invalidCommand.Message = "   "
	invalidPayload, err := json.Marshal(invalidCommand)
	if err != nil {
		t.Fatal(err)
	}
	invalid := writingRequest(t, router, token, http.MethodPost, path, string(invalidPayload), "")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid message status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	if !strings.Contains(invalid.Body.String(), "INVALID_WRITING_SPEC") || !strings.Contains(invalid.Body.String(), "message must not be blank") {
		t.Fatalf("invalid message error not actionable: %s", invalid.Body.String())
	}

	// research_review is refused with the pointer to its own endpoint.
	research := writingRequest(t, router, token, http.MethodPost, path,
		strings.Replace(writingDraftRequestBody(), `"flow":"long_form"`, `"flow":"research_review"`, 1), "")
	assertWritingErrorCode(t, research, http.StatusBadRequest, "INVALID_WRITING_SPEC")
	if !strings.Contains(research.Body.String(), "research-contract-draft") {
		t.Fatalf("research_review refusal must point to research-contract-draft: %s", research.Body.String())
	}

	// Unknown top-level fields are rejected by the strict request decoder.
	unknown := writingRequest(t, router, token, http.MethodPost, path,
		strings.Replace(writingDraftRequestBody(), `{`, `{"surprise":1,`, 1), "")
	assertWritingErrorCode(t, unknown, http.StatusBadRequest, "INVALID_JSON")
}

// ── 5. service level: authorization before sealing (dbtest-gated) ──────────

func TestWritingContractDraftAuthorizesDocumentOwner(t *testing.T) {
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
	var ownerID string
	if err := db.QueryRow(`INSERT INTO users (uid, name) VALUES ('00000000-0000-0000-0000-00000000w201', 'writing draft owner')
		ON CONFLICT (uid) DO UPDATE SET name = EXCLUDED.name RETURNING id::text`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDocument(context.Background(), writingstore.DocumentRecord{
		DocumentID: "doc_wdraft_auth", OwnerUserID: ownerID, Title: "普通流草稿",
		Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: ownerID}}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// The owner seals a valid pair through the REAL service path.
	view, err := api.DraftWritingContract(ctx, writingAccess{UserID: ownerID}, writingDraftTestCommand("doc_wdraft_auth"))
	if err != nil {
		t.Fatalf("owner draft: %v", err)
	}
	if err := view.Contract.Validate(); err != nil {
		t.Fatalf("service-sealed draft invalid: %v", err)
	}

	// A non-owner is forbidden before any sealing happens.
	if _, err := api.DraftWritingContract(ctx, writingAccess{UserID: "00000000-0000-0000-0000-00000000w202"}, writingDraftTestCommand("doc_wdraft_auth")); !errors.Is(err, errWritingForbidden) {
		t.Fatalf("non-owner draft error = %v, want errWritingForbidden", err)
	}

	// An unknown document is not found.
	if _, err := api.DraftWritingContract(ctx, writingAccess{UserID: ownerID}, writingDraftTestCommand("doc_wdraft_missing")); !errors.Is(err, writingstore.ErrNotFound) {
		t.Fatalf("missing document draft error = %v, want writingstore.ErrNotFound", err)
	}
}
