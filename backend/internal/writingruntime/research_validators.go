package writingruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// Research validators (T06 structural skeleton, T07 full projection).
// Both run as direct runtime executors and are report producers, never
// gates — the quality node (mechanical half: ResearchQualityGateRunner) is
// the acceptance authority. The citation validator performs the full
// deterministic T07 check: marker ↔ index ↔ pack consistency, draft-hash
// binding, wrong-pack/hash-tamper detection, scope-overclaim projection,
// heuristic unsupported-assertion scan, and the bibliography projection.
// The fact validator runs the lightweight semantic review when a reviewer is
// wired and degrades honestly otherwise; conclusions stay pending and are
// never verification passes.

// ResearchValidationDetails is the research_validation_details artifact
// content (contracts.md §2): the research-specific finding lists the quality
// gate and the workbench consume. The five contract fields keep their v1
// semantics; Findings/Bibliography/ModelRef are additive T07 projections.
type ResearchValidationDetails struct {
	SchemaVersion     string   `json:"schema_version"`
	InvalidCitations  []string `json:"invalid_citations"`
	UnsupportedClaims []string `json:"unsupported_claims"`
	ScopeOverclaims   []string `json:"scope_overclaims"`
	UnreviewedClaims  []string `json:"unreviewed_claims"`
	OmittedEvidence   []string `json:"omitted_evidence"`
	// Findings carries the typed, user-checkable entries (type/severity/
	// excerpt/source); severity "blocker" blocks formal delivery at the
	// quality gate, everything else waits for human review.
	Findings []detailFinding `json:"findings"`
	// Bibliography is the display projection: papers numbered by first
	// citation order; EvidenceID/CitationID stay the bindings.
	Bibliography []researchBibliographyEntry `json:"bibliography"`
	// CheckedAt pins the validation time; ModelRef records the fact
	// reviewer's model identity when the semantic check ran ("heuristic"
	// otherwise — heuristic findings never claim model authority).
	CheckedAt string `json:"checked_at"`
	ModelRef  string `json:"model_ref,omitempty"`
}

// ResearchValidationDetailsSchemaVersion pins the content shape; the T07
// extension is additive over the T06 field set.
const ResearchValidationDetailsSchemaVersion = "research-validation-details/1"

// ResearchCitationValidator executes core.research.validate.citations.
// Tamper of the structural inputs themselves (undecodable report artifacts,
// wrong draft hash, wrong pack binding) fails the node closed; finding-level
// blockers surface through the evidence_report and block at the quality
// gate, so the user sees the specific findings before the run pauses.
type ResearchCitationValidator struct {
	descriptor ExecutorDescriptor
	content    ContentGateway
	// runs optionally resolves run-level context (the confirmed contract) so
	// the scope-overclaim check can honor full_text_required even though the
	// template's citations node does not declare the contract as an input.
	// Nil keeps the check conservative (abstract citations flagged for human
	// review with a generic message).
	runs ResearchRunArtifactSource
	now  func() time.Time
}

// ResearchRunArtifactSource is the narrow store surface validators may use to
// resolve run-level context. *writingstore.Store implements it.
type ResearchRunArtifactSource interface {
	ListRunArtifacts(ctx context.Context, runID string) ([]writingstore.ArtifactRecord, error)
}

