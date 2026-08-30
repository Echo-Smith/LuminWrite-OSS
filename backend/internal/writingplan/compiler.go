package writingplan

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

type SelectionSource string

const (
	SelectionUser   SelectionSource = "user"
	SelectionSystem SelectionSource = "system"
)

type StrategyCandidate struct {
	PlanHash            string     `json:"plan_hash"`
	TrustLevel          TrustLevel `json:"trust_level"`
	TemplateID          *string    `json:"template_id"`
	EstimatedCostUSD    float64    `json:"estimated_cost_usd"`
	EstimatedDurationMS int64      `json:"estimated_duration_ms"`
	EstimatedConfidence float64    `json:"estimated_confidence"`
}

type StrategyDecision struct {
	DecisionID             string                          `json:"decision_id"`
	IntentPlanRef          ObjectRef                       `json:"intent_plan_ref"`
	Candidates             []StrategyCandidate             `json:"candidates"`
	SelectedPlanHash       string                          `json:"selected_plan_hash"`
	SelectionSource        SelectionSource                 `json:"selection_source"`
	RequestedOrchestration writingkernel.OrchestrationMode `json:"requested_orchestration"`
	EffectiveOrchestration writingkernel.OrchestrationMode `json:"effective_orchestration"`
	UserOverride           bool                            `json:"user_override"`
	ReasonCode             string                          `json:"reason_code"`
	Summary                string                          `json:"summary"`
	Confidence             float64                         `json:"confidence"`
	ApprovalRequired       bool                            `json:"approval_required"`
	DegradationConditions  []string                        `json:"degradation_conditions"`
	FallbackPlanHash       *string                         `json:"fallback_plan_hash"`
	CreatedAt              time.Time                       `json:"created_at"`
}

func (d StrategyDecision) Validate(plan ExecutablePlan) error {
	if !decisionIDPattern.MatchString(d.DecisionID) {
		return fmt.Errorf("decision_id must use decision_ prefix")
	}
	if err := validateRef(d.IntentPlanRef); err != nil {
		return err
	}
	if d.IntentPlanRef != plan.IntentPlanRef {
		return fmt.Errorf("intent plan reference mismatch")
	}
	if d.SelectedPlanHash != plan.PlanHash {
		return fmt.Errorf("selected plan hash does not match executable plan")
	}
	if d.SelectionSource != SelectionUser && d.SelectionSource != SelectionSystem {
		return fmt.Errorf("invalid selection source")
	}
	if (d.RequestedOrchestration == writingkernel.OrchestrationModeAuto) != (d.SelectionSource == SelectionSystem) {
		return fmt.Errorf("selection source does not match requested orchestration")
	}
	if d.UserOverride != (d.SelectionSource == SelectionUser) {
		return fmt.Errorf("user override does not match selection source")
	}
	if !d.RequestedOrchestration.Valid() || !d.EffectiveOrchestration.Valid() || d.EffectiveOrchestration == writingkernel.OrchestrationModeAuto {
		return fmt.Errorf("invalid requested/effective orchestration")
	}
	if d.RequestedOrchestration != writingkernel.OrchestrationModeAuto && d.EffectiveOrchestration != d.RequestedOrchestration {
		return fmt.Errorf("effective orchestration replaced explicit user strategy")
	}
	if d.Confidence < 0 || d.Confidence > 1 || math.IsNaN(d.Confidence) || math.IsInf(d.Confidence, 0) || d.CreatedAt.IsZero() || strings.TrimSpace(d.ReasonCode) == "" || strings.TrimSpace(d.Summary) == "" || d.DegradationConditions == nil {
		return fmt.Errorf("incomplete strategy decision")
	}
	selected := false
	for _, candidate := range d.Candidates {
		if !hashPattern.MatchString(candidate.PlanHash) || !validTrustLevel(candidate.TrustLevel) || candidate.EstimatedCostUSD < 0 || math.IsNaN(candidate.EstimatedCostUSD) || math.IsInf(candidate.EstimatedCostUSD, 0) || candidate.EstimatedDurationMS < 0 || candidate.EstimatedConfidence < 0 || candidate.EstimatedConfidence > 1 || math.IsNaN(candidate.EstimatedConfidence) || math.IsInf(candidate.EstimatedConfidence, 0) {
			return fmt.Errorf("invalid strategy candidate")
		}
		if candidate.PlanHash == d.SelectedPlanHash {
			if candidate.TrustLevel != plan.TrustLevel {
				return fmt.Errorf("selected candidate trust level does not match executable plan")
			}
			selected = true
		}
	}
	if !selected {
		return fmt.Errorf("selected plan is absent from candidates")
	}
	if d.FallbackPlanHash != nil && !hashPattern.MatchString(*d.FallbackPlanHash) {
		return fmt.Errorf("invalid fallback plan hash")
	}
	return nil
}

type ValidationContext struct {
	Registry                *CapabilityRegistry
	InitialArtifactTypes    []ArtifactType
	AllowedPermissions      []Permission
	Budget                  PlanBudget
	RequiredValidators      []string
	RequiredFinalArtifact   ArtifactType
	ExternalResearchAllowed bool
	Now                     time.Time
}

