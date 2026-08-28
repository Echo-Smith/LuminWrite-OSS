package writingruntime

import (
	"errors"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

func TestExecutionRequestBindsPlanNodeAndExactAttemptIdentity(t *testing.T) {
	request := validExecutionRequest()
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}

	changed := request
	changed.IdempotencyKey = "run_test:node_draft:2"
	if err := changed.Validate(); !errors.Is(err, ErrInvalidExecutionRequest) {
		t.Fatalf("mismatched idempotency key error = %v", err)
	}

	changed = request
	changed.NodeID = "node_other"
	if err := changed.Validate(); !errors.Is(err, ErrInvalidExecutionRequest) {
		t.Fatalf("mismatched plan node error = %v", err)
	}
}

func TestExecutionRequestRequiresEveryDeclaredInputType(t *testing.T) {
	request := validExecutionRequest()
	request.Node.InputArtifactTypes = append(request.Node.InputArtifactTypes, "source_pack")
	if err := request.Validate(); !errors.Is(err, ErrInvalidExecutionRequest) {
		t.Fatalf("missing declared input error = %v", err)
	}
}

func TestExecutionResultAcceptsOnlyDeclaredTypedArtifacts(t *testing.T) {
	request := validExecutionRequest()
	result := validExecutionResult(request)
	if err := result.Validate(request); err != nil {
		t.Fatal(err)
	}

	result.Artifacts[0].ArtifactType = "quality_report"
	if err := result.Validate(request); !errors.Is(err, ErrInvalidExecutionResult) {
		t.Fatalf("undeclared output type error = %v", err)
	}
}

func TestExecutionResultBindsProducerAndCapabilityVersion(t *testing.T) {
	request := validExecutionRequest()
	result := validExecutionResult(request)
	result.Artifacts[0].Producer = "legacy.harness"
	if err := result.Validate(request); !errors.Is(err, ErrInvalidExecutionResult) {
		t.Fatalf("producer drift error = %v", err)
	}

	result = validExecutionResult(request)
	result.Artifacts[0].CapabilityVersion = "0.9.0"
	if err := result.Validate(request); !errors.Is(err, ErrInvalidExecutionResult) {
		t.Fatalf("capability version drift error = %v", err)
	}
}

func TestExecutionResultRequiresCompleteInputLineage(t *testing.T) {
	request := validExecutionRequest()
	result := validExecutionResult(request)
	result.Artifacts[0].InputHashes = []string{}
	if err := result.Validate(request); !errors.Is(err, ErrInvalidExecutionResult) {
		t.Fatalf("missing input lineage error = %v", err)
	}

	result = validExecutionResult(request)
	result.Artifacts[0].Parents = []writingstore.ArtifactRef{{ArtifactID: "art_unknown", Version: 1}}
	if err := result.Validate(request); !errors.Is(err, ErrInvalidExecutionResult) {
		t.Fatalf("unknown parent error = %v", err)
	}
}

func TestExecutionResultRejectsDuplicateOutputKeysAndNegativeUsage(t *testing.T) {
	request := validExecutionRequest()
	result := validExecutionResult(request)
	result.Artifacts = append(result.Artifacts, result.Artifacts[0])
	if err := result.Validate(request); !errors.Is(err, ErrInvalidExecutionResult) {
		t.Fatalf("duplicate output key error = %v", err)
	}

	result = validExecutionResult(request)
	result.Usage.CostUSD = -1
	if err := result.Validate(request); !errors.Is(err, ErrInvalidExecutionResult) {
		t.Fatalf("negative usage error = %v", err)
	}
}

func TestExecutorDescriptorMustMatchGovernedNodeKinds(t *testing.T) {
	descriptor := ExecutorDescriptor{ExecutorID: "executor.draft", Version: "1.0.0",
		SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}, Cancellable: true}
	if err := descriptor.Validate(); err != nil {
		t.Fatal(err)
	}
	descriptor.SupportedNodeKinds = []writingplan.NodeKind{"legacy_direct_write"}
	if err := descriptor.Validate(); !errors.Is(err, ErrInvalidExecutorDescriptor) {
		t.Fatalf("unsupported node kind error = %v", err)
	}
}

func validExecutionRequest() ExecutionRequest {
	return ExecutionRequest{
		RunID: "run_test", PlanID: "plan_test", PlanVersion: 1,
		NodeID: "node_draft", Attempt: 1, IdempotencyKey: "run_test:node_draft:1",
		ContractRef: writingplan.ObjectRef{ID: "ctr_test", Version: 1, Hash: executorHash("contract")},
		Node: writingplan.PlanNode{NodeID: "node_draft", Kind: writingplan.NodeAction,
			Capability: "core.writing.draft", CapabilityVersion: "1.0.0",
			DependsOn: []string{}, InputArtifactTypes: []writingplan.ArtifactType{"brief"},
			OutputArtifactTypes: []writingplan.ArtifactType{"full_draft"},
			Bounds:              writingplan.Bounds{MaxAttempts: 2, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 2, TimeoutMS: 1000},
			FailurePath:         writingplan.FailureFail},
		Inputs: []InputArtifact{{ArtifactID: "art_brief", Version: 1, ArtifactType: "brief",
			ContentHash: executorHash("brief"), MediaType: "application/json", ContentRef: "object://brief/1"}},
		Permissions: []writingplan.Permission{"model.invoke"},
	}
}

func validExecutionResult(request ExecutionRequest) ExecutionResult {
	started := time.Date(2026, 8, 28, 6, 0, 0, 0, time.UTC)
	return ExecutionResult{
		Artifacts: []OutputArtifactDraft{{OutputKey: "draft", ArtifactType: "full_draft",
			ContentHash: executorHash("draft"), MediaType: "text/markdown", ContentRef: "object://draft/1",
			Parents:  []writingstore.ArtifactRef{{ArtifactID: request.Inputs[0].ArtifactID, Version: request.Inputs[0].Version}},
			Producer: request.Node.Capability, CapabilityVersion: request.Node.CapabilityVersion,
			InputHashes: []string{request.Inputs[0].ContentHash}, Provenance: map[string]any{"executor": "test"},
			SourceRefs: []string{}}},
		Usage:     ExecutionUsage{CostUSD: 0.2, InputTokens: 10, OutputTokens: 20, DurationMS: 50},
		StartedAt: started, CompletedAt: started.Add(50 * time.Millisecond),
	}
}

func executorHash(seed string) string {
	return writingstore.StableID("sha256:", seed) + "00000000000000000000000000000000"
}