// NewResearchCitationValidator wires the citation check. runs is optional
// (nil in unit fixtures); production wiring passes the run store.
func NewResearchCitationValidator(content ContentGateway, runs ResearchRunArtifactSource) (*ResearchCitationValidator, error) {
	if content == nil {
		return nil, ErrRuntimeNotReady
	}
	descriptor := ExecutorDescriptor{ExecutorID: "engine.step.research_citations", Version: "1",
		SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeValidate}, Cancellable: false}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	return &ResearchCitationValidator{descriptor: descriptor, content: content, runs: runs,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

func (validator *ResearchCitationValidator) Descriptor() ExecutorDescriptor {
	return validator.descriptor
}

// Execute runs the full deterministic citation verification and emits
// evidence_report + research_validation_details (+ bibliography projection).
func (validator *ResearchCitationValidator) Execute(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
	if err := request.Validate(); err != nil {
		return ExecutionResult{}, err
	}
	packInput, err := researchInputByType(request, "research_evidence_pack")
	if err != nil {
		return ExecutionResult{}, err
	}
	draftInput, err := researchInputByType(request, "full_draft")
	if err != nil {
		return ExecutionResult{}, err
	}
	indexInput, err := researchInputByType(request, "research_citation_index")
	if err != nil {
		return ExecutionResult{}, err
	}
	fail := func(message string) (ExecutionResult, error) {
		return ExecutionResult{}, runtimeError(CodeEvidenceInvalid, RetryNever, message, nil)
	}
	packBody, err := validator.content.Load(ctx, packInput)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeMaterialIntegrityFailed, RetrySafe, "load evidence pack", err)
	}
	if contentHash(packBody) != packInput.ContentHash {
		return fail("evidence pack content hash mismatch")
	}
	var pack writingkernel.ResearchEvidencePack
	if err := json.Unmarshal(packBody, &pack); err != nil || pack.Validate() != nil {
		return ExecutionResult{}, runtimeError(CodeEvidenceInvalid, RetryNever, "evidence pack invalid", err)
	}
	draftBody, err := validator.content.Load(ctx, draftInput)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeMaterialIntegrityFailed, RetrySafe, "load draft", err)
	}
	if contentHash(draftBody) != draftInput.ContentHash {
		return fail("draft content hash mismatch")
	}
	indexBody, err := validator.content.Load(ctx, indexInput)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeMaterialIntegrityFailed, RetrySafe, "load citation index", err)
	}
	var index writingkernel.ResearchCitationIndex
	if err := json.Unmarshal(indexBody, &index); err != nil {
		return ExecutionResult{}, runtimeError(CodeEvidenceInvalid, RetryNever, "decode citation index", err)
	}
	// Structural schema checks stay node-fatal: without them no report can be
	// trusted at all. Binding/content mismatches below become findings.
	if err := index.Validate(); err != nil {
		return ExecutionResult{}, runtimeError(CodeEvidenceInvalid, RetryNever, "citation index invalid", err)
	}
	// Contract evidence requirement (class d needs full_text_required): the
	// template's citations node does not declare the contract as an input, so
	// the confirmed contract is resolved from the run artifacts. Without it
	// the scope-overclaim check stays conservative (flagged for review with
	// the scope note), never guessed.
	var spec *writingkernel.ResearchSpec
	if validator.runs != nil {
		if artifacts, err := validator.runs.ListRunArtifacts(ctx, request.RunID); err == nil {
			for _, artifact := range artifacts {
				if artifact.ArtifactType != "contract" {
					continue
				}
				body, err := validator.content.Load(ctx, InputArtifact{ArtifactID: artifact.ArtifactID,
					Version: artifact.Version, ArtifactType: "contract", ContentHash: artifact.ContentHash,
					MediaType: artifact.MediaType, ContentRef: artifact.ContentRef})
				if err != nil {
					continue
				}
				if contract, err := writingkernel.DecodeWritingContractResearchStrict(body); err == nil {
					spec = contract.Research
				}
				break
			}
		}
	}
	// Hash verification loads each paper's parsed-document artifact once; a
	// pack whose evidence hashes were mutated after the read commit can no
	// longer match the stored blocks (class c).
	parsed, err := validator.loadParsedDocuments(ctx, pack)
	if err != nil {
		return ExecutionResult{}, err
	}
	inspection := inspectCitations(draftBody, index, pack, spec, parsed, validator.now())
	details := inspection.details
	// Binding checks: a draft-hash or pack-ref mismatch means the index does
	// not describe this draft/pack pair at all. They stay blocker-class
	// findings (the quality gate blocks on them), and the other checks keep
	// running so the report lists every orphaned citation in one pass.
	addBindingFinding := func(list *[]string, findingType, message string) {
		*list = append(*list, message)
		details.Findings = append(details.Findings, detailFinding{Type: findingType,
			Severity: "blocker", Message: message, Source: findingSourceStructural})
		inspection.issues = append(inspection.issues, validatorIssue{Severity: "blocker",
			Type: findingType, Message: message})
		inspection.blocker = true
	}
	if contentHash(draftBody) != index.DraftHash {
		addBindingFinding(&details.InvalidCitations, FindingInvalidCitation,
			"citation index draft hash does not match the draft artifact; the citation stream does not describe this draft")
	}
	if index.EvidencePackRef != (writingkernel.ArtifactRef{ArtifactID: packInput.ArtifactID, Version: packInput.Version, ContentHash: packInput.ContentHash}) {
		addBindingFinding(&details.InvalidCitations, FindingWrongPack,
			"citation index references a different evidence pack version than the run's frozen pack")
	} else if err := writingkernel.ValidateCitationIndexAgainstPack(index, pack); err != nil {
		addBindingFinding(&details.InvalidCitations, FindingWrongPack,
			"citation index failed pack cross-validation: "+err.Error())
	}
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeExecutorOutputInvalid, RetryNever, "marshal validation details", err)
	}
	report := validatorReport{Validator: "core.research.validate.citations",
		Mode: "structural",
		Scores: map[string]float64{"citation_integrity": func() float64 {
			if len(details.InvalidCitations) == 0 {
				return 1
			}
			return 0
		}()},
		Issues: inspection.issues, Passed: !inspection.blocker,
		CheckedAt: details.CheckedAt}
	if report.Issues == nil {
		report.Issues = []validatorIssue{}
	}
	reportBody, err := json.Marshal(report)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeExecutorOutputInvalid, RetryNever, "marshal evidence report", err)
	}
	return validator.emit(ctx, request, []stagedOutput{
		{outputKey: "evidence_report", artifactType: "evidence_report", mediaType: "application/json", body: reportBody},
		{outputKey: "research_validation_details", artifactType: "research_validation_details", mediaType: "application/json", body: detailsJSON},
	})
}

