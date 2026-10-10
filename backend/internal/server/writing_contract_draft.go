package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// Writing contract draft (normal flows — 长文创作 / 多材料综合 / 忠实改写):
// the same productization the research_review flow got with
// research-contract-draft, extended to the three普通 flows and to the intent
// plan. The composer posts ONLY the user-facing choices; the server builds
// the lcp/1.0 contract from the Go struct definitions — schema_version,
// ctr_-prefixed contract_id, version≥1, source attributions and both
// contract hashes are computed here via WithComputedHash, then the launch
// chain's /contracts and /confirm steps just forward the sealed pair
// verbatim. The frontend never hand-writes field-ordered JSON again, so
// schema drift between the hand-built contract and the strict server decode
// (the "普通写作从未成功" root cause: wrong schema_version, missing ctr_ id,
// client-side hashes) can no longer happen.
//
// The response also carries the intent plan (server-built and sealed with
// the Go canonical intent_plan_hash): a hand-rolled client builder cannot
// reproduce Go's struct-order sha256 without a bespoke serializer, and the
// plan's contract_ref must bind the sealed confirmed contract anyway.
//
// No persistence: unlike the research flow (whose settings panel can re-draft
// an existing document and whose replay store keeps seals byte-identical
// across seconds), one normal launch = one fresh document and exactly one
// draft call whose returned pair is forwarded verbatim — idempotency comes
// from the document Idempotency-Key plus the confirmation chain, so the
// minimal implementation seals fresh per call (same input + same clock still
// seal byte-identically; only the attribution recorded_at can move a hash).

// errInvalidWritingSpec is the normal-flow draft's 400 family (parallels
// errInvalidResearchSpec): the message always names the offending field.
var errInvalidWritingSpec = errors.New("writing api: invalid writing request")

// writingContractDraftCommand is the request body of
// POST /api/v2/documents/{documentId}/writing-contract-draft. It carries the
// composer's user-selectable dimensions; everything else in the contract is
// a server-side policy decision and never travels on the wire.
type writingContractDraftCommand struct {
	// DocumentID comes from the URL, never from the body.
	DocumentID string `json:"-"`
	// Message is the composer prompt: contract intent.purpose,
	// content.topic (first 100 runes) and content.central_question.
	// Trimmed, must not be blank.
	Message string `json:"message"`
	// Style is the composer's global style slug; non-blank it becomes
	// voice.tone (with preserve_user_voice=true), blank falls back to
	// "professional".
	Style string `json:"style,omitempty"`
	// Mode maps to collaboration.task_mode (writingkernel.TaskMode:
	// auto/writing/guided/polish); blank defaults to auto.
	Mode string `json:"mode,omitempty"`
	// Flow selects the writing flow (long_form/multi_material/
	// faithful_rewrite). research_review is refused: its launch chain is
	// POST /documents/{id}/research-contract-draft (lcp/1.1 + research spec).
	Flow string `json:"flow"`
	// AssuranceLevel maps to collaboration.assurance_level and
	// evidence_policy.level (flexible/standard/sourced/strict); blank
	// defaults to standard.
	AssuranceLevel string `json:"assurance_level,omitempty"`
	// ApprovalMode maps to collaboration.approval_mode
	// (conditional/always/auto); blank defaults to auto.
	ApprovalMode string `json:"approval_mode,omitempty"`
	// Language maps to delivery.language; blank defaults to zh-CN.
	Language string `json:"language,omitempty"`
}

// writingDraftSeed is the canonical input identity of one draft request: the
// trimmed/defaulted values the seal actually consumes. The contract id and
// the intent plan id derive from it, so omitting an optional field and
// sending its default produce the same ids.
type writingDraftSeed struct {
	DocumentID     string                          `json:"document_id"`
	Message        string                          `json:"message"`
	Topic          string                          `json:"topic"`
	Style          string                          `json:"style"`
	Mode           writingkernel.TaskMode          `json:"mode"`
	Flow           string                          `json:"flow"`
	AssuranceLevel writingkernel.AssuranceLevel    `json:"assurance_level"`
	ApprovalMode   writingkernel.ApprovalMode      `json:"approval_mode"`
	Language       string                          `json:"language"`
	Operation      writingkernel.Operation         `json:"operation"`
	Orchestration  writingkernel.OrchestrationMode `json:"orchestration"`
}

