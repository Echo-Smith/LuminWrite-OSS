package database

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNormalizedWABenchFailureMapsPrivateRunnerShapeToPublicContract(t *testing.T) {
	tests := []struct {
		raw               map[string]interface{}
		stage, kind, code string
	}{
		{map[string]interface{}{"id": "generation.failed", "stage": "generation"}, "generation", "failed", "GENERATION_FAILED"},
		{map[string]interface{}{"id": "routing.boundary_violation", "stage": "routing"}, "retrieval", "boundary_violation", "ROUTING_BOUNDARY_VIOLATION"},
		{map[string]interface{}{"id": "capture.input_unavailable", "stage": "capture"}, "input", "input_unavailable", "CAPTURE_INPUT_UNAVAILABLE"},
	}
	for _, test := range tests {
		got := normalizedWABenchFailure(test.raw)
		if got.Stage != test.stage || got.Type != test.kind || got.Code != test.code || !got.HardFailure {
			t.Fatalf("normalized failure = %+v", got)
		}
	}
}

func TestNormalizeWABenchMetricsKeepsUnavailableCostNull(t *testing.T) {
	metrics := normalizeWABenchMetrics(map[string]interface{}{"latencyMs": float64(125), "totalTokens": float64(42), "cost": map[string]interface{}{"availability": "unavailable"}},
		[]WABenchNormalizedCheck{{CheckID: "tool.search_knowledge.1", Status: "fail"}})
	if metrics.LatencyMs == nil || *metrics.LatencyMs != 125 || metrics.ToolFailureCount != 1 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
	if metrics.InputTokens != nil || metrics.OutputTokens != nil || metrics.Cost != nil || metrics.CostStatus != "unavailable" {
		t.Fatalf("total tokens or unavailable cost were misrepresented: %+v", metrics)
	}
}

func TestNormalizedWABenchAliasIsSchemaSafe(t *testing.T) {
	if got := normalizedWABenchAlias("luminbuddy-v2-judge"); got != "luminbuddy_v2_judge" {
		t.Fatalf("alias = %q", got)
	}
}

func TestNormalizedRunBundleIntegration(t *testing.T) {
	databaseURL := os.Getenv("WABENCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set WABENCH_TEST_DATABASE_URL to run PostgreSQL integration test")
	}
	db, err := NewPostgres(databaseURL, 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewWABenchRepo(db)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	inputHash := "sha256:" + strings.Repeat("a", 64)
	batchHash := "sha256:" + strings.Repeat("b", 64)
	caseID := "bundle_case_" + suffix
	batch := WABenchFrozenBatch{SchemaVersion: WABenchSchemaVersion, BatchID: "bundle_batch_" + suffix, Version: "v1.0.0",
		Description: "Synthetic normalized bundle integration batch.", Visibility: "public", SuiteID: "wabench.public.bundle-" + suffix,
		ContentHash: batchHash, FrozenAt: time.Now().UTC(), CaseRefs: []WABenchFrozenCaseRef{{CaseID: caseID, InputHash: inputHash, PrivacyLevel: "synthetic", SourceFixtureRefs: []string{}}}}
	_, err = repo.ImportFrozenPublicBatch(context.Background(), batch, []WABenchPortableCase{{SchemaVersion: WABenchSchemaVersion, CaseID: caseID,
		TaskType: "writing", Difficulty: "L1", Input: "合成输入", Context: map[string]interface{}{}, SourceMode: "none", SourceFixtureRefs: []string{},
		ExpectedBehavior: "answer", MustHave: []string{}, MustNotHave: []string{}, HardGateIDs: []string{},
		RubricWeights:  map[string]int{"taskCompliance": 25, "sourceFidelity": 25, "structureReasoning": 15, "styleConsistency": 15, "directUsability": 20},
		CapabilityTags: []string{}, RiskTags: []string{}, RuleProfileRefs: []string{"wabench.public.general-writing"}, PrivacyLevel: "synthetic", InputHash: inputHash}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidateID := "bundle_candidate_" + suffix
	_, err = repo.UpsertCandidate(context.Background(), WABenchCandidateDraft{CandidateID: candidateID, Name: "bundle integration", PromptHash: inputHash,
		ModelManifest: map[string]interface{}{"model": "fake"}, CodeHash: inputHash, ToolManifest: map[string]interface{}{}, FeatureFlags: map[string]interface{}{"memoryEnabled": false}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.CreateRun(context.Background(), batch.SuiteID, candidateID, "shadow", "replay_bundle")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.StartRun(context.Background(), run.RunID); err != nil {
		t.Fatal(err)
	}
	cases, err := repo.ListCases(context.Background(), run.SuitePK)
	if err != nil || len(cases) != 1 {
		t.Fatalf("cases=%d err=%v", len(cases), err)
	}
	burden := 1
	err = repo.SaveCaseResult(context.Background(), run.PK, cases[0].PK, WABenchOutputWrite{OutputID: "bundle_output_" + suffix, Status: "complete", OutputHash: batchHash,
		TextStorage: "inline_public", OutputText: "合成输出", Failures: []map[string]interface{}{},
		Metrics: map[string]interface{}{"latencyMs": 125, "totalTokens": 42, "cost": map[string]interface{}{"availability": "unavailable"}},
		Routing: map[string]interface{}{"webSearchTriggered": false, "knowledgeProviders": []string{}},
		Checks:  []WABenchCheckWrite{{CheckID: "generation.non_empty", Status: "pass", Severity: "critical", Evidence: map[string]interface{}{}}},
		Review: &WABenchReviewWrite{ReviewID: "bundle_review_" + suffix, ReviewerID: "luminbuddy-v2-judge", ReviewerRole: "judge", ReviewerType: "model",
			ReviewMethod: "llm_as_judge", LabelSource: "wabench.v1", IsBlind: true, TaskCompliance: 4, SourceFidelity: 4, StructureReasoning: 4,
			StyleConsistency: 4, DirectUsability: 4, AcceptanceLabel: "unknown", ModificationBurden: &burden, HardFailureIDs: []string{},
			SecondaryRootCauses: []string{}, Evidence: map[string]interface{}{"symptoms": []string{}}, ReviewedAt: time.Now().UTC()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteRun(context.Background(), run.RunID, "completed"); err != nil {
		t.Fatal(err)
	}
	bundle, err := repo.GetNormalizedRunBundle(context.Background(), run.RunID, batch.BatchID, batch.ContentHash)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.ReviewStatus != "complete" || len(bundle.Outputs) != 1 || len(bundle.Reviews) != 1 {
		t.Fatalf("unexpected bundle: %+v", bundle)
	}
	if bundle.Outputs[0].Metrics.InputTokens != nil || bundle.Outputs[0].Metrics.OutputTokens != nil || bundle.Outputs[0].Metrics.Cost != nil {
		t.Fatalf("unavailable token split/cost was invented: %+v", bundle.Outputs[0].Metrics)
	}
	data, _ := json.Marshal(bundle)
	if strings.Contains(string(data), "totalTokens") || !strings.Contains(string(data), "luminbuddy_v2_judge") {
		t.Fatalf("normalized JSON contract drift: %s", data)
	}
}