// loadParsedDocuments loads every paper's parsed-document artifact from the
// content store: the frozen block texts and hashes hash verification needs
// (class c). Papers without a parsed ref (unread) contribute no entry; a
// missing/corrupt parsed artifact degrades to no hash re-verification for
// that paper — the report keeps its other classes rather than failing the
// whole node on content-store drift outside the citation stream.
func (validator *ResearchCitationValidator) loadParsedDocuments(ctx context.Context, pack writingkernel.ResearchEvidencePack) (citationParsedDocs, error) {
	parsed := citationParsedDocs{}
	for _, paper := range pack.Papers {
		if paper.ParsedDocumentRef == nil {
			continue
		}
		body, err := validator.content.Load(ctx, InputArtifact{ArtifactID: paper.ParsedDocumentRef.ArtifactID,
			Version: paper.ParsedDocumentRef.Version, ArtifactType: "research_note",
			ContentHash: paper.ParsedDocumentRef.ContentHash, MediaType: "application/json",
			ContentRef: "artifact://" + paper.ParsedDocumentRef.ContentHash})
		if err != nil {
			continue
		}
		var outputs ParseOutputs
		if err := json.Unmarshal(body, &outputs); err != nil {
			continue
		}
		blocks := map[string]ParsedBlock{}
		for _, block := range outputs.Blocks {
			blocks[block.BlockID] = ParsedBlock{BlockID: block.BlockID,
				Text: block.Text, Page: block.Page, BlockHash: block.BlockHash}
		}
		parsed[paper.PaperID] = blocks
	}
	return parsed, nil
}

// ResearchFactValidator executes core.research.validate.fact. With a wired
// reviewer it runs the lightweight claim/quote consistency inquiry (pending
// conclusions with model identity, never verification passes); without one it
// honestly reports mode=degraded (ValidatorRunner posture).
type ResearchFactValidator struct {
	descriptor ExecutorDescriptor
	content    ContentGateway
	reviewer   ResearchFactReviewer
	now        func() time.Time
}