type CompileRequest struct {
	IntentPlan               IntentPlan
	Contract                 writingkernel.WritingContract
	Registry                 *CapabilityRegistry
	Templates                *TemplateRegistry
	InitialArtifactTypes     []ArtifactType
	AllowedPermissions       []Permission
	Budget                   PlanBudget
	RequiredValidators       []string
	RequiredFinalArtifact    ArtifactType
	SystemRecommendation     writingkernel.OrchestrationMode
	ApprovalCostThresholdUSD float64
}

type CompileResult struct {
	Plan     ExecutablePlan
	Decision StrategyDecision
}

type PlanCompileError struct{ Errors []string }

func (e *PlanCompileError) Error() string {
	return "PLAN_NOT_EXECUTABLE: " + strings.Join(e.Errors, "; ")
}
func (e *PlanCompileError) Is(target error) bool { _, ok := target.(*PlanCompileError); return ok }

var ErrPlanNotExecutable error = &PlanCompileError{}

type Compiler struct{ Now func() time.Time }

func NewCompiler() *Compiler                            { return &Compiler{Now: func() time.Time { return time.Now().UTC() }} }
func Compile(req CompileRequest) (CompileResult, error) { return NewCompiler().Compile(req) }

func (c *Compiler) Compile(req CompileRequest) (CompileResult, error) {
	if req.Registry == nil {
		return CompileResult{}, fmt.Errorf("capability registry is required")
	}
	if req.Templates == nil {
		req.Templates = DefaultTemplateRegistry()
	}
	if err := req.IntentPlan.Validate(); err != nil {
		return CompileResult{}, fmt.Errorf("invalid intent plan: %w", err)
	}
	if err := req.Contract.Validate(); err != nil {
		return CompileResult{}, fmt.Errorf("invalid contract: %w", err)
	}
	if req.IntentPlan.ContractRef.ID != req.Contract.ContractID || req.IntentPlan.ContractRef.Version != req.Contract.Version || req.IntentPlan.ContractRef.Hash != req.Contract.ContractHash {
		return CompileResult{}, fmt.Errorf("CONTRACT_REF_MISMATCH")
	}

	recommendation := req.SystemRecommendation
	if recommendation == "" || recommendation == writingkernel.OrchestrationModeAuto {
		recommendation = recommendMode(req.Contract)
	}
	resolved, err := writingkernel.ResolveExecutionControl(req.Contract, writingkernel.ExecutionRecommendation{OrchestrationMode: recommendation})
	if err != nil {
		return CompileResult{}, err
	}
	requested := resolved.Requested.OrchestrationMode
	effective := resolved.Effective.OrchestrationMode
	if effective == writingkernel.OrchestrationModeAuto {
		effective = recommendMode(req.Contract)
	}

	plan := ExecutablePlan{PlanID: deterministicID("plan_", req.IntentPlan.IntentPlanHash+"\x00"+string(effective)), IntentPlanRef: ObjectRef{ID: req.IntentPlan.IntentPlanID, Version: 1, Hash: req.IntentPlan.IntentPlanHash}, Status: PlanDraft}
	template, hasTemplate := req.Templates.Get(effective)
	missing := make([]string, 0)
	if hasTemplate {
		plan.TrustLevel = template.TrustLevel
		plan.RootNodeID = template.RootNodeID
		for _, spec := range template.Nodes {
			node, ok := resolveTemplateNode(spec, req.Registry)
			if !ok {
				missing = append(missing, spec.CapabilityClass)
				continue
			}
			plan.Nodes = append(plan.Nodes, node)
		}
		req.RequiredValidators = unionStrings(req.RequiredValidators, template.RequiredValidators)
	} else {
		plan.TrustLevel = TrustT3
		plan.Nodes, missing = compileDynamic(req.IntentPlan, req.Registry)
		if len(plan.Nodes) > 0 {
			plan.RootNodeID = plan.Nodes[0].NodeID
		}
	}
	if req.RequiredFinalArtifact == "" {
		req.RequiredFinalArtifact = "revision_set"
	}
	if req.RequiredFinalArtifact == "revision_set" {
		req.RequiredValidators = unionStrings(req.RequiredValidators, validatorsForAssurance(req.Contract.Collaboration.AssuranceLevel))
	}
	nodesBeforeRequiredValidators := len(plan.Nodes)
	plan.Nodes, missing = ensureValidators(plan.Nodes, req.RequiredValidators, req.Registry, missing)
	if hasTemplate && plan.TrustLevel == TrustT1 && len(plan.Nodes) > nodesBeforeRequiredValidators {
		plan.TrustLevel = TrustT2
	}
	if len(missing) > 0 {
		plan.TrustLevel = TrustT4
	}

	now := c.Now()
	validation := ValidatePlan(plan, ValidationContext{Registry: req.Registry, InitialArtifactTypes: req.InitialArtifactTypes, AllowedPermissions: req.AllowedPermissions,
		Budget: req.Budget, RequiredValidators: req.RequiredValidators, RequiredFinalArtifact: req.RequiredFinalArtifact,
		ExternalResearchAllowed: req.Contract.MaterialPolicy.AllowExternalResearch, Now: now})
	for _, capabilityClass := range missing {
		validation.Errors = append(validation.Errors, "MISSING_CAPABILITY_CLASS: "+capabilityClass)
	}
	if len(missing) > 0 {
		validation.Valid = false
	}
	if hasErrorPrefix(validation.Errors, "MISSING_REQUIRED_VALIDATOR:") || hasErrorPrefix(validation.Errors, "CONTRACT_FORBIDS_EXTERNAL_RESEARCH:") {
		plan.TrustLevel = TrustT4
	}
	if !validation.Valid {
		plan.TrustLevel = TrustT4
	}
	plan.StaticValidation = validation
	if validation.Valid {
		plan.Status = PlanValidated
	}
	plan, err = plan.WithComputedHash()
	if err != nil {
		return CompileResult{}, err
	}

	selectionSource := SelectionUser
	if requested == writingkernel.OrchestrationModeAuto {
		selectionSource = SelectionSystem
	}
	templateID := (*string)(nil)
	if hasTemplate {
		id := template.ID
		templateID = &id
	}
	cost, duration := estimatePlan(plan, req.Registry)
	approvalThreshold := req.ApprovalCostThresholdUSD
	if approvalThreshold <= 0 {
		approvalThreshold = 5
	}
	decision := StrategyDecision{
		DecisionID: deterministicID("decision_", plan.PlanHash), IntentPlanRef: plan.IntentPlanRef,
		Candidates:       []StrategyCandidate{{PlanHash: plan.PlanHash, TrustLevel: plan.TrustLevel, TemplateID: templateID, EstimatedCostUSD: cost, EstimatedDurationMS: duration, EstimatedConfidence: confidenceFor(plan.TrustLevel)}},
		SelectedPlanHash: plan.PlanHash, SelectionSource: selectionSource, RequestedOrchestration: requested, EffectiveOrchestration: effective,
		UserOverride: requested != writingkernel.OrchestrationModeAuto, ReasonCode: reasonCode(effective, plan.TrustLevel),
		Summary: strategySummary(effective, plan.TrustLevel), Confidence: confidenceFor(plan.TrustLevel),
		ApprovalRequired:      plan.TrustLevel == TrustT3 || plan.TrustLevel == TrustT4 || cost > approvalThreshold || req.Contract.Collaboration.ApprovalMode == writingkernel.ApprovalModeAlways,
		DegradationConditions: append([]string{}, missing...), CreatedAt: now,
	}
	result := CompileResult{Plan: plan, Decision: decision}
	if !validation.Valid {
		return result, &PlanCompileError{Errors: append([]string(nil), validation.Errors...)}
	}
	return result, nil
}

