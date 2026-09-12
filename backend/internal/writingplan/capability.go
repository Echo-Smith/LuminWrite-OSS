package writingplan

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
)

type IdempotencyClass string

const (
	IdempotencySafe     IdempotencyClass = "safe"
	IdempotencyRequired IdempotencyClass = "required"
	IdempotencyExternal IdempotencyClass = "external_side_effect"
)

// ContextBlockName names one compiled context block of a capability's
// context contract (docs/18 §18.5.5). The set is pinned by the context
// compiler; the plan layer only checks membership and disjointness.
type ContextBlockName string

const (
	ContextContractDigest  ContextBlockName = "contract_digest"
	ContextThroughLine     ContextBlockName = "through_line_anchor"
	ContextCanonFacts      ContextBlockName = "canon_facts"
	ContextTerminology     ContextBlockName = "terminology"
	ContextOpenDecisions   ContextBlockName = "open_decisions"
	ContextEntitiesCards   ContextBlockName = "entities_cards"
	ContextSourceEvidence  ContextBlockName = "source_evidence"
	ContextDocumentState   ContextBlockName = "document_state"
	ContextStyleDirectives ContextBlockName = "style_directives"
)

// ValidContextBlock reports whether the name is one of the compiler's blocks.
func ValidContextBlock(name ContextBlockName) bool {
	switch name {
	case ContextContractDigest, ContextThroughLine, ContextCanonFacts, ContextTerminology,
		ContextOpenDecisions, ContextEntitiesCards, ContextSourceEvidence, ContextDocumentState, ContextStyleDirectives:
		return true
	}
	return false
}

// ContextContract is the manifest's declaration of what context a capability
// may see (docs/18 §18.5.5). Required blocks that the compiler cannot supply
// are recorded in the envelope; failing the node on them requires this
// capability to opt in via EnforceRequiredContext — an explicit, per-manifest
// activation reviewed with shadow data in hand (M4b).
type ContextContract struct {
	RequiredContext  []ContextBlockName `json:"required_context,omitempty"`
	OptionalContext  []ContextBlockName `json:"optional_context,omitempty"`
	ForbiddenContext []ContextBlockName `json:"forbidden_context,omitempty"`
	// ContextTokenBudget caps this capability's envelope; 0 means the
	// compiler default.
	ContextTokenBudget int `json:"context_token_budget,omitempty"`
	// EnforceRequiredContext fails the node when a required block is missing
	// from the compiled envelope. Default false (shadow semantics): the gap
	// is recorded and observed, never fatal. Activation is a per-manifest
	// reviewed decision.
	EnforceRequiredContext bool `json:"enforce_required_context,omitempty"`
	// RetentionPriority overrides the compiler's default overflow retention
	// order (docs/18 §18.5.5): earlier names claim leftover budget first
	// when the total binds. Names must be compiler blocks; the resident
	// block is always ranked first regardless of this list.
	RetentionPriority []ContextBlockName `json:"retention_priority,omitempty"`
}

type CapabilityManifest struct {
	ID                  string           `json:"id"`
	Class               string           `json:"class"`
	Executor            string           `json:"executor"`
	InputTypes          []ArtifactType   `json:"input_types"`
	OptionalInputTypes  []ArtifactType   `json:"optional_input_types"`
	OutputTypes         []ArtifactType   `json:"output_types"`
	Permissions         []Permission     `json:"permissions"`
	Context             ContextContract  `json:"context"`
	Streaming           bool             `json:"streaming"`
	EstimatedCostUSD    float64          `json:"estimated_cost_usd"`
	EstimatedDurationMS int64            `json:"estimated_duration_ms"`
	SupportsEvidence    bool             `json:"supports_evidence"`
	PreservesVoice      bool             `json:"preserves_voice"`
	Validator           bool             `json:"validator"`
	Version             string           `json:"version"`
	SupportedNodeKinds  []NodeKind       `json:"supported_node_kinds"`
	MaxBounds           Bounds           `json:"max_bounds"`
	Idempotency         IdempotencyClass `json:"idempotency"`
	Available           bool             `json:"available"`
	DirectDocumentWrite bool             `json:"direct_document_write"`
}

