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

func TestWABenchCenterNeverExposesPrivateContent(t *testing.T) {
	tests := []struct {
		storage string
		privacy string
		want    bool
	}{
		{storage: "inline_public", privacy: "synthetic", want: true},
		{storage: "inline_public", privacy: "private", want: false},
		{storage: "private_ref", privacy: "private", want: false},
		{storage: "hash_only", privacy: "anonymized", want: false},
	}
	for _, test := range tests {
		if got := WABenchContentAvailable(test.storage, test.privacy); got != test.want {
			t.Fatalf("WABenchContentAvailable(%q, %q) = %v, want %v", test.storage, test.privacy, got, test.want)
		}
	}
	if got := WABenchPrivacyLabel("public", "synthetic"); got != "public" {
		t.Fatalf("public label = %q", got)
	}
	if got := WABenchPrivacyLabel("private", "anonymized"); got != "redacted" {
		t.Fatalf("redacted label = %q", got)
	}
	if got := WABenchPrivacyLabel("private", "private"); got != "private" {
		t.Fatalf("private label = %q", got)
	}
	encoded, err := json.Marshal(WABenchCenterReview{ReviewID: "review-1", OutputID: "output-1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"evidence"`) || strings.Contains(string(encoded), `"content"`) {
		t.Fatalf("review projection exposed a free-text field: %s", encoded)
	}
}

func TestWABenchCenterArbitrationKeepsDisagreementVisible(t *testing.T) {
	reviews := []WABenchCenterReview{
		{OutputID: "out-1", ReviewerID: "reviewer-a", ReviewerType: "human", TaskCompliance: 5, SourceFidelity: 5, StructureReasoning: 4, StyleConsistency: 4, DirectUsability: 5, AcceptanceLabel: "direct_use"},
		{OutputID: "out-1", ReviewerID: "reviewer-b", ReviewerType: "human", TaskCompliance: 3, SourceFidelity: 3, StructureReasoning: 4, StyleConsistency: 4, DirectUsability: 3, AcceptanceLabel: "heavy_edit"},
		{OutputID: "out-2", ReviewerID: "reviewer-a", ReviewerType: "human", TaskCompliance: 4, SourceFidelity: 4, StructureReasoning: 4, StyleConsistency: 4, DirectUsability: 4, AcceptanceLabel: "light_edit"},
	}
	applyCenterArbitrationStatus(reviews)
	if reviews[0].ArbitrationStatus != "pending" || reviews[1].ArbitrationStatus != "pending" {
		t.Fatalf("disagreement status = %q/%q, want pending", reviews[0].ArbitrationStatus, reviews[1].ArbitrationStatus)
	}
	if reviews[2].ArbitrationStatus != "not_required" {
		t.Fatalf("single review status = %q", reviews[2].ArbitrationStatus)
	}

	reviews = append(reviews, WABenchCenterReview{
		OutputID: "out-1", ReviewerID: "reviewer-arbitrator", ReviewerType: "human", IsArbitration: true,
		TaskCompliance: 4, SourceFidelity: 4, StructureReasoning: 4, StyleConsistency: 4,
		DirectUsability: 4, AcceptanceLabel: "light_edit",
	})
	applyCenterArbitrationStatus(reviews)
	for _, index := range []int{0, 1, 3} {
		if reviews[index].ArbitrationStatus != "resolved" {
			t.Fatalf("review %d status = %q, want resolved", index, reviews[index].ArbitrationStatus)
		}
	}

	sameReviewer := []WABenchCenterReview{
		{OutputID: "out-3", ReviewerID: "reviewer-a", ReviewerType: "human", TaskCompliance: 5, AcceptanceLabel: "direct_use"},
		{OutputID: "out-3", ReviewerID: "reviewer-a", ReviewerType: "human", TaskCompliance: 2, AcceptanceLabel: "reject"},
	}
	applyCenterArbitrationStatus(sameReviewer)
	if sameReviewer[0].ArbitrationStatus != "not_required" {
		t.Fatalf("duplicate reviewer created a false disagreement: %q", sameReviewer[0].ArbitrationStatus)
	}
}

func validImportedHumanReview() WABenchHumanReviewImport {
	burden := 1
	return WABenchHumanReviewImport{
		ReviewID: "review-human-a", OutputID: "output-1", ReviewerID: "reviewer-a",
		ReviewerRole: "产品经理", ReviewMethod: "human_excel", LabelSource: "holdout_v1",
		IsBlind: true, TaskCompliance: 4, SourceFidelity: 4, StructureReasoning: 4,
		StyleConsistency: 4, DirectUsability: 4, AcceptanceLabel: "light_edit",
		ModificationBurden: &burden, PrimaryRootCause: "", ReviewedAt: time.Now().UTC(),
		Evidence: map[string]interface{}{"reviewStage": "initial"},
	}
}

func TestValidateImportedHumanReviewUsesCanonicalContract(t *testing.T) {
	review := validImportedHumanReview()
	if err := validateImportedHumanReview(review); err != nil {
		t.Fatalf("valid review rejected: %v", err)
	}

	review.TaskCompliance = 0
	if err := validateImportedHumanReview(review); err == nil {
		t.Fatal("legacy 0-1 score was accepted")
	}
	review = validImportedHumanReview()
	review.PrimaryRootCause = "network"
	if err := validateImportedHumanReview(review); err == nil {
		t.Fatal("noncanonical root cause was accepted")
	}
	review = validImportedHumanReview()
	review.AcceptanceLabel = "accepted"
	if err := validateImportedHumanReview(review); err == nil {
		t.Fatal("noncanonical acceptance label was accepted")
	}
}

func TestWABenchCenterReadModelsIntegration(t *testing.T) {
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
	ctx := context.Background()
	repo := NewWABenchRepo(db)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	suiteID := "center.integration." + suffix
	caseID := "center_case_" + suffix
	var suitePK, casePK string
	weights := `{"taskCompliance":25,"sourceFidelity":25,"structureReasoning":20,"styleConsistency":10,"directUsability":20}`
	err = db.QueryRowContext(ctx, `
		INSERT INTO wabench_suites (
			suite_id, version, name, description, partition, visibility, status,
			case_count, coverage, privacy, content_hash
		) VALUES ($1, 'v1.0.0', 'Center Integration', 'private masking test',
			'private_holdout', 'private', 'active', 1,
			'{"taskTypes":["writing"]}', '{"privacyLevel":"private","allowsRawText":false}', $2)
		RETURNING id::text
	`, suiteID, "sha256:"+strings.Repeat("a", 64)).Scan(&suitePK)
	if err != nil {
		t.Fatal(err)
	}
	err = db.QueryRowContext(ctx, `
		INSERT INTO wabench_cases (
			case_id, suite_pk, task_type, difficulty, input_storage, input_ref, input_hash,
			context, source_mode, expected_behavior, rubric_weights, privacy_level
		) VALUES ($1, $2, 'writing', 'L2', 'private_ref', $3, $4,
			'{}', 'none', 'answer', $5::jsonb, 'private')
		RETURNING id::text
	`, caseID, suitePK, "vault://holdout/"+caseID, "sha256:"+strings.Repeat("b", 64), weights).Scan(&casePK)
	if err != nil {
		t.Fatal(err)
	}
	candidateID := "center_candidate_" + suffix
	_, err = repo.UpsertCandidate(ctx, WABenchCandidateDraft{
		CandidateID: candidateID, Name: "Center Candidate",
		PromptHash:    "sha256:" + strings.Repeat("c", 64),
		ModelManifest: map[string]interface{}{"provider": "fake", "model": "fake-v1"},
		CodeHash:      "sha256:" + strings.Repeat("d", 64), ToolManifest: map[string]interface{}{},
		FeatureFlags: map[string]interface{}{"memoryEnabled": false, "sourceEvidenceGateEnabled": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.CreateRun(ctx, suiteID, candidateID, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.StartRun(ctx, run.RunID); err != nil {
		t.Fatal(err)
	}
	outputID := "center_output_" + suffix
	if err := repo.SaveCaseResult(ctx, run.PK, casePK, WABenchOutputWrite{
		OutputID: outputID, Status: "complete", OutputHash: "sha256:" + strings.Repeat("e", 64),
		TextStorage: "hash_only", Failures: []map[string]interface{}{},
		Metrics: map[string]interface{}{"latencyMs": 1200, "totalTokens": 500},
		Routing: map[string]interface{}{"knowledgeProvider": "lexiang"}, TraceRef: "trace:" + suffix,
		Checks: []WABenchCheckWrite{{CheckID: "routing.knowledge_only_no_websearch", Status: "pass", Severity: "critical", Evidence: map[string]interface{}{}}},
		Review: &WABenchReviewWrite{
			ReviewID: "center_model_review_" + suffix, ReviewerID: "judge", ReviewerRole: "quality_judge",
			ReviewerType: "model", ReviewMethod: "llm_judge", LabelSource: "wabench",
			IsBlind: true, TaskCompliance: 4, SourceFidelity: 4, StructureReasoning: 4,
			StyleConsistency: 4, DirectUsability: 4, AcceptanceLabel: "unknown",
			HardFailureIDs: []string{}, SecondaryRootCauses: []string{}, Evidence: map[string]interface{}{}, ReviewedAt: time.Now().UTC(),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteRun(ctx, run.RunID, "completed"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveGateDecision(ctx, run.PK, WABenchGateDecisionWrite{
		DecisionID: "center_gate_" + suffix, Decision: "pass", Evidence: map[string]interface{}{"qualityPassed": true},
		Exceptions: []map[string]interface{}{}, RollbackConditions: []map[string]interface{}{{"metric": "hardFailureRate", "threshold": 0}},
		OwnerRef: "eval-owner", DecidedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	humanA := validImportedHumanReview()
	humanA.ReviewID = "center_human_a_" + suffix
	humanA.OutputID = outputID
	humanB := validImportedHumanReview()
	humanB.ReviewID = "center_human_b_" + suffix
	humanB.OutputID = outputID
	humanB.ReviewerID = "reviewer-b"
	humanB.SourceFidelity = 2
	humanB.AcceptanceLabel = "heavy_edit"
	arbitration := validImportedHumanReview()
	arbitration.ReviewID = "center_arbitration_" + suffix
	arbitration.OutputID = outputID
	arbitration.ReviewerID = "reviewer-arbitrator"
	arbitration.ReviewerRole = "仲裁人"
	arbitration.Evidence = map[string]interface{}{"isArbitration": true, "reviewStage": "arbitration"}
	if err := repo.InsertHumanReviews(ctx, []WABenchHumanReviewImport{humanA, humanB, arbitration}); err != nil {
		t.Fatal(err)
	}

	overview, err := repo.GetCenterOverview(ctx)
	if err != nil || overview.SuiteCount < 1 || overview.LatestGateDecision != "pass" {
		t.Fatalf("overview = %+v, err = %v", overview, err)
	}
	suites, err := repo.ListCenterSuites(ctx, WABenchCenterListLimit)
	if err != nil {
		t.Fatal(err)
	}
	foundSuite := false
	for _, item := range suites {
		if item.SuiteID == suiteID {
			foundSuite = item.PrivacyLabel == "private" && item.CaseCount == 1
		}
	}
	if !foundSuite {
		t.Fatal("private suite projection missing or unsafe")
	}
	reviews, err := repo.ListCenterReviews(ctx, run.RunID, WABenchCenterListLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 4 {
		t.Fatalf("reviews = %d, want 4", len(reviews))
	}
	for _, review := range reviews {
		if review.ContentAvailable || review.PrivacyLevel != "private" {
			t.Fatalf("private review leaked content availability: %+v", review)
		}
		if review.ArbitrationStatus != "resolved" {
			t.Fatalf("arbitration status = %s, want resolved", review.ArbitrationStatus)
		}
	}
	releases, err := repo.ListCenterReleases(ctx, WABenchCenterListLimit)
	if err != nil || len(releases) == 0 || releases[0].Decision == "" {
		t.Fatalf("releases = %+v, err = %v", releases, err)
	}
}