func ValidatePlan(plan ExecutablePlan, ctx ValidationContext) StaticValidation {
	result := StaticValidation{CheckedAt: ctx.Now, Errors: []string{}, BudgetValid: true, PermissionsValid: true, ArtifactFlowValid: true, FailurePathsValid: true}
	if result.CheckedAt.IsZero() {
		result.CheckedAt = time.Now().UTC()
	}
	if ctx.Registry == nil {
		result.Errors = append(result.Errors, "REGISTRY_REQUIRED")
		return result
	}
	result.CapabilityRegistryVersion = ctx.Registry.Version()
	if strings.TrimSpace(result.CapabilityRegistryVersion) == "" {
		result.Errors = append(result.Errors, "INVALID_CAPABILITY_REGISTRY_VERSION")
	}
	if ctx.Budget.MaxCostUSD < 0 || math.IsNaN(ctx.Budget.MaxCostUSD) || math.IsInf(ctx.Budget.MaxCostUSD, 0) || ctx.Budget.MaxDurationMS < 0 || ctx.Budget.MaxConcurrency < 0 || ctx.Budget.MaxNodes < 0 || ctx.Budget.MaxItems < 0 {
		result.BudgetValid = false
		result.Errors = append(result.Errors, "INVALID_BUDGET")
	}
	if !planIDPattern.MatchString(plan.PlanID) {
		result.Errors = append(result.Errors, "INVALID_PLAN_ID: "+plan.PlanID)
	}
	if err := validateRef(plan.IntentPlanRef); err != nil {
		result.Errors = append(result.Errors, "INVALID_INTENT_PLAN_REF")
	}
	if !validTrustLevel(plan.TrustLevel) {
		result.Errors = append(result.Errors, "INVALID_TRUST_LEVEL")
	}
	if !validPlanStatus(plan.Status) {
		result.Errors = append(result.Errors, "INVALID_PLAN_STATUS")
	}
	nodes := make(map[string]PlanNode, len(plan.Nodes))
	for _, node := range plan.Nodes {
		if !nodeIDPattern.MatchString(node.NodeID) {
			result.Errors = append(result.Errors, "INVALID_NODE_ID: "+node.NodeID)
		}
		if _, exists := nodes[node.NodeID]; exists {
			result.Errors = append(result.Errors, "DUPLICATE_NODE: "+node.NodeID)
		}
		nodes[node.NodeID] = node
		if !validNodeKind(node.Kind) {
			result.Errors = append(result.Errors, "INVALID_NODE_KIND: "+node.NodeID)
		}
		if !capabilityPattern.MatchString(node.Capability) || strings.TrimSpace(node.CapabilityVersion) == "" {
			result.Errors = append(result.Errors, "INVALID_CAPABILITY_REF: "+node.NodeID)
		}
		if len(node.OutputArtifactTypes) == 0 {
			result.ArtifactFlowValid = false
			result.Errors = append(result.Errors, "MISSING_OUTPUT_TYPE: "+node.NodeID)
		}
		if !validFailurePath(node.FailurePath) {
			result.FailurePathsValid = false
			result.Errors = append(result.Errors, "INVALID_FAILURE_PATH: "+node.NodeID)
		}
	}
	if _, ok := nodes[plan.RootNodeID]; !ok {
		result.Errors = append(result.Errors, "MISSING_ROOT_NODE: "+plan.RootNodeID)
	}
	entries := []string{}
	for _, node := range plan.Nodes {
		if len(node.DependsOn) == 0 {
			entries = append(entries, node.NodeID)
		}
		for _, dep := range node.DependsOn {
			if _, ok := nodes[dep]; !ok {
				result.ArtifactFlowValid = false
				result.Errors = append(result.Errors, "UNKNOWN_DEPENDENCY: "+node.NodeID+":"+dep)
			}
		}
	}
	if len(entries) != 1 || (len(entries) == 1 && entries[0] != plan.RootNodeID) {
		result.Errors = append(result.Errors, "INVALID_PLAN_ROOT")
	}
	for _, orphan := range unreachableNodes(plan.RootNodeID, plan.Nodes) {
		result.Errors = append(result.Errors, "ORPHAN_NODE: "+orphan)
	}
	if ctx.Budget.MaxNodes > 0 && len(plan.Nodes) > ctx.Budget.MaxNodes {
		result.BudgetValid = false
		result.Errors = append(result.Errors, "BUDGET_NODES_EXCEEDED")
	}
	if cycle := dependencyCycle(plan.Nodes, nodes); cycle != "" {
		result.Errors = append(result.Errors, "DEPENDENCY_CYCLE: "+cycle)
		result.ArtifactFlowValid = false
	}
	if cycle := controlFlowCycle(plan.Nodes, nodes); cycle != "" {
		result.Errors = append(result.Errors, "CONTROL_FLOW_CYCLE: "+cycle)
		result.FailurePathsValid = false
	}

	allowedPermissions := stringSetPermissions(ctx.AllowedPermissions)
	initial := artifactSet(ctx.InitialArtifactTypes)
	produced := make(map[string]map[ArtifactType]struct{}, len(plan.Nodes))
	validatorSeen := make(map[string]bool)
	validatorNodeIDs := make(map[string]string)
	totalCost := 0.0
	totalDuration := int64(0)
	for _, node := range topologicalNodes(plan.Nodes, nodes) {
		manifest, ok := ctx.Registry.Get(node.Capability)
		if !ok {
			result.Errors = append(result.Errors, "UNKNOWN_CAPABILITY: "+node.NodeID+":"+node.Capability)
			result.ArtifactFlowValid = false
			continue
		}
		if !ctx.Registry.ExecutorRegistered(manifest.Executor) {
			result.Errors = append(result.Errors, "UNKNOWN_EXECUTOR: "+manifest.Executor)
		}
		if !manifest.Available {
			result.Errors = append(result.Errors, "CAPABILITY_UNAVAILABLE: "+manifest.ID)
		}
		if node.CapabilityVersion != manifest.Version {
			result.Errors = append(result.Errors, "CAPABILITY_VERSION_MISMATCH: "+node.NodeID)
		}
		if !containsNodeKind(manifest.SupportedNodeKinds, node.Kind) {
			result.Errors = append(result.Errors, "UNSUPPORTED_NODE_KIND: "+node.NodeID)
		}
		if !validBounds(node.Bounds) || exceedsBounds(node.Bounds, manifest.MaxBounds) {
			result.BudgetValid = false
			result.Errors = append(result.Errors, "UNBOUNDED_NODE: "+node.NodeID)
		}
		if ctx.Budget.MaxConcurrency > 0 && node.Bounds.MaxConcurrency > ctx.Budget.MaxConcurrency {
			result.BudgetValid = false
			result.Errors = append(result.Errors, "BUDGET_CONCURRENCY_EXCEEDED: "+node.NodeID)
		}
		if ctx.Budget.MaxItems > 0 && node.Bounds.MaxItems > ctx.Budget.MaxItems {
			result.BudgetValid = false
			result.Errors = append(result.Errors, "BUDGET_ITEMS_EXCEEDED: "+node.NodeID)
		}
		worstCaseExecutions := float64(node.Bounds.MaxAttempts)
		if node.Kind == NodeMap {
			worstCaseExecutions *= float64(node.Bounds.MaxItems)
		}
		if manifest.EstimatedCostUSD*worstCaseExecutions > node.Bounds.MaxCostUSD {
			result.BudgetValid = false
			result.Errors = append(result.Errors, "NODE_COST_BOUND_TOO_LOW: "+node.NodeID)
		}
		if float64(manifest.EstimatedDurationMS)*worstCaseExecutions > float64(node.Bounds.TimeoutMS) {
			result.BudgetValid = false
			result.Errors = append(result.Errors, "NODE_TIMEOUT_BOUND_TOO_LOW: "+node.NodeID)
		}
		if node.Kind == NodeParallel && node.Bounds.MaxConcurrency < 2 {
			result.Errors = append(result.Errors, "INVALID_PARALLEL_BOUND: "+node.NodeID)
		}
		if node.Kind == NodeMap && node.Bounds.MaxItems < 1 {
			result.Errors = append(result.Errors, "INVALID_MAP_BOUND: "+node.NodeID)
		}
		for _, permission := range manifest.Permissions {
			if _, ok := allowedPermissions[permission]; !ok {
				result.PermissionsValid = false
				result.Errors = append(result.Errors, "PERMISSION_DENIED: "+node.NodeID+":"+string(permission))
			}
			if permission == "external.research" && !ctx.ExternalResearchAllowed {
				result.PermissionsValid = false
				result.Errors = append(result.Errors, "CONTRACT_FORBIDS_EXTERNAL_RESEARCH: "+node.NodeID)
			}
		}
		available := cloneArtifactSet(initial)
		for _, dep := range transitiveDependencies(node.NodeID, nodes) {
			for artifact := range produced[dep] {
				available[artifact] = struct{}{}
			}
		}
		for _, input := range node.InputArtifactTypes {
			if _, ok := available[input]; !ok {
				result.ArtifactFlowValid = false
				result.Errors = append(result.Errors, "MISSING_INPUT: "+node.NodeID+":"+string(input))
			}
		}
		acceptedInputs := append(append([]ArtifactType(nil), manifest.InputTypes...), manifest.OptionalInputTypes...)
		if !artifactSubset(manifest.InputTypes, node.InputArtifactTypes) || !artifactSubset(node.InputArtifactTypes, acceptedInputs) || !artifactSubset(node.OutputArtifactTypes, manifest.OutputTypes) {
			result.ArtifactFlowValid = false
			result.Errors = append(result.Errors, "CAPABILITY_TYPE_MISMATCH: "+node.NodeID)
		}
		produced[node.NodeID] = artifactSet(node.OutputArtifactTypes)
		if manifest.Validator && node.Kind == NodeValidate {
			validatorSeen[manifest.ID] = true
			validatorNodeIDs[manifest.ID] = node.NodeID
		}
		if node.FailurePath == FailureFallback {
			fallback, exists := nodes[node.FallbackNodeID]
			if !exists || fallback.Kind != NodeFallback {
				result.FailurePathsValid = false
				result.Errors = append(result.Errors, "INVALID_FALLBACK: "+node.NodeID)
			}
		}
		if node.FailurePath != FailureFallback && node.FallbackNodeID != "" {
			result.FailurePathsValid = false
			result.Errors = append(result.Errors, "UNEXPECTED_FALLBACK_TARGET: "+node.NodeID)
		}
		if node.FailurePath == FailurePartial {
			if len(node.PartialOutputTypes) == 0 || !artifactSubset(node.PartialOutputTypes, node.OutputArtifactTypes) {
				result.FailurePathsValid = false
				result.Errors = append(result.Errors, "INVALID_PARTIAL_OUTPUTS: "+node.NodeID)
			}
		} else if len(node.PartialOutputTypes) > 0 {
			result.FailurePathsValid = false
			result.Errors = append(result.Errors, "UNEXPECTED_PARTIAL_OUTPUTS: "+node.NodeID)
		}
		if node.FailurePath == FailurePartial && containsArtifact(node.OutputArtifactTypes, ctx.RequiredFinalArtifact) {
			result.FailurePathsValid = false
			result.Errors = append(result.Errors, "PARTIAL_FINAL_DELIVERABLE: "+node.NodeID)
		}
		totalCost += node.Bounds.MaxCostUSD
		totalDuration += node.Bounds.TimeoutMS
	}
	if ctx.Budget.MaxCostUSD > 0 && totalCost > ctx.Budget.MaxCostUSD {
		result.BudgetValid = false
		result.Errors = append(result.Errors, "BUDGET_COST_EXCEEDED")
	}
	if ctx.Budget.MaxDurationMS > 0 && totalDuration > ctx.Budget.MaxDurationMS {
		result.BudgetValid = false
		result.Errors = append(result.Errors, "BUDGET_DURATION_EXCEEDED")
	}
	for _, validator := range ctx.RequiredValidators {
		if !validatorSeen[validator] {
			result.Errors = append(result.Errors, "MISSING_REQUIRED_VALIDATOR: "+validator)
		}
	}
	if ctx.RequiredFinalArtifact != "" {
		found := false
		for _, node := range plan.Nodes {
			if containsArtifact(node.OutputArtifactTypes, ctx.RequiredFinalArtifact) {
				found = true
				if ctx.RequiredFinalArtifact == "revision_set" {
					ancestors := map[string]bool{}
					for _, dep := range transitiveDependencies(node.NodeID, nodes) {
						ancestors[dep] = true
					}
					for _, validator := range ctx.RequiredValidators {
						validatorNodeID := validatorNodeIDs[validator]
						if validatorNodeID != "" && !ancestors[validatorNodeID] {
							result.Errors = append(result.Errors, "VALIDATOR_NOT_ON_FINAL_PATH: "+validator)
						}
						if validatorNodeID != "" {
							failurePath := nodes[validatorNodeID].FailurePath
							if failurePath != FailureFail && failurePath != FailurePause {
								result.FailurePathsValid = false
								result.Errors = append(result.Errors, "REQUIRED_VALIDATOR_BYPASS: "+validator)
							}
						}
					}
				}
				break
			}
		}
		if !found {
			result.Errors = append(result.Errors, "MISSING_FINAL_ARTIFACT: "+string(ctx.RequiredFinalArtifact))
		}
	}
	if plan.Status == PlanValidated || plan.Status == PlanApproved || plan.Status == PlanLocked {
		if len(plan.StaticValidation.Errors) > 0 || !plan.StaticValidation.Valid {
			result.Errors = append(result.Errors, "STALE_VALIDATED_STATUS")
		}
	}
	if plan.PlanHash != "" {
		if expected, err := plan.ComputeHash(); err != nil || expected != plan.PlanHash {
			result.Errors = append(result.Errors, "PLAN_HASH_MISMATCH")
		}
	}
	result.Valid = len(result.Errors) == 0 && result.BudgetValid && result.PermissionsValid && result.ArtifactFlowValid && result.FailurePathsValid
	return result
}