// ValidateContextContract checks the declaration: every name is a compiler
// block and the three sets are pairwise disjoint. A block cannot be both
// required and forbidden — that would make the capability unexecutable.
func (contract ContextContract) ValidateContextContract() error {
	seen := map[ContextBlockName]string{}
	for _, group := range []struct {
		kind  string
		names []ContextBlockName
	}{
		{"required", contract.RequiredContext},
		{"optional", contract.OptionalContext},
		{"forbidden", contract.ForbiddenContext},
	} {
		for _, name := range group.names {
			if !ValidContextBlock(name) {
				return fmt.Errorf("CONTEXT_BLOCK_UNKNOWN: %s is not a compiler block", name)
			}
			if first, clash := seen[name]; clash {
				return fmt.Errorf("CONTEXT_BLOCK_CONFLICT: %s is both %s and %s", name, first, group.kind)
			}
			seen[name] = group.kind
		}
	}
	for _, name := range contract.RetentionPriority {
		if !ValidContextBlock(name) {
			return fmt.Errorf("CONTEXT_BLOCK_UNKNOWN: retention priority %s is not a compiler block", name)
		}
	}
	if contract.ContextTokenBudget < 0 {
		return fmt.Errorf("CONTEXT_TOKEN_BUDGET_INVALID: %d", contract.ContextTokenBudget)
	}
	return nil
}

// ContextWanted returns the blocks the compiler should assemble for this
// contract: required plus optional, in a stable order.
func (contract ContextContract) ContextWanted() []string {
	wanted := make([]string, 0, len(contract.RequiredContext)+len(contract.OptionalContext))
	for _, name := range contract.RequiredContext {
		wanted = append(wanted, string(name))
	}
	for _, name := range contract.OptionalContext {
		wanted = append(wanted, string(name))
	}
	return wanted
}

// ContextRetentionPriority returns the contract's retention order as
// compiler block names, or nil when the contract keeps the default.
func (contract ContextContract) ContextRetentionPriority() []string {
	if len(contract.RetentionPriority) == 0 {
		return nil
	}
	priority := make([]string, 0, len(contract.RetentionPriority))
	for _, name := range contract.RetentionPriority {
		priority = append(priority, string(name))
	}
	return priority
}

type ExecutionRequest struct {
	Node   PlanNode
	Inputs map[ArtifactType][]byte
}

type ExecutionResult struct {
	Outputs map[ArtifactType][]byte
}

type ExecutorFunc func(context.Context, ExecutionRequest) (ExecutionResult, error)

type ExecutorBinding struct {
	ID                  string
	AcceptedInputTypes  []ArtifactType
	ProducedOutputTypes []ArtifactType
	Dispatch            ExecutorFunc
}

type CapabilityRegistry struct {
	mu        sync.RWMutex
	version   string
	executors map[string]ExecutorBinding
	manifests map[string]CapabilityManifest
}

func NewCapabilityRegistry(version string) *CapabilityRegistry {
	return &CapabilityRegistry{version: strings.TrimSpace(version), executors: map[string]ExecutorBinding{}, manifests: map[string]CapabilityManifest{}}
}

func (r *CapabilityRegistry) Version() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version
}