// NewResearchFactValidator wires the fact check. A nil reviewer keeps the
// honest degraded mode (no-model deployment); production wiring passes
// LLMResearchFactReviewer when an LLM client exists.
func NewResearchFactValidator(content ContentGateway, reviewer ResearchFactReviewer) (*ResearchFactValidator, error) {
	if content == nil {
		return nil, ErrRuntimeNotReady
	}
	descriptor := ExecutorDescriptor{ExecutorID: "engine.step.research_fact", Version: "1",
		SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeValidate}, Cancellable: false}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	return &ResearchFactValidator{descriptor: descriptor, content: content, reviewer: reviewer,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

func (validator *ResearchFactValidator) Descriptor() ExecutorDescriptor { return validator.descriptor }

// Execute emits the fact_report: degraded or model-assisted-pending.
func (validator *ResearchFactValidator) Execute(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
	if err := request.Validate(); err != nil {
		return ExecutionResult{}, err
	}
	packInput, err := researchInputByType(request, "research_evidence_pack")
	if err != nil {
		return ExecutionResult{}, err
	}
	packBody, err := validator.content.Load(ctx, packInput)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeMaterialIntegrityFailed, RetrySafe, "load evidence pack", err)
	}
	var pack writingkernel.ResearchEvidencePack
	if err := json.Unmarshal(packBody, &pack); err != nil || pack.Validate() != nil {
		return ExecutionResult{}, runtimeError(CodeEvidenceInvalid, RetryNever, "evidence pack invalid", err)
	}
	report := validatorReport{Validator: "core.research.validate.fact", Mode: "degraded",
		Scores: map[string]float64{}, Issues: []validatorIssue{}, Passed: true,
		CheckedAt: validator.now().Format(time.RFC3339)}
	if validator.reviewer == nil {
		pending := 0
		for _, claim := range pack.Claims {
			if claim.ReviewStatus == writingkernel.ClaimReviewPending {
				pending++
			}
		}
		report.Issues = append(report.Issues, validatorIssue{Severity: "medium", Type: "review_skipped",
			Message: fmt.Sprintf("语义事实核查未执行（未配置核查模型）；证据包含 %d 条待审核 claim，全部按未核验处理。", pending)})
		body, err := json.Marshal(report)
		if err != nil {
			return ExecutionResult{}, runtimeError(CodeExecutorOutputInvalid, RetryNever, "marshal fact report", err)
		}
		return validator.emit(ctx, request, []stagedOutput{
			{outputKey: "fact_report", artifactType: "fact_report", mediaType: "application/json", body: body}})
	}
	// Lightweight semantic review: one bounded inquiry per cited claim.
	evidenceByID := map[string]writingkernel.Evidence{}
	for _, evidence := range pack.Evidence {
		evidenceByID[evidence.EvidenceID] = evidence
	}
	paperByID := map[string]writingkernel.PaperEvidence{}
	for _, paper := range pack.Papers {
		paperByID[paper.PaperID] = paper
	}
	modelRefs := map[string]bool{}
	reviewed, degraded := 0, 0
	for _, claim := range pack.Claims {
		if claim.ReviewStatus != writingkernel.ClaimReviewPending {
			continue
		}
		quote := ""
		paperTitle := claim.PaperID
		for _, evidenceID := range claim.EvidenceIDs {
			if evidence, ok := evidenceByID[evidenceID]; ok {
				quote = evidence.Quote
				if paper, ok := paperByID[evidence.PaperID]; ok {
					paperTitle = paper.Bibliography.Title
				}
				break
			}
		}
		conclusion, err := validator.reviewer.ReviewClaim(ctx, ResearchFactClaimInput{
			ClaimID: claim.ClaimID, ClaimText: claim.Text, Quote: quote, PaperTitle: paperTitle})
		if err != nil || conclusion.Status == "" {
			degraded++
			report.Issues = append(report.Issues, validatorIssue{Severity: "medium", Type: "review_degraded",
				Message: fmt.Sprintf("claim %s 语义核查未完成，按未核验处理", claim.ClaimID)})
			continue
		}
		reviewed++
		if conclusion.ModelRef != "" {
			modelRefs[conclusion.ModelRef] = true
		}
		severity := "medium"
		if conclusion.Status == "inconsistent" {
			severity = "high"
		}
		note := conclusion.Note
		if note == "" {
			note = "结论待人工复核"
		}
		report.Issues = append(report.Issues, validatorIssue{Severity: severity,
			Type:    "fact_" + conclusion.Status,
			Message: fmt.Sprintf("claim %s 语义核查结论：%s（%s）——待人工复核，不作为验证通过", claim.ClaimID, conclusion.Status, note)})
	}
	if len(modelRefs) > 0 {
		refs := make([]string, 0, len(modelRefs))
		for ref := range modelRefs {
			refs = append(refs, ref)
		}
		report.Mode = "model_pending"
		report.Scores["claims_reviewed"] = float64(reviewed)
		// The model identity rides a dedicated low-severity issue: pending
		// conclusions are attached with model/version, never converted into
		// a verification pass (contracts.md §2).
		report.Issues = append(report.Issues, validatorIssue{Severity: "low", Type: "fact_review_model",
			Message: "语义核查模型：" + strings.Join(refs, ",") + "；全部结论为 pending，需人工复核。"})
	} else {
		report.Issues = append(report.Issues, validatorIssue{Severity: "medium", Type: "review_skipped",
			Message: "语义核查模型对全部 claim 均未给出结论，评审降级。"})
	}
	body, err := json.Marshal(report)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeExecutorOutputInvalid, RetryNever, "marshal fact report", err)
	}
	return validator.emit(ctx, request, []stagedOutput{
		{outputKey: "fact_report", artifactType: "fact_report", mediaType: "application/json", body: body}})
}