func resolveTemplateNode(spec TemplateNode, registry *CapabilityRegistry) (PlanNode, bool) {
	candidates := registry.ByClass(spec.CapabilityClass)
	if len(candidates) == 0 {
		candidates = registry.ByClassDeclared(spec.CapabilityClass)
	}
	for _, manifest := range candidates {
		if !containsNodeKind(manifest.SupportedNodeKinds, spec.Kind) {
			continue
		}
		bounds := spec.Bounds
		if bounds.MaxCostUSD > manifest.MaxBounds.MaxCostUSD {
			bounds.MaxCostUSD = manifest.MaxBounds.MaxCostUSD
		}
		if bounds.TimeoutMS > manifest.MaxBounds.TimeoutMS {
			bounds.TimeoutMS = manifest.MaxBounds.TimeoutMS
		}
		return PlanNode{NodeID: spec.NodeID, Kind: spec.Kind, Capability: manifest.ID, CapabilityVersion: manifest.Version, DependsOn: append([]string(nil), spec.DependsOn...), InputArtifactTypes: append([]ArtifactType(nil), spec.InputArtifactTypes...), OutputArtifactTypes: append([]ArtifactType(nil), spec.OutputArtifactTypes...), Bounds: bounds, FailurePath: spec.FailurePath, FallbackNodeID: spec.FallbackNodeID}, true
	}
	return PlanNode{}, false
}