func (r *CapabilityRegistry) RegisterExecutor(binding ExecutorBinding) error {
	binding.ID = strings.TrimSpace(binding.ID)
	if binding.ID == "" || binding.Dispatch == nil || len(binding.AcceptedInputTypes) == 0 || len(binding.ProducedOutputTypes) == 0 {
		return errors.New("executor id, dispatch, accepted inputs, and produced outputs are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.executors[binding.ID]; exists {
		return fmt.Errorf("DUPLICATE_EXECUTOR: %s", binding.ID)
	}
	binding.AcceptedInputTypes = append([]ArtifactType(nil), binding.AcceptedInputTypes...)
	binding.ProducedOutputTypes = append([]ArtifactType(nil), binding.ProducedOutputTypes...)
	r.executors[binding.ID] = binding
	return nil
}

func (r *CapabilityRegistry) Register(manifest CapabilityManifest) error {
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if !manifest.Available {
		return fmt.Errorf("CAPABILITY_UNAVAILABLE: %s", manifest.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	binding, ok := r.executors[manifest.Executor]
	if !ok || binding.Dispatch == nil {
		return fmt.Errorf("UNKNOWN_EXECUTOR: %s", manifest.Executor)
	}
	acceptedInputs := append(append([]ArtifactType(nil), manifest.InputTypes...), manifest.OptionalInputTypes...)
	if !artifactSubset(acceptedInputs, binding.AcceptedInputTypes) || !artifactSubset(manifest.OutputTypes, binding.ProducedOutputTypes) {
		return fmt.Errorf("EXECUTOR_TYPE_MISMATCH: %s", manifest.Executor)
	}
	return r.registerLocked(manifest)
}

// Declare records catalog metadata for a capability that is not dispatchable
// in this process. The compiler can explain the missing class, but cannot
// select the declaration into an executable plan.
func (r *CapabilityRegistry) Declare(manifest CapabilityManifest) error {
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if manifest.Available {
		return fmt.Errorf("declared capability must not be marked available: %s", manifest.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registerLocked(manifest)
}

// Enable flips a declared (unavailable) capability to available once a real
// executor binding has been registered that covers the manifest's input and
// output contract. This is how the governed composition root activates
// declared capabilities after wiring their production adapters.
func (r *CapabilityRegistry) Enable(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	manifest, ok := r.manifests[id]
	if !ok {
		return fmt.Errorf("UNKNOWN_CAPABILITY: %s", id)
	}
	if manifest.Available {
		return nil
	}
	binding, ok := r.executors[manifest.Executor]
	if !ok || binding.Dispatch == nil {
		return fmt.Errorf("UNKNOWN_EXECUTOR: %s", manifest.Executor)
	}
	acceptedInputs := append(append([]ArtifactType(nil), manifest.InputTypes...), manifest.OptionalInputTypes...)
	if !artifactSubset(acceptedInputs, binding.AcceptedInputTypes) || !artifactSubset(manifest.OutputTypes, binding.ProducedOutputTypes) {
		return fmt.Errorf("EXECUTOR_TYPE_MISMATCH: %s", manifest.Executor)
	}
	manifest.Available = true
	r.manifests[id] = manifest
	return nil
}

func (r *CapabilityRegistry) registerLocked(manifest CapabilityManifest) error {
	if _, exists := r.manifests[manifest.ID]; exists {
		return fmt.Errorf("DUPLICATE_CAPABILITY: %s", manifest.ID)
	}
	r.manifests[manifest.ID] = cloneManifest(manifest)
	return nil
}

// Activate re-registers an already-declared capability as dispatchable
// through the given executor binding. This is the runtime-composition seam:
// the default catalog ships declared-only (fail-closed), and the governed
// composition — the one authority allowed to serve a capability — flips its
// copy of the registry to available with the binding the rollout executor
// dispatches through. The executor binding must already exist.
func (r *CapabilityRegistry) Activate(id, executorID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	manifest, exists := r.manifests[id]
	if !exists {
		return fmt.Errorf("UNKNOWN_CAPABILITY: %s", id)
	}
	binding, ok := r.executors[executorID]
	if !ok || binding.Dispatch == nil {
		return fmt.Errorf("UNKNOWN_EXECUTOR: %s", executorID)
	}
	acceptedInputs := append(append([]ArtifactType(nil), manifest.InputTypes...), manifest.OptionalInputTypes...)
	if !artifactSubset(acceptedInputs, binding.AcceptedInputTypes) || !artifactSubset(manifest.OutputTypes, binding.ProducedOutputTypes) {
		return fmt.Errorf("EXECUTOR_TYPE_MISMATCH: %s", executorID)
	}
	manifest.Executor = executorID
	manifest.Available = true
	r.manifests[id] = cloneManifest(manifest)
	return nil
}

func (r *CapabilityRegistry) Get(id string) (CapabilityManifest, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	manifest, ok := r.manifests[id]
	return cloneManifest(manifest), ok
}

func (r *CapabilityRegistry) ByClass(class string) []CapabilityManifest {
	return r.byClass(class, true)
}

// ByClassDeclared includes unavailable catalog entries. It is used only to
// preserve the intended topology of a fail-closed T4 diagnostic plan.
func (r *CapabilityRegistry) ByClassDeclared(class string) []CapabilityManifest {
	return r.byClass(class, false)
}

func (r *CapabilityRegistry) byClass(class string, availableOnly bool) []CapabilityManifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]CapabilityManifest, 0)
	for _, manifest := range r.manifests {
		if manifest.Class == class && (!availableOnly || manifest.Available) {
			result = append(result, cloneManifest(manifest))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].EstimatedCostUSD != result[j].EstimatedCostUSD {
			return result[i].EstimatedCostUSD < result[j].EstimatedCostUSD
		}
		if result[i].EstimatedDurationMS != result[j].EstimatedDurationMS {
			return result[i].EstimatedDurationMS < result[j].EstimatedDurationMS
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func (r *CapabilityRegistry) All() []CapabilityManifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]CapabilityManifest, 0, len(r.manifests))
	for _, manifest := range r.manifests {
		result = append(result, cloneManifest(manifest))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (r *CapabilityRegistry) ExecutorRegistered(executor string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	binding, ok := r.executors[executor]
	return ok && binding.Dispatch != nil
}

func validateManifest(manifest CapabilityManifest) error {
	if !capabilityPattern.MatchString(manifest.ID) || !capabilityClassPattern.MatchString(manifest.Class) {
		return errors.New("capability id and class must be canonical")
	}
	if strings.TrimSpace(manifest.Executor) == "" || strings.TrimSpace(manifest.Version) == "" {
		return errors.New("executor and version are required")
	}
	if len(manifest.InputTypes) == 0 || len(manifest.OutputTypes) == 0 || len(manifest.SupportedNodeKinds) == 0 {
		return errors.New("input types, output types, and supported node kinds are required")
	}
	if manifest.EstimatedCostUSD < 0 || math.IsNaN(manifest.EstimatedCostUSD) || math.IsInf(manifest.EstimatedCostUSD, 0) || manifest.EstimatedDurationMS < 0 {
		return errors.New("cost and duration must not be negative")
	}
	if !validBounds(manifest.MaxBounds) {
		return errors.New("capability max bounds must be finite and positive")
	}
	if manifest.Idempotency != IdempotencySafe && manifest.Idempotency != IdempotencyRequired && manifest.Idempotency != IdempotencyExternal {
		return errors.New("invalid idempotency class")
	}
	if manifest.DirectDocumentWrite {
		return errors.New("DIRECT_DOCUMENT_WRITE_FORBIDDEN: capability must emit Artifact or RevisionSet")
	}
	// The kernel gate executor is reserved (design.md §3): a manifest may
	// claim it only when it IS one of the two pinned kernel gate capabilities
	// — exact id, class, NodeHumanGate-only kinds, version, and I/O — and it
	// carries no permissions at all (gates have no external call authority).
	// Every other manifest is rejected here, so the exemption cannot be
	// forged onto an arbitrary executor-backed capability.
	if manifest.Executor == KernelGateExecutor {
		if !IsKernelHumanGateCapability(manifest) {
			return fmt.Errorf("KERNEL_GATE_FORGERY: %s is not a kernel-owned human gate capability", manifest.ID)
		}
		if len(manifest.Permissions) != 0 {
			return errors.New("KERNEL_GATE_PERMISSIONS_FORBIDDEN: gate manifests carry no permissions")
		}
	}
	if err := manifest.Context.ValidateContextContract(); err != nil {
		return err
	}
	return nil
}

// DefaultCapabilityRegistry exposes the stable catalog but deliberately binds
// no legacy executor. Typed artifact adapters are added by the governed runtime;
// until then every template fails closed as T4 instead of claiming false T1.
func DefaultCapabilityRegistry() *CapabilityRegistry {
	registry := NewCapabilityRegistry("core-1.0.0")
	register := func(manifest CapabilityManifest) {
		manifest.Available = false
		if err := registry.Declare(manifest); err != nil {
			panic(err)
		}
	}
	base := func(id, class, executor string, inputs, outputs []ArtifactType, permissions []Permission, validator bool) CapabilityManifest {
		kinds := []NodeKind{NodeAction}
		if validator {
			kinds = []NodeKind{NodeValidate}
		}
		return CapabilityManifest{ID: id, Class: class, Executor: executor, InputTypes: inputs, OutputTypes: outputs, Permissions: permissions,
			EstimatedCostUSD: .5, EstimatedDurationMS: 30000, PreservesVoice: true, Validator: validator, Version: "1.0.0",
			SupportedNodeKinds: kinds, MaxBounds: Bounds{MaxAttempts: 2, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 2, TimeoutMS: 120000},
			Idempotency: IdempotencyRequired}
	}
	// Context contracts (docs/18 §18.5.5): required blocks that go unsupplied
	// are recorded in the envelope, never silently absent. Fail-closed on
	// required-missing is a separately activated policy (M4b).
	outline := base("core.outline.generate", "writing.outline", "engine.step.outline", []ArtifactType{"contract"}, []ArtifactType{"outline"}, []Permission{"model.invoke", "materials.read"}, false)
	outline.OptionalInputTypes = []ArtifactType{"materials", "source_pack"}
	outline.Context = ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest},
		OptionalContext:        []ContextBlockName{ContextThroughLine, ContextCanonFacts, ContextTerminology, ContextOpenDecisions, ContextEntitiesCards},
		EnforceRequiredContext: true}
	register(outline)
	draft := base("core.draft.generate", "writing.draft", "engine.step.write", []ArtifactType{"contract"}, []ArtifactType{"full_draft"}, []Permission{"model.invoke", "materials.read"}, false)
	draft.OptionalInputTypes = []ArtifactType{"materials", "outline", "source_pack"}
	// M5 activation (docs/18 §18.13): document_state is sourced from the
	// run document's committed current version, so draft's required set is
	// enforceable from the first run on. Evidence is style-neutral by
	// contract and quality reports stay out of the draft's context.
	draft.Context = ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest, ContextDocumentState},
		OptionalContext: []ContextBlockName{ContextThroughLine, ContextCanonFacts, ContextTerminology, ContextOpenDecisions, ContextEntitiesCards, ContextSourceEvidence, ContextStyleDirectives},
		// The evolving document is exactly what a continuation needs most:
		// keep it when the budget binds, ahead of the default tail order.
		RetentionPriority:      []ContextBlockName{ContextDocumentState},
		EnforceRequiredContext: true}
	register(draft)
	quality := base("core.validation.quality", "validation.quality", "engine.step.post_review", []ArtifactType{"full_draft"}, []ArtifactType{"quality_report"}, []Permission{"model.invoke", "validation.run"}, true)
	quality.OptionalInputTypes = []ArtifactType{"evidence_report", "fact_report"}
	// M5 activation: the report reviews the committed document, so its
	// required document_state has the same stable source as draft's.
	quality.Context = ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest, ContextDocumentState},
		OptionalContext:        []ContextBlockName{ContextTerminology, ContextSourceEvidence, ContextStyleDirectives},
		EnforceRequiredContext: true}
	register(quality)
	// M1.2 (docs/22): the sourced/strict templates and the compiler's
	// validatorsForAssurance floor reference the evidence and fact validators;
	// until these manifests existed those templates could only fail closed as
	// T4. Validators are report producers, not gates — the quality node stays
	// the sole acceptance authority — so they never fail a node on findings.
	evidence := base("core.validation.evidence", "validation.evidence", "engine.step.evidence", []ArtifactType{"source_pack", "full_draft"}, []ArtifactType{"evidence_report"}, []Permission{"model.invoke", "validation.run"}, true)
	// The evidence check reviews the draft against the run's own source pack;
	// the pack arrives as an input artifact, not through the context blocks.
	evidence.Context = ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest},
		OptionalContext:        []ContextBlockName{ContextTerminology, ContextSourceEvidence},
		EnforceRequiredContext: true}
	register(evidence)
	fact := base("core.validation.fact", "validation.fact", "engine.step.fact", []ArtifactType{"source_pack", "full_draft"}, []ArtifactType{"fact_report"}, []Permission{"model.invoke", "validation.run"}, true)
	fact.Context = ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest},
		OptionalContext:        []ContextBlockName{ContextTerminology, ContextSourceEvidence},
		EnforceRequiredContext: true}
	register(fact)
	finalize := base("core.document.finalize", "document.finalize", "kernel.document.finalize", []ArtifactType{"full_draft", "quality_report"}, []ArtifactType{"revision_set"}, []Permission{"document.revision"}, false)
	// M5 activation: the revision set is computed against the committed
	// document version; finalize without document state would guess.
	finalize.Context = ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest, ContextDocumentState},
		OptionalContext:        []ContextBlockName{ContextTerminology},
		EnforceRequiredContext: true}
	register(finalize)
	research := base("core.retrieval.search", "research.collect", "engine.step.search", []ArtifactType{"contract", "materials"}, []ArtifactType{"source_pack"}, []Permission{"external.research", "materials.read"}, false)
	research.SupportsEvidence = true
	// Style directives must not shape evidence collection: research stays
	// style-neutral by contract.
	research.Context = ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest},
		OptionalContext:        []ContextBlockName{ContextCanonFacts, ContextOpenDecisions, ContextTerminology, ContextEntitiesCards},
		ForbiddenContext:       []ContextBlockName{ContextStyleDirectives},
		EnforceRequiredContext: true}
	register(research)
	// M1.2 (docs/22): the strict template's research node references the
	// research.strict class, which had no catalog entry either. The strict
	// variant shares the collect executor; its stricter bounds live in the
	// per-request profile resolution (M1.3), not in a second capability.
	strictResearch := base("core.retrieval.strict_search", "research.strict", "engine.step.search", []ArtifactType{"contract", "materials"}, []ArtifactType{"source_pack"}, []Permission{"external.research", "materials.read"}, false)
	strictResearch.SupportsEvidence = true
	strictResearch.Context = ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest},
		OptionalContext:        []ContextBlockName{ContextCanonFacts, ContextOpenDecisions, ContextTerminology, ContextEntitiesCards},
		ForbiddenContext:       []ContextBlockName{ContextStyleDirectives},
		EnforceRequiredContext: true}
	register(strictResearch)
	registry.registerResearchReview()
	return registry
}

