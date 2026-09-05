package writingruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

// newTestLLMClient points a real LLMClient at the fake server.
func newTestLLMClient(t *testing.T, baseURL string) *tools.LLMClient {
	t.Helper()
	return tools.NewLLMClient(baseURL, "test-key", "fake-model", 2048, 0, 5*time.Second)
}

// fakeLLMResponse is the OpenAI-compatible body the tools.LLMClient parses.
func fakeLLMResponse(t *testing.T, content string) map[string]any {
	t.Helper()
	return map[string]any{
		"id": "chatcmpl-test", "object": "chat.completion", "created": 1, "model": "fake",
		"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": content}}},
		"usage": map[string]any{"prompt_tokens": 120, "completion_tokens": 40, "total_tokens": 160},
	}
}

// newFakeLLMServer serves one canned completion; requests captures bodies.
func newFakeLLMServer(t *testing.T, content string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	requests := &[]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*requests = append(*requests, body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fakeLLMResponse(t, content))
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func validatorTestInput(capability string) LegacyNodeInput {
	pack, _ := json.Marshal(map[string]any{"query": "test", "count": 1, "results": []map[string]any{
		{"title": "信源", "snippet": "信源内容", "url": "https://example.com/a", "source": "tavily"}}})
	return LegacyNodeInput{Request: ExecutionRequest{
		RunID: "run_validator_test", Node: writingplan.PlanNode{NodeID: "node_v", Kind: writingplan.NodeValidate, Capability: capability,
			OutputArtifactTypes: []writingplan.ArtifactType{validatorOutputType(capability)}}},
		Payloads: map[writingplan.ArtifactType][][]byte{
			"full_draft":  {[]byte("文章正文")},
			"source_pack": {pack},
		}}
}

func validatorOutputType(capability string) writingplan.ArtifactType {
	if capability == ValidatorCapabilityFact {
		return "fact_report"
	}
	return "evidence_report"
}

func decodeValidatorReport(t *testing.T, outputs []LegacyPayload) validatorReport {
	t.Helper()
	if len(outputs) != 1 {
		t.Fatalf("outputs=%#v", outputs)
	}
	var report validatorReport
	if err := json.Unmarshal(outputs[0].Body, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestValidatorRunnerDegradedWithoutLLM(t *testing.T) {
	runner := &ValidatorRunner{}
	for _, capability := range []string{ValidatorCapabilityEvidence, ValidatorCapabilityFact} {
		outputs, usage, err := runner.Run(context.Background(), validatorTestInput(capability))
		if err != nil {
			t.Fatalf("%s: %v", capability, err)
		}
		report := decodeValidatorReport(t, outputs)
		if report.Mode != "degraded" || !report.Passed || report.Validator != capability ||
			len(report.Issues) != 1 || report.Issues[0].Type != "review_skipped" {
			t.Fatalf("%s: degraded report mismatch: %#v", capability, report)
		}
		if outputs[0].ArtifactType != validatorOutputType(capability) || outputs[0].MediaType != "application/json" {
			t.Fatalf("%s: output shape mismatch: %#v", capability, outputs[0])
		}
		if !usage.Measured {
			t.Fatalf("%s: usage must be measured (zero cost, structurally recorded)", capability)
		}
	}
}

func TestValidatorRunnerDegradesOnMissingInputs(t *testing.T) {
	runner := &ValidatorRunner{}
	input := validatorTestInput(ValidatorCapabilityEvidence)
	delete(input.Payloads, "source_pack")
	outputs, _, err := runner.Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if report := decodeValidatorReport(t, outputs); report.Mode != "degraded" || !report.Passed {
		t.Fatalf("missing pack must degrade, not fail: %#v", report)
	}

	input = validatorTestInput(ValidatorCapabilityEvidence)
	delete(input.Payloads, "full_draft")
	outputs, _, err = runner.Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if report := decodeValidatorReport(t, outputs); report.Mode != "degraded" || !report.Passed {
		t.Fatalf("missing draft must degrade, not fail: %#v", report)
	}
}

func TestValidatorRunnerRejectsForeignCapability(t *testing.T) {
	runner := &ValidatorRunner{}
	_, _, err := runner.Run(context.Background(), validatorTestInput("core.draft.generate"))
	if err == nil || !strings.Contains(err.Error(), "cannot serve capability") {
		t.Fatalf("foreign capability err=%v", err)
	}
}

func TestValidatorRunnerRunsLLMReview(t *testing.T) {
	content := `{"scores": {"evidence_coverage": 0.82}, "issues": [{"severity": "medium", "type": "unsupported_claim", "message": "第二段缺引用"}], "passed": true}`
	server, requests := newFakeLLMServer(t, content)
	runner := &ValidatorRunner{LLM: newTestLLMClient(t, server.URL)}
	outputs, usage, err := runner.Run(context.Background(), validatorTestInput(ValidatorCapabilityEvidence))
	if err != nil {
		t.Fatal(err)
	}
	report := decodeValidatorReport(t, outputs)
	if report.Mode != "llm" || !report.Passed || report.Scores["evidence_coverage"] != .82 ||
		len(report.Issues) != 1 || report.Issues[0].Message != "第二段缺引用" {
		t.Fatalf("llm report mismatch: %#v", report)
	}
	if !usage.Measured || usage.InputTokens != 120 || usage.OutputTokens != 40 {
		t.Fatalf("usage not accounted: %#v", usage)
	}
	if len(*requests) != 1 {
		t.Fatalf("expected one LLM call, got %d", len(*requests))
	}
}

func TestValidatorRunnerDegradesOnUnparsableResponse(t *testing.T) {
	server, _ := newFakeLLMServer(t, "这不是 JSON")
	runner := &ValidatorRunner{LLM: newTestLLMClient(t, server.URL)}
	outputs, _, err := runner.Run(context.Background(), validatorTestInput(ValidatorCapabilityFact))
	if err != nil {
		t.Fatal(err)
	}
	if report := decodeValidatorReport(t, outputs); report.Mode != "degraded" || !report.Passed || report.Validator != ValidatorCapabilityFact {
		t.Fatalf("unparsable response must degrade: %#v", report)
	}
}

func TestValidatorRunnerDegradesOnLLMError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	runner := &ValidatorRunner{LLM: newTestLLMClient(t, server.URL)}
	outputs, _, err := runner.Run(context.Background(), validatorTestInput(ValidatorCapabilityEvidence))
	if err != nil {
		t.Fatal(err)
	}
	if report := decodeValidatorReport(t, outputs); report.Mode != "degraded" || !report.Passed {
		t.Fatalf("LLM failure must degrade: %#v", report)
	}
}
