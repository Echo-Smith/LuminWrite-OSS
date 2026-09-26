package writingruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/scholar"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// research_read executor (design.md §3 node table + §6 sub-task ledger): one
// action node that reads the selected workset paper by paper through three
// ledger-backed phases (fetch → parse → read), each persisted as a
// writing_research_tasks row with lease fencing. Restarts reuse hash-verified
// succeeded sub-task outputs instead of re-calling the worker; failures and
// budget boundaries pause cleanly with completed sub-tasks preserved.

const (
	// ResearchReadParserVersion / ResearchReadPromptVersion pin the versions
	// the sub-task input_hash covers (design.md §6).
	ResearchReadParserVersion = "parser/1"
	// ResearchReaderPolicyVersion matches the contract's reader_policy_version
	// the read executor pins into sub-task input hashes (T05); the real
	// worker rejects an empty version.
	ResearchReaderPolicyVersion = "reader/1"
	ResearchReadPromptVersion   = "reader-prompt/1"
	// ResearchReadBlockSelection is the v1 block-selection policy: the first
	// N blocks in parse order (N ≤ 24, contracts.md §4).
	ResearchReadBlockSelection = "first_n"
	ResearchReadMaxBlocks      = 24
	researchFetchSizeLimit     = 25 * 1024 * 1024
	researchLeaseTTL           = 10 * time.Minute
	// 480s：必须 ≥ scholar 层 llmReadTimeout（480s），否则外层先掐断把内层
	// 上限架空（run_78601704 两 attempt 均精确 120.0s 死于此错配）。reasoning
	// 模型 read 实测 217s→300s+ 波动；节点 bounds（600s）仍在外层兜底。
	researchCallTimeout = 480 * time.Second
)

// T05 research runtime error codes (additive; defined here so the shared
// errors.go table stays untouched). RESEARCH_UNAVAILABLE,
// INSUFFICIENT_EVIDENCE, and EVIDENCE_INVALID align with contracts.md §3.
const (
	CodeResearchUnavailable      ErrorCode = "RESEARCH_UNAVAILABLE"
	CodeInsufficientEvidence     ErrorCode = "INSUFFICIENT_EVIDENCE"
	CodeEvidenceInvalid          ErrorCode = "EVIDENCE_INVALID"
	CodeResearchSubTaskFenced    ErrorCode = "RESEARCH_SUBTASK_FENCED"
	CodeResearchOutcomeUnknown   ErrorCode = "RESEARCH_OUTCOME_UNKNOWN"
	CodeResearchTaskCacheInvalid ErrorCode = "RESEARCH_TASK_CACHE_INVALID"
	CodeResearchBudgetBoundary   ErrorCode = "RESEARCH_BUDGET_BOUNDARY"
)

// Sentinels the orchestrator/API layer map to run pauses and 422 responses:
//
//   - ErrInsufficientEvidence (research_pack.go): the workset cannot support
//     min_citable_sources → 422 INSUFFICIENT_EVIDENCE pause. Since F2 the
//     budget boundary routes through this sentinel too: a boundary with too
//     few citable sources pauses INSUFFICIENT_EVIDENCE (a larger budget
//     requires a new contract version + new run); a boundary with enough
//     citable sources freezes the PARTIAL pack and proceeds to the evidence
//     gate — ErrResearchBudgetBoundary itself is no longer returned by the
//     executor (the orchestrator's clean-pause branch stays for the
//     orchestrator-level BudgetBoundaryGuard).
var ErrResearchBudgetBoundary = errors.New("writingruntime: research budget boundary reached")

// ResearchTaskLedger is the store surface the read executor needs.
// *writingstore.Store implements it.
type ResearchTaskLedger interface {
	InTransaction(ctx context.Context, fn func(tx *writingstore.Tx) error) error
	GetResearchTaskByIdentity(ctx context.Context, runID, nodeID, taskKey, inputHash string) (writingstore.ResearchTask, error)
	AppendRunEvent(ctx context.Context, event writingstore.RunEvent) (writingstore.RunEvent, error)
	GetArtifactContent(ctx context.Context, contentHash string) (string, []byte, error)
}

// ResearchBudgetBoundary is the executor-side per-paper hook (T02's
// BudgetBoundaryGuard guards node dispatch; this guards the workset loop).
type ResearchBudgetBoundary interface {
	ResearchBudgetBoundaryReached(ctx context.Context, request ExecutionRequest, papersCompleted, papersTotal int) (bool, string)
}

// ResearchReadExecutor reads the selected papers into a frozen pack.
type ResearchReadExecutor struct {
	descriptor ExecutorDescriptor
	client     ResearchWorkerClient
	content    ContentGateway
	ledger     ResearchTaskLedger
	budget     ResearchBudgetBoundary
	now        func() time.Time
	leaseTTL   time.Duration
}

// NewResearchReadExecutor wires the executor.
func NewResearchReadExecutor(client ResearchWorkerClient, content ContentGateway, ledger ResearchTaskLedger, budget ResearchBudgetBoundary) (*ResearchReadExecutor, error) {
	return newResearchReadExecutor(client, content, ledger, budget, "engine.step.research_read")
}

// NewResearchReadMaterialExecutor wires the SAME executor under the material
// branch's executor id (F5): the no-external manifest binds this id, so a
// plan compiled for a no-external contract can never resolve the external
// binding.
func NewResearchReadMaterialExecutor(client ResearchWorkerClient, content ContentGateway, ledger ResearchTaskLedger, budget ResearchBudgetBoundary) (*ResearchReadExecutor, error) {
	return newResearchReadExecutor(client, content, ledger, budget, "engine.step.research_read_materials")
}

func newResearchReadExecutor(client ResearchWorkerClient, content ContentGateway, ledger ResearchTaskLedger, budget ResearchBudgetBoundary, executorID string) (*ResearchReadExecutor, error) {
	if client == nil || content == nil || ledger == nil {
		return nil, ErrRuntimeNotReady
	}
	descriptor := ExecutorDescriptor{ExecutorID: executorID, Version: "1",
		SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}, Cancellable: false}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	return &ResearchReadExecutor{descriptor: descriptor, client: client, content: content,
		ledger: ledger, budget: budget,
		now: func() time.Time { return time.Now().UTC() }, leaseTTL: researchLeaseTTL}, nil
}

func (executor *ResearchReadExecutor) Descriptor() ExecutorDescriptor { return executor.descriptor }

// researchCandidatesType is the artifact type binding the read node to its candidates input.
const researchCandidatesType = writingplan.ArtifactType("research_candidates")