// Research capability classes and ids (design.md §3 fixed node table, v1).
// Action/validate classes execute through research executors; the two gate
// classes are kernel-owned: the orchestrator pauses at NodeHumanGate nodes,
// no executor dispatch ever runs, and only these two catalog ids may pass
// plan validation without a live executor (see KernelHumanGateCapabilities).
const (
	ClassResearchDiscover         = "research.discover"
	ClassResearchRead             = "research.read"
	ClassResearchDiscoverMaterial = "research.discover.materials"
	ClassResearchReadMaterial     = "research.read.materials"
	ClassResearchGateEvidence     = "research.gate.evidence"
	ClassResearchOutline          = "research.outline"
	ClassResearchGateOutline      = "research.gate.outline"
	ClassResearchDraft            = "research.draft"
	ClassResearchValidateCitation = "research.validate.citations"
	ClassResearchValidateFact     = "research.validate.fact"

	CapabilityResearchDiscover    = "core.research.discover"
	CapabilityResearchRead        = "core.research.read"
	CapabilityResearchGateEvidenc = "core.research.gate.evidence"
	CapabilityResearchOutline     = "core.research.outline"
	CapabilityResearchGateOutline = "core.research.gate.outline"
	CapabilityResearchDraft       = "core.research.draft"
	CapabilityResearchCitations   = "core.research.validate.citations"
	CapabilityResearchFact        = "core.research.validate.fact"

	// F5 user-material research path: the no-external-research manifests of
	// the discover/read capabilities. Same executors as their external
	// siblings (the material policy branches inside the executors), but the
	// permission set is tightened — no external.research — so a plan compiled
	// for a contract that forbids external research can never carry the
	// external-call permission.
	CapabilityResearchDiscoverMaterial = "core.research.discover.materials"
	CapabilityResearchReadMaterial     = "core.research.read.materials"
)

