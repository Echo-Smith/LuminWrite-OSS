package writingplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const SchemaVersion = "lcp/1.0"

type NodeKind string

const (
	NodeSequence  NodeKind = "sequence"
	NodeParallel  NodeKind = "parallel"
	NodeMap       NodeKind = "map"
	NodeReduce    NodeKind = "reduce"
	NodeCondition NodeKind = "condition"
	NodeRetry     NodeKind = "retry"
	NodeRefine    NodeKind = "refine"
	NodeHumanGate NodeKind = "human_gate"
	NodeValidate  NodeKind = "validate"
	NodeFallback  NodeKind = "fallback"
	NodeAction    NodeKind = "action"
)

type FailurePath string

const (
	FailureFail     FailurePath = "fail"
	FailurePause    FailurePath = "pause"
	FailureFallback FailurePath = "fallback"
	FailurePartial  FailurePath = "partial"
)

type TrustLevel string

const (
	TrustT1 TrustLevel = "T1"
	TrustT2 TrustLevel = "T2"
	TrustT3 TrustLevel = "T3"
	TrustT4 TrustLevel = "T4"
)

type PlanStatus string

const (
	PlanDraft      PlanStatus = "draft"
	PlanValidated  PlanStatus = "validated"
	PlanApproved   PlanStatus = "approved"
	PlanLocked     PlanStatus = "locked"
	PlanSuperseded PlanStatus = "superseded"
)

type Actor string

const (
	ActorUser   Actor = "user"
	ActorSystem Actor = "system"
	ActorModel  Actor = "model"
)

type ArtifactType string
type Permission string

type ObjectRef struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Hash    string `json:"hash"`
}

type ProposedStep struct {
	StepID         string   `json:"step_id"`
	Objective      string   `json:"objective"`
	CapabilityHint string   `json:"capability_hint"`
	DependsOn      []string `json:"depends_on"`
}

type IntentPlan struct {
	IntentPlanID   string         `json:"intent_plan_id"`
	ContractRef    ObjectRef      `json:"contract_ref"`
	IntentPlanHash string         `json:"intent_plan_hash"`
	Summary        string         `json:"summary"`
	CreatedBy      Actor          `json:"created_by"`
	CreatedAt      time.Time      `json:"created_at"`
	ProposedSteps  []ProposedStep `json:"proposed_steps"`
}

type Bounds struct {
	MaxAttempts    int     `json:"max_attempts"`
	MaxConcurrency int     `json:"max_concurrency"`
	MaxItems       int     `json:"max_items"`
	MaxCostUSD     float64 `json:"max_cost_usd"`
	TimeoutMS      int64   `json:"timeout_ms"`
}

type PlanNode struct {
	NodeID              string         `json:"node_id"`
	Kind                NodeKind       `json:"kind"`
	Capability          string         `json:"capability"`
	CapabilityVersion   string         `json:"capability_version"`
	DependsOn           []string       `json:"depends_on"`
	InputArtifactTypes  []ArtifactType `json:"input_artifact_types"`
	OutputArtifactTypes []ArtifactType `json:"output_artifact_types"`
	Bounds              Bounds         `json:"bounds"`
	FailurePath         FailurePath    `json:"failure_path"`
	FallbackNodeID      string         `json:"fallback_node_id,omitempty"`
	PartialOutputTypes  []ArtifactType `json:"partial_output_types,omitempty"`
}

type StaticValidation struct {
	Valid                     bool      `json:"valid"`
	CheckedAt                 time.Time `json:"checked_at"`
	Errors                    []string  `json:"errors"`
	CapabilityRegistryVersion string    `json:"capability_registry_version"`
	BudgetValid               bool      `json:"budget_valid"`
	PermissionsValid          bool      `json:"permissions_valid"`
	ArtifactFlowValid         bool      `json:"artifact_flow_valid"`
	FailurePathsValid         bool      `json:"failure_paths_valid"`
}