// stagedOutput is one artifact to stage and commit.
type stagedOutput struct {
	outputKey    string
	artifactType string
	mediaType    string
	body         []byte
}

// emit stages every output through the content gateway and assembles the
// execution result with full input lineage.
func emitValidatedOutputs(ctx context.Context, content ContentGateway, request ExecutionRequest, outputs []stagedOutput) (ExecutionResult, error) {
	parents, inputHashes := lineageInputs(request)
	drafts := make([]OutputArtifactDraft, 0, len(outputs))
	for _, output := range outputs {
		ref, hash, err := content.Stage(ctx, request.IdempotencyKey+":"+output.outputKey, output.mediaType, output.body)
		if err != nil {
			return ExecutionResult{}, runtimeError(CodeArtifactCommitFailed, RetrySafe, "stage "+output.outputKey, err)
		}
		if hash != contentHash(output.body) {
			return ExecutionResult{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "staged "+output.outputKey+" hash mismatch", nil)
		}
		drafts = append(drafts, OutputArtifactDraft{OutputKey: output.outputKey,
			ArtifactType: artifactTypeOf(request.Node.OutputArtifactTypes, output.artifactType),
			ContentHash:  hash, MediaType: output.mediaType, ContentRef: ref,
			Parents: parents, Producer: request.Node.Capability,
			CapabilityVersion: request.Node.CapabilityVersion, InputHashes: inputHashes,
			Provenance: map[string]any{"validator": request.Node.Capability},
			SourceRefs: []string{}})
	}
	now := time.Now().UTC()
	return ExecutionResult{Artifacts: drafts, Usage: ExecutionUsage{}, StartedAt: now, CompletedAt: now}, nil
}

func (validator *ResearchCitationValidator) emit(ctx context.Context, request ExecutionRequest, outputs []stagedOutput) (ExecutionResult, error) {
	return emitValidatedOutputs(ctx, validator.content, request, outputs)
}

func (validator *ResearchFactValidator) emit(ctx context.Context, request ExecutionRequest, outputs []stagedOutput) (ExecutionResult, error) {
	return emitValidatedOutputs(ctx, validator.content, request, outputs)
}
