package services

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
)

// fakeRejudgeStore is an in-memory WABenchRejudgeStore.
type fakeRejudgeStore struct {
	candidates []database.WABenchRejudgeCandidate
	saved      []savedRejudgeReview
}

type savedRejudgeReview struct {
	outputPK string
	review   database.WABenchReviewWrite
}

func (s *fakeRejudgeStore) ListRejudgeableOutputs(_ context.Context, _ string) ([]database.WABenchRejudgeCandidate, error) {
	return s.candidates, nil
}

func (s *fakeRejudgeStore) SaveRejudgeReview(_ context.Context, outputPK string, review database.WABenchReviewWrite) error {
	s.saved = append(s.saved, savedRejudgeReview{outputPK: outputPK, review: review})
	return nil
}

func rejudgeCandidate() database.WABenchRejudgeCandidate {
	return database.WABenchRejudgeCandidate{
		OutputPK:   "out-pk-1",
		OutputID:   "output_1",
		OutputText: "## 冻结正文\n\n这是一段已经生成的完整文章。",
		Failures: []map[string]interface{}{
			{"id": "judge.failed", "stage": "judge", "symptom": "五项 Rubric Judge 失败", "rootCause": "model", "detail": "old"},
			{"id": "routing.boundary_violation", "stage": "routing", "symptom": "来源路由越界", "rootCause": "retrieval", "detail": ""},
		},
		Routing: map[string]interface{}{},
		CaseID:  "case_1",
		Case: database.WABenchCase{
			PK:            "case-pk-1",
			CaseID:        "case_1",
			RubricWeights: map[string]int{"taskCompliance": 25, "sourceFidelity": 25, "structureReasoning": 15, "styleConsistency": 15, "directUsability": 20},
		},
		Suite:     database.WABenchSuite{Partition: "development"},
		Candidate: database.WABenchCandidate{ModelManifest: map[string]interface{}{"model": "test-model"}},
	}
}

func TestWABenchRejudgeWritesMarkedReviewFromFrozenText(t *testing.T) {
	store := &fakeRejudgeStore{candidates: []database.WABenchRejudgeCandidate{rejudgeCandidate()}}
	svc := NewWABenchRejudgeService(store, fakeWABenchJudge{})

	outcomes, err := svc.RejudgeJudgeFailures(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].Status != "rejudged" {
		t.Fatalf("outcomes = %+v, want one rejudged", outcomes)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved reviews = %d, want 1", len(store.saved))
	}
	saved := store.saved[0]
	if saved.outputPK != "out-pk-1" {
		t.Fatalf("review saved for %s, want out-pk-1", saved.outputPK)
	}
	if !saved.review.IsBlind || saved.review.ReviewMethod != "llm_as_judge" {
		t.Fatalf("rejudge review must stay a blind LLM review: %+v", saved.review)
	}
	rejudgeMeta, ok := saved.review.Evidence["rejudge"].(map[string]interface{})
	if !ok {
		t.Fatalf("review evidence is missing the rejudge marker: %+v", saved.review.Evidence)
	}
	if rejudgeMeta["source"] != "judge_failure_rejudge" || rejudgeMeta["runId"] != "run-1" {
		t.Fatalf("rejudge provenance incomplete: %+v", rejudgeMeta)
	}
	if saved.review.Evidence["weightedScore"] != 100.0 {
		t.Fatalf("weightedScore = %v, want 100", saved.review.Evidence["weightedScore"])
	}
}

func TestWABenchRejudgeExcludesOriginalJudgeFailuresFromHardFailureIDs(t *testing.T) {
	store := &fakeRejudgeStore{candidates: []database.WABenchRejudgeCandidate{rejudgeCandidate()}}
	svc := NewWABenchRejudgeService(store, fakeWABenchJudge{})

	if _, err := svc.RejudgeJudgeFailures(context.Background(), "run-1"); err != nil {
		t.Fatal(err)
	}
	saved := store.saved[0]
	ids := strings.Join(saved.review.HardFailureIDs, ",")
	if strings.Contains(ids, "judge.failed") {
		t.Fatalf("the original run's judge failure leaked into hard failure ids: %v", saved.review.HardFailureIDs)
	}
	if ids != "routing.boundary_violation" {
		t.Fatalf("hard failure ids = %v, want [routing.boundary_violation]", saved.review.HardFailureIDs)
	}
}

func TestWABenchRejudgeStillFailedLeavesWorklistUntouched(t *testing.T) {
	candidate := rejudgeCandidate()
	// fakeWABenchJudge fails when the article mentions "case=case_1".
	candidate.OutputText = "冻结正文 case=case_1"
	store := &fakeRejudgeStore{candidates: []database.WABenchRejudgeCandidate{candidate}}
	svc := NewWABenchRejudgeService(store, fakeWABenchJudge{failCase: "case_1"})

	outcomes, err := svc.RejudgeJudgeFailures(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].Status != "still_failed" {
		t.Fatalf("outcomes = %+v, want one still_failed", outcomes)
	}
	if len(store.saved) != 0 {
		t.Fatalf("a failed rejudge must not write a review, saved = %d", len(store.saved))
	}
}

func TestWABenchRejudgeSkipsEmptyFrozenText(t *testing.T) {
	candidate := rejudgeCandidate()
	candidate.OutputText = "  "
	store := &fakeRejudgeStore{candidates: []database.WABenchRejudgeCandidate{candidate}}
	svc := NewWABenchRejudgeService(store, fakeWABenchJudge{})

	outcomes, err := svc.RejudgeJudgeFailures(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].Status != "skipped_empty_text" {
		t.Fatalf("outcomes = %+v, want skipped_empty_text", outcomes)
	}
	if len(store.saved) != 0 {
		t.Fatal("no review should be written for empty frozen text")
	}
}