// KernelHumanGateCapabilities are the only capability ids allowed to compile
// and dispatch as NodeHumanGate nodes without an executable executor behind
// their manifest (design.md §3: "gate manifest 无外部调用权限，由内核处理，
// 编译校验允许其受控无 executor 分支；不能以虚构 runner 绕过注册校验").
// The exemption is bound to exact ids + the NodeHumanGate kind + the pinned
// I/O shapes below; ValidatePlan, ValidateForDispatch, and the runtime's
// recovery all enforce the same predicate, so an arbitrary external manifest
// can never claim the exemption.
var KernelHumanGateCapabilities = map[string]KernelHumanGateShape{
	CapabilityResearchGateEvidenc: {Class: ClassResearchGateEvidence,
		InputTypes: []ArtifactType{"research_evidence_pack"}, OutputTypes: []ArtifactType{"evidence_approval"}},
	CapabilityResearchGateOutline: {Class: ClassResearchGateOutline,
		InputTypes: []ArtifactType{"research_outline"}, OutputTypes: []ArtifactType{"approved_research_outline"}},
}

// KernelHumanGateShape is the pinned I/O contract one gate capability's
// manifest must carry to qualify for the kernel exemption.
type KernelHumanGateShape struct {
	Class       string
	InputTypes  []ArtifactType
	OutputTypes []ArtifactType
}

