package database

import (
	"strings"
	"testing"
)

func TestWABenchPartitionsMatchCanonicalContract(t *testing.T) {
	want := []string{"development", "live_probe", "private_holdout", "public_holdout", "red_team"}
	got := SortedWABenchPartitions()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("partitions = %v, want %v", got, want)
	}
	for _, partition := range want {
		if err := ValidateWABenchPartition(partition); err != nil {
			t.Fatalf("canonical partition %s rejected: %v", partition, err)
		}
	}
	if err := ValidateWABenchPartition("migration_candidate"); err == nil {
		t.Fatal("migration_candidate must be a status, not a canonical partition")
	}
}

func TestLegacyImportOptionsKeepProductDataPrivate(t *testing.T) {
	options, err := normalizeLegacyImportOptions(LegacyImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if options.Partition != "development" || options.Visibility != "private" || options.PrivacyLevel != "private" {
		t.Fatalf("unexpected defaults: %+v", options)
	}
	_, err = normalizeLegacyImportOptions(LegacyImportOptions{Visibility: "public", PrivacyLevel: "synthetic"})
	if err == nil || !strings.Contains(err.Error(), "must remain private") {
		t.Fatalf("expected private-only error, got %v", err)
	}
}

func TestRuleProfileRefsSupportThreeBuiltinsAndVersionedUserStyles(t *testing.T) {
	for _, slug := range []string{"yinyue", "shenlun", "xiaohongshu"} {
		ref, err := BuiltinWABenchRuleProfileRef(slug)
		if err != nil {
			t.Fatalf("builtin style %s rejected: %v", slug, err)
		}
		if ref != "luminbuddy.builtin-style."+slug {
			t.Fatalf("builtin ref = %s", ref)
		}
	}
	if _, err := BuiltinWABenchRuleProfileRef("user_custom"); err == nil {
		t.Fatal("custom style must not be misclassified as one of the three builtins")
	}
	ref, err := UserWABenchRuleProfileRef("123e4567-e89b-12d3-a456-426614174000", 3)
	if err != nil {
		t.Fatal(err)
	}
	if ref != "luminbuddy.user-style.123e4567e89b12d3a456426614174000.v3" {
		t.Fatalf("user style ref = %s", ref)
	}
	if _, err := UserWABenchRuleProfileRef("not-a-uuid", 1); err == nil {
		t.Fatal("invalid user style id accepted")
	}
}

func TestMapLegacyEvaluationSetPreservesIdentityAsMigrationCandidate(t *testing.T) {
	legacy := EvaluationSet{
		ID: "11111111-2222-3333-4444-555555555555", Name: "旧风格评测集",
		StyleSlug: "yinyue", Description: "legacy", SampleCount: 25,
	}
	draft, err := MapLegacyEvaluationSet(legacy, LegacyImportOptions{Partition: "development"})
	if err != nil {
		t.Fatal(err)
	}
	if draft.SuiteID != "luminbuddy.migration.yinyue.111111112222" {
		t.Fatalf("suite id = %s", draft.SuiteID)
	}
	if draft.Status != "migration_candidate" || draft.LegacySetID != legacy.ID {
		t.Fatalf("legacy identity/status not preserved: %+v", draft)
	}
	if draft.Privacy["publicationPolicy"] != "aggregate_only" || draft.Privacy["allowsRawText"] != false {
		t.Fatalf("unsafe privacy policy: %+v", draft.Privacy)
	}
	if len(draft.MigrationWarnings) != 1 || !draft.MigrationWarnings[0].RequiresReview {
		t.Fatalf("expected style grouping warning: %+v", draft.MigrationWarnings)
	}
}

func TestMapLegacyEvaluationSampleKeepsScoreDiagnosticAndInputByReference(t *testing.T) {
	length := 900
	legacy := EvaluationSample{
		ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", SetID: "11111111-2222-3333-4444-555555555555",
		Topic: "数字政府", InputPrompt: "给定材料后写一篇文章", StyleSlug: "shenlun",
		ExpectedStructure: map[string]interface{}{"sections": float64(3)},
		ExpectedKeywords:  []string{"服务", "协同"}, ExpectedLength: &length,
		RedFlags:        []string{"不得编造数据"},
		ScoringCriteria: map[string]interface{}{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1},
	}
	draft, err := MapLegacyEvaluationSample(legacy, LegacyImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if draft.CaseID != "legacy_eval_aaaaaaaabbbbccccddddeeeeeeeeeeee" {
		t.Fatalf("case id = %s", draft.CaseID)
	}
	if draft.InputStorage != "private_ref" || draft.InputRef != "legacy:evaluation_samples/"+legacy.ID {
		t.Fatalf("legacy input was not kept by private reference: %+v", draft)
	}
	if draft.InputHash != sha256String(legacy.InputPrompt) {
		t.Fatalf("input hash mismatch: %s", draft.InputHash)
	}
	if draft.TaskType != "writing" || draft.Difficulty != "L2" || draft.PrivacyLevel != "private" {
		t.Fatalf("unexpected normalized fields: %+v", draft)
	}
	weightTotal := 0
	for _, weight := range draft.RubricWeights {
		weightTotal += weight
	}
	if weightTotal != 100 || len(draft.RubricWeights) != 5 {
		t.Fatalf("invalid canonical weights: %+v", draft.RubricWeights)
	}
	if len(draft.RuleProfileRefs) != 1 || draft.RuleProfileRefs[0] != "luminbuddy.builtin-style.shenlun" {
		t.Fatalf("builtin style was not mapped to its rule profile: %+v", draft.RuleProfileRefs)
	}
	if draft.LegacyScore["factuality"] != 0.3 {
		t.Fatalf("legacy score was not preserved: %+v", draft.LegacyScore)
	}
	draft.LegacyScore["factuality"] = 1.0
	if legacy.ScoringCriteria["factuality"] != 0.3 {
		t.Fatal("legacy score map was mutated")
	}
	codes := map[string]bool{}
	for _, warning := range draft.MigrationWarnings {
		codes[warning.Code] = true
	}
	for _, code := range []string{
		"LEGACY_TASK_TYPE_INFERRED", "LEGACY_DIFFICULTY_DEFAULTED", "LEGACY_INPUT_KEPT_BY_REFERENCE",
		"LEGACY_SCORE_DIAGNOSTIC_ONLY", "LEGACY_STYLE_MAPPED_TO_RULE_PROFILE", "LEGACY_RED_FLAGS_REQUIRE_MAPPING",
	} {
		if !codes[code] {
			t.Errorf("missing migration warning %s", code)
		}
	}
}

func TestLegacyCustomStyleRequiresManualOwnerAndVersionBinding(t *testing.T) {
	legacy := EvaluationSample{
		ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", InputPrompt: "写一篇文章", StyleSlug: "my-column",
		ScoringCriteria: map[string]interface{}{},
	}
	draft, err := MapLegacyEvaluationSample(legacy, LegacyImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if draft.RuleProfileRefs[0] != "luminbuddy.legacy-style.my-column" {
		t.Fatalf("unexpected unresolved legacy ref: %v", draft.RuleProfileRefs)
	}
	found := false
	for _, warning := range draft.MigrationWarnings {
		if warning.Code == "LEGACY_CUSTOM_STYLE_UNRESOLVED" && warning.RequiresReview {
			found = true
		}
	}
	if !found {
		t.Fatal("custom style ambiguity must produce a review warning")
	}
}

func TestWABenchMigrationCreatesParallelSchemaWithoutRewritingLegacyTables(t *testing.T) {
	upBytes, err := migrationFS.ReadFile("migrations/063_wabench_data_layer.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	downBytes, err := migrationFS.ReadFile("migrations/063_wabench_data_layer.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := string(upBytes)
	down := string(downBytes)
	for _, table := range []string{
		"wabench_suites", "wabench_source_fixtures", "wabench_cases", "wabench_candidates",
		"wabench_runs", "wabench_outputs", "wabench_checks", "wabench_reviews",
		"wabench_outcomes", "wabench_gate_decisions",
	} {
		if !strings.Contains(up, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("up migration does not create %s", table)
		}
		if !strings.Contains(down, "DROP TABLE IF EXISTS "+table) {
			t.Errorf("down migration does not drop %s", table)
		}
	}
	for _, legacy := range []string{"evaluation_sets", "evaluation_samples", "evaluation_runs"} {
		if strings.Contains(strings.ToUpper(up), "ALTER TABLE "+strings.ToUpper(legacy)) ||
			strings.Contains(strings.ToUpper(up), "DROP TABLE "+strings.ToUpper(legacy)) ||
			strings.Contains(strings.ToUpper(up), "COMMENT ON TABLE "+strings.ToUpper(legacy)) ||
			strings.Contains(strings.ToUpper(down), "DROP TABLE IF EXISTS "+strings.ToUpper(legacy)) {
			t.Errorf("migration mutates legacy table %s", legacy)
		}
	}
	for _, partition := range SortedWABenchPartitions() {
		if !strings.Contains(up, "'"+partition+"'") {
			t.Errorf("migration missing partition %s", partition)
		}
	}
	if !strings.Contains(up, "legacy_score") || !strings.Contains(up, "migration_warnings") {
		t.Fatal("migration must preserve legacy score and warnings")
	}
}

func TestLegacySeedInventoryRemains65Samples(t *testing.T) {
	seed, err := migrationFS.ReadFile("migrations/011_evaluation_seed.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	supplement, err := migrationFS.ReadFile("migrations/018_evaluation_seed_supplement.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	count := strings.Count(string(seed), "INSERT INTO evaluation_samples") + strings.Count(string(supplement), "INSERT INTO evaluation_samples")
	if count != 65 {
		t.Fatalf("legacy seed count = %d, want 65", count)
	}
}