func compileDynamic(intent IntentPlan, registry *CapabilityRegistry) ([]PlanNode, []string) {
	nodes := make([]PlanNode, 0, len(intent.ProposedSteps))
	missing := []string{}
	stepToNode := map[string]string{}
	for i, step := range intent.ProposedSteps {
		stepToNode[step.StepID] = fmt.Sprintf("node_dynamic_%d", i+1)
	}
	for i, step := range intent.ProposedSteps {
		manifest, ok := registry.Get(step.CapabilityHint)
		if !ok {
			candidates := registry.ByClass(step.CapabilityHint)
			if len(candidates) > 0 {
				manifest, ok = candidates[0], true
			}
		}
		if !ok {
			missing = append(missing, step.CapabilityHint)
			continue
		}
		deps := make([]string, 0, len(step.DependsOn))
		for _, dep := range step.DependsOn {
			deps = append(deps, stepToNode[dep])
		}
		kind := NodeAction
		if manifest.Validator {
			kind = NodeValidate
		}
		nodes = append(nodes, PlanNode{NodeID: stepToNode[step.StepID], Kind: kind, Capability: manifest.ID, CapabilityVersion: manifest.Version, DependsOn: deps,
			InputArtifactTypes: append([]ArtifactType(nil), manifest.InputTypes...), OutputArtifactTypes: append([]ArtifactType(nil), manifest.OutputTypes...), Bounds: manifest.MaxBounds, FailurePath: FailurePause})
		_ = i
	}
	return nodes, missing
}