// writingFlowContractPlan is the server-authoritative per-flow mapping
// (frontend mirror: writing-flows.ts WRITING_FLOW_SPECS). research_review is
// deliberately absent — the fourth flow has its own lcp/1.1 draft endpoint.
type writingFlowContractPlan struct {
	operation     writingkernel.Operation
	orchestration writingkernel.OrchestrationMode
	summary       string
	steps         []writingplan.ProposedStep
}

var writingFlowContractPlans = map[string]writingFlowContractPlan{
	"long_form": {
		operation:     writingkernel.OperationCreate,
		orchestration: writingkernel.OrchestrationModeOutlineFirst,
		summary:       "Long-form writing pipeline (tpl_outline_first_v1): outline → draft → quality → finalize; evidence policy long_form governs draft@index 1",
		steps: []writingplan.ProposedStep{
			{StepID: "outline", Objective: "Generate article outline", CapabilityHint: "core.outline.generate", DependsOn: []string{}},
			{StepID: "draft", Objective: "Write full draft", CapabilityHint: "core.draft.generate", DependsOn: []string{"outline"}},
			{StepID: "quality", Objective: "Quality validation", CapabilityHint: "core.validation.quality", DependsOn: []string{"draft"}},
			{StepID: "finalize", Objective: "Finalize revision set", CapabilityHint: "core.document.finalize", DependsOn: []string{"quality"}},
		},
	},
	"multi_material": {
		operation:     writingkernel.OperationSynthesize,
		orchestration: writingkernel.OrchestrationModeSourced,
		summary:       "Multi-material synthesis pipeline (tpl_sourced_v1): synthesis → quality → finalize; evidence policy multi_material governs synthesis@index 1",
		steps: []writingplan.ProposedStep{
			{StepID: "synthesis", Objective: "Synthesize materials into unified analysis", CapabilityHint: "core.draft.generate", DependsOn: []string{}},
			{StepID: "quality", Objective: "Quality validation", CapabilityHint: "core.validation.quality", DependsOn: []string{"synthesis"}},
			{StepID: "finalize", Objective: "Finalize revision set", CapabilityHint: "core.document.finalize", DependsOn: []string{"quality"}},
		},
	},
	"faithful_rewrite": {
		operation:     writingkernel.OperationRewrite,
		orchestration: writingkernel.OrchestrationModeFast,
		summary:       "Faithful rewrite pipeline (tpl_fast_v1): rewrite → quality → finalize; evidence policy faithful_rewrite governs rewrite@index 0",
		steps: []writingplan.ProposedStep{
			{StepID: "rewrite", Objective: "Rewrite preserving facts, opinions and author voice", CapabilityHint: "core.draft.generate", DependsOn: []string{}},
			{StepID: "quality", Objective: "Quality validation", CapabilityHint: "core.validation.quality", DependsOn: []string{"rewrite"}},
			{StepID: "finalize", Objective: "Finalize revision set", CapabilityHint: "core.document.finalize", DependsOn: []string{"rewrite"}},
		},
	},
}

