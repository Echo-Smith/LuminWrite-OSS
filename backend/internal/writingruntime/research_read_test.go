package writingruntime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database/dbtest"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/scholar"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// T05 runtime tests (docs/plans/2026-09-07-research-review-integration.md
// §T05): sub-task cache reuse across restarts (fake worker call counting),
// lease-fencing negatives, pack verification negatives, research.progress
// sequence monotonicity, budget-boundary clean pause + resume, and the
// INSUFFICIENT_EVIDENCE sentinel. Store-backed where the ledger or events
// are involved.

const (
	t05DiscoverNode = "node_t05_discover"
	t05ReadNode     = "node_t05_read"
)

// ── Fake scholar worker client (read surface) ──────────────────────────────

type fakeWorkerClient struct {
	mu      sync.Mutex
	fetches map[string]int
	parses  int
	reads   map[string]int

	fetchFunc func(paperID, oaURL string) (*scholar.FetchFullTextOutputs, error)
	parseFunc func(document []byte, mediaType string) (*ParseOutputs, error)
	readFunc  func(paperID string, blocks []ReaderBlock) (*ReadOutputs, error)
}

func newFakeWorkerClient() *fakeWorkerClient {
	return &fakeWorkerClient{fetches: map[string]int{}, reads: map[string]int{}}
}

func (fake *fakeWorkerClient) FetchFullText(_ context.Context, paperID, oaURL string, _ int64, _ ...scholar.CallOption) (*scholar.FetchFullTextOutputs, *scholar.OperationResponse, error) {
	fake.mu.Lock()
	fake.fetches[paperID]++
	fake.mu.Unlock()
	if fake.fetchFunc != nil {
		outputs, err := fake.fetchFunc(paperID, oaURL)
		return outputs, &scholar.OperationResponse{Usage: scholar.Usage{Measured: false}}, err
	}
	content := []byte("full text of " + paperID)
	return &scholar.FetchFullTextOutputs{PaperID: paperID, AcquisitionStatus: "full_text_available",
			ContentHash: t05sha256(content), SizeBytes: int64(len(content)), MediaType: "text/plain",
			ContentBase64: base64.StdEncoding.EncodeToString(content)},
		&scholar.OperationResponse{Usage: scholar.Usage{Measured: false}}, nil
}

func (fake *fakeWorkerClient) ParseDocument(_ context.Context, document []byte, mediaType, _ string, _ ...scholar.CallOption) (*ParseOutputs, *scholar.OperationResponse, error) {
	fake.mu.Lock()
	fake.parses++
	fake.mu.Unlock()
	if fake.parseFunc != nil {
		outputs, err := fake.parseFunc(document, mediaType)
		return outputs, &scholar.OperationResponse{Usage: scholar.Usage{Measured: false}}, err
	}
	text := string(document)
	blocks := make([]ParsedBlock, 0, 2)
	const page = 1
	for index, paragraph := range strings.Split(text, "\n\n") {
		trimmed := strings.TrimSpace(paragraph)
		if trimmed == "" {
			continue
		}
		localPage := page + index
		blocks = append(blocks, ParsedBlock{BlockID: fmt.Sprintf("blk-%04d", index+1),
			Text: trimmed, Page: &localPage, BlockHash: t05sha256([]byte(trimmed))})
	}
	scanned := false
	return &ParseOutputs{Blocks: blocks, Coverage: ParseCoverage{MediaType: mediaType,
			ParserVersion: ResearchReadParserVersion, TotalBlocks: len(blocks),
			TotalCodepoints: len(text), Complete: true, LikelyScanned: &scanned}},
		&scholar.OperationResponse{Usage: scholar.Usage{Measured: false}}, nil
}

func (fake *fakeWorkerClient) ReadPaper(_ context.Context, _, paperID string, blocks []ReaderBlock, _ ReaderPolicy, _ ...scholar.CallOption) (*ReadOutputs, *scholar.OperationResponse, error) {
	fake.mu.Lock()
	fake.reads[paperID]++
	fake.mu.Unlock()
	if fake.readFunc != nil {
		outputs, err := fake.readFunc(paperID, blocks)
		return outputs, &scholar.OperationResponse{Usage: scholar.Usage{Measured: false}}, err
	}
	if len(blocks) == 0 {
		return &ReadOutputs{PaperID: paperID, Claims: []ReaderClaimOutput{},
				Evidence: []ReaderEvidence{}, BlocksRead: []string{}},
			&scholar.OperationResponse{Usage: scholar.Usage{Measured: false}}, nil
	}
	block := blocks[0]
	quote := []rune(block.Text)
	if len(quote) > 20 {
		quote = quote[:20]
	}
	scope := "full_text"
	if block.Page != nil && strings.Contains(block.Text, "abstract marker") {
		scope = "abstract"
	}
	return &ReadOutputs{PaperID: paperID,
			Claims: []ReaderClaimOutput{{ClaimID: "c1", Text: "claim from " + paperID,
				Kind: "source_assertion", EvidenceIDs: []string{"e1"}, Limitations: []string{}}},
			Evidence: []ReaderEvidence{{EvidenceID: "e1", BlockID: block.BlockID,
				BlockHash: block.BlockHash, Quote: string(quote), StartChar: 0, EndChar: len(quote),
				Page: block.Page, EvidenceScope: scope}},
			BlocksRead:          []string{block.BlockID},
			Limitations:         []string{"read covers the supplied blocks only"},
			ReaderPolicyVersion: "reader/1"},
		&scholar.OperationResponse{Usage: scholar.Usage{Measured: false}}, nil
}