func ensureValidators(nodes []PlanNode, required []string, registry *CapabilityRegistry, missing []string) ([]PlanNode, []string) {
	present := map[string]bool{}
	for _, node := range nodes {
		if manifest, ok := registry.Get(node.Capability); ok && manifest.Validator && node.Kind == NodeValidate {
			present[manifest.ID] = true
		}
	}
	for _, capabilityID := range required {
		if present[capabilityID] {
			continue
		}
		manifest, ok := registry.Get(capabilityID)
		if !ok || !manifest.Validator || !containsNodeKind(manifest.SupportedNodeKinds, NodeValidate) {
			missing = append(missing, capabilityID)
			continue
		}
		deps := validatorDependencies(nodes)
		nodeID := fmt.Sprintf("node_required_validator_%d", len(nodes)+1)
		nodes = append(nodes, PlanNode{NodeID: nodeID, Kind: NodeValidate, Capability: manifest.ID, CapabilityVersion: manifest.Version,
			DependsOn: deps, InputArtifactTypes: append([]ArtifactType(nil), manifest.InputTypes...), OutputArtifactTypes: append([]ArtifactType(nil), manifest.OutputTypes...),
			Bounds: manifest.MaxBounds, FailurePath: FailurePause})
		present[capabilityID] = true
		for i := range nodes {
			if i == len(nodes)-1 || !containsArtifact(nodes[i].OutputArtifactTypes, "revision_set") {
				continue
			}
			nodes[i].DependsOn = appendUnique(nodes[i].DependsOn, nodeID)
		}
	}
	return nodes, missing
}