// KernelGateExecutor is the reserved executor id of the kernel-owned gate
// capabilities. No dispatchable executor is ever registered under it by the
// runtime; the orchestrator pauses at NodeHumanGate nodes and the gate
// decision transaction completes them (design.md §5).
const KernelGateExecutor = "kernel.human_gate"

// IsKernelHumanGateCapability reports whether the manifest is exactly one of
// the two kernel-owned gate capabilities: the id, class, node kinds, and I/O
// must all match the pinned shape. Anything else — including a foreign
// manifest that merely renames itself — is rejected.
func IsKernelHumanGateCapability(manifest CapabilityManifest) bool {
	shape, ok := KernelHumanGateCapabilities[manifest.ID]
	if !ok {
		return false
	}
	if manifest.Class != shape.Class {
		return false
	}
	if !containsNodeKind(manifest.SupportedNodeKinds, NodeHumanGate) || len(manifest.SupportedNodeKinds) != 1 {
		return false
	}
	if !artifactSubset(shape.InputTypes, manifest.InputTypes) || len(shape.InputTypes) != len(manifest.InputTypes) ||
		!artifactSubset(shape.OutputTypes, manifest.OutputTypes) || len(shape.OutputTypes) != len(manifest.OutputTypes) {
		return false
	}
	return manifest.Version == "1.0.0"
}

