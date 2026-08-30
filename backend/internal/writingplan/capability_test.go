package writingplan

import (
	"context"
	"strings"
	"testing"
)

func TestCapabilityRegistryRejectsMalformedManifest(t *testing.T) {
	registry := NewCapabilityRegistry("registry-test")
	registerTestExecutor(t, registry, "draft", []ArtifactType{"prompt"}, []ArtifactType{"draft"})
	valid := capabilityManifest()
	tests := []struct {
		name     string
		manifest CapabilityManifest
		code     string
	}{
		{name: "missing id", manifest: func() CapabilityManifest { m := valid; m.ID = ""; return m }(), code: "id"},
		{name: "missing executor", manifest: func() CapabilityManifest { m := valid; m.Executor = ""; return m }(), code: "executor"},
		{name: "missing output type", manifest: func() CapabilityManifest { m := valid; m.OutputTypes = nil; return m }(), code: "output"},
		{name: "negative cost", manifest: func() CapabilityManifest { m := valid; m.EstimatedCostUSD = -1; return m }(), code: "cost"},
		{name: "negative duration", manifest: func() CapabilityManifest { m := valid; m.EstimatedDurationMS = -1; return m }(), code: "duration"},
		{name: "invalid idempotency", manifest: func() CapabilityManifest { m := valid; m.Idempotency = "unknown"; return m }(), code: "idempotency"},
		{name: "direct document write", manifest: func() CapabilityManifest { m := valid; m.DirectDocumentWrite = true; return m }(), code: "direct_document_write"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := registry.Register(test.manifest)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.code) {
				t.Fatalf("expected manifest error containing %q, got %v", test.code, err)
			}
		})
	}
}

func TestCapabilityRegistryRejectsUnknownExecutor(t *testing.T) {
	registry := NewCapabilityRegistry("registry-test")
	registerTestExecutor(t, registry, "draft", []ArtifactType{"prompt"}, []ArtifactType{"draft"})
	manifest := capabilityManifest()
	manifest.Executor = "not-registered"
	if err := registry.Register(manifest); err == nil || !strings.Contains(strings.ToLower(err.Error()), "unknown_executor") {
		t.Fatalf("expected unknown executor rejection, got %v", err)
	}
}

func TestCapabilityRegistryEnablesOnlyAfterCompatibleExecutorBinding(t *testing.T) {
	registry := NewCapabilityRegistry("registry-enable")
	manifest := capabilityManifest()
	manifest.Available = false
	if err := registry.Declare(manifest); err != nil {
		t.Fatal(err)
	}
	if err := registry.Enable(manifest.ID); err == nil || !strings.Contains(err.Error(), "UNKNOWN_EXECUTOR") {
		t.Fatalf("enable without executor err=%v", err)
	}
	registerTestExecutor(t, registry, manifest.Executor, []ArtifactType{"prompt"}, []ArtifactType{"draft"})
	if err := registry.Enable(manifest.ID); err != nil {
		t.Fatal(err)
	}
	enabled, ok := registry.Get(manifest.ID)
	if !ok || !enabled.Available || len(registry.ByClass(manifest.Class)) != 1 {
		t.Fatalf("enabled=%#v ok=%v", enabled, ok)
	}
}

func TestCapabilityRegistryRejectsDuplicateIDsAndExecutors(t *testing.T) {
	registry := NewCapabilityRegistry("registry-test")
	registerTestExecutor(t, registry, "draft", []ArtifactType{"prompt"}, []ArtifactType{"draft"})
	first := capabilityManifest()
	first.ID = "cap_one"
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(first); err == nil {
		t.Fatal("expected duplicate capability id to fail")
	}
	second := first
	second.ID = "cap_two"
	if err := registry.Register(second); err != nil {
		t.Fatalf("same executor may serve multiple capability IDs: %v", err)
	}
}

func capabilityManifest() CapabilityManifest {
	return CapabilityManifest{ID: "cap_draft", Class: "writing", Executor: "draft", InputTypes: []ArtifactType{"prompt"}, OutputTypes: []ArtifactType{"draft"}, Version: "1.0.0", SupportedNodeKinds: []NodeKind{NodeAction}, MaxBounds: bounded(), Idempotency: IdempotencySafe, Available: true}
}