func terminalNodeIDs(nodes []PlanNode) []string {
	dependedOn := map[string]bool{}
	for _, node := range nodes {
		for _, dep := range node.DependsOn {
			dependedOn[dep] = true
		}
	}
	result := []string{}
	for _, node := range nodes {
		if !dependedOn[node.NodeID] && !containsArtifact(node.OutputArtifactTypes, "revision_set") {
			result = append(result, node.NodeID)
		}
	}
	if len(result) == 0 && len(nodes) > 0 {
		result = append(result, nodes[len(nodes)-1].NodeID)
	}
	return result
}

func validatorDependencies(nodes []PlanNode) []string {
	deps := []string{}
	for _, node := range nodes {
		if containsArtifact(node.OutputArtifactTypes, "revision_set") {
			for _, dep := range node.DependsOn {
				deps = appendUnique(deps, dep)
			}
		}
	}
	if len(deps) > 0 {
		return deps
	}
	return terminalNodeIDs(nodes)
}

func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func recommendMode(contract writingkernel.WritingContract) writingkernel.OrchestrationMode {
	if contract.Collaboration.AssuranceLevel == writingkernel.AssuranceLevelStrict {
		return writingkernel.OrchestrationModeStrictResearch
	}
	if contract.Collaboration.AssuranceLevel == writingkernel.AssuranceLevelSourced || contract.EvidencePolicy.Level == writingkernel.EvidenceLevelSourced || contract.EvidencePolicy.Level == writingkernel.EvidenceLevelStrict {
		return writingkernel.OrchestrationModeSourced
	}
	if contract.Intent.Operation == writingkernel.OperationPolish {
		return writingkernel.OrchestrationModeFast
	}
	return writingkernel.OrchestrationModeOutlineFirst
}

func validatorsForAssurance(level writingkernel.AssuranceLevel) []string {
	switch level {
	case writingkernel.AssuranceLevelStrict:
		return []string{"core.validation.fact", "core.validation.evidence", "core.validation.quality"}
	case writingkernel.AssuranceLevelSourced:
		return []string{"core.validation.evidence", "core.validation.quality"}
	default:
		return []string{"core.validation.quality"}
	}
}

// RequiredValidatorsForAssurance exposes the compiler's mandatory quality
// floor so dispatch authorization cannot trust a validator list supplied by a
// client or by an older plan snapshot.
func RequiredValidatorsForAssurance(level writingkernel.AssuranceLevel) []string {
	return append([]string(nil), validatorsForAssurance(level)...)
}

func hasErrorPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func dependencyCycle(plan []PlanNode, nodes map[string]PlanNode) string {
	state := map[string]uint8{}
	var visit func(string) string
	visit = func(id string) string {
		if state[id] == 1 {
			return id
		}
		if state[id] == 2 {
			return ""
		}
		state[id] = 1
		for _, dep := range nodes[id].DependsOn {
			if _, ok := nodes[dep]; !ok {
				return id + "->" + dep
			}
			if cycle := visit(dep); cycle != "" {
				return cycle
			}
		}
		state[id] = 2
		return ""
	}
	for _, node := range plan {
		if cycle := visit(node.NodeID); cycle != "" {
			return cycle
		}
	}
	return ""
}

func controlFlowCycle(plan []PlanNode, nodes map[string]PlanNode) string {
	hasFallback := false
	for _, node := range plan {
		if node.FailurePath == FailureFallback {
			hasFallback = true
			break
		}
	}
	if !hasFallback {
		return ""
	}
	state := map[string]uint8{}
	var visit func(string) string
	visit = func(id string) string {
		if state[id] == 1 {
			return id
		}
		if state[id] == 2 {
			return ""
		}
		state[id] = 1
		node := nodes[id]
		next := append([]string(nil), node.DependsOn...)
		if node.FailurePath == FailureFallback && node.FallbackNodeID != "" {
			next = append(next, node.FallbackNodeID)
		}
		for _, target := range next {
			if _, ok := nodes[target]; !ok {
				continue
			}
			if cycle := visit(target); cycle != "" {
				return id + "->" + cycle
			}
		}
		state[id] = 2
		return ""
	}
	for _, node := range plan {
		if cycle := visit(node.NodeID); cycle != "" {
			return cycle
		}
	}
	return ""
}