// registerResearchReview declares the T06 research_review capability catalog
// (design.md §3). Every entry is declared-only: the governed composition
// activates the executable ones via Activate; the two gate entries stay
// declared + available through the controlled kernel exemption (no executor
// dispatch exists for a kernel node).
func (registry *CapabilityRegistry) registerResearchReview() {
	// declare stores an executable-class capability as declared-only
	// (fail-closed until the governed composition activates it); gate
	// capabilities register as available — the kernel owns their dispatch.
	register := func(manifest CapabilityManifest) {
		var err error
		if manifest.Executor == KernelGateExecutor {
			err = registry.Register(manifest)
		} else {
			manifest.Available = false
			err = registry.Declare(manifest)
		}
		if err != nil {
			panic(err)
		}
	}
	research := func(id, class, executor string, inputs, outputs []ArtifactType, permissions []Permission, validator bool, maxItems int) CapabilityManifest {
		kinds := []NodeKind{NodeAction}
		if validator {
			kinds = []NodeKind{NodeValidate}
		}
		return CapabilityManifest{ID: id, Class: class, Executor: executor, InputTypes: inputs, OutputTypes: outputs,
			Permissions: permissions, EstimatedCostUSD: .5, EstimatedDurationMS: 30000, PreservesVoice: true,
			Validator: validator, Version: "1.0.0", SupportsEvidence: true,
			SupportedNodeKinds: kinds,
			// The read node's 20-paper workset and 20-minute cap (design.md
			// §3) are template bounds; the catalog's ceilings must admit them.
			MaxBounds:   Bounds{MaxAttempts: 2, MaxConcurrency: 1, MaxItems: maxItems, MaxCostUSD: 10, TimeoutMS: 1200000},
			Idempotency: IdempotencyRequired}
	}
	// Discovery plans queries and ranks candidates; the read node's research
	// budget is primarily a duration boundary (design.md §3), so the dollar
	// estimates stay nominal.
	discover := research(CapabilityResearchDiscover, ClassResearchDiscover, "engine.step.research_discover",
		[]ArtifactType{"contract"}, []ArtifactType{"research_candidates"},
		[]Permission{"external.research", "materials.read", "model.invoke"}, false, 1)
	// design.md §3: discover consumes the run materials as optional context.
	discover.OptionalInputTypes = []ArtifactType{"materials"}
	discover.Context = ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest},
		ForbiddenContext:       []ContextBlockName{ContextStyleDirectives},
		EnforceRequiredContext: true}
	register(discover)
	read := research(CapabilityResearchRead, ClassResearchRead, "engine.step.research_read",
		[]ArtifactType{"contract", "research_candidates"}, []ArtifactType{"research_evidence_pack"},
		[]Permission{"external.research", "materials.read", "model.invoke"}, false, 20)
	read.OptionalInputTypes = []ArtifactType{"materials"}
	read.Context = ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest},
		ForbiddenContext:       []ContextBlockName{ContextStyleDirectives},
		EnforceRequiredContext: true}
	register(read)
	// F5 user-material branch manifests: the same I/O as the external
	// discover/read capabilities, but the permissions carry NO
	// external.research and the executor ids are the material branch's own
	// bindings — the compiler selects these for a contract whose material
	// policy forbids external research, and ValidatePlan's
	// CONTRACT_FORBIDS_EXTERNAL_RESEARCH check stays as the fail-closed
	// backstop for any manifest that still claims the external permission.
	materialDiscover := research(CapabilityResearchDiscoverMaterial, ClassResearchDiscoverMaterial, "engine.step.research_discover_materials",
		[]ArtifactType{"contract"}, []ArtifactType{"research_candidates"},
		[]Permission{"materials.read", "model.invoke"}, false, 1)
	materialDiscover.OptionalInputTypes = []ArtifactType{"materials"}
	materialDiscover.Context = ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest},
		ForbiddenContext:       []ContextBlockName{ContextStyleDirectives},
		EnforceRequiredContext: true}
	register(materialDiscover)
	materialRead := research(CapabilityResearchReadMaterial, ClassResearchReadMaterial, "engine.step.research_read_materials",
		[]ArtifactType{"contract", "research_candidates"}, []ArtifactType{"research_evidence_pack"},
		[]Permission{"materials.read", "model.invoke"}, false, 20)
	materialRead.OptionalInputTypes = []ArtifactType{"materials"}
	materialRead.Context = ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest},
		ForbiddenContext:       []ContextBlockName{ContextStyleDirectives},
		EnforceRequiredContext: true}
	register(materialRead)
	// Kernel-owned gate capabilities: declared available with the pinned
	// kernel.human_gate executor so ValidatePlan's executor-existence check
	// holds, but no dispatch ever happens — the orchestrator pauses at the
	// node and the decision transaction completes it.
	gateExecutor := KernelGateExecutor
	if _, exists := registry.executors[gateExecutor]; !exists {
		_ = registry.RegisterExecutor(ExecutorBinding{ID: gateExecutor,
			AcceptedInputTypes:  []ArtifactType{"research_evidence_pack", "research_outline"},
			ProducedOutputTypes: []ArtifactType{"evidence_approval", "approved_research_outline"},
			Dispatch: func(context.Context, ExecutionRequest) (ExecutionResult, error) {
				return ExecutionResult{}, errors.New("kernel.human_gate is never dispatched")
			}})
	}
	gateEvidence := CapabilityManifest{ID: CapabilityResearchGateEvidenc, Class: ClassResearchGateEvidence,
		Executor: gateExecutor, InputTypes: []ArtifactType{"research_evidence_pack"},
		OutputTypes: []ArtifactType{"evidence_approval"}, Permissions: []Permission{},
		EstimatedCostUSD: 0, EstimatedDurationMS: 0, Version: "1.0.0",
		SupportedNodeKinds: []NodeKind{NodeHumanGate},
		MaxBounds:          Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 0, TimeoutMS: 1200000},
		Idempotency:        IdempotencySafe, Available: true}
	register(gateEvidence)
	gateOutline := CapabilityManifest{ID: CapabilityResearchGateOutline, Class: ClassResearchGateOutline,
		Executor: gateExecutor, InputTypes: []ArtifactType{"research_outline"},
		OutputTypes: []ArtifactType{"approved_research_outline"}, Permissions: []Permission{},
		EstimatedCostUSD: 0, EstimatedDurationMS: 0, Version: "1.0.0",
		SupportedNodeKinds: []NodeKind{NodeHumanGate},
		MaxBounds:          Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 0, TimeoutMS: 1200000},
		Idempotency:        IdempotencySafe, Available: true}
	register(gateOutline)
	outline := research(CapabilityResearchOutline, ClassResearchOutline, "engine.step.research_outline",
		[]ArtifactType{"contract", "research_evidence_pack", "evidence_approval"}, []ArtifactType{"research_outline"},
		[]Permission{"materials.read", "model.invoke"}, false, 1)
	outline.Context = ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest},
		ForbiddenContext:       []ContextBlockName{ContextStyleDirectives},
		EnforceRequiredContext: true}
	register(outline)
	draft := research(CapabilityResearchDraft, ClassResearchDraft, "engine.step.research_draft",
		[]ArtifactType{"contract", "research_evidence_pack", "evidence_approval", "approved_research_outline"},
		[]ArtifactType{"full_draft", "research_citation_index"},
		[]Permission{"materials.read", "model.invoke"}, false, 1)
	draft.Context = ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest},
		ForbiddenContext:       []ContextBlockName{ContextStyleDirectives},
		EnforceRequiredContext: true}
	register(draft)
	// The v1 citation/fact validators are deterministic host-side checks over
	// the pack + draft + citation index (T07 adds the model-assisted detail
	// projection); they run as report producers, never gates.
	citations := research(CapabilityResearchCitations, ClassResearchValidateCitation, "engine.step.research_citations",
		[]ArtifactType{"research_evidence_pack", "full_draft", "research_citation_index"},
		[]ArtifactType{"evidence_report", "research_validation_details"},
		[]Permission{"validation.run"}, true, 1)
	register(citations)
	fact := research(CapabilityResearchFact, ClassResearchValidateFact, "engine.step.research_fact",
		[]ArtifactType{"research_evidence_pack", "full_draft"}, []ArtifactType{"fact_report"},
		[]Permission{"validation.run"}, true, 1)
	register(fact)
}