// normalizeWritingDraftCommand validates the user-facing choices and returns
// the canonical seal inputs. Every rejection names its field so the composer
// can surface an actionable 400 (INVALID_WRITING_SPEC).
func normalizeWritingDraftCommand(command writingContractDraftCommand) (writingDraftSeed, error) {
	message := strings.TrimSpace(command.Message)
	if message == "" {
		return writingDraftSeed{}, fmt.Errorf("%w: message must not be blank (contract intent.purpose / content.central_question)", errInvalidWritingSpec)
	}
	flowKey := strings.TrimSpace(command.Flow)
	if flowKey == "" {
		return writingDraftSeed{}, fmt.Errorf("%w: flow must not be blank (long_form / multi_material / faithful_rewrite)", errInvalidWritingSpec)
	}
	if flowKey == "research_review" {
		return writingDraftSeed{}, fmt.Errorf("%w: flow research_review has its own launch chain — POST /documents/{id}/research-contract-draft (lcp/1.1 research contract via the research settings form)", errInvalidWritingSpec)
	}
	flow, ok := writingFlowContractPlans[flowKey]
	if !ok {
		return writingDraftSeed{}, fmt.Errorf("%w: flow %q is not a writing flow (long_form / multi_material / faithful_rewrite)", errInvalidWritingSpec, flowKey)
	}
	mode := strings.TrimSpace(command.Mode)
	if mode == "" {
		mode = string(writingkernel.TaskModeAuto)
	}
	if !writingkernel.TaskMode(mode).Valid() {
		return writingDraftSeed{}, fmt.Errorf("%w: mode %q is invalid (auto / writing / guided / polish)", errInvalidWritingSpec, mode)
	}
	assurance := strings.TrimSpace(command.AssuranceLevel)
	if assurance == "" {
		assurance = string(writingkernel.AssuranceLevelStandard)
	}
	if !writingkernel.AssuranceLevel(assurance).Valid() {
		return writingDraftSeed{}, fmt.Errorf("%w: assurance_level %q is invalid (flexible / standard / sourced / strict)", errInvalidWritingSpec, assurance)
	}
	approval := strings.TrimSpace(command.ApprovalMode)
	if approval == "" {
		approval = string(writingkernel.ApprovalModeAuto)
	}
	if !writingkernel.ApprovalMode(approval).Valid() {
		return writingDraftSeed{}, fmt.Errorf("%w: approval_mode %q is invalid (conditional / always / auto)", errInvalidWritingSpec, approval)
	}
	language := strings.TrimSpace(command.Language)
	if language == "" {
		language = "zh-CN"
	}
	return writingDraftSeed{
		DocumentID:     command.DocumentID,
		Message:        message,
		Topic:          truncateWritingDraftRunes(message, 100),
		Style:          strings.TrimSpace(command.Style),
		Mode:           writingkernel.TaskMode(mode),
		Flow:           flowKey,
		AssuranceLevel: writingkernel.AssuranceLevel(assurance),
		ApprovalMode:   writingkernel.ApprovalMode(approval),
		Language:       language,
		Operation:      flow.operation,
		Orchestration:  flow.orchestration,
	}, nil
}

// truncateWritingDraftRunes cuts by runes (not bytes), so a CJK prompt keeps
// whole characters in content.topic.
func truncateWritingDraftRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// writingContractDraftView returns everything the six-step launch chain
// posts verbatim: contract → POST /documents/{id}/contracts (draft v1),
// confirmed_contract → POST /contracts/{id}/confirm (v2), intent_plan →
// POST /documents/{id}/plans. All three carry canonical server-computed
// hashes; contract_hash/intent_plan_hash are repeated top-level for the
// caller's convenience and logging.
type writingContractDraftView struct {
	DocumentID        string                        `json:"document_id"`
	Contract          writingkernel.WritingContract `json:"contract"`
	ConfirmedContract writingkernel.WritingContract `json:"confirmed_contract"`
	ContractHash      string                        `json:"contract_hash"`
	IntentPlan        writingplan.IntentPlan        `json:"intent_plan"`
	IntentPlanHash    string                        `json:"intent_plan_hash"`
}