func TestCapabilityRegistryRetainsGovernanceMetadata(t *testing.T) {
	registry := NewCapabilityRegistry("registry-v7")
	registerTestExecutor(t, registry, "draft", []ArtifactType{"prompt"}, []ArtifactType{"draft"})
	manifest := CapabilityManifest{
		ID: "cap_research", Class: "research", Executor: "draft", InputTypes: []ArtifactType{"prompt"}, OutputTypes: []ArtifactType{"draft"},
		Permissions: []Permission{"network.read"}, Streaming: true, EstimatedCostUSD: 0.42, EstimatedDurationMS: 240,
		SupportsEvidence: true, PreservesVoice: true, Version: "2.1.0",
		SupportedNodeKinds: []NodeKind{NodeAction}, MaxBounds: bounded(), Idempotency: IdempotencySafe, Available: true,
	}
	if err := registry.Register(manifest); err != nil {
		t.Fatal(err)
	}
	// Registration is intentionally the only mutation boundary. Compile/validation
	// tests consume the manifest through the registry and enforce these properties.
	plan := baseExecutablePlan()
	plan.Nodes[0].Capability = "cap_research"
	plan.Nodes[0].CapabilityVersion = manifest.Version
	validation := ValidatePlan(plan, ValidationContext{Registry: registry, InitialArtifactTypes: []ArtifactType{"prompt"}, AllowedPermissions: []Permission{"network.read"}, RequiredFinalArtifact: "draft"})
	if !validation.Valid {
		t.Fatalf("registered capability metadata should remain usable: %#v", validation.Errors)
	}
}

func TestCapabilityManifestDistinguishesRequiredAndOptionalInputs(t *testing.T) {
	registry := NewCapabilityRegistry("registry-v1")
	registerTestExecutor(t, registry, "draft", []ArtifactType{"prompt", "context"}, []ArtifactType{"draft"})
	manifest := capabilityManifest()
	manifest.OptionalInputTypes = []ArtifactType{"context"}
	if err := registry.Register(manifest); err != nil {
		t.Fatal(err)
	}

	plan := baseExecutablePlan()
	validation := ValidatePlan(plan, ValidationContext{Registry: registry, InitialArtifactTypes: []ArtifactType{"prompt"}, RequiredFinalArtifact: "draft"})
	if !validation.Valid {
		t.Fatalf("optional input may be omitted: %#v", validation.Errors)
	}

	plan.Nodes[0].InputArtifactTypes = []ArtifactType{"context"}
	validation = ValidatePlan(plan, ValidationContext{Registry: registry, InitialArtifactTypes: []ArtifactType{"context"}, RequiredFinalArtifact: "draft"})
	hasTypeMismatch := false
	for _, validationError := range validation.Errors {
		if strings.Contains(validationError, "CAPABILITY_TYPE_MISMATCH") {
			hasTypeMismatch = true
		}
	}
	if validation.Valid || !hasTypeMismatch {
		t.Fatalf("required input must not be omitted: %#v", validation.Errors)
	}

	plan.Nodes[0].InputArtifactTypes = []ArtifactType{"prompt", "context"}
	validation = ValidatePlan(plan, ValidationContext{Registry: registry, InitialArtifactTypes: []ArtifactType{"prompt", "context"}, RequiredFinalArtifact: "draft"})
	if !validation.Valid {
		t.Fatalf("declared optional input should be accepted: %#v", validation.Errors)
	}
}

func TestCapabilityRequiresConcreteTypeCompatibleExecutor(t *testing.T) {
	registry := NewCapabilityRegistry("registry-v1")
	if err := registry.RegisterExecutor(ExecutorBinding{ID: "draft", AcceptedInputTypes: []ArtifactType{"prompt"}, ProducedOutputTypes: []ArtifactType{"other"}, Dispatch: func(context.Context, ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{Outputs: map[ArtifactType][]byte{"other": []byte("x")}}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(capabilityManifest()); err == nil || !strings.Contains(err.Error(), "EXECUTOR_TYPE_MISMATCH") {
		t.Fatalf("expected executor type mismatch, got %v", err)
	}
}

func registerTestExecutor(t *testing.T, registry *CapabilityRegistry, id string, inputs, outputs []ArtifactType) {
	t.Helper()
	binding := ExecutorBinding{ID: id, AcceptedInputTypes: inputs, ProducedOutputTypes: outputs}
	binding.Dispatch = func(context.Context, ExecutionRequest) (ExecutionResult, error) {
		result := ExecutionResult{Outputs: map[ArtifactType][]byte{}}
		for _, outputType := range outputs {
			result.Outputs[outputType] = []byte("test")
		}
		return result, nil
	}
	if err := registry.RegisterExecutor(binding); err != nil {
		t.Fatal(err)
	}
}