type ExecutablePlan struct {
	PlanID           string           `json:"plan_id"`
	IntentPlanRef    ObjectRef        `json:"intent_plan_ref"`
	PlanHash         string           `json:"plan_hash"`
	TrustLevel       TrustLevel       `json:"trust_level"`
	Status           PlanStatus       `json:"status"`
	RootNodeID       string           `json:"root_node_id"`
	Nodes            []PlanNode       `json:"nodes"`
	StaticValidation StaticValidation `json:"static_validation"`
}

type PlanBudget struct {
	MaxCostUSD     float64 `json:"max_cost_usd"`
	MaxDurationMS  int64   `json:"max_duration_ms"`
	MaxConcurrency int     `json:"max_concurrency"`
	MaxNodes       int     `json:"max_nodes"`
	MaxItems       int     `json:"max_items"`
}

type WritingPlanEnvelope struct {
	SchemaVersion    string           `json:"schema_version"`
	IntentPlan       IntentPlan       `json:"intent_plan"`
	ExecutablePlan   ExecutablePlan   `json:"executable_plan"`
	StrategyDecision StrategyDecision `json:"strategy_decision"`
}

func (e WritingPlanEnvelope) Validate() error {
	if e.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version must be %q", SchemaVersion)
	}
	if err := e.IntentPlan.Validate(); err != nil {
		return fmt.Errorf("intent_plan: %w", err)
	}
	if e.ExecutablePlan.IntentPlanRef.ID != e.IntentPlan.IntentPlanID || e.ExecutablePlan.IntentPlanRef.Version != 1 || e.ExecutablePlan.IntentPlanRef.Hash != e.IntentPlan.IntentPlanHash {
		return errors.New("executable plan does not reference the enclosed intent plan")
	}
	if expected, err := e.ExecutablePlan.ComputeHash(); err != nil || expected != e.ExecutablePlan.PlanHash {
		return errors.New("executable plan hash mismatch")
	}
	if e.ExecutablePlan.StaticValidation.CheckedAt.IsZero() || strings.TrimSpace(e.ExecutablePlan.StaticValidation.CapabilityRegistryVersion) == "" {
		return errors.New("executable plan lacks static validation evidence")
	}
	if e.ExecutablePlan.Status == PlanValidated || e.ExecutablePlan.Status == PlanApproved || e.ExecutablePlan.Status == PlanLocked {
		if !e.ExecutablePlan.StaticValidation.Valid || len(e.ExecutablePlan.StaticValidation.Errors) > 0 {
			return errors.New("executable status is inconsistent with static validation")
		}
	}
	if e.ExecutablePlan.TrustLevel == TrustT4 && e.ExecutablePlan.Status != PlanDraft && e.ExecutablePlan.Status != PlanSuperseded {
		return errors.New("T4 plan must not be executable")
	}
	if err := e.StrategyDecision.Validate(e.ExecutablePlan); err != nil {
		return fmt.Errorf("strategy_decision: %w", err)
	}
	return nil
}

// ValidateForDispatch re-runs static validation against the current registry,
// budget, permissions, contract policy, and validator requirements. Wire-level
// static_validation is immutable evidence, never authorization to execute.
func (e WritingPlanEnvelope) ValidateForDispatch(ctx ValidationContext) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if e.ExecutablePlan.Status != PlanValidated && e.ExecutablePlan.Status != PlanApproved && e.ExecutablePlan.Status != PlanLocked {
		return errors.New("plan is not in a dispatchable status")
	}
	plan := e.ExecutablePlan
	plan.PlanHash = ""
	plan.Status = PlanDraft
	plan.StaticValidation = StaticValidation{}
	fresh := ValidatePlan(plan, ctx)
	if !fresh.Valid {
		return fmt.Errorf("plan failed dispatch validation: %s", strings.Join(fresh.Errors, "; "))
	}
	if fresh.CapabilityRegistryVersion != e.ExecutablePlan.StaticValidation.CapabilityRegistryVersion {
		return errors.New("capability registry snapshot changed since plan validation")
	}
	return nil
}

