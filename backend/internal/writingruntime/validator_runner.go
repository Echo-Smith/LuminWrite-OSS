// Validator runner (V3.0 M1.2, docs/22): executes the core.validation.evidence
// and core.validation.fact capabilities that the sourced/strict templates
// reference. A validator is a report producer, not a gate — the quality node
// stays the sole acceptance authority — so degradation follows the post-review
// precedent: whenever the check cannot actually run (no LLM, failed call,
// unparsable response or input) the runner still emits a structurally complete
// report with mode="degraded" and a review_skipped issue, never a node
// failure. Findings never fail the node either; they only inform quality.
package writingruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

// Capability classes serviced by ValidatorRunner.
const (
	ValidatorCapabilityEvidence = "core.validation.evidence"
	ValidatorCapabilityFact     = "core.validation.fact"
)

// ValidatorRunner runs one validation capability per node invocation. LLM is
// optional: nil (or a failing client) yields a degraded report so the run can
// complete while honestly recording that the check did not happen.
type ValidatorRunner struct {
	// LLM is the model client used for the actual review. Nil means the
	// deployment has not wired a model for validators (shadow lanes, tests).
	LLM *tools.LLMClient
	// Now overrides the wall clock in tests. Zero means time.Now.
	Now func() time.Time
}

// validatorIssue mirrors engine.ReviewIssue so reports stay compatible with
// the quality consumer's issue vocabulary.
type validatorIssue struct {
	Severity string `json:"severity"`
	Type     string `json:"type"`
	Message  string `json:"message"`
}

// validatorReport is the durable artifact body both validators emit. Mode
// records whether the review actually ran ("llm") or was degraded
// ("degraded"); scores/issues/passed keep the engine.ReviewResult shape so
// the quality node can consume reports without a second schema.
type validatorReport struct {
	Validator string             `json:"validator"`
	Mode      string             `json:"mode"`
	Scores    map[string]float64 `json:"scores"`
	Issues    []validatorIssue   `json:"issues"`
	Passed    bool               `json:"passed"`
	CheckedAt string             `json:"checked_at"`
}

// validatorKindFromCapability maps the capability id to its review kind.
func validatorKindFromCapability(capability string) (kind, score string, ok bool) {
	switch capability {
	case ValidatorCapabilityEvidence:
		return "evidence", "evidence_coverage", true
	case ValidatorCapabilityFact:
		return "fact", "factuality", true
	}
	return "", "", false
}

func (runner *ValidatorRunner) Run(ctx context.Context, input LegacyNodeInput) ([]LegacyPayload, LegacyUsage, error) {
	kind, score, ok := validatorKindFromCapability(input.Request.Node.Capability)
	if !ok {
		return nil, LegacyUsage{}, fmt.Errorf("%w: validator runner cannot serve capability %s",
			ErrInvalidExecutionRequest, input.Request.Node.Capability)
	}
	report := validatorReport{Validator: input.Request.Node.Capability, Mode: "degraded",
		Scores: map[string]float64{}, Issues: []validatorIssue{}, Passed: true, CheckedAt: runner.clock().Format(time.RFC3339)}
	draft, _ := findArtifactPayload(input.Payloads, "full_draft")
	if len(draft) == 0 {
		report.Issues = append(report.Issues, validatorIssue{Severity: "medium", Type: "review_skipped",
			Message: "缺少 full_draft 输入，评审未执行"})
		return runner.emit(input, report), LegacyUsage{Measured: true}, nil
	}
	pack, _ := findArtifactPayload(input.Payloads, "source_pack")
	sources := decodeSourcePack(pack)
	if len(sources) == 0 {
		report.Issues = append(report.Issues, validatorIssue{Severity: "medium", Type: "review_skipped",
			Message: "缺少可解析的 source_pack 输入，评审未执行"})
		return runner.emit(input, report), LegacyUsage{Measured: true}, nil
	}
	if runner.LLM == nil {
		report.Issues = append(report.Issues, validatorIssue{Severity: "medium", Type: "review_skipped",
			Message: "未接入评审模型，评审未执行"})
		return runner.emit(input, report), LegacyUsage{Measured: true}, nil
	}
	response, usage, err := runner.LLM.Chat(ctx, []tools.LLMMessage{{Role: "user", Content: validatorPrompt(kind, score, draft, sources)}},
		tools.WithInstructions("你是写作评审员，只返回 JSON。"),
		tools.WithTemperature(0), tools.WithJSONResponse())
	if err != nil {
		report.Issues = append(report.Issues, validatorIssue{Severity: "medium", Type: "review_skipped",
			Message: "评审模型调用失败，评审未执行"})
		return runner.emit(input, report), LegacyUsage{Measured: true}, nil
	}
	var review struct {
		Scores map[string]float64   `json:"scores"`
		Issues []engine.ReviewIssue `json:"issues"`
		Passed bool                 `json:"passed"`
	}
	if err := json.Unmarshal([]byte(tools.ExtractJSONObject(response)), &review); err != nil {
		report.Issues = append(report.Issues, validatorIssue{Severity: "medium", Type: "review_skipped",
			Message: "评审响应无法解析，评审未执行"})
		return runner.emit(input, report), LegacyUsage{Measured: true}, nil
	}
	report.Mode = "llm"
	if review.Scores == nil {
		review.Scores = map[string]float64{}
	}
	report.Scores = review.Scores
	for _, issue := range review.Issues {
		report.Issues = append(report.Issues, validatorIssue{Severity: issue.Severity, Type: issue.Type, Message: issue.Message})
	}
	report.Passed = review.Passed
	tokenUsage := LegacyUsage{Measured: true}
	if usage != nil {
		tokenUsage.InputTokens = int64(usage.Usage.PromptTokens)
		tokenUsage.OutputTokens = int64(usage.Usage.CompletionTokens)
	}
	return runner.emit(input, report), tokenUsage, nil
}