// buildWritingContractDrafts is the pure sealing core: same command and same
// clock => byte-identical contracts and intent plan (ids and every hash).
// The only clock inputs are the attribution recorded_at and the intent
// plan's created_at, so tests pin determinism by freezing the clock.
func buildWritingContractDrafts(command writingContractDraftCommand, now time.Time) (writingContractDraftView, error) {
	seed, err := normalizeWritingDraftCommand(command)
	if err != nil {
		return writingContractDraftView{}, err
	}
	contractID, err := deriveWritingContractID(seed)
	if err != nil {
		return writingContractDraftView{}, fmt.Errorf("%w: derive contract id: %v", errInvalidWritingSpec, err)
	}
	draft := writingkernel.WritingContract{
		SchemaVersion: writingkernel.SchemaVersionV1,
		ContractVersion: writingkernel.ContractVersion{
			ContractID: contractID,
			Version:    1,
		},
		Status: writingkernel.ContractStatusDraft,
		Intent: writingkernel.IntentSpec{
			Operation: seed.Operation,
			Genre:     "article",
			Purpose:   seed.Message,
		},
		Audience: writingkernel.AudienceSpec{
			Role:           "reader",
			KnowledgeLevel: "intermediate",
		},
		Content: writingkernel.ContentSpec{
			Topic:            seed.Topic,
			CentralQuestion:  seed.Message,
			RequiredPoints:   []string{},
			ProhibitedPoints: []string{},
		},
		Voice: writingkernel.VoiceSpec{
			Tone:              writingDraftTone(seed.Style),
			PreserveUserVoice: true,
		},
		MaterialPolicy: writingkernel.MaterialPolicy{
			UserMaterialPriority:  writingkernel.UserMaterialPriorityPreferred,
			AllowExternalResearch: false,
			ConflictHandling:      writingkernel.ConflictHandlingPreferUserMaterial,
		},
		EvidencePolicy: writingkernel.EvidencePolicy{
			Level:             writingkernel.EvidenceLevel(seed.AssuranceLevel),
			UnsupportedClaims: writingkernel.UnsupportedClaimsFlag,
		},
		Delivery: writingkernel.DeliverySpec{
			Format:   writingkernel.DeliveryFormatMarkdown,
			Language: seed.Language,
			Length:   writingkernel.LengthRange{Min: 300, Max: 5000},
		},
		Collaboration: writingkernel.ExecutionControl{
			TaskMode:          seed.Mode,
			OrchestrationMode: seed.Orchestration,
			AssuranceLevel:    seed.AssuranceLevel,
			ApprovalMode:      seed.ApprovalMode,
		},
		Inferences: []writingkernel.Inference{},
	}
	attributions, err := collaborationAttributions(draft, now)
	if err != nil {
		return writingContractDraftView{}, fmt.Errorf("%w: %v", errInvalidWritingSpec, err)
	}
	draft.SourceAttributions = attributions
	sealedDraft, err := draft.WithComputedHash()
	if err != nil {
		return writingContractDraftView{}, fmt.Errorf("%w: seal draft: %v", errInvalidWritingSpec, err)
	}
	if err := sealedDraft.Validate(); err != nil {
		return writingContractDraftView{}, fmt.Errorf("%w: sealed draft is not valid: %v", errInvalidWritingSpec, err)
	}
	confirmed := sealedDraft
	confirmed.Version = 2
	confirmed.Status = writingkernel.ContractStatusConfirmed
	confirmed.ContractHash = ""
	sealedConfirmed, err := confirmed.WithComputedHash()
	if err != nil {
		return writingContractDraftView{}, fmt.Errorf("%w: seal confirmed: %v", errInvalidWritingSpec, err)
	}
	if err := writingkernel.ValidateTransition(sealedDraft, sealedConfirmed); err != nil {
		return writingContractDraftView{}, fmt.Errorf("%w: draft→confirm transition invalid: %v", errInvalidWritingSpec, err)
	}
	intentPlan, err := buildWritingDraftIntentPlan(sealedConfirmed, seed, now)
	if err != nil {
		return writingContractDraftView{}, fmt.Errorf("%w: %v", errInvalidWritingSpec, err)
	}
	return writingContractDraftView{
		DocumentID:        command.DocumentID,
		Contract:          sealedDraft,
		ConfirmedContract: sealedConfirmed,
		ContractHash:      sealedConfirmed.ContractHash,
		IntentPlan:        intentPlan,
		IntentPlanHash:    intentPlan.IntentPlanHash,
	}, nil
}