// subTaskSpec identifies one ledger unit.
type subTaskSpec struct {
	taskKey   string
	phase     string
	inputHash string
}

// subTaskOutput is the content-addressed artifact a phase produced.
type subTaskOutput struct {
	artifactID   string
	contentHash  string
	inputTokens  int64
	outputTokens int64
}

// fetchMeta is the fetch sub-task's committed output: a small JSON envelope
// describing the staged raw document artifact. Caching the meta (not the raw
// bytes) keeps the ledger output self-describing: reuse verifies the meta
// hash, then loads the document by its own content hash.
type fetchMeta struct {
	DocumentHash  string `json:"document_hash"`
	SizeBytes     int64  `json:"size_bytes"`
	MediaType     string `json:"media_type"`
	LikelyScanned *bool  `json:"likely_scanned"`
	Acquisition   string `json:"acquisition_status"`
}

// Execute reads every selected paper and freezes the evidence pack.
func (executor *ResearchReadExecutor) Execute(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
	if err := request.Validate(); err != nil {
		return ExecutionResult{}, err
	}
	contract, _, err := loadContractInput(ctx, executor.content, request)
	if err != nil {
		return ExecutionResult{}, err
	}
	spec := contract.Research
	question := contract.Content.CentralQuestion
	// A02 fail-closed (defense in depth behind the plan compile's
	// CONTRACT_FORBIDS_EXTERNAL_RESEARCH check and the discover executor's
	// material-only candidates): a contract that forbids external research
	// never fetches an external document. User-material papers (origin
	// user_material, owner-authorized material_ref) stay readable through the
	// local ContentGateway bytes — zero external calls (T09 A02 + F5).
	allowExternal := contract.MaterialPolicy.AllowExternalResearch
	candidatesInput, err := researchInputByType(request, researchCandidatesType)
	if err != nil {
		return ExecutionResult{}, err
	}
	candidatesBody, err := executor.content.Load(ctx, candidatesInput)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeMaterialIntegrityFailed, RetrySafe, "load research candidates", err)
	}
	if contentHash(candidatesBody) != candidatesInput.ContentHash {
		return ExecutionResult{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "research candidates content hash mismatch", nil)
	}
	var candidates writingkernel.ResearchCandidates
	if err := json.Unmarshal(candidatesBody, &candidates); err != nil {
		return ExecutionResult{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "decode research candidates", err)
	}
	if err := candidates.Validate(); err != nil {
		return ExecutionResult{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "research candidates failed kernel validation", err)
	}

	// Workset: selected papers in candidate order, capped by the contract and
	// the node bounds (whichever is smaller).
	workset := make([]writingkernel.PaperCandidate, 0, len(candidates.Papers))
	limit := spec.MaxPapers
	if request.Node.Bounds.MaxItems > 0 && request.Node.Bounds.MaxItems < limit {
		limit = request.Node.Bounds.MaxItems
	}
	for _, paper := range candidates.Papers {
		if paper.Selection.Status != writingkernel.SelectionStatusSelected {
			continue
		}
		if len(workset) >= limit {
			break
		}
		workset = append(workset, paper)
	}

	results := make([]PaperReadResult, 0, len(workset))
	quota := EvidenceQuota{MinCitableSources: spec.MinCitableSources, EvidenceRequirement: spec.EvidenceRequirement}
	provenance := []string{"executor:writingruntime.research_read@1",
		"parser:" + ResearchReadParserVersion, "prompt:" + ResearchReadPromptVersion,
		"block_selection:" + ResearchReadBlockSelection, "policy:" + candidates.PolicyVersion,
		"reader_policy:" + spec.ReaderPolicyVersion}
	usage := ExecutionUsage{}
	started := executor.now()
	for index, paper := range workset {
		if executor.budget != nil {
			if reached, reason := executor.budget.ResearchBudgetBoundaryReached(ctx, request, index, len(workset)); reached {
				if reason == "" {
					reason = "budget boundary reached"
				}
				// F2 boundary semantics (design.md §3): the boundary stops new
				// paid sub-calls, then decides honestly on what has been read.
				// Enough citable sources → freeze the PARTIAL pack (the
				// remaining papers land in coverage.gaps as budget-truncated)
				// and let the evidence gate decide; not enough → the typed
				// INSUFFICIENT_EVIDENCE clean pause. The guard is a pure ledger
				// function, so a resume with an unchanged budget re-fires here
				// and never re-opens paid reading.
				executor.emitProgress(request, index, len(workset), index, 0, len(workset)-index, paper.PaperID)
				return executor.boundaryOutcome(ctx, request, contract, spec, candidatesInput, results,
					quota, provenance, usage, started, index, len(workset), reason)
			}
		}
		result, paperErr := executor.readPaper(ctx, request, spec, allowExternal, paper, question)
		if paperErr != nil {
			return ExecutionResult{}, paperErr
		}
		usage.InputTokens += result.InputTokens
		usage.OutputTokens += result.OutputTokens
		results = append(results, result)
		executor.emitProgress(request, index+1, len(workset), index+1, 0, len(workset)-index-1, paper.PaperID)
	}

	return executor.freezePack(ctx, request, contract, spec, candidatesInput, results, quota, provenance, usage, started)
}

// freezePack assembles, verifies, and stages the evidence pack for the given
// read results, returning the node's success ExecutionResult.
func (executor *ResearchReadExecutor) freezePack(ctx context.Context, request ExecutionRequest,
	contract writingkernel.WritingContract, spec *writingkernel.ResearchSpec,
	candidatesInput InputArtifact, results []PaperReadResult, quota EvidenceQuota,
	provenance []string, usage ExecutionUsage, started time.Time) (ExecutionResult, error) {
	pack, err := BuildEvidencePack(contract.ContractHash, candidatesRef(candidatesInput), results, quota, provenance)
	if err != nil {
		return ExecutionResult{}, runtimeError(researchCodeOf(err), RetrySafe, "assemble evidence pack", err)
	}
	body, err := json.Marshal(pack)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeExecutorOutputInvalid, RetryNever, "marshal evidence pack", err)
	}
	ref, hash, err := executor.content.Stage(ctx, request.IdempotencyKey+":research_evidence_pack", "application/json", body)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeArtifactCommitFailed, RetrySafe, "stage evidence pack", err)
	}
	if hash != contentHash(body) {
		return ExecutionResult{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "staged evidence pack hash mismatch", nil)
	}
	parents, inputHashes := lineageInputs(request)
	completedAt := executor.now()
	usage.DurationMS = completedAt.Sub(started).Milliseconds()
	return ExecutionResult{
		Artifacts: []OutputArtifactDraft{{OutputKey: "research_evidence_pack",
			ArtifactType: request.Node.OutputArtifactTypes[0],
			ContentHash:  hash, MediaType: "application/json", ContentRef: ref,
			Parents: parents, Producer: request.Node.Capability,
			CapabilityVersion: request.Node.CapabilityVersion, InputHashes: inputHashes,
			Provenance: map[string]any{"contract_hash": contract.ContractHash,
				"papers": len(pack.Papers), "evidence": len(pack.Evidence), "claims": len(pack.Claims),
				"reader_policy_version": spec.ReaderPolicyVersion},
			SourceRefs: []string{}}},
		Usage:     usage,
		StartedAt: started, CompletedAt: completedAt,
	}, nil
}