// emit wraps the report as the node's single output artifact.
func (runner *ValidatorRunner) emit(input LegacyNodeInput, report validatorReport) []LegacyPayload {
	body, err := json.Marshal(report)
	if err != nil {
		return nil
	}
	outputType := "evidence_report"
	if report.Validator == ValidatorCapabilityFact {
		outputType = "fact_report"
	}
	return []LegacyPayload{{OutputKey: outputType, ArtifactType: writingplan.ArtifactType(outputType),
		MediaType: "application/json", Body: body,
		Provenance: map[string]any{"adapter": "validator_runner", "mode": report.Mode}}}
}

// decodeSourcePack parses the research artifact ({count, results, query}).
// Anything unparsable yields no sources — the runner degrades rather than
// failing, because the pack is upstream state the validator cannot repair.
func decodeSourcePack(payload []byte) []engine.SearchResult {
	if len(payload) == 0 {
		return nil
	}
	var wrapper struct {
		Results []engine.SearchResult `json:"results"`
	}
	if json.Unmarshal(payload, &wrapper) != nil {
		return nil
	}
	return wrapper.Results
}

// validatorPrompt builds the review prompt for one kind. Inputs are truncated
// so a large draft or pack cannot blow the context: the validators are
// advisory reports for the quality gate, not exhaustive audits.
func validatorPrompt(kind, score string, draft []byte, sources []engine.SearchResult) string {
	var pack strings.Builder
	const maxSources, maxSnippet = 20, 800
	limit := len(sources)
	if limit > maxSources {
		limit = maxSources
	}
	for index, source := range sources[:limit] {
		snippet := source.Snippet
		if len(snippet) > maxSnippet {
			snippet = snippet[:maxSnippet]
		}
		fmt.Fprintf(&pack, "[%d] %s（%s）：%s\n", index+1, source.Title, source.URL, snippet)
	}
	draftText := string(draft)
	if len(draftText) > 8000 {
		draftText = draftText[:8000]
	}
	if kind == "evidence" {
		return fmt.Sprintf(`请评估以下文章对信源的使用覆盖情况。

文章：
%s

信源：
%s

评审维度：%s（0-1，文章内容被信源支持的比例）。对无信源支撑的关键声明，输出 issues（severity: high|medium|low，type: unsupported_claim）。

返回格式：
{"scores": {"%s": 0.0}, "issues": [{"severity": "high", "type": "unsupported_claim", "message": "..."}], "passed": true}`,
			draftText, pack.String(), score, score)
	}
	return fmt.Sprintf(`请核查以下文章中的事实性陈述是否与信源一致或矛盾。

文章：
%s

信源：
%s

评审维度：%s（0-1，事实陈述与信源一致的比例）。对与信源矛盾或信源无法证实的关键事实，输出 issues（severity: high|medium|low，type: fact_conflict 或 unverified_claim）。

返回格式：
{"scores": {"%s": 0.0}, "issues": [{"severity": "high", "type": "fact_conflict", "message": "..."}], "passed": true}`,
		draftText, pack.String(), score, score)
}

func (runner *ValidatorRunner) clock() time.Time {
	if runner.Now != nil {
		return runner.Now()
	}
	return time.Now().UTC()
}
