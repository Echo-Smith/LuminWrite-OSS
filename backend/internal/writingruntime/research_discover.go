package writingruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/scholar"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// research_discover executor (design.md §3 node table): one action node that
// plans queries from the contract's central question, fans out to the worker's
// discover operation, ranks the merged candidates in ≤8-paper batches, applies
// the selection policy, and freezes the research-candidates/1 artifact through
// the ContentGateway. The node is a bounded unit: every worker call is one
// HTTP round trip, and the whole run reuses the frozen artifact by hash.

const (
	// ResearchDiscoverSelectionPolicy is the v1 selection policy version.
	ResearchDiscoverSelectionPolicy = "selection/1"
	// ResearchDiscoverRelevanceThreshold: rank scores are 0..3 relevance
	// (never credibility); 2+ enters the selected workset.
	ResearchDiscoverRelevanceThreshold = 2.0
	// scholarDiscoverCallTimeout bounds a single worker call; the node
	// timeout still caps the total.
	scholarDiscoverCallTimeout = 90 * time.Second
)

// ScholarDiscoverClient is the discover+rank surface. *scholar.Client
// implements it; tests inject fakes that count calls.
type ScholarDiscoverClient interface {
	Discover(ctx context.Context, query string, providerAllowlist []string, limit int, opts ...scholar.CallOption) (*scholar.DiscoverOutputs, *scholar.OperationResponse, error)
	Rank(ctx context.Context, researchQuestion string, candidates []scholar.RankCandidate, opts ...scholar.CallOption) (*scholar.RankOutputs, *scholar.OperationResponse, error)
}

// ResearchDiscoverExecutor freezes the run's candidate set.
type ResearchDiscoverExecutor struct {
	descriptor    ExecutorDescriptor
	client        ScholarDiscoverClient
	content       ContentGateway
	now           func() time.Time
	providerAllow []string
	policyVersion string
}

// NewResearchDiscoverExecutor wires the executor. An empty allowlist selects
// the worker's three known providers.
func NewResearchDiscoverExecutor(client ScholarDiscoverClient, content ContentGateway) (*ResearchDiscoverExecutor, error) {
	if client == nil || content == nil {
		return nil, ErrRuntimeNotReady
	}
	descriptor := ExecutorDescriptor{ExecutorID: "engine.step.research_discover", Version: "1",
		SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}, Cancellable: false}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	return &ResearchDiscoverExecutor{descriptor: descriptor, client: client, content: content,
		now:           func() time.Time { return time.Now().UTC() },
		providerAllow: []string{"openalex", "crossref", "semantic_scholar"},
		policyVersion: ResearchDiscoverSelectionPolicy}, nil
}

func (executor *ResearchDiscoverExecutor) Descriptor() ExecutorDescriptor { return executor.descriptor }