// boundaryOutcome resolves the budget boundary the guard fired between papers
// (design.md §3): the boundary stops new paid sub-calls, then
//
//   - enough citable sources among the completed results → freeze the PARTIAL
//     evidence pack (the boundary-truncated remainder is appended as honest
//     unread entries so coverage.gaps records the budget truncation) and let
//     the evidence gate decide what the partial corpus supports;
//   - otherwise → the typed INSUFFICIENT_EVIDENCE clean pause. Completed
//     sub-tasks stay in the ledger; a plain resume cannot continue reading
//     under an unchanged budget because the guard is a pure ledger judgement —
//     a larger budget requires a new contract version + new run.
func (executor *ResearchReadExecutor) boundaryOutcome(ctx context.Context, request ExecutionRequest,
	contract writingkernel.WritingContract, spec *writingkernel.ResearchSpec, candidatesInput InputArtifact,
	results []PaperReadResult, quota EvidenceQuota, provenance []string,
	usage ExecutionUsage, started time.Time, completedCount, totalCount int, reason string) (ExecutionResult, error) {
	citable := 0
	for _, result := range results {
		if result.Citable(quota.EvidenceRequirement) {
			citable++
		}
	}
	if len(results) == 0 || citable < quota.MinCitableSources {
		return ExecutionResult{}, runtimeError(CodeInsufficientEvidence, RetryAfterHuman,
			fmt.Sprintf("%s; %d citable sources below the contract floor of %d — the run pauses instead of continuing paid reading (a larger budget requires a new contract + run)",
				reason, citable, quota.MinCitableSources),
			fmt.Errorf("%w: budget boundary at %d/%d papers", ErrInsufficientEvidence, completedCount, totalCount))
	}
	// Partial pack: append the boundary-truncated remainder as unread entries
	// with an explicit budget reason — they surface in coverage.gaps and the
	// pack's honest per-paper coverage. The truncated set is the selected
	// workset the pack's candidates input declared, beyond what was read.
	partial := append([]PaperReadResult(nil), results...)
	for _, paper := range executor.truncatedRemainder(ctx, request, candidatesInput, completedCount) {
		partial = append(partial, PaperReadResult{
			PaperID:         paper.PaperID,
			Bibliography:    writingkernel.PaperBibliography{Title: paper.Title, Authors: paper.Authors, Year: paper.Year, Venue: paper.Venue, DOI: paper.DOI, CanonicalURL: paper.CanonicalURL},
			Origin:          candidateOrigin(paper),
			MaterialRef:     candidateMaterialRef(paper),
			SelectionReason: paper.Selection.Reason,
			RelevanceStatus: paper.RelevanceStatus,
			ReadingScope:    writingkernel.ReadingScopeUnread,
			BlocksByID:      map[string]ParsedBlock{},
			UnreadReason:    "research budget boundary reached before reading (partial pack)",
		})
	}
	budgetProvenance := append(append([]string(nil), provenance...),
		fmt.Sprintf("budget_boundary:partial_pack:%d/%d papers read", completedCount, totalCount))
	return executor.freezePack(ctx, request, contract, spec, candidatesInput, partial, quota, budgetProvenance, usage, started)
}

// truncatedRemainder re-derives the selected workset from the frozen
// candidates artifact and returns the papers beyond completedCount that the
// boundary stopped before reading. A candidates load/decode failure yields
// nothing: the pack then simply omits the unread remainder rather than
// fabricating identities it cannot verify.
func (executor *ResearchReadExecutor) truncatedRemainder(ctx context.Context, request ExecutionRequest, candidatesInput InputArtifact, completedCount int) []writingkernel.PaperCandidate {
	body, err := executor.content.Load(ctx, candidatesInput)
	if err != nil || contentHash(body) != candidatesInput.ContentHash {
		return nil
	}
	var candidates writingkernel.ResearchCandidates
	if err := json.Unmarshal(body, &candidates); err != nil {
		return nil
	}
	remainder := []writingkernel.PaperCandidate{}
	position := 0
	for _, paper := range candidates.Papers {
		if paper.Selection.Status != writingkernel.SelectionStatusSelected {
			continue
		}
		if position >= completedCount {
			remainder = append(remainder, paper)
		}
		position++
	}
	return remainder
}

