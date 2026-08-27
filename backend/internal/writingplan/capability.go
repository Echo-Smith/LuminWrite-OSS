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

type CapabilityManifest struct {
	ID                  string           `json:"id"`
	Class               string           `json:"class"`
	Executor            string           `json:"executor"`
	InputTypes          []ArtifactType   `json:"input_types"`
	OptionalInputTypes  []ArtifactType   `json:"optional_input_types"`
	OutputTypes         []ArtifactType   `json:"output_types"`
	Permissions         []Permission     `json:"permissions"`
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

func (r *CapabilityRegistry) registerLocked(manifest CapabilityManifest) error {
	if _, exists := r.manifests[manifest.ID]; exists {
		return fmt.Errorf("DUPLICATE_CAPABILITY: %s", manifest.ID)
	}
	r.manifests[manifest.ID] = cloneManifest(manifest)
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
	outline := base("core.outline.generate", "writing.outline", "engine.step.outline", []ArtifactType{"contract"}, []ArtifactType{"outline"}, []Permission{"model.invoke", "materials.read"}, false)
	outline.OptionalInputTypes = []ArtifactType{"source_pack"}
	register(outline)
	draft := base("core.draft.generate", "writing.draft", "engine.step.write", []ArtifactType{"contract"}, []ArtifactType{"candidate_draft"}, []Permission{"model.invoke", "materials.read"}, false)
	draft.OptionalInputTypes = []ArtifactType{"outline", "source_pack"}
	register(draft)
	quality := base("core.validation.quality", "validation.quality", "engine.step.post_review", []ArtifactType{"candidate_draft"}, []ArtifactType{"quality_report"}, []Permission{"model.invoke", "validation.run"}, true)
	quality.OptionalInputTypes = []ArtifactType{"evidence_report", "fact_report"}
	register(quality)
	register(base("core.document.finalize", "document.finalize", "kernel.document.finalize", []ArtifactType{"candidate_draft", "quality_report"}, []ArtifactType{"verified_deliverable"}, []Permission{"document.revision"}, false))
	research := base("core.retrieval.search", "research.collect", "engine.step.search", []ArtifactType{"contract", "materials"}, []ArtifactType{"source_pack"}, []Permission{"external.research", "materials.read"}, false)
	research.SupportsEvidence = true
	register(research)
	return registry
}

func validBounds(bounds Bounds) bool {
	return bounds.MaxAttempts > 0 && bounds.MaxConcurrency > 0 && bounds.MaxItems > 0 && bounds.MaxCostUSD >= 0 && !math.IsNaN(bounds.MaxCostUSD) && !math.IsInf(bounds.MaxCostUSD, 0) && bounds.TimeoutMS > 0
}

func cloneManifest(manifest CapabilityManifest) CapabilityManifest {
	manifest.InputTypes = append([]ArtifactType(nil), manifest.InputTypes...)
	manifest.OptionalInputTypes = append([]ArtifactType(nil), manifest.OptionalInputTypes...)
	manifest.OutputTypes = append([]ArtifactType(nil), manifest.OutputTypes...)
	manifest.Permissions = append([]Permission(nil), manifest.Permissions...)
	manifest.SupportedNodeKinds = append([]NodeKind(nil), manifest.SupportedNodeKinds...)
	return manifest
}