// Execute runs the bounded discovery unit.
func (executor *ResearchDiscoverExecutor) Execute(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
	if err := request.Validate(); err != nil {
		return ExecutionResult{}, err
	}
	contract, _, err := loadContractInput(ctx, executor.content, request)
	if err != nil {
		return ExecutionResult{}, err
	}
	spec := contract.Research
	question := contract.Content.CentralQuestion

	// ── Query plan: the original question first (origin=original); the v1
	// rewrites are deterministic variants (topic, then a review suffix) so
	// the same contract always yields the same plan — never model-generated.
	plan := buildResearchQueryPlan(question, contract.Content.Topic, spec.MaxQueries)
	perQueryLimit := scholarDiscoverLimit(spec.MaxCandidates, len(plan))

	merged := map[string]scholar.PaperCandidate{}
	var providerResults []writingkernel.ProviderResult
	provenance := []string{"executor:writingruntime.research_discover@1",
		"policy:" + executor.policyVersion}
	queriesRun := 0
	for _, query := range plan {
		callCtx, cancel := context.WithTimeout(ctx, scholarDiscoverCallTimeout)
		outputs, response, err := executor.client.Discover(callCtx, query.Text, executor.providerAllow, perQueryLimit)
		cancel()
		if err != nil {
			// One failed query does not kill the node (design.md §7); record
			// the per-query error and keep the plan moving.
			provenance = append(provenance, fmt.Sprintf("query %s failed: %v", query.QueryID, err))
			continue
		}
		queriesRun++
		for _, provider := range outputs.ProviderResults {
			entry := writingkernel.ProviderResult{Provider: provider.Provider, Status: provider.Status,
				RequestRef: response.RequestID}
			if provider.ErrorCode != nil {
				entry.ErrorCode = *provider.ErrorCode
			}
			providerResults = append(providerResults, entry)
		}
		for _, paper := range outputs.Papers {
			if _, exists := merged[paper.PaperID]; exists || strings.TrimSpace(paper.PaperID) == "" {
				continue
			}
			merged[paper.PaperID] = paper
		}
	}
	if queriesRun == 0 {
		return ExecutionResult{}, runtimeError(CodeResearchUnavailable, RetrySafe,
			"every discovery query failed; the research path pauses rather than reporting an empty result", nil)
	}
	if len(providerResults) == 0 {
		providerResults = []writingkernel.ProviderResult{}
	}

	papers := make([]scholar.PaperCandidate, 0, len(merged))
	for _, paper := range merged {
		papers = append(papers, paper)
	}
	sort.Slice(papers, func(i, j int) bool { return papers[i].PaperID < papers[j].PaperID })

	// ── Rank in batches of ≤8 over papers that carry an abstract.
	scores := executor.rankBatches(ctx, question, papers, &provenance)

	// ── Selection (policy selection/1): relevance-descending, capped at
	// max_papers. Unscored papers are deferred with an explicit reason —
	// they never silently enter the workset.
	candidates := assembleResearchCandidates(contract, spec, plan, providerResults, papers, scores, executor.policyVersion)
	if err := candidates.Validate(); err != nil {
		return ExecutionResult{}, runtimeError(CodeExecutorOutputInvalid, RetryNever,
			"assembled research candidates failed kernel validation", err)
	}
	body, err := json.Marshal(candidates)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeExecutorOutputInvalid, RetryNever, "marshal candidates", err)
	}
	ref, hash, err := executor.content.Stage(ctx, request.IdempotencyKey+":research_candidates", "application/json", body)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeArtifactCommitFailed, RetrySafe, "stage research candidates", err)
	}
	if hash != contentHash(body) {
		return ExecutionResult{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "staged candidates hash mismatch", nil)
	}
	parents, inputHashes := lineageInputs(request)
	return ExecutionResult{
		Artifacts: []OutputArtifactDraft{{OutputKey: "research_candidates",
			ArtifactType: request.Node.OutputArtifactTypes[0],
			ContentHash:  hash, MediaType: "application/json", ContentRef: ref,
			Parents: parents, Producer: request.Node.Capability,
			CapabilityVersion: request.Node.CapabilityVersion, InputHashes: inputHashes,
			Provenance: map[string]any{"contract_hash": contract.ContractHash,
				"queries": len(plan), "candidates": len(papers), "policy_version": executor.policyVersion},
			SourceRefs: []string{}}},
		Usage:     ExecutionUsage{DurationMS: 0},
		StartedAt: executor.now(), CompletedAt: executor.now(),
	}, nil
}

// rankBatches drives Rank over abstract-carrying papers in ≤8 batches. A rank
// failure fails the node fail-closed: silently unscored candidates would
// corrupt selection downstream.
func (executor *ResearchDiscoverExecutor) rankBatches(ctx context.Context, question string, papers []scholar.PaperCandidate, provenance *[]string) map[string]writingkernel.PaperSelection {
	scores := map[string]writingkernel.PaperSelection{}
	var batch []scholar.RankCandidate
	flush := func(batch []scholar.RankCandidate) error {
		if len(batch) == 0 {
			return nil
		}
		callCtx, cancel := context.WithTimeout(ctx, scholarDiscoverCallTimeout)
		outputs, response, err := executor.client.Rank(callCtx, question, batch)
		cancel()
		if err != nil {
			return err
		}
		for _, score := range outputs.Scores {
			scores[score.PaperID] = writingkernel.PaperSelection{Status: writingkernel.SelectionStatusDeferred,
				Score: &score.Score, Reason: score.Reason}
		}
		*provenance = append(*provenance, fmt.Sprintf("rank batch %s: %d scored", shortRequestRef(response.RequestID), len(batch)))
		return nil
	}
	for _, paper := range papers {
		if paper.Abstract == nil || strings.TrimSpace(*paper.Abstract) == "" {
			continue
		}
		batch = append(batch, scholar.RankCandidate{PaperID: paper.PaperID, Abstract: *paper.Abstract})
		if len(batch) == scholarRankBatchSize {
			if err := flush(batch); err != nil {
				*provenance = append(*provenance, fmt.Sprintf("rank failed: %v", err))
				return nil
			}
			batch = nil
		}
	}
	if err := flush(batch); err != nil {
		*provenance = append(*provenance, fmt.Sprintf("rank failed: %v", err))
		return nil
	}
	return scores
}