// readPaper drives one paper through fetch → parse → read with per-phase
// ledger persistence. A degraded paper (no OA URL, likely scanned, unreadable
// parse) returns a terminal unread/abstract result instead of failing the
// node; only ledger integrity problems fail the whole node.
//
// User-material papers (origin user_material) skip the external fetch
// entirely: their owner-authorized bytes load from the ContentGateway by
// material_ref and go straight to parse → read (F5: 不经 fetch_full_text,
// zero external downloads).
func (executor *ResearchReadExecutor) readPaper(ctx context.Context, request ExecutionRequest, spec *writingkernel.ResearchSpec, allowExternal bool, paper writingkernel.PaperCandidate, question string) (PaperReadResult, error) {
	result := PaperReadResult{
		PaperID: paper.PaperID,
		Bibliography: writingkernel.PaperBibliography{Title: paper.Title, Authors: paper.Authors,
			Year: paper.Year, Venue: paper.Venue, DOI: paper.DOI, CanonicalURL: paper.CanonicalURL},
		Origin:          candidateOrigin(paper),
		MaterialRef:     candidateMaterialRef(paper),
		SelectionReason: paper.Selection.Reason,
		RelevanceStatus: paper.RelevanceStatus,
		BlocksByID:      map[string]ParsedBlock{},
	}
	abstractText := ""
	if paper.Abstract != nil {
		abstractText = strings.TrimSpace(*paper.Abstract)
	}
	abstractAllowed := spec.EvidenceRequirement == writingkernel.EvidenceRequirementAbstractAllowed
	degradeToAbstract := func(reason string) bool {
		return abstractAllowed && abstractText != ""
	}

	// ── Acquisition phase: user material bytes, or the constrained external
	// fetch (only when an OA location is known).
	haveFullText := false
	scanned := false
	var documentHash, documentMediaType string
	switch {
	case paper.Origin == writingkernel.PaperOriginUserMaterial:
		if paper.MaterialRef == nil || paper.MaterialRef.ContentHash == "" {
			result.UnreadReason = "user material lacks its owner-authorized material_ref"
			break
		}
		_, body, err := executor.ledger.GetArtifactContent(ctx, paper.MaterialRef.ContentHash)
		if err != nil || contentHash(body) != paper.MaterialRef.ContentHash {
			return result, runtimeError(CodeMaterialIntegrityFailed, RetryNever,
				"user material content failed hash verification", err)
		}
		documentHash = paper.MaterialRef.ContentHash
		documentMediaType = "text/plain"
		haveFullText = true
	default:
		if !allowExternal {
			// Defense in depth (A02): a no-external contract must never fetch;
			// only its abstract may back the paper (degraded, local bytes).
			result.UnreadReason = "external fetch forbidden by contract material policy"
			break
		}
		if oaURL := strings.TrimSpace(paper.Acquisition.OAURL); oaURL != "" {
			fetchHash, hashErr := scholar.HashPayload(map[string]any{
				"phase": "fetch", "paper_id": paper.PaperID, "doi": deref(paper.DOI),
				"oa_url": oaURL, "size_limit": researchFetchSizeLimit,
				"selection_policy_version": ResearchDiscoverSelectionPolicy,
			})
			if hashErr != nil {
				return result, runtimeError(CodeExecutorOutputInvalid, RetryNever, "hash fetch input", hashErr)
			}
			output, err := executor.runSubTask(ctx, request, subTaskSpec{taskKey: paper.PaperID, phase: "fetch", inputHash: fetchHash},
				func(callCtx context.Context) (subTaskOutput, error) {
					return executor.fetchDocument(callCtx, request, paper.PaperID, oaURL)
				})
			if err != nil {
				if fatal := nodeFatalSubTaskError(err); fatal != nil {
					return result, fatal
				}
				result.UnreadReason = "acquisition failed: " + err.Error()
			} else {
				meta, metaErr := executor.decodeFetchMeta(ctx, output)
				if metaErr != nil {
					return result, metaErr
				}
				documentHash = meta.DocumentHash
				documentMediaType = meta.MediaType
				if meta.LikelyScanned != nil {
					scanned = *meta.LikelyScanned
				}
				haveFullText = true
				result.InputTokens += output.inputTokens
				result.OutputTokens += output.outputTokens
			}
		} else {
			result.UnreadReason = "no open-access URL"
		}
	}

	// ── Document source decision: full text, or the abstract as its own
	// content artifact — an abstract quote is never backed by the PDF hash
	// (contracts.md §2).
	scope := "full_text"
	var documentBytes []byte
	var documentRef writingkernel.ArtifactRef
	if !haveFullText || scanned {
		if !degradeToAbstract(result.UnreadReason) {
			result.ReadingScope = writingkernel.ReadingScopeUnread
			if result.UnreadReason == "" {
				result.UnreadReason = "no readable full text and no abstract fallback"
			}
			return result, nil
		}
		if scanned {
			result.UnreadReason = "likely scanned; degraded to abstract"
		} else if result.UnreadReason == "" {
			result.UnreadReason = "degraded to abstract"
		}
		scope = "abstract"
		documentMediaType = "text/plain"
		documentBytes = []byte(abstractText)
		abstractHash := contentHash(documentBytes)
		if _, _, err := executor.content.Stage(ctx, request.IdempotencyKey+":"+paper.PaperID+":abstract", "text/plain", documentBytes); err != nil {
			return result, runtimeError(CodeArtifactCommitFailed, RetrySafe, "stage abstract document", err)
		}
		documentHash = abstractHash
		documentRef = writingkernel.ArtifactRef{ArtifactID: writingstore.StableID("art_", request.RunID, request.NodeID, paper.PaperID, "document"),
			Version: 1, ContentHash: abstractHash}
	} else {
		_, body, loadErr := executor.ledger.GetArtifactContent(ctx, documentHash)
		if loadErr != nil || contentHash(body) != documentHash {
			return result, runtimeError(CodeResearchTaskCacheInvalid, RetryNever, "fetched document content failed hash verification", loadErr)
		}
		documentBytes = body
		documentRef = writingkernel.ArtifactRef{ArtifactID: writingstore.StableID("art_", request.RunID, request.NodeID, paper.PaperID, "document"),
			Version: 1, ContentHash: documentHash}
	}

	// ── Parse phase.
	parsed, parsedOutput, err := executor.runParse(ctx, request, paper.PaperID, documentHash, documentBytes, documentMediaType, scope)
	if err != nil {
		if fatal := nodeFatalSubTaskError(err); fatal != nil {
			return result, fatal
		}
		result.ReadingScope = writingkernel.ReadingScopeUnread
		if result.UnreadReason == "" {
			result.UnreadReason = "parse failed: " + err.Error()
		}
		return result, nil
	}
	result.InputTokens += parsedOutput.inputTokens
	result.OutputTokens += parsedOutput.outputTokens

	// The parse proved the full text unreadable: degrade once to abstract.
	if scope == "full_text" && ((parsed.Coverage.LikelyScanned != nil && *parsed.Coverage.LikelyScanned) || len(parsed.Blocks) == 0) {
		if !degradeToAbstract(result.UnreadReason) {
			result.ReadingScope = writingkernel.ReadingScopeUnread
			if result.UnreadReason == "" {
				result.UnreadReason = "no text blocks parsed"
			}
			return result, nil
		}
		documentBytes = []byte(abstractText)
		abstractHash := contentHash(documentBytes)
		if _, _, err := executor.content.Stage(ctx, request.IdempotencyKey+":"+paper.PaperID+":abstract", "text/plain", documentBytes); err != nil {
			return result, runtimeError(CodeArtifactCommitFailed, RetrySafe, "stage abstract document", err)
		}
		documentHash = abstractHash
		documentRef = writingkernel.ArtifactRef{ArtifactID: writingstore.StableID("art_", request.RunID, request.NodeID, paper.PaperID, "document"),
			Version: 1, ContentHash: abstractHash}
		parsed, parsedOutput, err = executor.runParse(ctx, request, paper.PaperID, documentHash, documentBytes, "text/plain", "abstract")
		if err != nil {
			if fatal := nodeFatalSubTaskError(err); fatal != nil {
				return result, fatal
			}
			result.ReadingScope = writingkernel.ReadingScopeUnread
			result.UnreadReason = "abstract parse failed"
			return result, nil
		}
		result.InputTokens += parsedOutput.inputTokens
		result.OutputTokens += parsedOutput.outputTokens
		scope = "abstract"
		if result.UnreadReason == "" {
			result.UnreadReason = "degraded to abstract: full text unreadable"
		}
	}
	if len(parsed.Blocks) == 0 {
		result.ReadingScope = writingkernel.ReadingScopeUnread
		if result.UnreadReason == "" {
			result.UnreadReason = "abstract produced no text blocks"
		}
		parsedRef := artifactRef(parsedOutput)
		result.DocumentRef = &documentRef
		result.ParsedDocumentRef = &parsedRef
		result.TotalBlocks = parsed.Coverage.TotalBlocks
		result.Truncated = parsed.Coverage.Truncated
		return result, nil
	}

	// ── Read phase: ≤24 blocks (first_n), reader policy pinned by version.
	parsedRef := artifactRef(parsedOutput)
	readBlocks := parsed.Blocks
	if len(readBlocks) > ResearchReadMaxBlocks {
		readBlocks = readBlocks[:ResearchReadMaxBlocks]
	}
	readerBlocks := make([]ReaderBlock, 0, len(readBlocks))
	for _, block := range readBlocks {
		readerBlocks = append(readerBlocks, ReaderBlock{BlockID: block.BlockID, Text: block.Text,
			BlockHash: block.BlockHash, Page: block.Page})
	}
	readHash, hashErr := scholar.HashPayload(map[string]any{
		"phase": "read", "research_question": question, "paper_id": paper.PaperID,
		"reader_policy_version": spec.ReaderPolicyVersion, "prompt_version": ResearchReadPromptVersion,
		"parser_version": ResearchReadParserVersion, "content_hash": documentHash,
		"parsed_hash": parsedOutput.contentHash, "block_selection": ResearchReadBlockSelection,
		"n_blocks": len(readerBlocks),
	})
	if hashErr != nil {
		return result, runtimeError(CodeExecutorOutputInvalid, RetryNever, "hash read input", hashErr)
	}
	readOutput, err := executor.runSubTask(ctx, request, subTaskSpec{taskKey: paper.PaperID, phase: "read", inputHash: readHash},
		func(callCtx context.Context) (subTaskOutput, error) {
			return executor.readBlocks(callCtx, request, paper.PaperID, readerBlocks, question)
		})
	if err != nil {
		if fatal := nodeFatalSubTaskError(err); fatal != nil {
			return result, fatal
		}
		// The read output failed host-side verification (e.g. tampered
		// evidence) or the worker refused: the paper keeps its parsed
		// coverage but contributes no citable evidence.
		result.ReadingScope = writingkernel.ReadingScopeAbstract
		if scope == "full_text" {
			result.ReadingScope = writingkernel.ReadingScopeFullText
		}
		if result.UnreadReason == "" {
			result.UnreadReason = "read rejected: " + err.Error()
		}
		result.DocumentRef = &documentRef
		result.ParsedDocumentRef = &parsedRef
		result.BlocksByID = blocksByID(parsed.Blocks)
		result.Blocks = parsed.Blocks
		result.ReadBlockIDs = []string{}
		result.TotalBlocks = parsed.Coverage.TotalBlocks
		result.Truncated = parsed.Coverage.Truncated
		result.InputTokens += readOutput.inputTokens
		result.OutputTokens += readOutput.outputTokens
		return result, nil
	}
	result.InputTokens += readOutput.inputTokens
	result.OutputTokens += readOutput.outputTokens
	readBody, err := executor.loadSubTaskContent(ctx, readOutput)
	if err != nil {
		return result, err
	}
	var outputs ReadOutputs
	if err := json.Unmarshal(readBody, &outputs); err != nil {
		return result, runtimeError(CodeResearchTaskCacheInvalid, RetryNever, "decode read outputs", err)
	}

	claims, evidence, evidenceErr := mapReaderOutputs(paper.PaperID, outputs, blocksByID(parsed.Blocks), scope == "abstract")
	if evidenceErr != nil {
		// Tampered or inconsistent worker output: fail closed for the whole
		// node (EVIDENCE_INVALID) — the run pauses with candidates preserved.
		return result, runtimeError(CodeEvidenceInvalid, RetryNever, evidenceErr.Error(), evidenceErr)
	}
	result.ReadingScope = writingkernel.ReadingScopeAbstract
	if scope == "full_text" {
		result.ReadingScope = writingkernel.ReadingScopeFullText
	}
	result.DocumentRef = &documentRef
	result.ParsedDocumentRef = &parsedRef
	result.Blocks = parsed.Blocks
	result.BlocksByID = blocksByID(parsed.Blocks)
	result.ReadBlockIDs = outputs.BlocksRead
	result.TotalBlocks = parsed.Coverage.TotalBlocks
	result.Truncated = parsed.Coverage.Truncated
	result.Claims = claims
	result.Evidence = evidence
	return result, nil
}

