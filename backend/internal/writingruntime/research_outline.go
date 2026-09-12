package writingruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

// T06 research runtime error code (additive; defined here so the shared
// errors.go table stays untouched — same pattern as research_read.go).
const CodeOutlineEvidenceMismatch ErrorCode = "OUTLINE_EVIDENCE_MISMATCH"

// ErrOutlineEvidenceMismatch sentinel: the outline does not bind the pack it
// claims (contracts.md §3 → 422 OUTLINE_EVIDENCE_MISMATCH).
var ErrOutlineEvidenceMismatch = errors.New("writingruntime: outline does not match the evidence pack")

// research_outline executor (design.md §3 node table): consumes the frozen
// evidence pack and the evidence gate's approval artifact and produces the
// research-outline/1 artifact. The v1 outline is assembled deterministically
// on the Go side — no new worker operation, no model call — with honest
// limitations recorded in the artifact. An optional injected
// ResearchOutlineGenerator can produce central-point copy (tests / later
// model wiring); when absent the deterministic template text is used and the
// artifact says so.

// Outline non-factual section ids: sections without evidence may exist only
// for introduction / research-gap purposes (contracts.md §2). The draft
// executor's pause rule accepts exactly these prefixes (plus keyword matches
// on the title for user-edited outlines).
const (
	researchOutlineIntroSectionID    = "sec_introduction"
	researchOutlineResearchGapID     = "sec_research_gap"
	researchOutlineDeterministicNote = "提纲由确定性组装生成；central_point 为模板文案，未经模型生成。"
)

// ResearchOutlineGenerator produces central-point copy for one section.
// Implementations must be deterministic per (section, evidence) or carry
// their model identity in the outline provenance; nil selects the
// deterministic assembly.
type ResearchOutlineGenerator interface {
	CentralPoint(ctx context.Context, section ResearchOutlineSectionInput) (string, error)
}

// ResearchOutlineSectionInput is one section's assembly input.
type ResearchOutlineSectionInput struct {
	SectionID  string
	Title      string
	Topic      string
	EvidenceID []string
}

// ResearchOutlineExecutor freezes the run's research outline.
type ResearchOutlineExecutor struct {
	descriptor ExecutorDescriptor
	content    ContentGateway
	generator  ResearchOutlineGenerator
	now        func() time.Time
}

// NewResearchOutlineExecutor wires the executor. generator may be nil (the
// deterministic v1 assembly).
func NewResearchOutlineExecutor(content ContentGateway, generator ResearchOutlineGenerator) (*ResearchOutlineExecutor, error) {
	if content == nil {
		return nil, ErrRuntimeNotReady
	}
	descriptor := ExecutorDescriptor{ExecutorID: "engine.step.research_outline", Version: "1",
		SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}, Cancellable: false}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	return &ResearchOutlineExecutor{descriptor: descriptor, content: content, generator: generator,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

func (executor *ResearchOutlineExecutor) Descriptor() ExecutorDescriptor { return executor.descriptor }

// Execute assembles the outline from the pack + approval inputs.
func (executor *ResearchOutlineExecutor) Execute(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
	if err := request.Validate(); err != nil {
		return ExecutionResult{}, err
	}
	contract, _, err := loadContractInput(ctx, executor.content, request)
	if err != nil {
		return ExecutionResult{}, err
	}
	packInput, approvalInput, err := loadOutlineInputs(ctx, executor.content, request)
	if err != nil {
		return ExecutionResult{}, err
	}
	outline, err := assembleResearchOutline(contract.ContractHash, packInput, approvalInput, executor.generator, ctx)
	if err != nil {
		return ExecutionResult{}, err
	}
	body, err := json.Marshal(outline)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeExecutorOutputInvalid, RetryNever, "marshal research outline", err)
	}
	ref, hash, err := executor.content.Stage(ctx, request.IdempotencyKey+":research_outline", "application/json", body)
	if err != nil {
		return ExecutionResult{}, runtimeError(CodeArtifactCommitFailed, RetrySafe, "stage research outline", err)
	}
	if hash != contentHash(body) {
		return ExecutionResult{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "staged research outline hash mismatch", nil)
	}
	parents, inputHashes := lineageInputs(request)
	return ExecutionResult{
		Artifacts: []OutputArtifactDraft{{OutputKey: "research_outline",
			ArtifactType: request.Node.OutputArtifactTypes[0],
			ContentHash:  hash, MediaType: "application/json", ContentRef: ref,
			Parents: parents, Producer: request.Node.Capability,
			CapabilityVersion: request.Node.CapabilityVersion, InputHashes: inputHashes,
			Provenance: map[string]any{"contract_hash": contract.ContractHash,
				"sections": len(outline.Sections), "generator": outlineGeneratorName(executor.generator)},
			SourceRefs: []string{}}},
		Usage:     ExecutionUsage{DurationMS: 0},
		StartedAt: executor.now(), CompletedAt: executor.now(),
	}, nil
}

