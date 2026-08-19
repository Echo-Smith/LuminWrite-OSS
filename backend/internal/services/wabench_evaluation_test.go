package services

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

func TestWABenchJudgeRequiresFiveOneToFiveScores(t *testing.T) {
	valid := WABenchJudgeResult{Scores: WABenchRubricScores{
		TaskCompliance: 4, SourceFidelity: 5, StructureReasoning: 3,
		StyleConsistency: 4, DirectUsability: 4,
	}}
	if err := ValidateWABenchJudgeResult(valid); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Scores.SourceFidelity = 0
	if err := ValidateWABenchJudgeResult(invalid); err == nil {
		t.Fatal("0-1 legacy score was accepted as a WABench 1-5 score")
	}
	invalid = valid
	invalid.PrimaryRootCause = "hallucination"
	if err := ValidateWABenchJudgeResult(invalid); err == nil {
		t.Fatal("root cause outside the seven canonical categories was accepted")
	}
}

func TestWABenchWeightedScoreDoesNotMixChecks(t *testing.T) {
	score, err := WABenchWeightedScore(WABenchRubricScores{
		TaskCompliance: 4, SourceFidelity: 4, StructureReasoning: 4,
		StyleConsistency: 4, DirectUsability: 4,
	}, map[string]int{
		"taskCompliance": 25, "sourceFidelity": 25, "structureReasoning": 15,
		"styleConsistency": 15, "directUsability": 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if score != 80 {
		t.Fatalf("score = %.2f, want 80", score)
	}
	_, err = WABenchWeightedScore(WABenchRubricScores{
		TaskCompliance: 4, SourceFidelity: 4, StructureReasoning: 4,
		StyleConsistency: 4, DirectUsability: 4,
	}, map[string]int{
		"taskCompliance": 20, "sourceFidelity": 20, "structureReasoning": 15,
		"styleConsistency": 15, "directUsability": 20, "length": 10,
	})
	if err == nil {
		t.Fatal("deterministic length check was mixed into the Rubric score")
	}
}

func TestWABenchGateHardFailuresAndRedTeamTakePrecedence(t *testing.T) {
	suite := database.WABenchSuite{Partition: "development"}
	decision := BuildWABenchGateDecision(suite, wabenchRunAccumulator{
		totalCases: 2, completedCases: 2, scoredCases: 2,
		weightedScoreSum: 190, hardFailureCases: 1,
	})
	if decision.Decision != "fail" {
		t.Fatal("high average incorrectly offset a hard failure")
	}
	suite.Partition = "red_team"
	decision = BuildWABenchGateDecision(suite, wabenchRunAccumulator{
		totalCases: 2, completedCases: 2, scoredCases: 2,
		weightedScoreSum: 200, redTeamCompromised: 1,
	})
	if decision.Decision != "fail" {
		t.Fatal("red-team compromise did not block the release gate")
	}
}

func TestWABenchFailedPublicOutputUsesHashOnlyStorage(t *testing.T) {
	output := database.WABenchOutputWrite{TextStorage: "inline_public"}
	applyWABenchTextStorage(&output, database.WABenchSuite{Visibility: "public"}, database.WABenchCase{PrivacyLevel: "synthetic"}, &WABenchAgentTrace{})
	if output.TextStorage != "hash_only" || output.OutputText != "" || output.PrivateRef != "" {
		t.Fatalf("failed public output storage = %+v", output)
	}
	applyWABenchTextStorage(&output, database.WABenchSuite{Visibility: "public"}, database.WABenchCase{PrivacyLevel: "synthetic"}, &WABenchAgentTrace{Article: "合成正文"})
	if output.TextStorage != "inline_public" || output.OutputText != "合成正文" {
		t.Fatalf("successful public output storage = %+v", output)
	}
}

type fakeWABenchAgent struct {
	failCase string
}

func (a fakeWABenchAgent) Execute(_ context.Context, request WABenchAgentRequest) (*WABenchAgentTrace, error) {
	if request.Case.CaseID == a.failCase {
		return &WABenchAgentTrace{Status: engine.StatusFailed}, errors.New("synthetic generation failure")
	}
	return &WABenchAgentTrace{
		TraceID:     "fake_" + request.Case.CaseID,
		Article:     "## 安全回应\n\n已拒绝执行攻击指令，并提供安全替代建议。 case=" + request.Case.CaseID,
		Status:      engine.StatusCompleted,
		LatencyMs:   12,
		TotalTokens: 42,
	}, nil
}

type fakeWABenchJudge struct {
	failCase string
}

func (j fakeWABenchJudge) Judge(_ context.Context, input WABenchJudgeInput) (*WABenchJudgeResult, error) {
	if j.failCase != "" && strings.Contains(input.Article, "case="+j.failCase) {
		return nil, errors.New("synthetic judge failure")
	}
	return &WABenchJudgeResult{
		Scores: WABenchRubricScores{
			TaskCompliance: 5, SourceFidelity: 5, StructureReasoning: 5,
			StyleConsistency: 5, DirectUsability: 5,
		},
		Feedback: "pass", RedTeamCompromised: false,
	}, nil
}

func TestWABenchRunFailureStagesAreNotScoredIntegration(t *testing.T) {
	databaseURL := os.Getenv("WABENCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set WABENCH_TEST_DATABASE_URL to run PostgreSQL integration test")
	}
	db, err := database.NewPostgres(databaseURL, 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := database.NewWABenchRepo(db)
	seedService := NewWABenchEvaluationService(repo, fakeWABenchAgent{}, fakeWABenchJudge{})
	suite, err := seedService.EnsureDefaultRedTeamSuite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cases, err := repo.ListCases(context.Background(), suite.PK)
	if err != nil || len(cases) != 20 {
		t.Fatalf("red-team cases = %d, err = %v", len(cases), err)
	}
	failedCase := cases[0].CaseID
	hash := "sha256:" + strings.Repeat("a", 64)
	candidateID := "integration_" + strings.ReplaceAll(failedCase, "-", "_")
	_, err = repo.UpsertCandidate(context.Background(), database.WABenchCandidateDraft{
		CandidateID: candidateID, Name: "integration", PromptHash: hash,
		ModelManifest: map[string]interface{}{"provider": "fake", "model": "fake"},
		CodeHash:      hash, ToolManifest: map[string]interface{}{},
		FeatureFlags: map[string]interface{}{"memoryEnabled": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.UpsertCandidate(context.Background(), database.WABenchCandidateDraft{
		CandidateID: candidateID, Name: "integration", PromptHash: hash,
		ModelManifest: map[string]interface{}{"provider": "fake", "model": "fake"},
		CodeHash:      "sha256:" + strings.Repeat("b", 64), ToolManifest: map[string]interface{}{},
		FeatureFlags: map[string]interface{}{"memoryEnabled": false},
	})
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("changed candidate manifest/hash was not rejected: %v", err)
	}
	_, err = repo.UpsertCandidate(context.Background(), database.WABenchCandidateDraft{
		CandidateID: candidateID + "_memory", Name: "invalid memory candidate", PromptHash: hash,
		ModelManifest: map[string]interface{}{"provider": "fake", "model": "fake"},
		CodeHash:      hash, ToolManifest: map[string]interface{}{},
		FeatureFlags: map[string]interface{}{"memoryEnabled": true},
	})
	if err == nil || !strings.Contains(err.Error(), "memoryHash") {
		t.Fatalf("memory opt-in without frozen memoryHash was not rejected: %v", err)
	}

	t.Run("generation failure", func(t *testing.T) {
		service := NewWABenchEvaluationService(repo, fakeWABenchAgent{failCase: failedCase}, fakeWABenchJudge{})
		run, err := service.CreateRun(context.Background(), WABenchRunRequest{SuiteID: suite.SuiteID, CandidateID: candidateID, Environment: "test"})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.ExecuteRun(context.Background(), run.RunID); err != nil {
			t.Fatal(err)
		}
		assertWABenchFailureNotScored(t, db, run.RunID, "generation.failed", "generation_failed")
	})

	t.Run("judge failure", func(t *testing.T) {
		service := NewWABenchEvaluationService(repo, fakeWABenchAgent{}, fakeWABenchJudge{failCase: failedCase})
		run, err := service.CreateRun(context.Background(), WABenchRunRequest{SuiteID: suite.SuiteID, CandidateID: candidateID, Environment: "test"})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.ExecuteRun(context.Background(), run.RunID); err != nil {
			t.Fatal(err)
		}
		assertWABenchFailureNotScored(t, db, run.RunID, "judge.failed", "partial")
	})
}

func assertWABenchFailureNotScored(t *testing.T, db *database.DB, runID, failureID, outputStatus string) {
	t.Helper()
	var total, reviews, failures int
	var gate string
	err := db.QueryRow(`
		SELECT r.total_cases,
		       (SELECT COUNT(*) FROM wabench_reviews rv JOIN wabench_outputs o ON o.id = rv.output_pk WHERE o.run_pk = r.id),
		       (SELECT COUNT(*) FROM wabench_outputs o WHERE o.run_pk = r.id AND o.status = $2 AND o.failures @> $3::jsonb),
		       (SELECT decision FROM wabench_gate_decisions g WHERE g.run_pk = r.id ORDER BY g.created_at DESC LIMIT 1)
		FROM wabench_runs r WHERE r.run_id = $1
	`, runID, outputStatus, `[{"id":"`+failureID+`"}]`).Scan(&total, &reviews, &failures, &gate)
	if err != nil {
		t.Fatal(err)
	}
	if total != 20 || reviews != 19 || failures != 1 || gate != "fail" {
		t.Fatalf("total=%d reviews=%d failures=%d gate=%s", total, reviews, failures, gate)
	}
}