// writingDraftTone maps the style choice onto voice.tone: a non-blank style
// slug expresses the tone directly (with preserve_user_voice=true the
// executor keeps the author's voice), blank falls back to "professional".
func writingDraftTone(style string) string {
	if style == "" {
		return "professional"
	}
	return style
}

// deriveWritingContractID pins the deterministic-id convention
// (writingplan.deterministicID): sha256 over the canonical seed JSON that
// includes the owning document. Same document + same choices => same id; two
// documents never collide because document_id is part of the seed.
func deriveWritingContractID(seed writingDraftSeed) (string, error) {
	payload, err := json.Marshal(seed)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "ctr_" + hex.EncodeToString(sum[:])[:24], nil
}

// buildWritingDraftIntentPlan seals the launch chain's intent plan server
// side: iplan_ id derived from the contract id, contract_ref bound to the
// sealed confirmed contract (exactly what /plans and /runs validate), Go
// canonical intent_plan_hash. created_at is the same clock as the
// attributions, so a frozen clock seals a byte-identical plan.
func buildWritingDraftIntentPlan(confirmed writingkernel.WritingContract, seed writingDraftSeed, now time.Time) (writingplan.IntentPlan, error) {
	sum := sha256.Sum256([]byte("intent_plan\x00" + confirmed.ContractID))
	planID := "iplan_" + hex.EncodeToString(sum[:])[:24]
	flow := writingFlowContractPlans[seed.Flow]
	plan, err := writingplan.IntentPlan{
		IntentPlanID:  planID,
		ContractRef:   writingplan.ObjectRef{ID: confirmed.ContractID, Version: confirmed.Version, Hash: confirmed.ContractHash},
		Summary:       flow.summary,
		CreatedBy:     writingplan.ActorUser,
		CreatedAt:     now.UTC(),
		ProposedSteps: flow.steps,
	}.WithComputedHash()
	if err != nil {
		return writingplan.IntentPlan{}, fmt.Errorf("seal intent plan: %v", err)
	}
	return plan, nil
}

// DraftWritingContract authorizes the document owner (same 404/403 as any
// other document-scoped endpoint) and seals the normal-flow contract pair
// plus the intent plan. Deliberately no replay store: see the file comment.
func (service *persistentWritingAPI) DraftWritingContract(ctx context.Context, access writingAccess, command writingContractDraftCommand) (writingContractDraftView, error) {
	if _, err := service.authorizeDocument(ctx, access, command.DocumentID); err != nil {
		return writingContractDraftView{}, err
	}
	now := time.Now().UTC()
	if service.now != nil {
		now = service.now()
	}
	return buildWritingContractDrafts(command, now)
}

// writingContractDraftService is the normal-flow draft slice of the writing
// API, resolved lazily like the research slice so partial fakes keep
// compiling: only the governed persistentWritingAPI implements it.
type writingContractDraftService interface {
	DraftWritingContract(ctx context.Context, access writingAccess, command writingContractDraftCommand) (writingContractDraftView, error)
}

func writingDraftAPIOf(service writingAPIService) (writingContractDraftService, error) {
	if draft, ok := service.(writingContractDraftService); ok {
		return draft, nil
	}
	return nil, errWritingRuntimeUnavailable
}

// handleCreateWritingContractDraft serves
// POST /api/v2/documents/{documentId}/writing-contract-draft.
func (s *Server) handleCreateWritingContractDraft(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	draft, err := writingDraftAPIOf(s.writingAPI)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	var body writingContractDraftCommand
	if err := decodeWritingJSON(w, r, &body); err != nil {
		s.writeWritingError(w, err)
		return
	}
	body.DocumentID = chi.URLParam(r, "documentId")
	view, err := draft.DraftWritingContract(r.Context(), access, body)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.Created(w, view)
}
