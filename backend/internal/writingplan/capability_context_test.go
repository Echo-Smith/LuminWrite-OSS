package writingplan

import (
	"context"
	"strings"
	"testing"
)

func TestContextContractValidation(t *testing.T) {
	valid := ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest},
		OptionalContext: []ContextBlockName{ContextCanonFacts}, ForbiddenContext: []ContextBlockName{ContextStyleDirectives}}
	if err := valid.ValidateContextContract(); err != nil {
		t.Fatalf("err=%v", err)
	}
	if wanted := valid.ContextWanted(); len(wanted) != 2 || wanted[0] != "contract_digest" || wanted[1] != "canon_facts" {
		t.Fatalf("wanted=%v", wanted)
	}

	clash := ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest},
		ForbiddenContext: []ContextBlockName{ContextContractDigest}}
	if err := clash.ValidateContextContract(); err == nil || !strings.Contains(err.Error(), "CONTEXT_BLOCK_CONFLICT") {
		t.Fatalf("clash err=%v", err)
	}
	unknown := ContextContract{OptionalContext: []ContextBlockName{"secret_block"}}
	if err := unknown.ValidateContextContract(); err == nil || !strings.Contains(err.Error(), "CONTEXT_BLOCK_UNKNOWN") {
		t.Fatalf("unknown err=%v", err)
	}
	negative := ContextContract{ContextTokenBudget: -1}
	if err := negative.ValidateContextContract(); err == nil || !strings.Contains(err.Error(), "CONTEXT_TOKEN_BUDGET_INVALID") {
		t.Fatalf("budget err=%v", err)
	}
}

func TestRegistryRejectsInvalidContextContract(t *testing.T) {
	registry := NewCapabilityRegistry("test")
	if err := registry.RegisterExecutor(ExecutorBinding{ID: "engine.step.write", AcceptedInputTypes: []ArtifactType{"contract"}, ProducedOutputTypes: []ArtifactType{"full_draft"}, Dispatch: func(context.Context, ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	manifest := CapabilityManifest{ID: "core.draft.generate", Class: "writing.draft", Executor: "engine.step.write",
		InputTypes: []ArtifactType{"contract"}, OutputTypes: []ArtifactType{"full_draft"},
		Permissions: []Permission{"model.invoke"}, Version: "1.0.0",
		SupportedNodeKinds: []NodeKind{NodeAction}, Idempotency: IdempotencySafe, Available: true,
		MaxBounds: Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 1, TimeoutMS: 1000},
		Context:   ContextContract{RequiredContext: []ContextBlockName{ContextContractDigest, ContextContractDigest}}}
	if err := registry.Register(manifest); err == nil || !strings.Contains(err.Error(), "CONTEXT_BLOCK_CONFLICT") {
		t.Fatalf("err=%v", err)
	}
}

func TestDefaultCapabilityContextContracts(t *testing.T) {
	registry := DefaultCapabilityRegistry()
	// Research stays style-neutral: style directives are forbidden.
	research, ok := registry.Get("core.retrieval.search")
	if !ok {
		t.Fatal("research capability missing")
	}
	forbidden := map[ContextBlockName]bool{}
	for _, name := range research.Context.ForbiddenContext {
		forbidden[name] = true
	}
	if !forbidden[ContextStyleDirectives] {
		t.Fatalf("research forbidden=%v", research.Context.ForbiddenContext)
	}
	// Draft must see the document state and the resident through-line.
	draft, ok := registry.Get("core.draft.generate")
	if !ok {
		t.Fatal("draft capability missing")
	}
	required := map[ContextBlockName]bool{}
	for _, name := range draft.Context.RequiredContext {
		required[name] = true
	}
	if !required[ContextContractDigest] || !required[ContextDocumentState] {
		t.Fatalf("draft required=%v", draft.Context.RequiredContext)
	}
	resident := false
	for _, name := range draft.Context.OptionalContext {
		if name == ContextThroughLine {
			resident = true
		}
	}
	if !resident {
		t.Fatal("draft must opt into the resident layer")
	}
}
