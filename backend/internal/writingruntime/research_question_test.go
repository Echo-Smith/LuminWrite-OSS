package writingruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/scholar"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// Concurrency regression for the ResearchReadExecutor question handoff
// (T05): before the fix, Execute cached the contract's central question on
// the executor struct and readBlocks read it back at call time. Two
// concurrent Execute calls on one registered executor (multi-tenant /
// multi-replica dispatch of a shared executor instance) could cross
// questions between runs. The fix threads the question through parameters
// (Execute → readPaper → readBlocks). These tests pin both layers:
//
//  1. readBlocks-level: concurrent readBlocks calls, each with its own
//     question, must deliver the exact (paperID, question) pair to the
//     worker — the rendezvous makes the calls necessarily overlap.
//  2. Execute-level: two contracts with DIFFERENT central questions execute
//     concurrently through the same executor over the real ledger (distinct
//     runs of one shared user in the same store); each paper must be read
//     under its own run's question.
type questionWorker struct {
	*fakeWorkerClient
	calls   chan [2]string
	release chan struct{}
}

func (w *questionWorker) ReadPaper(_ context.Context, question, paperID string, _ []ReaderBlock, _ ReaderPolicy, _ ...scholar.CallOption) (*ReadOutputs, *scholar.OperationResponse, error) {
	w.calls <- [2]string{paperID, question}
	<-w.release
	return nil, nil, errors.New("stop before staging")
}

func TestResearchReadKeepsConcurrentQuestionsLocal(t *testing.T) {
	w := &questionWorker{fakeWorkerClient: newFakeWorkerClient(), calls: make(chan [2]string, 2), release: make(chan struct{})}
	executor := &ResearchReadExecutor{client: w}
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, _ = executor.readBlocks(context.Background(), ExecutionRequest{}, id, nil, "question-"+id)
		}(id)
	}
	for i := 0; i < 2; i++ {
		call := <-w.calls
		if call[1] != "question-"+call[0] {
			t.Errorf("question crossed requests: %v", call)
		}
	}
	close(w.release)
	wg.Wait()
}

// questionRendezvousWorker holds both concurrent executions inside the parse
// phase until both have arrived. The old executor-field implementation
// assigned the question at the TOP of Execute and read it back in readBlocks;
// a parse-phase rendezvous guarantees both assignments landed before either
// execution evaluates the question for its read call, so a shared field
// crosses deterministically. The fixed code carries the question as a
// parameter, so the rendezvous is inert for it; ReadPaper just records what
// each paper's read actually received.
type questionRendezvousWorker struct {
	*fakeWorkerClient

	mu     sync.Mutex
	seen   map[string]string // paperID → question
	arrive sync.WaitGroup
}

func (w *questionRendezvousWorker) ParseDocument(ctx context.Context, document []byte, mediaType, parserVersion string, opts ...scholar.CallOption) (*ParseOutputs, *scholar.OperationResponse, error) {
	w.arrive.Done()
	w.arrive.Wait() // both executes past their question assignment before either reads
	return w.fakeWorkerClient.ParseDocument(ctx, document, mediaType, parserVersion, opts...)
}

func (w *questionRendezvousWorker) ReadPaper(ctx context.Context, question, paperID string, blocks []ReaderBlock, policy ReaderPolicy, opts ...scholar.CallOption) (*ReadOutputs, *scholar.OperationResponse, error) {
	w.mu.Lock()
	w.seen[paperID] = question
	w.mu.Unlock()
	return w.fakeWorkerClient.ReadPaper(ctx, question, paperID, blocks, policy, opts...)
}