// ── Phase implementations ──────────────────────────────────────────────────

// fetchDocument downloads through the worker's constrained downloader, stages
// the raw document content-addressed, and commits the fetch meta envelope as
// the sub-task output.
func (executor *ResearchReadExecutor) fetchDocument(ctx context.Context, request ExecutionRequest, paperID, oaURL string) (subTaskOutput, error) {
	callCtx, cancel := context.WithTimeout(ctx, researchCallTimeout)
	outputs, response, err := executor.client.FetchFullText(callCtx, paperID, oaURL, researchFetchSizeLimit)
	cancel()
	if err != nil {
		return subTaskOutput{}, err
	}
	content := outputs.Content()
	if content == nil {
		return subTaskOutput{}, fmt.Errorf("fetch output content is not valid base64")
	}
	documentHash := contentHash(content)
	if documentHash != outputs.ContentHash {
		return subTaskOutput{}, fmt.Errorf("fetched content hash %s does not match worker-reported %s", documentHash, outputs.ContentHash)
	}
	if outputs.SizeBytes > 0 && int64(len(content)) != outputs.SizeBytes {
		return subTaskOutput{}, fmt.Errorf("fetched size %d does not match reported %d", len(content), outputs.SizeBytes)
	}
	mediaType := outputs.MediaType
	if mediaType == "" {
		mediaType = "application/pdf"
	}
	if _, _, err := executor.content.Stage(ctx, request.IdempotencyKey+":"+paperID+":document", mediaType, content); err != nil {
		return subTaskOutput{}, err
	}
	meta := fetchMeta{DocumentHash: documentHash, SizeBytes: outputs.SizeBytes, MediaType: mediaType,
		LikelyScanned: outputs.LikelyScanned, Acquisition: outputs.AcquisitionStatus}
	metaBody, err := json.Marshal(meta)
	if err != nil {
		return subTaskOutput{}, err
	}
	_, metaHash, err := executor.content.Stage(ctx, request.IdempotencyKey+":"+paperID+":fetch_meta", "application/json", metaBody)
	if err != nil {
		return subTaskOutput{}, err
	}
	if metaHash != contentHash(metaBody) {
		return subTaskOutput{}, fmt.Errorf("staged fetch meta hash mismatch")
	}
	return subTaskOutput{artifactID: writingstore.StableID("art_", request.RunID, request.NodeID, paperID, "fetch_meta"),
		contentHash: metaHash, inputTokens: response.Usage.InputTokens, outputTokens: response.Usage.OutputTokens}, nil
}