var (
	hashPattern            = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	intentIDPattern        = regexp.MustCompile(`^iplan_[A-Za-z0-9_-]+$`)
	planIDPattern          = regexp.MustCompile(`^plan_[A-Za-z0-9_-]+$`)
	nodeIDPattern          = regexp.MustCompile(`^node_[A-Za-z0-9_-]+$`)
	decisionIDPattern      = regexp.MustCompile(`^decision_[A-Za-z0-9_-]+$`)
	capabilityPattern      = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)+$`)
	capabilityClassPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
)

func (p IntentPlan) ComputeHash() (string, error) {
	copy := p
	copy.IntentPlanHash = ""
	payload, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	return hashBytes(payload), nil
}

func (p IntentPlan) WithComputedHash() (IntentPlan, error) {
	for i := range p.ProposedSteps {
		if p.ProposedSteps[i].DependsOn == nil {
			p.ProposedSteps[i].DependsOn = []string{}
		}
	}
	hash, err := p.ComputeHash()
	if err != nil {
		return IntentPlan{}, err
	}
	p.IntentPlanHash = hash
	return p, p.Validate()
}

func (p IntentPlan) Validate() error {
	if !intentIDPattern.MatchString(p.IntentPlanID) {
		return errors.New("intent_plan_id must use the iplan_ prefix")
	}
	if err := validateRef(p.ContractRef); err != nil {
		return fmt.Errorf("contract_ref: %w", err)
	}
	if strings.TrimSpace(p.Summary) == "" || p.CreatedAt.IsZero() {
		return errors.New("summary and created_at are required")
	}
	if p.CreatedBy != ActorUser && p.CreatedBy != ActorSystem && p.CreatedBy != ActorModel {
		return fmt.Errorf("invalid created_by %q", p.CreatedBy)
	}
	if len(p.ProposedSteps) == 0 {
		return errors.New("proposed_steps must not be empty")
	}
	seen := make(map[string]struct{}, len(p.ProposedSteps))
	for _, step := range p.ProposedSteps {
		if strings.TrimSpace(step.StepID) == "" || strings.TrimSpace(step.Objective) == "" || !capabilityClassPattern.MatchString(step.CapabilityHint) {
			return fmt.Errorf("invalid proposed step %q", step.StepID)
		}
		if _, exists := seen[step.StepID]; exists {
			return fmt.Errorf("duplicate proposed step %q", step.StepID)
		}
		seen[step.StepID] = struct{}{}
	}
	for _, step := range p.ProposedSteps {
		for _, dep := range step.DependsOn {
			if _, ok := seen[dep]; !ok {
				return fmt.Errorf("step %q depends on unknown step %q", step.StepID, dep)
			}
		}
	}
	expected, err := p.ComputeHash()
	if err != nil {
		return err
	}
	if p.IntentPlanHash != expected {
		return fmt.Errorf("intent_plan_hash mismatch: expected %s", expected)
	}
	return nil
}

func (p ExecutablePlan) ComputeHash() (string, error) {
	copy := p
	copy.PlanHash = ""
	// checked_at is observational metadata. The deterministic validation result,
	// registry version, status, bounds, and topology remain hash-bound.
	copy.StaticValidation.CheckedAt = time.Time{}
	payload, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	return hashBytes(payload), nil
}

func (p ExecutablePlan) WithComputedHash() (ExecutablePlan, error) {
	for i := range p.Nodes {
		if p.Nodes[i].DependsOn == nil {
			p.Nodes[i].DependsOn = []string{}
		}
		if p.Nodes[i].InputArtifactTypes == nil {
			p.Nodes[i].InputArtifactTypes = []ArtifactType{}
		}
		if p.Nodes[i].OutputArtifactTypes == nil {
			p.Nodes[i].OutputArtifactTypes = []ArtifactType{}
		}
	}
	if p.StaticValidation.Errors == nil {
		p.StaticValidation.Errors = []string{}
	}
	hash, err := p.ComputeHash()
	if err != nil {
		return ExecutablePlan{}, err
	}
	p.PlanHash = hash
	return p, nil
}

func validateRef(ref ObjectRef) error {
	if strings.TrimSpace(ref.ID) == "" || ref.Version < 1 || !hashPattern.MatchString(ref.Hash) {
		return errors.New("id, positive version, and sha256 hash are required")
	}
	return nil
}

func hashBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}