func validBounds(bounds Bounds) bool {
	return bounds.MaxAttempts > 0 && bounds.MaxConcurrency > 0 && bounds.MaxItems > 0 && bounds.MaxCostUSD >= 0 && !math.IsNaN(bounds.MaxCostUSD) && !math.IsInf(bounds.MaxCostUSD, 0) && bounds.TimeoutMS > 0
}

func cloneManifest(manifest CapabilityManifest) CapabilityManifest {
	manifest.InputTypes = append([]ArtifactType(nil), manifest.InputTypes...)
	manifest.OptionalInputTypes = append([]ArtifactType(nil), manifest.OptionalInputTypes...)
	manifest.OutputTypes = append([]ArtifactType(nil), manifest.OutputTypes...)
	manifest.Permissions = append([]Permission(nil), manifest.Permissions...)
	manifest.Context.RequiredContext = append([]ContextBlockName(nil), manifest.Context.RequiredContext...)
	manifest.Context.OptionalContext = append([]ContextBlockName(nil), manifest.Context.OptionalContext...)
	manifest.Context.ForbiddenContext = append([]ContextBlockName(nil), manifest.Context.ForbiddenContext...)
	manifest.SupportedNodeKinds = append([]NodeKind(nil), manifest.SupportedNodeKinds...)
	return manifest
}