func (fake *fakeWorkerClient) fetchCount(paperID string) int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.fetches[paperID]
}

func (fake *fakeWorkerClient) readCount(paperID string) int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.reads[paperID]
}

func t05sha256(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ── Fake discovery client ──────────────────────────────────────────────────

type fakeDiscoverClient struct {
	discoverCalls int
	rankCalls     int
	papers        []scholar.PaperCandidate
}

func (fake *fakeDiscoverClient) Discover(_ context.Context, _ string, _ []string, _ int, _ ...scholar.CallOption) (*scholar.DiscoverOutputs, *scholar.OperationResponse, error) {
	fake.discoverCalls++
	return &scholar.DiscoverOutputs{Papers: fake.papers,
			ProviderResults: []scholar.ProviderResult{{Provider: "openalex", Status: "ok", Returned: len(fake.papers)}}},
		&scholar.OperationResponse{RequestID: "req-discover", Usage: scholar.Usage{Measured: false}}, nil
}

func (fake *fakeDiscoverClient) Rank(_ context.Context, _ string, candidates []scholar.RankCandidate, _ ...scholar.CallOption) (*scholar.RankOutputs, *scholar.OperationResponse, error) {
	fake.rankCalls++
	scores := make([]scholar.RankScore, 0, len(candidates))
	for index, candidate := range candidates {
		score := float64(3 - index%2)
		scores = append(scores, scholar.RankScore{PaperID: candidate.PaperID, Score: score,
			Reason: "relevant to the question"})
	}
	return &scholar.RankOutputs{Scores: scores}, &scholar.OperationResponse{RequestID: "req-rank",
		Usage: scholar.Usage{Measured: false}}, nil
}

// ── Budget guard fake ──────────────────────────────────────────────────────

type fakeBudget struct {
	mu      sync.Mutex
	scripts []bool // per-call verdicts; the last value repeats
	calls   int
	reason  string
}

func (fake *fakeBudget) ResearchBudgetBoundaryReached(_ context.Context, _ ExecutionRequest, _, _ int) (bool, string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	verdict := fake.scripts[len(fake.scripts)-1]
	if fake.calls < len(fake.scripts) {
		verdict = fake.scripts[fake.calls]
	}
	fake.calls++
	return verdict, fake.reason
}

// ── Fixture ────────────────────────────────────────────────────────────────

type t05Fixture struct {
	store    *writingstore.Store
	gateway  WritingStoreContentGateway
	runID    string
	userID   string
	contract writingkernel.WritingContract
	planID   string
}

func newT05Fixture(t *testing.T) *t05Fixture {
	return newT05FixtureMutate(t, nil)
}

// newT05FixtureMutate builds the same fixture while letting a test mutate the
// decoded contract before it is resealed (e.g. material policy for A02).
func newT05FixtureMutate(t *testing.T, mutate func(*writingkernel.WritingContract)) *t05Fixture {
	t.Helper()
	db, cleanup, err := dbtest.Open(os.Getenv("TEST_DATABASE_URL"), 6, 2)
	if err != nil {
		if errors.Is(err, dbtest.ErrNoDatabaseURL) {
			t.Skip("TEST_DATABASE_URL not set")
		}
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	ctx := context.Background()
	store, err := writingstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	var userID string
	if err := db.QueryRowContext(ctx, `INSERT INTO users (uid, name) VALUES ($1,'t05 runtime') RETURNING id::text`,
		fmt.Sprintf("t05rt_%d", time.Now().UnixNano())).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "specs", "lcp", "v1.1", "fixtures", "writing-contract.research-review.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := writingkernel.DecodeWritingContractResearchStrict(payload)
	if err != nil {
		t.Fatal(err)
	}
	// The v1.1 fixture requires 5 citable sources; the runtime tests drive a
	// two-paper workset, so the fixture is resealed with a 1-source floor.
	contract.Research.MinCitableSources = 1
	if mutate != nil {
		mutate(&contract)
	}
	sealed, err := contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	documentID := writingstore.StableID("doc_", "t05rt", fmt.Sprint(time.Now().UnixNano()))
	if err := store.CreateDocument(ctx, writingstore.DocumentRecord{DocumentID: documentID,
		OwnerUserID: userID, Title: "T05 runtime", Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutContract(ctx, writingstore.ContractRecord{DocumentID: documentID, Contract: sealed,
		Trace: writingstore.TraceContext{Provenance: map[string]any{}, SourceRefs: []string{},
			Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}}); err != nil {
		t.Fatal(err)
	}
	version := deliveryBaseVersion(t, documentID, writingstore.StableID("ver_", documentID, "t05"), "T05 base")
	if _, err := store.CommitDocumentVersion(ctx, writingstore.CommitDocumentVersionParams{
		Version: version, ContractID: sealed.ContractID, ContractVersion: sealed.Version,
		Trace: writingstore.TraceContext{Provenance: map[string]any{}, SourceRefs: []string{},
			Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}}); err != nil {
		t.Fatal(err)
	}
	runID := writingstore.StableID("run_", userID, "t05rt", fmt.Sprint(time.Now().UnixNano()))
	budget := writingplan.PlanBudget{MaxCostUSD: 100, MaxDurationMS: 3000000, MaxConcurrency: 1, MaxNodes: 10, MaxItems: 4}
	if err := store.CreateRun(ctx, writingstore.RunRecord{RunID: runID, DocumentID: documentID,
		ContractID: sealed.ContractID, ContractVersion: sealed.Version, ContractHash: sealed.ContractHash,
		BaseVersionID: version.VersionID, Status: "planned", ApprovalMode: sealed.Collaboration.ApprovalMode,
		RequestedAssurance: sealed.Collaboration.AssuranceLevel, Budget: budget,
		Permissions: []writingplan.Permission{"model.invoke", "materials.read"},
		Trace:       writingstore.TraceContext{Provenance: map[string]any{}, SourceRefs: []string{}, Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}}); err != nil {
		t.Fatal(err)
	}
	contractBody, err := json.Marshal(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutArtifactContent(ctx, contentHash(contractBody), "application/json", contractBody); err != nil {
		t.Fatal(err)
	}
	return &t05Fixture{store: store, gateway: WritingStoreContentGateway{Store: store},
		runID: runID, userID: userID, contract: sealed,
		planID: writingstore.StableID("plan_", documentID, "t05")}
}

func (fixture *t05Fixture) contractInput(t *testing.T) InputArtifact {
	t.Helper()
	body, err := json.Marshal(fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	return InputArtifact{ArtifactID: writingstore.StableID("art_", fixture.runID, "contract"),
		Version: 1, ArtifactType: "contract", ContentHash: contentHash(body),
		MediaType: "application/json", ContentRef: "artifact://" + strings.TrimPrefix(contentHash(body), "sha256:")}
}

func (fixture *t05Fixture) readNode(maxItems int) writingplan.PlanNode {
	return writingplan.PlanNode{NodeID: t05ReadNode, Kind: writingplan.NodeAction,
		Capability: "core.research.read", CapabilityVersion: "1.0.0",
		DependsOn:           []string{t05DiscoverNode},
		InputArtifactTypes:  []writingplan.ArtifactType{"contract", researchCandidatesType},
		OutputArtifactTypes: []writingplan.ArtifactType{"claim_map"},
		Bounds: writingplan.Bounds{MaxAttempts: 3, MaxConcurrency: 1, MaxItems: maxItems,
			MaxCostUSD: 100, TimeoutMS: 1200000},
		FailurePath: writingplan.FailurePause}
}

func (fixture *t05Fixture) readRequest(t *testing.T, candidatesInput InputArtifact, maxItems int) ExecutionRequest {
	t.Helper()
	node := fixture.readNode(maxItems)
	key, err := writingstore.NodeAttemptKey(fixture.runID, node.NodeID, 1)
	if err != nil {
		t.Fatal(err)
	}
	request := ExecutionRequest{RunID: fixture.runID, PlanID: fixture.planID, PlanVersion: 1,
		NodeID: node.NodeID, Attempt: 1, IdempotencyKey: key,
		ContractRef: writingplan.ObjectRef{ID: fixture.contract.ContractID, Version: fixture.contract.Version, Hash: fixture.contract.ContractHash},
		Node:        node, Inputs: []InputArtifact{fixture.contractInput(t), candidatesInput},
		Permissions: []writingplan.Permission{"model.invoke", "materials.read"}, UserID: fixture.userID}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	return request
}

// candidatesFromBody builds the read node's candidates input artifact from a
// ResearchCandidates content the test stages through the gateway.
func (fixture *t05Fixture) candidatesFromBody(t *testing.T, candidates writingkernel.ResearchCandidates) InputArtifact {
	t.Helper()
	body, err := json.Marshal(candidates)
	if err != nil {
		t.Fatal(err)
	}
	hash := contentHash(body)
	if err := fixture.store.PutArtifactContent(context.Background(), hash, "application/json", body); err != nil {
		t.Fatal(err)
	}
	return InputArtifact{ArtifactID: writingstore.StableID("art_", fixture.runID, t05DiscoverNode, "research_candidates"),
		Version: 1, ArtifactType: researchCandidatesType, ContentHash: hash,
		MediaType: "application/json", ContentRef: "artifact://" + strings.TrimPrefix(hash, "sha256:")}
}

// runDiscover executes the discover node over the given discovery client and
// returns the candidates input artifact for the read node.
func (fixture *t05Fixture) runDiscover(t *testing.T, client ScholarDiscoverClient) InputArtifact {
	t.Helper()
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
	executor, err := NewResearchDiscoverExecutor(client, fixture.gateway)
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
	result, err := executor.Execute(context.Background(), request)
	if err != nil {
		t.Fatalf("discover execute: %v", err)
	}
	body, err := fixture.gateway.Load(context.Background(), InputArtifact{ContentHash: result.Artifacts[0].ContentHash})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.PutArtifactContent(context.Background(), result.Artifacts[0].ContentHash, "application/json", body); err != nil {
		t.Fatal(err)
	}
	return InputArtifact{ArtifactID: writingstore.StableID("art_", fixture.runID, t05DiscoverNode, "research_candidates"),
		Version: 1, ArtifactType: researchCandidatesType, ContentHash: result.Artifacts[0].ContentHash,
		MediaType: "application/json", ContentRef: result.Artifacts[0].ContentRef}
}

// t05Candidates is a valid candidates/1 content with `selected` papers ready
// for the read workset.
func (fixture *t05Fixture) t05Candidates(t *testing.T, papers ...writingkernel.PaperCandidate) writingkernel.ResearchCandidates {
	t.Helper()
	if papers == nil {
		papers = []writingkernel.PaperCandidate{}
	}
	candidates := writingkernel.ResearchCandidates{SchemaVersion: writingkernel.ResearchCandidatesSchemaVersion,
		ContractHash: fixture.contract.ContractHash,
		QueryPlan: []writingkernel.QueryPlanEntry{{QueryID: "q1",
			Text: fixture.contract.Content.CentralQuestion, Origin: writingkernel.QueryOriginOriginal}},
		ProviderResults: []writingkernel.ProviderResult{{Provider: "openalex", Status: "ok"}},
		Papers:          papers,
		PolicyVersion:   ResearchDiscoverSelectionPolicy,
		Provenance:      []string{"test"}}
	if err := candidates.Validate(); err != nil {
		t.Fatal(err)
	}
	return candidates
}

func t05SelectedPaper(paperID string, withOA bool) writingkernel.PaperCandidate {
	doi := "10.5555/" + strings.ToLower(paperID)
	abstract := "background of " + paperID
	acquisition := writingkernel.PaperAcquisition{Status: writingkernel.AcquisitionAbstractAvailable}
	if withOA {
		acquisition = writingkernel.PaperAcquisition{Status: writingkernel.AcquisitionNotAttempted,
			OAURL: "https://oa.example.org/" + paperID + ".txt"}
	}
	return writingkernel.PaperCandidate{PaperID: paperID, DOI: &doi,
		Title: "Paper " + paperID, Authors: []string{"A. Author"}, Aliases: []string{"alias-" + paperID},
		Abstract: &abstract,
		Selection: writingkernel.PaperSelection{Status: writingkernel.SelectionStatusSelected,
			Score: floatPtr(3), Reason: "directly on topic"},
		Acquisition:     acquisition,
		RelevanceStatus: writingkernel.RelevanceScored}
}

func floatPtr(value float64) *float64 { return &value }

// t05UnreadPaper is selected but has neither an OA URL nor an abstract: the
// read executor must record it as unread (no citable evidence).
func t05UnreadPaper(paperID string) writingkernel.PaperCandidate {
	return writingkernel.PaperCandidate{PaperID: paperID,
		Title: "Paper " + paperID, Authors: []string{"A. Author"}, Aliases: []string{"alias-" + paperID},
		Selection: writingkernel.PaperSelection{Status: writingkernel.SelectionStatusSelected,
			Score: floatPtr(3), Reason: "directly on topic"},
		Acquisition:     writingkernel.PaperAcquisition{Status: writingkernel.AcquisitionMetadataOnly},
		RelevanceStatus: writingkernel.RelevanceScored}
}

// runRead executes the read node end to end.
func runRead(t *testing.T, fixture *t05Fixture, request ExecutionRequest, worker ResearchWorkerClient, budget ResearchBudgetBoundary) (ExecutionResult, error) {
	t.Helper()
	executor, err := NewResearchReadExecutor(worker, fixture.gateway, fixture.store, budget)
	if err != nil {
		t.Fatal(err)
	}
	return executor.Execute(context.Background(), request)
}

// ── Tests ──────────────────────────────────────────────────────────────────

// TestResearchDiscoverAssemblesCandidates drives the discover node and
// verifies the frozen research-candidates/1 content (query plan, selection,
// provenance).
func TestResearchDiscoverAssemblesCandidates(t *testing.T) {
	fixture := newT05Fixture(t)
	discovery := &fakeDiscoverClient{papers: []scholar.PaperCandidate{
		{PaperID: "p_aaa", Title: strPtr("Alpha paper"), Authors: []string{"A"}, Aliases: []string{"x"},
			Abstract: strPtr("alpha abstract"), OAURL: strPtr("https://oa.example.org/a.pdf"), Year: int64Ptr(2024)},
		{PaperID: "p_bbb", Title: strPtr("Beta paper"), Authors: []string{"B"}, Aliases: []string{"y"}},
	}}
	input := fixture.runDiscover(t, discovery)
	if discovery.discoverCalls != 3 { // max_queries=3 in the v1.1 fixture contract
		t.Fatalf("discover calls = %d", discovery.discoverCalls)
	}
	body, err := fixture.gateway.Load(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	var candidates writingkernel.ResearchCandidates
	if err := json.Unmarshal(body, &candidates); err != nil {
		t.Fatal(err)
	}
	if err := candidates.Validate(); err != nil {
		t.Fatalf("assembled candidates invalid: %v", err)
	}
	if candidates.ContractHash != fixture.contract.ContractHash {
		t.Fatal("candidates bound to a different contract")
	}
	if len(candidates.QueryPlan) != 3 || candidates.QueryPlan[0].Origin != writingkernel.QueryOriginOriginal ||
		candidates.QueryPlan[1].Origin != writingkernel.QueryOriginRewrite {
		t.Fatalf("query plan = %#v", candidates.QueryPlan)
	}
	if len(candidates.Papers) != 2 {
		t.Fatalf("papers = %d", len(candidates.Papers))
	}
	selected := 0
	for _, paper := range candidates.Papers {
		if paper.Selection.Status == writingkernel.SelectionStatusSelected {
			selected++
		}
	}
	if selected != 1 {
		t.Fatalf("selected = %d (paper without abstract must not enter the workset)", selected)
	}
}

func strPtr(value string) *string { return &value }

func int64Ptr(value int64) *int64 { return &value }

// TestResearchReadSubTaskCacheReuse is the restart contract: after a first
// successful execution, a NEW executor (fresh process) with identical inputs
// must reuse every hash-verified sub-task output and never call the worker
// again — and the rebuilt pack must equal the first one byte for byte.
func TestResearchReadSubTaskCacheReuse(t *testing.T) {
	fixture := newT05Fixture(t)
	ctx := context.Background()
	candidatesInput := fixture.candidatesFromBody(t, fixture.t05Candidates(t,
		t05SelectedPaper("p_one", true), t05SelectedPaper("p_two", true)))
	request := fixture.readRequest(t, candidatesInput, 4)

	worker := newFakeWorkerClient()
	budget := &fakeBudget{scripts: []bool{false}, reason: "budget"}
	first, err := runRead(t, fixture, request, worker, budget)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if worker.fetchCount("p_one") != 1 || worker.readCount("p_one") != 1 {
		t.Fatalf("first run call counts: fetch=%d read=%d", worker.fetchCount("p_one"), worker.readCount("p_one"))
	}

	// Simulated restart: fresh executor + fresh fake client over the same
	// ledger. The counts must stay zero — outputs are reused by hash.
	restarted := newFakeWorkerClient()
	restarted.fetchFunc = func(string, string) (*scholar.FetchFullTextOutputs, error) {
		return nil, errors.New("worker must not be called on cache reuse")
	}
	restarted.readFunc = func(string, []ReaderBlock) (*ReadOutputs, error) {
		return nil, errors.New("worker must not be called on cache reuse")
	}
	reuseBudget := &fakeBudget{scripts: []bool{false}, reason: "budget"}
	reuse, err := runRead(t, fixture, request, restarted, reuseBudget)
	if err != nil {
		t.Fatalf("restarted read: %v", err)
	}
	if restarted.fetchCount("p_one") != 0 || restarted.readCount("p_one") != 0 {
		t.Fatal("restarted run called the worker despite hash-verified cache")
	}
	if reuse.Artifacts[0].ContentHash != first.Artifacts[0].ContentHash {
		t.Fatalf("reused pack hash %s differs from first run %s",
			reuse.Artifacts[0].ContentHash, first.Artifacts[0].ContentHash)
	}
	firstBody, err := fixture.gateway.Load(ctx, InputArtifact{ContentHash: first.Artifacts[0].ContentHash})
	if err != nil {
		t.Fatal(err)
	}
	var pack writingkernel.ResearchEvidencePack
	if err := json.Unmarshal(firstBody, &pack); err != nil {
		t.Fatal(err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("pack invalid: %v", err)
	}
	if len(pack.Papers) != 2 || len(pack.Evidence) != 2 {
		t.Fatalf("pack shape: papers=%d evidence=%d", len(pack.Papers), len(pack.Evidence))
	}
}

// TestResearchReadFencingNegative verifies a late submission from an expired
// worker cannot overwrite the reclaimed task: the conditional complete must
// reject it (T02 semantics exercised through the research ledger).
func TestResearchReadFencingNegative(t *testing.T) {
	fixture := newT05Fixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	err := fixture.store.InTransaction(ctx, func(tx *writingstore.Tx) error {
		if _, err := tx.EnsureResearchTask(ctx, writingstore.CreateResearchTask{OwnerUserID: fixture.userID,
			RunID: fixture.runID, NodeID: t05ReadNode, TaskKey: "paper-x", Phase: "read",
			InputHash: t05sha256([]byte("read-input"))}, now); err != nil {
			return err
		}
		first, ok, err := tx.ClaimResearchTaskByIdentity(ctx, fixture.runID, t05ReadNode, "paper-x",
			t05sha256([]byte("read-input")), "worker-a", time.Minute, now)
		if err != nil || !ok {
			return fmt.Errorf("first claim ok=%v err=%v", ok, err)
		}
		// Worker A's lease expires; worker B reclaims.
		if _, ok, err := tx.ClaimResearchTaskByIdentity(ctx, fixture.runID, t05ReadNode, "paper-x",
			t05sha256([]byte("read-input")), "worker-b", time.Minute, now.Add(2*time.Minute)); err != nil || !ok {
			return fmt.Errorf("reclaim ok=%v err=%v", ok, err)
		}
		// Worker A's late completion must be fenced out.
		if err := tx.CompleteResearchTask(ctx, first.ID, "worker-a", writingstore.ResearchTaskCompletion{
			OutputArtifactID: "art_late", OutputHash: t05sha256([]byte("late"))}, now.Add(time.Second)); err == nil {
			return errors.New("late completion must be fenced")
		}
		// And the live reclaim owner completes cleanly.
		return tx.CompleteResearchTask(ctx, first.ID, "worker-b", writingstore.ResearchTaskCompletion{
			OutputArtifactID: "art_reclaimed", OutputHash: t05sha256([]byte("reclaimed"))}, now.Add(2*time.Minute))
	})
	if err != nil {
		t.Fatalf("fencing negative: %v", err)
	}
	// The identity lookup sees exactly the reclaimed outcome.
	task, err := fixture.store.GetResearchTaskByIdentity(ctx, fixture.runID, t05ReadNode,
		"paper-x", t05sha256([]byte("read-input")))
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != writingstore.ResearchTaskSucceeded || task.OutputHash != t05sha256([]byte("reclaimed")) {
		t.Fatalf("fenced task state = %s %s", task.Status, task.OutputHash)
	}
}

// TestResearchReadProgressSequenceMonotonic drives a two-paper read and
// verifies the research.progress events exist with strictly increasing
// sequences and the completed counter advancing.
func TestResearchReadProgressSequenceMonotonic(t *testing.T) {
	fixture := newT05Fixture(t)
	candidatesInput := fixture.candidatesFromBody(t, fixture.t05Candidates(t,
		t05SelectedPaper("p_one", true), t05SelectedPaper("p_two", true)))
	request := fixture.readRequest(t, candidatesInput, 4)
	if _, err := runRead(t, fixture, request, newFakeWorkerClient(), &fakeBudget{scripts: []bool{false}}); err != nil {
		t.Fatalf("read: %v", err)
	}
	events, err := fixture.store.ListRunEvents(context.Background(), fixture.runID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	last := int64(0)
	progressCount := 0
	completed := 0
	for _, event := range events {
		if event.Sequence <= last {
			t.Fatalf("event sequence %d not greater than %d", event.Sequence, last)
		}
		last = event.Sequence
		if event.EventType != "research.progress" {
			continue
		}
		progressCount++
		value, ok := event.Payload["completed"].(float64)
		if !ok {
			t.Fatalf("progress payload missing completed: %#v", event.Payload)
		}
		if int(value) < completed {
			t.Fatalf("completed counter went backwards: %d after %d", int(value), completed)
		}
		completed = int(value)
	}
	if progressCount != 2 {
		t.Fatalf("research.progress events = %d, want 2", progressCount)
	}
	if completed != 2 {
		t.Fatalf("final completed = %d", completed)
	}
}

// TestResearchReadBudgetBoundaryCacheReuse (updated for F2): the boundary
// fires between papers with one citable source (>= the fixture's 1-source
// floor) → the partial pack freezes IN the same dispatch. A later fresh
// dispatch over the same ledger (e.g. the owner retried after raising the
// budget in a new run) reuses the hash-verified sub-task outputs: paper 1 is
// never re-read.
func TestResearchReadBudgetBoundaryCacheReuse(t *testing.T) {
	fixture := newT05Fixture(t)
	candidatesInput := fixture.candidatesFromBody(t, fixture.t05Candidates(t,
		t05SelectedPaper("p_one", true), t05SelectedPaper("p_two", true)))
	request := fixture.readRequest(t, candidatesInput, 4)

	worker := newFakeWorkerClient()
	guard := &fakeBudget{scripts: []bool{false, true}, reason: "budget boundary"}
	result, err := runRead(t, fixture, request, worker, guard)
	if err != nil {
		t.Fatalf("boundary with sufficient sources must freeze a partial pack: %v", err)
	}
	if worker.fetchCount("p_one") != 1 || worker.fetchCount("p_two") != 0 {
		t.Fatalf("fetch counts p_one=%d p_two=%d", worker.fetchCount("p_one"), worker.fetchCount("p_two"))
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].OutputKey != "research_evidence_pack" {
		t.Fatalf("partial-pack artifacts = %#v", result.Artifacts)
	}

	// Fresh-process reuse: the finished paper's sub-tasks stay hash-verified
	// in the ledger and are never re-driven. Paper 2 is still unread (its
	// dispatch was budget-stopped, not completed), so IT re-runs — that is
	// the new run's fresh budget at work.
	beforeFetchesTwo := worker.fetchCount("p_two")
	reuse := &fakeBudget{scripts: []bool{false}}
	if _, err := runRead(t, fixture, request, worker, reuse); err != nil {
		t.Fatalf("reuse dispatch: %v", err)
	}
	if worker.fetchCount("p_one") != 1 || worker.readCount("p_one") != 1 {
		t.Fatalf("finished paper re-drove calls: fetch=%d read=%d", worker.fetchCount("p_one"), worker.readCount("p_one"))
	}
	if worker.fetchCount("p_two") != beforeFetchesTwo+1 {
		t.Fatalf("unfinished paper fetch count = %d, want +1 (fresh budget reads it once)", worker.fetchCount("p_two"))
	}
}

// TestResearchReadInsufficientEvidence: fewer citable sources than
// min_citable_sources → the typed sentinel pauses the run with candidates
// preserved (422 INSUFFICIENT_EVIDENCE contract).
func TestResearchReadInsufficientEvidence(t *testing.T) {
	fixture := newT05Fixture(t)
	// Two selected papers with neither OA full text nor abstract: the workset
	// yields zero citable sources against the contract's 1-source floor.
	candidatesInput := fixture.candidatesFromBody(t, fixture.t05Candidates(t,
		t05UnreadPaper("p_none_one"), t05UnreadPaper("p_none_two")))
	request := fixture.readRequest(t, candidatesInput, 4)
	_, err := runRead(t, fixture, request, newFakeWorkerClient(), &fakeBudget{scripts: []bool{false}})
	if !errors.Is(err, ErrInsufficientEvidence) {
		t.Fatalf("error = %v, want ErrInsufficientEvidence", err)
	}
	var typed *RuntimeError
	if !errors.As(err, &typed) || typed.Code != CodeInsufficientEvidence {
		t.Fatalf("error code = %v", typed)
	}
}

// TestResearchReadAbstractDegradation: a likely-scanned PDF with
// abstract_allowed degrades to abstract reading with a separate abstract
// document hash — the PDF hash never backs an abstract quote.
func TestResearchReadAbstractDegradation(t *testing.T) {
	fixture := newT05Fixture(t)
	worker := newFakeWorkerClient()
	worker.fetchFunc = func(paperID, _ string) (*scholar.FetchFullTextOutputs, error) {
		content := []byte("scanned pixels of " + paperID)
		scanned := true
		return &scholar.FetchFullTextOutputs{PaperID: paperID, AcquisitionStatus: "full_text_available",
			ContentHash: t05sha256(content), SizeBytes: int64(len(content)), MediaType: "application/pdf",
			ContentBase64: base64.StdEncoding.EncodeToString(content), LikelyScanned: &scanned}, nil
	}
	candidatesInput := fixture.candidatesFromBody(t, fixture.t05Candidates(t, t05SelectedPaper("p_scan", true)))
	request := fixture.readRequest(t, candidatesInput, 4)
	result, err := runRead(t, fixture, request, worker, &fakeBudget{scripts: []bool{false}})
	if err != nil {
		t.Fatalf("degraded read: %v", err)
	}
	body, err := fixture.gateway.Load(context.Background(), InputArtifact{ContentHash: result.Artifacts[0].ContentHash})
	if err != nil {
		t.Fatal(err)
	}
	var pack writingkernel.ResearchEvidencePack
	if err := json.Unmarshal(body, &pack); err != nil {
		t.Fatal(err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("degraded pack invalid: %v", err)
	}
	if len(pack.Papers) != 1 || pack.Papers[0].ReadingScope != writingkernel.ReadingScopeAbstract {
		t.Fatalf("reading scope = %#v", pack.Papers)
	}
	if len(pack.Evidence) == 0 || pack.Evidence[0].EvidenceScope != writingkernel.EvidenceScopeAbstract {
		t.Fatalf("evidence scope = %#v", pack.Evidence)
	}
	// The abstract content is its own artifact; the PDF hash must not appear.
	pdfHash := t05sha256([]byte("scanned pixels of p_scan"))
	for _, evidence := range pack.Evidence {
		if evidence.BlockHash == pdfHash {
			t.Fatal("abstract quote backed by the PDF hash")
		}
	}
}

// TestBuildEvidencePackNegatives locks the pack verifier: offset tampering,
// quote mismatch, and hash errors must be rejected (EVIDENCE_INVALID), while
// a valid pack passes with quotes verified against stored block text.
func TestBuildEvidencePackNegatives(t *testing.T) {
	blockText := "The catalyst degrades rapidly above 80 degrees Celsius."
	blockHash := t05sha256([]byte(blockText))
	blocks := []ParsedBlock{{BlockID: "blk-1", Text: blockText, BlockHash: blockHash}}
	blockMap := map[string]ParsedBlock{"blk-1": blocks[0]}
	documentRef := writingkernel.ArtifactRef{ArtifactID: "art_doc", Version: 1, ContentHash: t05sha256([]byte("doc"))}
	parsedRef := documentRef
	base := PaperReadResult{PaperID: "p_ok",
		Bibliography:    writingkernel.PaperBibliography{Title: "Catalyst paper", Authors: []string{"A"}},
		Origin:          writingkernel.PaperOriginExternal,
		SelectionReason: "selected by relevance", RelevanceStatus: writingkernel.RelevanceScored,
		ReadingScope: writingkernel.ReadingScopeFullText,
		DocumentRef:  &documentRef, ParsedDocumentRef: &parsedRef,
		Blocks: blocks, BlocksByID: blockMap, ReadBlockIDs: []string{"blk-1"},
		TotalBlocks: 1,
		Claims: []writingkernel.Claim{{ClaimID: "clm_1", PaperID: "p_ok", Text: "The catalyst degrades.",
			Kind: writingkernel.ClaimKindSourceAssertion, EvidenceIDs: []string{"ev_1"},
			ReviewStatus: writingkernel.ClaimReviewPending, Limitations: []string{}}},
		Evidence: []writingkernel.Evidence{{EvidenceID: "ev_1", PaperID: "p_ok", DocumentRef: documentRef,
			BlockID: "blk-1", BlockHash: blockHash, Quote: blockText[0:12], StartChar: 0, EndChar: 12,
			EvidenceScope: writingkernel.EvidenceScopeFullText}},
	}
	candidatesRef := writingkernel.ArtifactRef{ArtifactID: "art_cand", Version: 1, ContentHash: t05sha256([]byte("cand"))}
	quota := EvidenceQuota{MinCitableSources: 1, EvidenceRequirement: writingkernel.EvidenceRequirementAbstractAllowed}

	if _, err := BuildEvidencePack(t05sha256([]byte("contract")), candidatesRef, []PaperReadResult{base}, quota, []string{"p"}); err != nil {
		t.Fatalf("valid pack rejected: %v", err)
	}

	// Negative 1: offset tampering — the quote no longer matches the block
	// slice.
	tampered := base
	tampered.Evidence = []writingkernel.Evidence{{EvidenceID: "ev_1", PaperID: "p_ok", DocumentRef: documentRef,
		BlockID: "blk-1", BlockHash: blockHash, Quote: blockText[0:12], StartChar: 4, EndChar: 16,
		EvidenceScope: writingkernel.EvidenceScopeFullText}}
	if _, err := BuildEvidencePack(t05sha256([]byte("contract")), candidatesRef, []PaperReadResult{tampered}, quota, []string{"p"}); !errors.Is(err, ErrEvidenceInvalid) {
		t.Fatalf("tampered offsets accepted: %v", err)
	}

	// Negative 2: block hash error.
	wrongHash := base
	wrongHash.Evidence = []writingkernel.Evidence{{EvidenceID: "ev_1", PaperID: "p_ok", DocumentRef: documentRef,
		BlockID: "blk-1", BlockHash: t05sha256([]byte("other")), Quote: blockText[0:12], StartChar: 0, EndChar: 12,
		EvidenceScope: writingkernel.EvidenceScopeFullText}}
	if _, err := BuildEvidencePack(t05sha256([]byte("contract")), candidatesRef, []PaperReadResult{wrongHash}, quota, []string{"p"}); !errors.Is(err, ErrEvidenceInvalid) {
		t.Fatalf("wrong block hash accepted: %v", err)
	}

	// Negative 3: evidence naming a block that is not in the parsed document.
	ghost := base
	ghost.Evidence = []writingkernel.Evidence{{EvidenceID: "ev_1", PaperID: "p_ok", DocumentRef: documentRef,
		BlockID: "blk-ghost", BlockHash: blockHash, Quote: blockText[0:12], StartChar: 0, EndChar: 12,
		EvidenceScope: writingkernel.EvidenceScopeFullText}}
	if _, err := BuildEvidencePack(t05sha256([]byte("contract")), candidatesRef, []PaperReadResult{ghost}, quota, []string{"p"}); !errors.Is(err, ErrEvidenceInvalid) {
		t.Fatalf("ghost block accepted: %v", err)
	}

	// Negative 4: unread paper producing evidence (kernel validation).
	unread := base
	unread.ReadingScope = writingkernel.ReadingScopeUnread
	unread.ReadBlockIDs = []string{}
	if _, err := BuildEvidencePack(t05sha256([]byte("contract")), candidatesRef, []PaperReadResult{unread}, quota, []string{"p"}); !errors.Is(err, ErrEvidenceInvalid) {
		t.Fatalf("unread evidence accepted: %v", err)
	}
}

// TestResearchReadOutcomeUnknownParksRow: a lost connection after the worker
// may have accepted the call parks the sub-task as outcome_unknown, and a
// re-drive fails closed instead of silently re-billing.
func TestResearchReadOutcomeUnknownParksRow(t *testing.T) {
	fixture := newT05Fixture(t)
	worker := newFakeWorkerClient()
	worker.fetchFunc = func(paperID, _ string) (*scholar.FetchFullTextOutputs, error) {
		return nil, &scholar.Error{Kind: scholar.ErrOutcomeUnknown, Code: "client_transport_error", Message: "connection lost"}
	}
	candidatesInput := fixture.candidatesFromBody(t, fixture.t05Candidates(t, t05SelectedPaper("p_lost", true)))
	request := fixture.readRequest(t, candidatesInput, 4)
	if _, err := runRead(t, fixture, request, worker, &fakeBudget{scripts: []bool{false}}); err == nil {
		t.Fatal("outcome-unknown must fail the node")
	}
	tasks, err := fixture.store.ListResearchTasksByNode(context.Background(), fixture.runID, t05ReadNode, fixture.userID)
	if err != nil {
		t.Fatal(err)
	}
	sawUnknown := false
	for _, task := range tasks {
		if task.Status == writingstore.ResearchTaskOutcomeUnknwn {
			sawUnknown = true
		}
	}
	if !sawUnknown {
		t.Fatalf("no outcome_unknown row: %+v", tasks)
	}
}

// TestResearchDiscoverFailsClosedWhenAllQueriesFail: every query failing is
// RESEARCH_UNAVAILABLE, never a silent empty candidate set.
func TestResearchDiscoverFailsClosedWhenAllQueriesFail(t *testing.T) {
	fixture := newT05Fixture(t)
	executor, err := NewResearchDiscoverExecutor(failingDiscoverClient{}, fixture.gateway)
	if err != nil {
		t.Fatal(err)
	}
	node := writingplan.PlanNode{NodeID: t05DiscoverNode, Kind: writingplan.NodeAction,
		Capability: "core.research.discover", CapabilityVersion: "1.0.0",
		InputArtifactTypes:  []writingplan.ArtifactType{"contract"},
		OutputArtifactTypes: []writingplan.ArtifactType{"research_note"},
		Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 20, MaxCostUSD: 10, TimeoutMS: 600000},
		FailurePath:         writingplan.FailurePause}
	key, err := writingstore.NodeAttemptKey(fixture.runID, node.NodeID, 1)
	if err != nil {
		t.Fatal(err)
	}
	request := ExecutionRequest{RunID: fixture.runID, PlanID: fixture.planID, PlanVersion: 1,
		NodeID: node.NodeID, Attempt: 1, IdempotencyKey: key,
		ContractRef: writingplan.ObjectRef{ID: fixture.contract.ContractID, Version: fixture.contract.Version, Hash: fixture.contract.ContractHash},
		Node:        node, Inputs: []InputArtifact{fixture.contractInput(t)},
		Permissions: []writingplan.Permission{"model.invoke", "materials.read"}, UserID: fixture.userID}
	if _, err := executor.Execute(context.Background(), request); err == nil {
		t.Fatal("all-queries-failed must fail closed")
	}
}

type failingDiscoverClient struct{}

func (failingDiscoverClient) Discover(context.Context, string, []string, int, ...scholar.CallOption) (*scholar.DiscoverOutputs, *scholar.OperationResponse, error) {
	return nil, nil, &scholar.Error{Kind: scholar.ErrRemote, Code: "all_providers_failed", Message: "upstream down"}
}

func (failingDiscoverClient) Rank(context.Context, string, []scholar.RankCandidate, ...scholar.CallOption) (*scholar.RankOutputs, *scholar.OperationResponse, error) {
	return nil, nil, &scholar.Error{Kind: scholar.ErrRemote, Code: "all_providers_failed", Message: "upstream down"}
}
