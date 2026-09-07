package writingruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

// Research validator skeletons (T06; the full model-assisted projection is
// T07's research_validation.go). Both run as direct runtime executors and are
// report producers, never gates — the quality node stays the acceptance
// authority. The citation validator performs the deterministic structural
// half of the T07 check today: marker ↔ index ↔ pack consistency, draft-hash
// binding, and scope/unreviewed projection. The fact validator honestly
// reports that the semantic check did not run (mode=degraded), mirroring
// ValidatorRunner's no-LLM posture — it never fabricates findings.

// ResearchValidationDetails is the research_validation_details artifact
// content (contracts.md §2): the research-specific finding lists the quality
// gate and the workbench consume.
type ResearchValidationDetails struct {
	SchemaVersion     string   `json:"schema_version"`
	InvalidCitations  []string `json:"invalid_citations"`
	UnsupportedClaims []string `json:"unsupported_claims"`
	ScopeOverclaims   []string `json:"scope_overclaims"`
	UnreviewedClaims  []string `json:"unreviewed_claims"`
	OmittedEvidence   []string `json:"omitted_evidence"`
	CheckedAt         string   `json:"checked_at"`
}

// ResearchValidationDetailsSchemaVersion pins the skeleton's content shape;
// T07 extends it without rewriting the field set.
const ResearchValidationDetailsSchemaVersion = "research-validation-details/1"

// ResearchCitationValidator executes core.research.validate.citations.
type ResearchCitationValidator struct {
	descriptor ExecutorDescriptor
	content    ContentGateway
	now        func() time.Time
}

