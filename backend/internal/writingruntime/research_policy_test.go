package writingruntime

// T09 acceptance-matrix gap tests (docs/plans/2026-09-07-research-review-
// integration.md, A02): a contract with
// material_policy.allow_external_research=false must never reach the scholar
// worker. The plan compile's CONTRACT_FORBIDS_EXTERNAL_RESEARCH check is the
// primary gate; the discover/read executors add fail-closed defense in depth —
// zero worker calls and a typed refusal even if a stale runtime dispatched
// them.

import (
	"context"
	"errors"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// TestResearchExecutorsNeverCallWorkerWhenExternalResearchForbidden drives the
// discover and read executors over a no-external-research contract with
// counting fakes: every call counter must stay at zero and both executors must
// refuse with the typed contract-mismatch code.
func TestResearchExecutorsNeverCallWorkerWhenExternalResearchForbidden(t *testing.T) {
	fixture := newT05FixtureMutate(t, func(contract *writingkernel.WritingContract) {
		contract.MaterialPolicy.AllowExternalResearch = false
	})
	ctx := context.Background()

	// Discover: zero Discover/Rank calls.
	discovery := &fakeDiscoverClient{}
	discover, err := NewResearchDiscoverExecutor(discovery, fixture.gateway)
	if err != nil {
		t.Fatal(err)
	}
	node := writingplan.PlanNode{NodeID: t05DiscoverNode, Kind: writingplan.NodeAction,
		Capability: "core.research.discover", CapabilityVersion: "1.0.0",
		InputArtifactTypes:  []writingplan.ArtifactType{"contract"},
		OutputArtifactTypes: []writingplan.ArtifactType{"research_note"},
		Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 20, MaxCostUSD: 10, TimeoutMS: 600000},
		FailurePath:         writingplan.FailureFail}
	key, err := writingstore.NodeAttemptKey(fixture.runID, node.NodeID, 1)
	if err != nil {
		t.Fatal(err)
	}
	request := ExecutionRequest{RunID: fixture.runID, PlanID: fixture.planID, PlanVersion: 1,
		NodeID: node.NodeID, Attempt: 1, IdempotencyKey: key,
		ContractRef: writingplan.ObjectRef{ID: fixture.contract.ContractID, Version: fixture.contract.Version, Hash: fixture.contract.ContractHash},
		Node:        node, Inputs: []InputArtifact{fixture.contractInput(t)},
		Permissions: []writingplan.Permission{"model.invoke", "materials.read"}, UserID: fixture.userID}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := discover.Execute(ctx, request); err == nil {
		t.Fatal("discover executed against a no-external-research contract")
	} else {
		assertNoExternalResearchRefusal(t, err)
	}
	if discovery.discoverCalls != 0 || discovery.rankCalls != 0 {
		t.Fatalf("discover executor called the worker: discover=%d rank=%d", discovery.discoverCalls, discovery.rankCalls)
	}

	// Read: zero fetch/parse/read calls.
	worker := newFakeWorkerClient()
	read, err := NewResearchReadExecutor(worker, fixture.gateway, fixture.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidatesInput := fixture.candidatesFromBody(t, fixture.t05Candidates(t))
	readRequest := fixture.readRequest(t, candidatesInput, 4)
	if _, err := read.Execute(ctx, readRequest); err == nil {
		t.Fatal("read executed against a no-external-research contract")
	} else {
		assertNoExternalResearchRefusal(t, err)
	}
	if len(worker.fetches) != 0 || worker.parses != 0 || len(worker.reads) != 0 {
		t.Fatalf("read executor called the worker: fetches=%d parses=%d reads=%d",
			len(worker.fetches), worker.parses, len(worker.reads))
	}
}

func assertNoExternalResearchRefusal(t *testing.T, err error) {
	t.Helper()
	var typed *RuntimeError
	if !errors.As(err, &typed) {
		t.Fatalf("refusal is not typed: %v", err)
	}
	if typed.Code != CodeExecutorContractMismatch {
		t.Fatalf("refusal code = %s, want %s", typed.Code, CodeExecutorContractMismatch)
	}
	if typed.RetryClass != RetryNever {
		t.Fatalf("refusal retry class = %s, want %s", typed.RetryClass, RetryNever)
	}
}