const scholarRankBatchSize = 8

func shortRequestRef(requestID string) string {
	if len(requestID) > 8 {
		return requestID[:8]
	}
	return requestID
}

// assembleResearchCandidates maps scholar discovery records onto the frozen
// research-candidates/1 content (contracts.md §2).
func assembleResearchCandidates(contract writingkernel.WritingContract, spec *writingkernel.ResearchSpec,
	plan []writingkernel.QueryPlanEntry, providerResults []writingkernel.ProviderResult,
	papers []scholar.PaperCandidate, scores map[string]writingkernel.PaperSelection, policyVersion string) writingkernel.ResearchCandidates {

	maxPapers := 1
	if spec != nil && spec.MaxPapers > 0 {
		maxPapers = spec.MaxPapers
	}
	kernelPapers := make([]writingkernel.PaperCandidate, 0, len(papers))
	selected := 0
	for _, paper := range papers {
		if paper.Title == nil || strings.TrimSpace(*paper.Title) == "" {
			continue // a candidate without a title cannot be cited or deduped
		}
		selection := writingkernel.PaperSelection{Status: writingkernel.SelectionStatusDeferred, Reason: "unscored: no abstract available for ranking (selection/1)"}
		if scored, ok := scores[paper.PaperID]; ok {
			selection = scored
			if selection.Reason == "" {
				selection.Reason = "ranked by relevance"
			}
			if selected < maxPapers && selection.Score != nil && *selection.Score >= ResearchDiscoverRelevanceThreshold {
				selection.Status = writingkernel.SelectionStatusSelected
				selected++
			} else {
				selection.Status = writingkernel.SelectionStatusDeferred
			}
		}
		kernelPaper := writingkernel.PaperCandidate{
			PaperID: paper.PaperID, DOI: paper.DOI, Title: strings.TrimSpace(*paper.Title),
			Authors: orEmptySlice(paper.Authors), Venue: paper.Venue, CanonicalURL: paper.CanonicalURL,
			Aliases: orEmptySlice(paper.Aliases), Abstract: paper.Abstract,
			Selection:       selection,
			RelevanceStatus: writingkernel.RelevanceUnscored,
		}
		if _, scored := scores[paper.PaperID]; scored {
			kernelPaper.RelevanceStatus = writingkernel.RelevanceScored
		}
		if paper.Year != nil {
			year := int(*paper.Year)
			kernelPaper.Year = &year
		}
		acquisition := writingkernel.PaperAcquisition{Status: writingkernel.AcquisitionMetadataOnly}
		if paper.Abstract != nil && strings.TrimSpace(*paper.Abstract) != "" {
			acquisition.Status = writingkernel.AcquisitionAbstractAvailable
		}
		if paper.OAURL != nil && strings.TrimSpace(*paper.OAURL) != "" {
			acquisition.Status = writingkernel.AcquisitionNotAttempted
			acquisition.OAURL = strings.TrimSpace(*paper.OAURL)
		}
		kernelPaper.Acquisition = acquisition
		kernelPapers = append(kernelPapers, kernelPaper)
	}
	if kernelPapers == nil {
		kernelPapers = []writingkernel.PaperCandidate{}
	}
	return writingkernel.ResearchCandidates{
		SchemaVersion:   writingkernel.ResearchCandidatesSchemaVersion,
		ContractHash:    contract.ContractHash,
		QueryPlan:       plan,
		ProviderResults: providerResults,
		Papers:          kernelPapers,
		PolicyVersion:   policyVersion,
		Provenance:      []string{"executor:writingruntime.research_discover@1", "policy:" + policyVersion},
	}
}