func (executor *ResearchReadExecutor) decodeFetchMeta(ctx context.Context, output subTaskOutput) (fetchMeta, error) {
	body, err := executor.loadSubTaskContent(ctx, output)
	if err != nil {
		return fetchMeta{}, err
	}
	var meta fetchMeta
	if err := json.Unmarshal(body, &meta); err != nil {
		return fetchMeta{}, runtimeError(CodeResearchTaskCacheInvalid, RetryNever, "decode fetch meta", err)
	}
	if meta.DocumentHash == "" {
		return fetchMeta{}, runtimeError(CodeResearchTaskCacheInvalid, RetryNever, "fetch meta lacks its document hash", nil)
	}
	return meta, nil
}

// runParse parses one document through the worker with a ledger-backed task.
func (executor *ResearchReadExecutor) runParse(ctx context.Context, request ExecutionRequest, paperID, documentHash string, documentBytes []byte, mediaType, scope string) (*ParseOutputs, subTaskOutput, error) {
	parseHash, err := scholar.HashPayload(map[string]any{
		"phase": "parse", "parser_version": ResearchReadParserVersion,
		"media_type": mediaType, "content_hash": documentHash, "scope": scope,
	})
	if err != nil {
		return nil, subTaskOutput{}, runtimeError(CodeExecutorOutputInvalid, RetryNever, "hash parse input", err)
	}
	output, err := executor.runSubTask(ctx, request, subTaskSpec{taskKey: paperID, phase: "parse", inputHash: parseHash},
		func(callCtx context.Context) (subTaskOutput, error) {
			return executor.parseDocument(callCtx, request, paperID, documentBytes, mediaType)
		})
	if err != nil {
		return nil, output, err
	}
	body, err := executor.loadSubTaskContent(ctx, output)
	if err != nil {
		return nil, output, err
	}
	var parsed ParseOutputs
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, output, runtimeError(CodeResearchTaskCacheInvalid, RetryNever, "decode parsed blocks", err)
	}
	return &parsed, output, nil
}

func (executor *ResearchReadExecutor) parseDocument(ctx context.Context, request ExecutionRequest, paperID string, document []byte, mediaType string) (subTaskOutput, error) {
	callCtx, cancel := context.WithTimeout(ctx, researchCallTimeout)
	outputs, response, err := executor.client.ParseDocument(callCtx, document, mediaType, ResearchReadParserVersion)
	cancel()
	if err != nil {
		return subTaskOutput{}, err
	}
	body, err := json.Marshal(outputs)
	if err != nil {
		return subTaskOutput{}, err
	}
	_, hash, err := executor.content.Stage(ctx, request.IdempotencyKey+":"+paperID+":parsed", "application/json", body)
	if err != nil {
		return subTaskOutput{}, err
	}
	if hash != contentHash(body) {
		return subTaskOutput{}, fmt.Errorf("staged parsed blocks hash mismatch")
	}
	return subTaskOutput{artifactID: writingstore.StableID("art_", request.RunID, request.NodeID, paperID, "parsed"),
		contentHash: hash, inputTokens: response.Usage.InputTokens, outputTokens: response.Usage.OutputTokens}, nil
}

func (executor *ResearchReadExecutor) readBlocks(ctx context.Context, request ExecutionRequest, paperID string, blocks []ReaderBlock, question string) (subTaskOutput, error) {
	callCtx, cancel := context.WithTimeout(ctx, researchCallTimeout)
	// The worker enforces a non-empty reader_policy_version (operations.py
	// _require_str); an empty value fails the real worker even though fake
	// test doubles accept it.
	outputs, response, err := executor.client.ReadPaper(callCtx, question, paperID, blocks, ReaderPolicy{ReaderPolicyVersion: ResearchReaderPolicyVersion})
	cancel()
	if err != nil {
		return subTaskOutput{}, err
	}
	// reader 的自检 warnings（含 evidence 偏移重锚定记录）落在 response 上，
	// 适配层此前直接丢弃——重锚定等关键自愈行为必须可观测。
	for _, w := range response.Warnings {
		slog.Warn("research read worker warning", "paper_id", paperID, "warning", w)
	}
	body, err := json.Marshal(outputs)
	if err != nil {
		return subTaskOutput{}, err
	}
	_, hash, err := executor.content.Stage(ctx, request.IdempotencyKey+":"+paperID+":read", "application/json", body)
	if err != nil {
		return subTaskOutput{}, err
	}
	if hash != contentHash(body) {
		return subTaskOutput{}, fmt.Errorf("staged read outputs hash mismatch")
	}
	return subTaskOutput{artifactID: writingstore.StableID("art_", request.RunID, request.NodeID, paperID, "read"),
		contentHash: hash, inputTokens: response.Usage.InputTokens, outputTokens: response.Usage.OutputTokens}, nil
}