// TestResearchReadConcurrentExecuteNoQuestionCrossing drives the full read
// executor: one executor instance, two runs with different central questions
// executing concurrently through the real ledger (distinct runs of one user
// in the same store), each paper read under its own run's question.
func TestResearchReadConcurrentExecuteNoQuestionCrossing(t *testing.T) {
	fixture := newT05Fixture(t)
	ctx := context.Background()

	// Second run over the same store: own document, own resealed contract
	// with a different central question — a run's contract_hash pins the
	// question, so the reseal is recomputed after the flip. A fresh
	// contract_id keeps the immutable (contract_id, version) row apart.
	secondContract := fixture.contract
	secondContract.ContractID = writingstore.StableID("ctr_", "t05b", fmt.Sprint(time.Now().UnixNano()))
	secondContract.Content.CentralQuestion = "second run research question B"
	secondContract, err := secondContract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	secondRun := fixture.newSecondRun(t, secondContract)

	// Paper IDs differ per run, so every seen record maps one paper to the
	// question the worker observed for it.
	rendezvous := &questionRendezvousWorker{
		fakeWorkerClient: newFakeWorkerClient(),
		seen:             map[string]string{},
	}
	rendezvous.arrive.Add(2)
	executor, err := NewResearchReadExecutor(rendezvous, fixture.gateway, fixture.store, &fakeBudget{scripts: []bool{false}})
	if err != nil {
		t.Fatal(err)
	}

	candidatesA := fixture.candidatesFromBody(t, fixture.t05Candidates(t, t05SelectedPaper("p_run_a", true)))
	requestA := fixture.readRequest(t, candidatesA, 4)
	candidatesB := secondRun.candidatesFromBody(t, secondRun.t05Candidates(t, t05SelectedPaper("p_run_b", true)))
	requestB := secondRun.readRequest(t, candidatesB, 4)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	for i, req := range []ExecutionRequest{requestA, requestB} {
		wg.Add(1)
		go func(i int, req ExecutionRequest) {
			defer wg.Done()
			<-start
			_, errs[i] = executor.Execute(ctx, req)
		}(i, req)
	}
	close(start)
	wg.Wait()

	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("concurrent execute failed: %v / %v", errs[0], errs[1])
	}
	want := map[string]string{
		"p_run_a": fixture.contract.Content.CentralQuestion,
		"p_run_b": secondContract.Content.CentralQuestion,
	}
	for paperID, question := range want {
		if got := rendezvous.seen[paperID]; got != question {
			t.Errorf("worker saw paper %s under question %q, want its own run's question %q", paperID, got, question)
		}
	}
}

// newSecondRun builds a second t05 fixture run over the SAME store with its
// own document, resealed contract, base version, and run row — everything the
// research_tasks ledger and pack provenance need to coexist with the first
// run without cross-run foreign keys.
func (fixture *t05Fixture) newSecondRun(t *testing.T, contract writingkernel.WritingContract) *t05Fixture {
	t.Helper()
	store := fixture.store
	userID := fixture.userID
	documentID := writingstore.StableID("doc_", "t05b", fmt.Sprint(time.Now().UnixNano()))
	if err := store.CreateDocument(context.Background(), writingstore.DocumentRecord{DocumentID: documentID,
		OwnerUserID: userID, Title: "T05 concurrent", Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutContract(context.Background(), writingstore.ContractRecord{DocumentID: documentID, Contract: contract,
		Trace: writingstore.TraceContext{Provenance: map[string]any{}, SourceRefs: []string{},
			Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}}); err != nil {
		t.Fatal(err)
	}
	version := deliveryBaseVersion(t, documentID, writingstore.StableID("ver_", documentID, "t05b"), "T05 concurrent base")
	if _, err := store.CommitDocumentVersion(context.Background(), writingstore.CommitDocumentVersionParams{
		Version: version, ContractID: contract.ContractID, ContractVersion: contract.Version,
		Trace: writingstore.TraceContext{Provenance: map[string]any{}, SourceRefs: []string{},
			Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}}); err != nil {
		t.Fatal(err)
	}
	runID := writingstore.StableID("run_", userID, "t05b", fmt.Sprint(time.Now().UnixNano()))
	budget := writingplan.PlanBudget{MaxCostUSD: 100, MaxDurationMS: 3000000, MaxConcurrency: 1, MaxNodes: 10, MaxItems: 4}
	if err := store.CreateRun(context.Background(), writingstore.RunRecord{RunID: runID, DocumentID: documentID,
		ContractID: contract.ContractID, ContractVersion: contract.Version, ContractHash: contract.ContractHash,
		BaseVersionID: version.VersionID, Status: "planned", ApprovalMode: contract.Collaboration.ApprovalMode,
		RequestedAssurance: contract.Collaboration.AssuranceLevel, Budget: budget,
		Permissions: []writingplan.Permission{"model.invoke", "materials.read"},
		Trace:       writingstore.TraceContext{Provenance: map[string]any{}, SourceRefs: []string{}, Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}}); err != nil {
		t.Fatal(err)
	}
	contractBody, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutArtifactContent(context.Background(), contentHash(contractBody), "application/json", contractBody); err != nil {
		t.Fatal(err)
	}
	return &t05Fixture{store: store, gateway: WritingStoreContentGateway{Store: store},
		runID: runID, userID: userID, contract: contract,
		planID: writingstore.StableID("plan_", documentID, "t05b")}
}