// NewResearchCitationValidator wires the structural citation check.
func NewResearchCitationValidator(content ContentGateway) (*ResearchCitationValidator, error) {
	if content == nil {
		return nil, ErrRuntimeNotReady
	}
	descriptor := ExecutorDescriptor{ExecutorID: "engine.step.research_citations", Version: "1",
		SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeValidate}, Cancellable: false}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	return &ResearchCitationValidator{descriptor: descriptor, content: content,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

func (validator *ResearchCitationValidator) Descriptor() ExecutorDescriptor {
	return validator.descriptor
}

// Execute runs the structural citation verification and emits
// evidence_report + research_validation_details.
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
	packBody, err := validator.content.Load(ctx, packInput)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeMaterialIntegrityFailed, RetrySafe, "load evidence pack", err)
	}
	var pack writingkernel.ResearchEvidencePack
	if err := json.Unmarshal(packBody, &pack); err != nil || pack.Validate() != nil {
		return ExecutionResult{}, runtimeError(CodeEvidenceInvalid, RetryNever, "evidence pack invalid", err)
	}
	draftBody, err := validator.content.Load(ctx, draftInput)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeMaterialIntegrityFailed, RetrySafe, "load draft", err)
	}
	indexBody, err := validator.content.Load(ctx, indexInput)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeMaterialIntegrityFailed, RetrySafe, "load citation index", err)
	}
	var index writingkernel.ResearchCitationIndex
	if err := json.Unmarshal(indexBody, &index); err != nil {
		return ExecutionResult{}, runtimeError(CodeEvidenceInvalid, RetryNever, "decode citation index", err)
	}
	details := ResearchValidationDetails{SchemaVersion: ResearchValidationDetailsSchemaVersion,
		InvalidCitations: []string{}, UnsupportedClaims: []string{}, ScopeOverclaims: []string{},
		UnreviewedClaims: []string{}, OmittedEvidence: []string{}, CheckedAt: validator.now().Format(time.RFC3339)}
	issues := []map[string]string{}
	fail := func(message string) (ExecutionResult, error) {
		return ExecutionResult{}, runtimeError(CodeEvidenceInvalid, RetryNever, message, nil)
	}
	if contentHash(draftBody) != index.DraftHash {
		return fail("citation index draft hash does not match the draft artifact")
	}
	if index.EvidencePackRef != (writingkernel.ArtifactRef{ArtifactID: packInput.ArtifactID, Version: packInput.Version, ContentHash: packInput.ContentHash}) {
		return fail("citation index references a different evidence pack version")
	}
	if err := writingkernel.ValidateCitationIndexAgainstPack(index, pack); err != nil {
		return fail("citation index failed pack cross-validation: " + err.Error())
	}
	// Marker ↔ index bijection over the draft text.
	draftText := string(draftBody)
	markers := map[string]int{}
	for _, match := range researchDraftMarkerRE.FindAllStringSubmatch(draftText, -1) {
		markers[match[1]]++
	}
	indexed := map[string]bool{}
	for _, citation := range index.Citations {
		if markers[citation.EvidenceID] == 0 {
			details.InvalidCitations = append(details.InvalidCitations, citation.CitationID+": index entry without draft marker")
			issues = append(issues, map[string]string{"severity": "high", "type": "invalid_citation", "message": citation.CitationID})
		}
		indexed[citation.EvidenceID] = true
	}
	for marker := range markers {
		if !indexed[marker] {
			details.InvalidCitations = append(details.InvalidCitations, marker+": draft marker without index entry")
			issues = append(issues, map[string]string{"severity": "high", "type": "invalid_citation", "message": marker})
		}
	}
	// Scope honesty: abstract-scope citations and pending-review claims are
	// surfaced for human review, never auto-passed (design.md §4).
	evidenceByID := map[string]writingkernel.Evidence{}
	for _, evidence := range pack.Evidence {
		evidenceByID[evidence.EvidenceID] = evidence
	}
	for _, citation := range index.Citations {
		if evidence, ok := evidenceByID[citation.EvidenceID]; ok && evidence.EvidenceScope == writingkernel.EvidenceScopeAbstract {
			details.ScopeOverclaims = append(details.ScopeOverclaims, citation.EvidenceID)
		}
	}
	for _, claim := range pack.Claims {
		if claim.ReviewStatus == writingkernel.ClaimReviewPending {
			details.UnreviewedClaims = append(details.UnreviewedClaims, claim.ClaimID)
		}
	}
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeExecutorOutputInvalid, RetryNever, "marshal validation details", err)
	}
	report := validatorReport{Validator: "core.research.validate.citations", Mode: "structural",
		Scores: map[string]float64{}, Issues: []validatorIssue{}, Passed: len(details.InvalidCitations) == 0,
		CheckedAt: details.CheckedAt}
	for _, issue := range issues {
		report.Issues = append(report.Issues, validatorIssue{Severity: issue["severity"], Type: issue["type"], Message: issue["message"]})
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

// ResearchFactValidator executes core.research.validate.fact as the T06
// skeleton: it reports the unreviewed-claim projection honestly and marks the
// semantic factuality check as not performed (T07 completes it).
type ResearchFactValidator struct {
	descriptor ExecutorDescriptor
	content    ContentGateway
	now        func() time.Time
}

// NewResearchFactValidator wires the skeleton fact check.
func NewResearchFactValidator(content ContentGateway) (*ResearchFactValidator, error) {
	if content == nil {
		return nil, ErrRuntimeNotReady
	}
	descriptor := ExecutorDescriptor{ExecutorID: "engine.step.research_fact", Version: "1",
		SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeValidate}, Cancellable: false}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	return &ResearchFactValidator{descriptor: descriptor, content: content,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

func (validator *ResearchFactValidator) Descriptor() ExecutorDescriptor { return validator.descriptor }

// Execute emits the degraded fact_report.
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
	pending := 0
	for _, claim := range pack.Claims {
		if claim.ReviewStatus == writingkernel.ClaimReviewPending {
			pending++
		}
	}
	report := validatorReport{Validator: "core.research.validate.fact", Mode: "degraded",
		Scores: map[string]float64{}, Issues: []validatorIssue{}, Passed: true,
		CheckedAt: validator.now().Format(time.RFC3339)}
	report.Issues = append(report.Issues, validatorIssue{Severity: "medium", Type: "review_skipped",
		Message: fmt.Sprintf("语义事实核查未执行（T06 骨架）；证据包含 %d 条待审核 claim，全部按未核验处理。", pending)})
	body, err := json.Marshal(report)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeExecutorOutputInvalid, RetryNever, "marshal fact report", err)
	}
	return validator.emit(ctx, request, []stagedOutput{
		{outputKey: "fact_report", artifactType: "fact_report", mediaType: "application/json", body: body},
	})
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