// runSubTask is the ledger driver shared by all phases:
// ensure → reuse on hash-verified success → claim → work → complete /
// classify → fail. Work errors propagate raw (paper-level handling decides);
// ledger integrity errors are typed and node-fatal.
func (executor *ResearchReadExecutor) runSubTask(ctx context.Context, request ExecutionRequest, spec subTaskSpec, work func(context.Context) (subTaskOutput, error)) (subTaskOutput, error) {
	worker := "exec:" + request.IdempotencyKey
	now := executor.now()
	var ensured writingstore.ResearchTask
	ensureErr := executor.ledger.InTransaction(ctx, func(tx *writingstore.Tx) error {
		task, err := tx.EnsureResearchTask(ctx, writingstore.CreateResearchTask{OwnerUserID: request.UserID,
			RunID: request.RunID, NodeID: request.NodeID, TaskKey: spec.taskKey, Phase: spec.phase,
			InputHash: spec.inputHash}, now)
		if err != nil {
			return err
		}
		ensured = task
		return nil
	})
	if ensureErr != nil {
		return subTaskOutput{}, runtimeError(CodeArtifactCommitFailed, RetrySafe, "ensure research sub-task", ensureErr)
	}
	if ensured.Status == writingstore.ResearchTaskSucceeded {
		// Cache reuse: only a hash-verified output may stand in for the
		// upstream call (design.md §6); anything else is corruption.
		if ensured.OutputHash == "" {
			return subTaskOutput{}, runtimeError(CodeResearchTaskCacheInvalid, RetryNever,
				"succeeded research sub-task lacks its output hash", nil)
		}
		if _, body, err := executor.ledger.GetArtifactContent(ctx, ensured.OutputHash); err != nil || contentHash(body) != ensured.OutputHash {
			return subTaskOutput{}, runtimeError(CodeResearchTaskCacheInvalid, RetryNever,
				"succeeded research sub-task output failed hash verification", err)
		}
		return subTaskOutput{artifactID: ensured.OutputArtifactID, contentHash: ensured.OutputHash}, nil
	}
	if ensured.Status == writingstore.ResearchTaskOutcomeUnknwn {
		return subTaskOutput{}, runtimeError(CodeResearchOutcomeUnknown, RetryAfterHuman,
			"research sub-task outcome unknown; owner decides whether to re-drive (may re-bill)", nil)
	}
	if ensured.Status == writingstore.ResearchTaskCancelled {
		return subTaskOutput{}, runtimeError(CodeResearchSubTaskFenced, RetryNever, "research sub-task was cancelled", nil)
	}

	var claimed writingstore.ResearchTask
	claimErr := executor.ledger.InTransaction(ctx, func(tx *writingstore.Tx) error {
		task, ok, err := tx.ClaimResearchTaskByIdentity(ctx, request.RunID, request.NodeID, spec.taskKey, spec.inputHash, worker, executor.leaseTTL, executor.now())
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("research sub-task %s/%s is not claimable (live lease or retry_after pending)", spec.taskKey, spec.phase)
		}
		claimed = task
		return nil
	})
	if claimErr != nil {
		// A busy row is paper-level (skip this paper); anything else is a
		// store failure surfaced through the typed fence code.
		if strings.Contains(claimErr.Error(), "not claimable") {
			return subTaskOutput{}, claimErr
		}
		return subTaskOutput{}, runtimeError(CodeResearchSubTaskFenced, RetrySafe, "claim research sub-task", claimErr)
	}

	// F2 budget accounting: the claim marks the sub-task's active window; the
	// completion persists the phase's wall time into usage_json so the budget
	// guard counts every paid call even when the owning attempt row is
	// replayed across resumes.
	claimTime := executor.now()
	output, workErr := work(ctx)
	finishTime := executor.now()
	usage := usageMapOf(output)
	usage["duration_ms"] = finishTime.Sub(claimTime).Milliseconds()
	if workErr != nil {
		failure := classifyWorkerFailure(workErr)
		if failErr := executor.ledger.InTransaction(ctx, func(tx *writingstore.Tx) error {
			return tx.FailResearchTask(ctx, claimed.ID, worker, failure, executor.now())
		}); failErr != nil {
			return subTaskOutput{}, runtimeError(CodeResearchSubTaskFenced, RetrySafe, "fail research sub-task", failErr)
		}
		if failure.OutcomeUnknown {
			// The worker may have accepted the call: park the node instead of
			// letting the paper degrade silently (retrying may re-bill).
			return subTaskOutput{}, runtimeError(CodeResearchOutcomeUnknown, RetryAfterHuman,
				"research sub-task outcome unknown; owner decides whether to re-drive", workErr)
		}
		return subTaskOutput{}, workErr
	}
	if completionErr := executor.ledger.InTransaction(ctx, func(tx *writingstore.Tx) error {
		return tx.CompleteResearchTask(ctx, claimed.ID, worker, writingstore.ResearchTaskCompletion{
			OutputArtifactID: output.artifactID, OutputHash: output.contentHash, Usage: usage}, executor.now())
	}); completionErr != nil {
		return subTaskOutput{}, runtimeError(CodeResearchSubTaskFenced, RetrySafe, "complete research sub-task", completionErr)
	}
	return output, nil
}

// loadSubTaskContent resolves a phase output to its bytes by content hash.
func (executor *ResearchReadExecutor) loadSubTaskContent(ctx context.Context, output subTaskOutput) ([]byte, error) {
	if output.contentHash == "" {
		return nil, runtimeError(CodeResearchTaskCacheInvalid, RetryNever, "sub-task output lacks its content hash", nil)
	}
	_, body, err := executor.ledger.GetArtifactContent(ctx, output.contentHash)
	if err != nil {
		return nil, runtimeError(CodeMaterialIntegrityFailed, RetrySafe, "load sub-task content", err)
	}
	if contentHash(body) != output.contentHash {
		return nil, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "sub-task content hash mismatch", nil)
	}
	return body, nil
}

// nodeFatalSubTaskError reports whether a sub-task error must fail the whole
// node (pause with ledger intact) instead of degrading one paper.
func nodeFatalSubTaskError(err error) error {
	var typed *RuntimeError
	if errors.As(err, &typed) {
		switch typed.Code {
		case CodeResearchOutcomeUnknown, CodeResearchTaskCacheInvalid, CodeResearchSubTaskFenced:
			return err
		}
	}
	return nil
}

// classifyWorkerFailure maps a worker/client error onto ledger failure
// semantics (design.md §6/§7): unknown outcomes park the row inspectably;
// retryable remote errors carry the worker's Retry-After.
func classifyWorkerFailure(err error) writingstore.ResearchTaskFailure {
	failure := writingstore.ResearchTaskFailure{ErrorCode: "research_worker_error"}
	var scholarErr *scholar.Error
	if errors.As(err, &scholarErr) {
		if scholarErr.Code != "" {
			failure.ErrorCode = scholarErr.Code
		}
		if scholarErr.OutcomeUnknown() {
			failure.OutcomeUnknown = true
			return failure
		}
		if scholarErr.Retryable && scholarErr.RetryAfter > 0 {
			failure.RetryAfter = time.Now().UTC().Add(scholarErr.RetryAfter)
		}
	}
	return failure
}