func unreachableNodes(root string, plan []PlanNode) []string {
	reverse := map[string][]string{}
	for _, node := range plan {
		for _, dep := range node.DependsOn {
			reverse[dep] = append(reverse[dep], node.NodeID)
		}
	}
	reached := map[string]bool{}
	var walk func(string)
	walk = func(id string) {
		if reached[id] {
			return
		}
		reached[id] = true
		for _, next := range reverse[id] {
			walk(next)
		}
	}
	walk(root)
	result := []string{}
	for _, node := range plan {
		if !reached[node.NodeID] {
			result = append(result, node.NodeID)
		}
	}
	sort.Strings(result)
	return result
}

func topologicalNodes(plan []PlanNode, nodes map[string]PlanNode) []PlanNode {
	result := make([]PlanNode, 0, len(plan))
	added := map[string]bool{}
	for len(result) < len(plan) {
		progress := false
		for _, node := range plan {
			if added[node.NodeID] {
				continue
			}
			ready := true
			for _, dep := range node.DependsOn {
				if !added[dep] {
					ready = false
					break
				}
			}
			if ready {
				result = append(result, node)
				added[node.NodeID] = true
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	return result
}

func transitiveDependencies(id string, nodes map[string]PlanNode) []string {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(current string) {
		for _, dep := range nodes[current].DependsOn {
			if !seen[dep] {
				seen[dep] = true
				walk(dep)
			}
		}
	}
	walk(id)
	result := make([]string, 0, len(seen))
	for dep := range seen {
		result = append(result, dep)
	}
	sort.Strings(result)
	return result
}
func artifactSet(values []ArtifactType) map[ArtifactType]struct{} {
	result := map[ArtifactType]struct{}{}
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
func cloneArtifactSet(source map[ArtifactType]struct{}) map[ArtifactType]struct{} {
	result := map[ArtifactType]struct{}{}
	for key := range source {
		result[key] = struct{}{}
	}
	return result
}
func stringSetPermissions(values []Permission) map[Permission]struct{} {
	result := map[Permission]struct{}{}
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
func artifactSubset(values, allowed []ArtifactType) bool {
	set := artifactSet(allowed)
	for _, value := range values {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}
func containsArtifact(values []ArtifactType, target ArtifactType) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func containsNodeKind(values []NodeKind, target NodeKind) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func exceedsBounds(actual, maximum Bounds) bool {
	return actual.MaxAttempts > maximum.MaxAttempts || actual.MaxConcurrency > maximum.MaxConcurrency || actual.MaxItems > maximum.MaxItems || actual.MaxCostUSD > maximum.MaxCostUSD || actual.TimeoutMS > maximum.TimeoutMS
}
func unionStrings(left, right []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, values := range [][]string{left, right} {
		for _, value := range values {
			if !seen[value] {
				seen[value] = true
				result = append(result, value)
			}
		}
	}
	return result
}
func estimatePlan(plan ExecutablePlan, registry *CapabilityRegistry) (float64, int64) {
	cost := 0.0
	duration := int64(0)
	for _, node := range plan.Nodes {
		if manifest, ok := registry.Get(node.Capability); ok {
			cost += manifest.EstimatedCostUSD * float64(node.Bounds.MaxAttempts)
			duration += manifest.EstimatedDurationMS * int64(node.Bounds.MaxAttempts)
		}
	}
	return cost, duration
}
func confidenceFor(level TrustLevel) float64 {
	switch level {
	case TrustT1:
		return .95
	case TrustT2:
		return .85
	case TrustT3:
		return .7
	default:
		return .2
	}
}
func reasonCode(mode writingkernel.OrchestrationMode, level TrustLevel) string {
	if level == TrustT4 {
		return "required_capability_unavailable"
	}
	return "selected_" + string(mode)
}
func strategySummary(mode writingkernel.OrchestrationMode, level TrustLevel) string {
	if level == TrustT4 {
		return "所选策略缺少必要能力，计划不可调度。"
	}
	return fmt.Sprintf("选择 %s 策略并以 %s 信任等级完成静态编译。", mode, level)
}
func deterministicID(prefix, seed string) string {
	hash := hashBytes([]byte(seed))
	return prefix + strings.TrimPrefix(hash, "sha256:")[:24]
}

func validNodeKind(kind NodeKind) bool {
	switch kind {
	case NodeSequence, NodeParallel, NodeMap, NodeReduce, NodeCondition, NodeRetry, NodeRefine, NodeHumanGate, NodeValidate, NodeFallback, NodeAction:
		return true
	}
	return false
}
func validFailurePath(path FailurePath) bool {
	switch path {
	case FailureFail, FailurePause, FailureFallback, FailurePartial:
		return true
	}
	return false
}
func validTrustLevel(level TrustLevel) bool {
	switch level {
	case TrustT1, TrustT2, TrustT3, TrustT4:
		return true
	}
	return false
}
func validPlanStatus(status PlanStatus) bool {
	switch status {
	case PlanDraft, PlanValidated, PlanApproved, PlanLocked, PlanSuperseded:
		return true
	}
	return false
}