// outlineInputs bundles the verified evidence pack and its gate approval.
type outlineInputs struct {
	pack     writingkernel.ResearchEvidencePack
	packRef  writingkernel.ArtifactRef
	approval writingkernel.EvidenceApproval
}

// loadOutlineInputs loads and verifies the evidence pack and the
// evidence_approval decision artifact, then binds them to each other: the
// approval must reference exactly this pack version (contracts.md §2).
func loadOutlineInputs(ctx context.Context, content ContentGateway, request ExecutionRequest) (outlineInputs, InputArtifact, error) {
	var inputs outlineInputs
	packArtifact, err := researchInputByType(request, "research_evidence_pack")
	if err != nil {
		return inputs, InputArtifact{}, err
	}
	approvalArtifact, err := researchInputByType(request, "evidence_approval")
	if err != nil {
		return inputs, InputArtifact{}, err
	}
	packBody, err := content.Load(ctx, packArtifact)
	if err != nil {
		return inputs, InputArtifact{}, runtimeError(CodeMaterialIntegrityFailed, RetrySafe, "load evidence pack", err)
	}
	if contentHash(packBody) != packArtifact.ContentHash {
		return inputs, InputArtifact{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "evidence pack content hash mismatch", nil)
	}
	if err := json.Unmarshal(packBody, &inputs.pack); err != nil {
		return inputs, InputArtifact{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "decode evidence pack", err)
	}
	if err := inputs.pack.Validate(); err != nil {
		return inputs, InputArtifact{}, runtimeError(CodeEvidenceInvalid, RetryNever, "evidence pack failed kernel validation", err)
	}
	inputs.packRef = writingkernel.ArtifactRef{ArtifactID: packArtifact.ArtifactID, Version: packArtifact.Version, ContentHash: packArtifact.ContentHash}
	approvalBody, err := content.Load(ctx, approvalArtifact)
	if err != nil {
		return inputs, InputArtifact{}, runtimeError(CodeMaterialIntegrityFailed, RetrySafe, "load evidence approval", err)
	}
	if contentHash(approvalBody) != approvalArtifact.ContentHash {
		return inputs, InputArtifact{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "evidence approval content hash mismatch", nil)
	}
	if err := json.Unmarshal(approvalBody, &inputs.approval); err != nil {
		return inputs, InputArtifact{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "decode evidence approval", err)
	}
	if err := inputs.approval.Validate(); err != nil {
		return inputs, InputArtifact{}, runtimeError(CodeEvidenceInvalid, RetryNever, "evidence approval failed kernel validation", err)
	}
	if inputs.approval.EvidencePackRef != inputs.packRef {
		return inputs, InputArtifact{}, runtimeError(CodeExecutorContractMismatch, RetryNever,
			"evidence approval does not reference the pack version under review", nil)
	}
	return inputs, approvalArtifact, nil
}