func usageMapOf(output subTaskOutput) map[string]any {
	return map[string]any{"input_tokens": output.inputTokens, "output_tokens": output.outputTokens}
}

// mapReaderOutputs converts worker outputs into kernel claims/evidence and
// re-verifies every evidence entry against the parsed blocks (defense in
// depth behind the worker's self-validation). A single invalid entry fails
// the paper's read — the host never silently curates worker output.
func mapReaderOutputs(paperID string, outputs ReadOutputs, blocks map[string]ParsedBlock, abstractScope bool) ([]writingkernel.Claim, []writingkernel.Evidence, error) {
	scope := writingkernel.EvidenceScopeFullText
	if abstractScope {
		scope = writingkernel.EvidenceScopeAbstract
	}
	evidenceIDMap := make(map[string]string, len(outputs.Evidence))
	evidence := make([]writingkernel.Evidence, 0, len(outputs.Evidence))
	for _, entry := range outputs.Evidence {
		block, ok := blocks[entry.BlockID]
		if !ok {
			return nil, nil, fmt.Errorf("evidence %q names unknown block %q", entry.EvidenceID, entry.BlockID)
		}
		if entry.BlockHash != block.BlockHash {
			return nil, nil, fmt.Errorf("evidence %q block hash mismatch", entry.EvidenceID)
		}
		kernelEvidence := writingkernel.Evidence{
			EvidenceID: entry.EvidenceID, PaperID: paperID,
			BlockID: entry.BlockID, BlockHash: entry.BlockHash, Quote: entry.Quote,
			StartChar: entry.StartChar, EndChar: entry.EndChar, Page: entry.Page,
			EvidenceScope: scope,
		}
		if !writingkernel.VerifyEvidenceQuote(kernelEvidence, block.Text) {
			return nil, nil, fmt.Errorf("evidence %q quote does not match block offsets", entry.EvidenceID)
		}
		kernelID := "ev_" + writingstore.StableID("research", paperID, entry.EvidenceID)
		evidenceIDMap[entry.EvidenceID] = kernelID
		kernelEvidence.EvidenceID = kernelID
		evidence = append(evidence, kernelEvidence)
	}
	claims := make([]writingkernel.Claim, 0, len(outputs.Claims))
	for _, claim := range outputs.Claims {
		kind := writingkernel.ClaimKind(claim.Kind)
		if !kind.Valid() {
			return nil, nil, fmt.Errorf("claim %q has invalid kind %q", claim.ClaimID, claim.Kind)
		}
		bound := make([]string, 0, len(claim.EvidenceIDs))
		for _, evidenceID := range claim.EvidenceIDs {
			kernelID, ok := evidenceIDMap[evidenceID]
			if !ok {
				return nil, nil, fmt.Errorf("claim %q references unverified evidence %q", claim.ClaimID, evidenceID)
			}
			bound = append(bound, kernelID)
		}
		if kind == writingkernel.ClaimKindSourceAssertion && len(bound) == 0 {
			return nil, nil, fmt.Errorf("claim %q is a source_assertion without evidence", claim.ClaimID)
		}
		claims = append(claims, writingkernel.Claim{ClaimID: "clm_" + writingstore.StableID("research", paperID, claim.ClaimID),
			PaperID: paperID, Text: claim.Text, Kind: kind, EvidenceIDs: bound,
			ReviewStatus: writingkernel.ClaimReviewPending, Limitations: orEmptySlice(claim.Limitations)})
	}
	return claims, evidence, nil
}

// emitProgress publishes one research.progress event through the T02 event
// pipeline (run-scoped ledger event; the store enforces sequence monotonicity).
func (executor *ResearchReadExecutor) emitProgress(request ExecutionRequest, completed, total, succeeded, failed, deferred int, paperID string) {
	payload := map[string]any{"phase": "read", "node_id": request.NodeID,
		"completed": completed, "total": total, "failed": failed, "deferred": deferred,
		"last_paper_id": paperID}
	_, _ = executor.ledger.AppendRunEvent(context.Background(), writingstore.RunEvent{
		RunID: request.RunID, EventType: "research.progress", EntityKind: "run",
		EntityID: request.RunID, Payload: payload, Trace: researchTrace()})
}

func researchTrace() writingstore.TraceContext {
	return writingstore.TraceContext{Provenance: map[string]any{"runtime": "governed", "flow": "research_review"},
		SourceRefs: []string{}, Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime.research"}}
}

func researchCodeOf(err error) ErrorCode {
	switch {
	case errors.Is(err, ErrInsufficientEvidence):
		return CodeInsufficientEvidence
	case errors.Is(err, ErrResearchBudgetBoundary):
		return CodeResearchBudgetBoundary
	case errors.Is(err, ErrEvidenceInvalid):
		return CodeEvidenceInvalid
	default:
		return CodeExecutionFailed
	}
}

func candidatesRef(input InputArtifact) writingkernel.ArtifactRef {
	return writingkernel.ArtifactRef{ArtifactID: input.ArtifactID, Version: input.Version, ContentHash: input.ContentHash}
}

func artifactRef(output subTaskOutput) writingkernel.ArtifactRef {
	return writingkernel.ArtifactRef{ArtifactID: output.artifactID, Version: 1, ContentHash: output.contentHash}
}

func blocksByID(blocks []ParsedBlock) map[string]ParsedBlock {
	byID := make(map[string]ParsedBlock, len(blocks))
	for _, block := range blocks {
		byID[block.BlockID] = block
	}
	return byID
}

func researchInputByType(request ExecutionRequest, artifactType writingplan.ArtifactType) (InputArtifact, error) {
	for _, input := range request.Inputs {
		if input.ArtifactType == artifactType {
			return input, nil
		}
	}
	return InputArtifact{}, runtimeError(CodeExecutorContractMismatch, RetryNever,
		fmt.Sprintf("input artifact type %q is missing", artifactType), ErrInvalidExecutionRequest)
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// candidateOrigin reports the pack origin of a candidate (user material vs
// external discovery).
func candidateOrigin(paper writingkernel.PaperCandidate) writingkernel.PaperOrigin {
	if paper.Origin == writingkernel.PaperOriginUserMaterial {
		return writingkernel.PaperOriginUserMaterial
	}
	return writingkernel.PaperOriginExternal
}

// candidateMaterialRef projects a user-material candidate's material reference
// onto the pack paper entry (nil for external papers).
func candidateMaterialRef(paper writingkernel.PaperCandidate) *writingkernel.ArtifactRef {
	return paper.MaterialRef
}