// TestWABenchRejudgeEndToEndIntegration exercises the real repository SQL
// (worklist query + marked-review upsert) against PostgreSQL. Set
// WABENCH_TEST_DATABASE_URL to run it; it is skipped otherwise so it never
// touches a live database.
func TestWABenchRejudgeEndToEndIntegration(t *testing.T) {
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

	// Pass 1: the judge fails for one case, leaving a frozen output without a
	// valid review.
	suite, err := NewWABenchEvaluationService(repo, fakeWABenchAgent{}, fakeWABenchJudge{}).EnsureDefaultRedTeamSuite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cases, err := repo.ListCases(context.Background(), suite.PK)
	if err != nil || len(cases) == 0 {
		t.Fatalf("red-team cases = %d, err = %v", len(cases), err)
	}
	seedService := NewWABenchEvaluationService(repo, fakeWABenchAgent{}, fakeWABenchJudge{failCase: cases[0].CaseID})
	failedCase := cases[0]
	hash := "sha256:" + strings.Repeat("c", 64)
	candidateID := "rejudge_it_" + strings.ReplaceAll(failedCase.CaseID, "-", "_")
	if _, err = repo.UpsertCandidate(context.Background(), database.WABenchCandidateDraft{
		CandidateID: candidateID, Name: "rejudge", PromptHash: hash,
		ModelManifest: map[string]interface{}{"provider": "fake", "model": "fake"},
		CodeHash:      hash, ToolManifest: map[string]interface{}{},
		FeatureFlags: map[string]interface{}{"memoryEnabled": false},
	}); err != nil {
		t.Fatal(err)
	}
	// Make the suite public (test DB only) so applyWABenchTextStorage stores
	// the frozen article inline — the same shape the ablation suite has, and
	// the only shape the frozen-text rejudge can read. Restore on cleanup so
	// the red-team suite stays immutable for other tests.
	if _, err = db.Exec(`UPDATE wabench_suites SET visibility = 'public' WHERE id = $1`, suite.PK); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`UPDATE wabench_suites SET visibility = 'private' WHERE id = $1`, suite.PK); err != nil {
			t.Logf("restore red-team suite visibility: %v", err)
		}
	})
	brokenRun, err := seedService.CreateRun(context.Background(), WABenchRunRequest{SuiteID: suite.SuiteID, CandidateID: candidateID, Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := seedService.ExecuteRun(context.Background(), brokenRun.RunID); err != nil {
		t.Fatal(err)
	}

	// The run produced one output without a review (judge infra failure).
	worklist, err := repo.ListRejudgeableOutputs(context.Background(), brokenRun.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(worklist) != 1 {
		t.Fatalf("rejudge worklist = %d, want 1 (the judge-failed case with frozen text)", len(worklist))
	}
	if strings.Contains(strings.Join(worklistFailureStages(worklist[0]), ","), "judge") {
		t.Fatalf("judge-stage failure should exist on the frozen output: %+v", worklist[0].Failures)
	}

	// Pass 2: the judge now works; rejudge against the frozen text.
	fixedService := NewWABenchRejudgeService(repo, fakeWABenchJudge{})
	outcomes, err := fixedService.RejudgeJudgeFailures(context.Background(), brokenRun.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].Status != "rejudged" {
		t.Fatalf("outcomes = %+v, want one rejudged", outcomes)
	}

	// Idempotency: the worklist is now empty and a second pass is a no-op.
	secondPass, err := fixedService.RejudgeJudgeFailures(context.Background(), brokenRun.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondPass) != 0 {
		t.Fatalf("second rejudge pass should be a no-op, got %+v", secondPass)
	}

	// The output row itself was never rewritten: status stays "partial" with
	// the original judge.failed failure code, while a new marked review exists.
	var outputStatus string
	var judgeFailures, reviews, rejudgeReviews int
	if err := db.QueryRow(`
		SELECT o.status,
		       (SELECT COUNT(*) FROM wabench_outputs o2 WHERE o2.id = o.id AND o2.failures @> '[{"id":"judge.failed"}]'::jsonb),
		       (SELECT COUNT(*) FROM wabench_reviews rv WHERE rv.output_pk = o.id),
		       (SELECT COUNT(*) FROM wabench_reviews rv WHERE rv.output_pk = o.id AND rv.evidence ? 'rejudge')
		FROM wabench_outputs o
		JOIN wabench_runs r ON r.id = o.run_pk
		WHERE r.run_id = $1
	`, brokenRun.RunID).Scan(&outputStatus, &judgeFailures, &reviews, &rejudgeReviews); err != nil {
		t.Fatal(err)
	}
	if outputStatus != "partial" || judgeFailures != 1 {
		t.Fatalf("output was rewritten: status=%s judgeFailures=%d", outputStatus, judgeFailures)
	}
	if reviews != 1 || rejudgeReviews != 1 {
		t.Fatalf("reviews=%d rejudgeReviews=%d, want exactly one marked review", reviews, rejudgeReviews)
	}
}

func worklistFailureStages(item database.WABenchRejudgeCandidate) []string {
	stages := []string{}
	for _, failure := range item.Failures {
		stage, _ := failure["stage"].(string)
		stages = append(stages, stage)
	}
	return stages
}