// assembleResearchOutline builds the deterministic v1 outline: one
// introduction section (no evidence), one thematic section per evidence
// pack paper carrying evidence (evidence_ids = that paper's evidence), and
// one research-gap section from the pack's honest gaps. Evidence-less
// sections carry only the two non-factual purposes (contracts.md §2).
func assembleResearchOutline(contractHash string, inputs outlineInputs, approvalArtifact InputArtifact,
	generator ResearchOutlineGenerator, ctx context.Context) (writingkernel.ResearchOutline, error) {

	evidenceByPaper := map[string][]string{}
	for _, evidence := range inputs.pack.Evidence {
		evidenceByPaper[evidence.PaperID] = append(evidenceByPaper[evidence.PaperID], evidence.EvidenceID)
	}
	paperByID := map[string]writingkernel.PaperEvidence{}
	for _, paper := range inputs.pack.Papers {
		paperByID[paper.PaperID] = paper
	}
	// Deterministic paper order (paper_id ascending) keeps the same pack on
	// the same contract compiling to the same outline bytes.
	paperIDs := make([]string, 0, len(evidenceByPaper))
	for paperID := range evidenceByPaper {
		paperIDs = append(paperIDs, paperID)
	}
	sortStrings(paperIDs)

	sections := []writingkernel.OutlineSection{{
		SectionID: researchOutlineIntroSectionID, Title: "引言",
		CentralPoint: "界定研究问题与综述范围，说明选题背景与综述结构。",
		EvidenceIDs:  []string{},
		Gaps:         []string{"引言为非事实综合章节，不引用证据。"},
	}}
	for _, paperID := range paperIDs {
		paper := paperByID[paperID]
		ids := evidenceByPaper[paperID]
		central := fmt.Sprintf("综述 %s 的主要发现与证据。", paper.Bibliography.Title)
		if generator != nil {
			generated, err := generator.CentralPoint(ctx, ResearchOutlineSectionInput{SectionID: "sec_" + paperID,
				Title: paper.Bibliography.Title, EvidenceID: ids})
			if err == nil && strings.TrimSpace(generated) != "" {
				central = strings.TrimSpace(generated)
			}
		}
		sections = append(sections, writingkernel.OutlineSection{SectionID: "sec_" + writingstoreSafeID(paperID),
			Title: paper.Bibliography.Title, CentralPoint: central, EvidenceIDs: ids,
			Gaps: paperScopeGaps(paper)})
	}
	gapSection := writingkernel.OutlineSection{SectionID: researchOutlineResearchGapID, Title: "研究缺口",
		CentralPoint: "总结未覆盖的问题与相互矛盾的证据，指出后续研究方向。",
		EvidenceIDs:  []string{}, Gaps: append([]string{}, inputs.pack.Coverage.Gaps...)}
	if len(inputs.pack.Coverage.Gaps) == 0 {
		gapSection.Gaps = []string{"证据包未报告明确缺口。"}
	}
	sections = append(sections, gapSection)

	limitations := []string{researchOutlineDeterministicNote}
	if generator != nil {
		limitations = []string{"central_point 由注入的生成器生成；其余结构为确定性组装。"}
	}
	for _, gap := range inputs.pack.Coverage.Gaps {
		limitations = append(limitations, "coverage gap: "+gap)
	}
	outline := writingkernel.ResearchOutline{
		SchemaVersion:   writingkernel.ResearchOutlineSchemaVersion,
		ContractHash:    contractHash,
		EvidencePackRef: inputs.packRef,
		Sections:        sections,
		Limitations:     limitations,
	}
	if err := writingkernel.ValidateOutlineAgainstPack(outline, inputs.pack); err != nil {
		return writingkernel.ResearchOutline{}, runtimeError(CodeOutlineEvidenceMismatch, RetryNever,
			"assembled outline failed pack cross-validation", err)
	}
	_ = approvalArtifact
	return outline, nil
}

// paperScopeGaps records honest reading-boundary notes per section.
func paperScopeGaps(paper writingkernel.PaperEvidence) []string {
	gaps := []string{}
	if paper.ReadingScope == writingkernel.ReadingScopeAbstract {
		gaps = append(gaps, "论文仅完成摘要级阅读；结论覆盖受摘要边界限制。")
	}
	if paper.Truncated {
		gaps = append(gaps, "解析块被截断；部分全文内容未读取。")
	}
	return gaps
}

func outlineGeneratorName(generator ResearchOutlineGenerator) string {
	if generator == nil {
		return "deterministic"
	}
	return "injected"
}

func sortStrings(values []string) { sort.Strings(values) }

func writingstoreSafeID(value string) string {
	replaced := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, value)
	if len(replaced) > 48 {
		replaced = replaced[:48]
	}
	return replaced
}