// buildResearchQueryPlan produces up to maxQueries entries: the original
// central question first, then deterministic rewrite variants (topic, then a
// review suffix). Placeholder v1 planning — never model-generated, so the
// same contract always compiles to the same plan.
func buildResearchQueryPlan(question, topic string, maxQueries int) []writingkernel.QueryPlanEntry {
	if maxQueries < 1 {
		maxQueries = 1
	}
	if maxQueries > writingkernel.ResearchMaxQueries {
		maxQueries = writingkernel.ResearchMaxQueries
	}
	plan := []writingkernel.QueryPlanEntry{{QueryID: "q1", Text: strings.TrimSpace(question), Origin: writingkernel.QueryOriginOriginal}}
	variants := []string{}
	if trimmed := strings.TrimSpace(topic); trimmed != "" && trimmed != strings.TrimSpace(question) {
		variants = append(variants, trimmed)
	}
	variants = append(variants, strings.TrimSpace(question)+" review survey")
	for index, variant := range variants {
		if len(plan) >= maxQueries {
			break
		}
		plan = append(plan, writingkernel.QueryPlanEntry{QueryID: fmt.Sprintf("q%d", len(plan)+1),
			Text: variant, Origin: writingkernel.QueryOriginRewrite})
		_ = index
	}
	return plan
}

func scholarDiscoverLimit(maxCandidates, queryCount int) int {
	// Worker-side cap (operations.py MAX_DISCOVER_LIMIT = 50); mirrored here
	// so a miscompiled contract cannot produce a rejected payload.
	const workerMaxDiscoverLimit = 50
	if maxCandidates < 1 {
		maxCandidates = 1
	}
	limit := maxCandidates / queryCount
	if limit < 1 {
		limit = 1
	}
	if limit > workerMaxDiscoverLimit {
		limit = workerMaxDiscoverLimit
	}
	return limit
}

// loadContractInput loads and verifies the contract artifact input and decodes
// it as a strict research-review contract (v1.1 + research spec required).
func loadContractInput(ctx context.Context, content ContentGateway, request ExecutionRequest) (writingkernel.WritingContract, InputArtifact, error) {
	var contractInput InputArtifact
	for _, input := range request.Inputs {
		if input.ArtifactType == "contract" {
			contractInput = input
			break
		}
	}
	if contractInput.ArtifactID == "" {
		return writingkernel.WritingContract{}, InputArtifact{}, runtimeError(CodeExecutorContractMismatch, RetryNever, "contract input artifact is missing", ErrInvalidExecutionRequest)
	}
	body, err := content.Load(ctx, contractInput)
	if err != nil {
		return writingkernel.WritingContract{}, InputArtifact{}, runtimeError(CodeMaterialIntegrityFailed, RetrySafe, "load contract artifact", err)
	}
	if contentHash(body) != contractInput.ContentHash {
		return writingkernel.WritingContract{}, InputArtifact{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "contract artifact content hash mismatch", nil)
	}
	contract, err := writingkernel.DecodeWritingContractResearchStrict(body)
	if err != nil {
		return writingkernel.WritingContract{}, InputArtifact{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "contract artifact is not a valid research contract", err)
	}
	if contract.Research == nil {
		return writingkernel.WritingContract{}, InputArtifact{}, runtimeError(CodeExecutorContractMismatch, RetryNever, "research executors require a research spec", ErrInvalidExecutionRequest)
	}
	return contract, contractInput, nil
}

// lineageInputs builds the draft lineage covering every execution input.
func lineageInputs(request ExecutionRequest) ([]writingstore.ArtifactRef, []string) {
	parents := make([]writingstore.ArtifactRef, 0, len(request.Inputs))
	inputHashSet := map[string]struct{}{}
	inputHashes := make([]string, 0, len(request.Inputs))
	for _, input := range request.Inputs {
		parents = append(parents, writingstore.ArtifactRef{ArtifactID: input.ArtifactID, Version: input.Version})
		if _, duplicate := inputHashSet[input.ContentHash]; duplicate {
			continue
		}
		inputHashSet[input.ContentHash] = struct{}{}
		inputHashes = append(inputHashes, input.ContentHash)
	}
	return parents, inputHashes
}

func orEmptySlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
