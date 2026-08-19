package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const WABenchSchemaVersion = "wabench.v1"

var wabenchPartitions = map[string]struct{}{
	"development":     {},
	"public_holdout":  {},
	"private_holdout": {},
	"red_team":        {},
	"live_probe":      {},
}

var builtinStyleSlugs = map[string]struct{}{
	"yinyue":      {},
	"shenlun":     {},
	"xiaohongshu": {},
}

var nonIDChars = regexp.MustCompile(`[^a-z0-9_-]+`)

type WABenchMigrationWarning struct {
	Code           string `json:"code"`
	LegacyType     string `json:"legacy_type"`
	LegacyID       string `json:"legacy_id"`
	Field          string `json:"field,omitempty"`
	Message        string `json:"message"`
	RequiresReview bool   `json:"requires_review"`
}

type LegacyImportOptions struct {
	Partition    string
	Visibility   string
	PrivacyLevel string
}

type LegacyImportReport struct {
	SchemaVersion   string                    `json:"schema_version"`
	Partition       string                    `json:"partition"`
	SuitesProcessed int                       `json:"suites_processed"`
	CasesProcessed  int                       `json:"cases_processed"`
	Warnings        []WABenchMigrationWarning `json:"warnings"`
	StartedAt       time.Time                 `json:"started_at"`
	CompletedAt     time.Time                 `json:"completed_at"`
}

type WABenchSuiteDraft struct {
	SuiteID           string
	Version           string
	Name              string
	Description       string
	Partition         string
	Visibility        string
	Status            string
	Coverage          map[string]interface{}
	Privacy           map[string]interface{}
	LegacySetID       string
	MigrationWarnings []WABenchMigrationWarning
}

type WABenchCaseDraft struct {
	CaseID            string
	TaskType          string
	Difficulty        string
	InputStorage      string
	InputRef          string
	InputHash         string
	Context           map[string]interface{}
	SourceMode        string
	SourceFixtureRefs []string
	ExpectedBehavior  string
	MustHave          []string
	MustNotHave       []string
	HardGateIDs       []string
	RubricWeights     map[string]int
	CapabilityTags    []string
	RiskTags          []string
	RuleProfileRefs   []string
	PrivacyLevel      string
	LegacySampleID    string
	LegacyScore       map[string]interface{}
	MigrationWarnings []WABenchMigrationWarning
}

type WABenchRepo struct {
	db *DB
}

func NewWABenchRepo(db *DB) *WABenchRepo {
	return &WABenchRepo{db: db}
}

func ValidateWABenchPartition(partition string) error {
	if _, ok := wabenchPartitions[partition]; !ok {
		return fmt.Errorf("invalid WABench partition %q", partition)
	}
	return nil
}

func normalizeLegacyImportOptions(options LegacyImportOptions) (LegacyImportOptions, error) {
	if options.Partition == "" {
		options.Partition = "development"
	}
	if err := ValidateWABenchPartition(options.Partition); err != nil {
		return LegacyImportOptions{}, err
	}
	if options.Visibility == "" {
		options.Visibility = "private"
	}
	if options.Visibility != "public" && options.Visibility != "private" {
		return LegacyImportOptions{}, fmt.Errorf("invalid WABench visibility %q", options.Visibility)
	}
	if options.PrivacyLevel == "" {
		options.PrivacyLevel = "private"
	}
	if options.PrivacyLevel != "synthetic" && options.PrivacyLevel != "anonymized" && options.PrivacyLevel != "private" {
		return LegacyImportOptions{}, fmt.Errorf("invalid WABench privacy level %q", options.PrivacyLevel)
	}
	if options.Visibility != "private" {
		return LegacyImportOptions{}, fmt.Errorf("legacy evaluation imports must remain private")
	}
	return options, nil
}

func safeWABenchID(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = nonIDChars.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return fallback
	}
	if value[0] < 'a' || value[0] > 'z' {
		value = fallback + "_" + value
	}
	if len(value) > 120 {
		value = value[:120]
	}
	return value
}

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func compactUUID(value string) string {
	return strings.ReplaceAll(strings.ToLower(value), "-", "")
}

func shortLegacyID(value string) string {
	compact := compactUUID(value)
	if len(compact) >= 12 {
		return compact[:12]
	}
	return strings.TrimPrefix(sha256String(value), "sha256:")[:12]
}

func BuiltinWABenchRuleProfileRef(styleSlug string) (string, error) {
	styleSlug = safeWABenchID(styleSlug, "")
	if _, ok := builtinStyleSlugs[styleSlug]; !ok {
		return "", fmt.Errorf("unknown builtin style %q", styleSlug)
	}
	return "luminbuddy.builtin-style." + styleSlug, nil
}

func UserWABenchRuleProfileRef(profileID string, version int) (string, error) {
	parsed, err := uuid.Parse(profileID)
	if err != nil {
		return "", fmt.Errorf("invalid user style profile id: %w", err)
	}
	if version < 1 {
		return "", fmt.Errorf("user style profile version must be positive")
	}
	return fmt.Sprintf("luminbuddy.user-style.%s.v%d", strings.ReplaceAll(parsed.String(), "-", ""), version), nil
}

func legacyRuleProfileRef(styleSlug string) (string, bool) {
	styleSlug = safeWABenchID(styleSlug, "legacy_style")
	if _, ok := builtinStyleSlugs[styleSlug]; ok {
		return "luminbuddy.builtin-style." + styleSlug, true
	}
	return "luminbuddy.legacy-style." + styleSlug, false
}

func MapLegacyEvaluationSet(set EvaluationSet, options LegacyImportOptions) (WABenchSuiteDraft, error) {
	options, err := normalizeLegacyImportOptions(options)
	if err != nil {
		return WABenchSuiteDraft{}, err
	}
	styleSlug := safeWABenchID(set.StyleSlug, "legacy_style")
	_, isBuiltinStyle := legacyRuleProfileRef(styleSlug)
	warnings := []WABenchMigrationWarning{
		{
			Code: "LEGACY_STYLE_ONLY_GROUPING", LegacyType: "evaluation_set", LegacyID: set.ID,
			Field: "style_slug", Message: "Legacy style grouping was preserved as a rule profile reference, not as the WABench task partition.", RequiresReview: true,
		},
	}
	if !isBuiltinStyle {
		warnings = append(warnings, WABenchMigrationWarning{
			Code: "LEGACY_CUSTOM_STYLE_UNRESOLVED", LegacyType: "evaluation_set", LegacyID: set.ID,
			Field: "style_slug", Message: "Legacy custom style slug is ambiguous without an owner and immutable version; bind it to a user-style profile before promotion.", RequiresReview: true,
		})
	}
	return WABenchSuiteDraft{
		SuiteID:     fmt.Sprintf("luminbuddy.migration.%s.%s", strings.ReplaceAll(styleSlug, "_", "-"), shortLegacyID(set.ID)),
		Version:     "v1.0.0",
		Name:        set.Name,
		Description: set.Description,
		Partition:   options.Partition,
		Visibility:  options.Visibility,
		Status:      "migration_candidate",
		Coverage: map[string]interface{}{
			"taskTypes":    []string{"writing"},
			"capabilities": []string{"long_form_writing", "style_control"},
			"riskTags":     []string{"legacy.unreviewed_mapping"},
		},
		Privacy: map[string]interface{}{
			"allowsRawText":     false,
			"publicationPolicy": "aggregate_only",
		},
		LegacySetID:       set.ID,
		MigrationWarnings: warnings,
	}, nil
}

func MapLegacyEvaluationSample(sample EvaluationSample, options LegacyImportOptions) (WABenchCaseDraft, error) {
	options, err := normalizeLegacyImportOptions(options)
	if err != nil {
		return WABenchCaseDraft{}, err
	}
	if strings.TrimSpace(sample.ID) == "" || strings.TrimSpace(sample.InputPrompt) == "" {
		return WABenchCaseDraft{}, fmt.Errorf("legacy sample id and input prompt are required")
	}
	styleSlug := safeWABenchID(sample.StyleSlug, "legacy_style")
	ruleProfileRef, isBuiltinStyle := legacyRuleProfileRef(styleSlug)
	warnings := []WABenchMigrationWarning{
		{Code: "LEGACY_TASK_TYPE_INFERRED", LegacyType: "evaluation_sample", LegacyID: sample.ID, Field: "task_type", Message: "Task type was inferred as writing from the legacy collection.", RequiresReview: true},
		{Code: "LEGACY_DIFFICULTY_DEFAULTED", LegacyType: "evaluation_sample", LegacyID: sample.ID, Field: "difficulty", Message: "Difficulty defaulted to L2 because the legacy schema had no difficulty field.", RequiresReview: true},
		{Code: "LEGACY_INPUT_KEPT_BY_REFERENCE", LegacyType: "evaluation_sample", LegacyID: sample.ID, Field: "input", Message: "Private input remains in evaluation_samples and is referenced instead of copied.", RequiresReview: false},
		{Code: "LEGACY_SCORE_DIAGNOSTIC_ONLY", LegacyType: "evaluation_sample", LegacyID: sample.ID, Field: "legacy_score", Message: "Legacy 0-1 scoring criteria are preserved for diagnostics and must not affect WABench release gates.", RequiresReview: true},
		{Code: "LEGACY_STYLE_MAPPED_TO_RULE_PROFILE", LegacyType: "evaluation_sample", LegacyID: sample.ID, Field: "rule_profile_refs", Message: "Legacy style_slug was mapped to a Luminbuddy private rule profile reference.", RequiresReview: true},
	}
	if len(sample.RedFlags) > 0 {
		warnings = append(warnings, WABenchMigrationWarning{
			Code: "LEGACY_RED_FLAGS_REQUIRE_MAPPING", LegacyType: "evaluation_sample", LegacyID: sample.ID,
			Field: "risk_tags", Message: "Legacy free-text red flags require manual mapping to WABench risk tags and hard gates.", RequiresReview: true,
		})
	}
	if !isBuiltinStyle {
		warnings = append(warnings, WABenchMigrationWarning{
			Code: "LEGACY_CUSTOM_STYLE_UNRESOLVED", LegacyType: "evaluation_sample", LegacyID: sample.ID,
			Field: "rule_profile_refs", Message: "Legacy custom style slug cannot identify its owner or immutable version and must be manually rebound.", RequiresReview: true,
		})
	}

	contextData := map[string]interface{}{
		"legacyTopic":             sample.Topic,
		"legacyStyleSlug":         sample.StyleSlug,
		"legacyExpectedStructure": sample.ExpectedStructure,
		"legacyExpectedKeywords":  sample.ExpectedKeywords,
		"legacyExpectedLength":    sample.ExpectedLength,
		"legacyRedFlags":          sample.RedFlags,
	}
	riskTags := []string{"legacy.unreviewed_mapping"}
	if len(sample.RedFlags) > 0 {
		riskTags = append(riskTags, "legacy.red_flag")
	}
	mustHave := make([]string, 0, len(sample.ExpectedKeywords)+1)
	if sample.ExpectedLength != nil {
		mustHave = append(mustHave, fmt.Sprintf("目标长度约 %d 字；作为检查项验证，不计入主观分数", *sample.ExpectedLength))
	}
	for _, keyword := range sample.ExpectedKeywords {
		mustHave = append(mustHave, "覆盖给定关键词："+keyword)
	}

	return WABenchCaseDraft{
		CaseID:            "legacy_eval_" + compactUUID(sample.ID),
		TaskType:          "writing",
		Difficulty:        "L2",
		InputStorage:      "private_ref",
		InputRef:          "legacy:evaluation_samples/" + sample.ID,
		InputHash:         sha256String(sample.InputPrompt),
		Context:           contextData,
		SourceMode:        "none",
		SourceFixtureRefs: []string{},
		ExpectedBehavior:  "answer",
		MustHave:          mustHave,
		MustNotHave:       append([]string(nil), sample.RedFlags...),
		HardGateIDs:       []string{},
		RubricWeights: map[string]int{
			"taskCompliance": 25, "sourceFidelity": 25, "structureReasoning": 15,
			"styleConsistency": 15, "directUsability": 20,
		},
		CapabilityTags:    []string{"constraint_following", "long_form_writing", "style_control"},
		RiskTags:          riskTags,
		RuleProfileRefs:   []string{ruleProfileRef},
		PrivacyLevel:      options.PrivacyLevel,
		LegacySampleID:    sample.ID,
		LegacyScore:       cloneMap(sample.ScoringCriteria),
		MigrationWarnings: warnings,
	}, nil
}

func cloneMap(source map[string]interface{}) map[string]interface{} {
	if source == nil {
		return map[string]interface{}{}
	}
	copyMap := make(map[string]interface{}, len(source))
	for key, value := range source {
		copyMap[key] = value
	}
	return copyMap
}

func marshalJSON(value interface{}) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (r *WABenchRepo) ImportLegacyEvaluations(ctx context.Context, options LegacyImportOptions) (*LegacyImportReport, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	options, err := normalizeLegacyImportOptions(options)
	if err != nil {
		return nil, err
	}
	report := &LegacyImportReport{SchemaVersion: WABenchSchemaVersion, Partition: options.Partition, StartedAt: time.Now().UTC()}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin WABench legacy import: %w", err)
	}
	defer tx.Rollback()

	sets, err := loadLegacyEvaluationSets(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, legacySet := range sets {
		suite, mapErr := MapLegacyEvaluationSet(legacySet, options)
		if mapErr != nil {
			return nil, mapErr
		}
		suitePK, upsertErr := upsertWABenchSuite(ctx, tx, suite)
		if upsertErr != nil {
			return nil, upsertErr
		}
		report.SuitesProcessed++
		report.Warnings = append(report.Warnings, suite.MigrationWarnings...)

		samples, loadErr := loadLegacyEvaluationSamples(ctx, tx, legacySet.ID)
		if loadErr != nil {
			return nil, loadErr
		}
		for _, legacySample := range samples {
			caseDraft, caseErr := MapLegacyEvaluationSample(legacySample, options)
			if caseErr != nil {
				return nil, caseErr
			}
			if caseErr = upsertWABenchCase(ctx, tx, suitePK, caseDraft); caseErr != nil {
				return nil, caseErr
			}
			report.CasesProcessed++
			report.Warnings = append(report.Warnings, caseDraft.MigrationWarnings...)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE wabench_suites SET case_count = $2, updated_at = NOW() WHERE id = $1`, suitePK, len(samples)); err != nil {
			return nil, fmt.Errorf("update WABench suite count: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit WABench legacy import: %w", err)
	}
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func loadLegacyEvaluationSets(ctx context.Context, tx *sql.Tx) ([]EvaluationSet, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id::text, name, style_slug, COALESCE(description, ''), status, sample_count, created_at, updated_at
		FROM evaluation_sets ORDER BY created_at, id
	`)
	if err != nil {
		return nil, fmt.Errorf("read legacy evaluation sets: %w", err)
	}
	defer rows.Close()
	sets := []EvaluationSet{}
	for rows.Next() {
		var set EvaluationSet
		if err := rows.Scan(&set.ID, &set.Name, &set.StyleSlug, &set.Description, &set.Status, &set.SampleCount, &set.CreatedAt, &set.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan legacy evaluation set: %w", err)
		}
		sets = append(sets, set)
	}
	return sets, rows.Err()
}

func loadLegacyEvaluationSamples(ctx context.Context, tx *sql.Tx, setID string) ([]EvaluationSample, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id::text, set_id::text, topic, input_prompt, style_slug,
		       expected_structure, expected_keywords, expected_length, red_flags,
		       scoring_criteria, status, created_at
		FROM evaluation_samples WHERE set_id = $1 ORDER BY created_at, id
	`, setID)
	if err != nil {
		return nil, fmt.Errorf("read legacy evaluation samples: %w", err)
	}
	defer rows.Close()
	samples := []EvaluationSample{}
	for rows.Next() {
		var sample EvaluationSample
		var structureJSON, criteriaJSON []byte
		if err := rows.Scan(
			&sample.ID, &sample.SetID, &sample.Topic, &sample.InputPrompt, &sample.StyleSlug,
			&structureJSON, pq.Array(&sample.ExpectedKeywords), &sample.ExpectedLength, pq.Array(&sample.RedFlags),
			&criteriaJSON, &sample.Status, &sample.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan legacy evaluation sample: %w", err)
		}
		if len(structureJSON) > 0 {
			if err := json.Unmarshal(structureJSON, &sample.ExpectedStructure); err != nil {
				return nil, fmt.Errorf("decode legacy expected_structure for %s: %w", sample.ID, err)
			}
		}
		if len(criteriaJSON) > 0 {
			if err := json.Unmarshal(criteriaJSON, &sample.ScoringCriteria); err != nil {
				return nil, fmt.Errorf("decode legacy scoring_criteria for %s: %w", sample.ID, err)
			}
		}
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}

func upsertWABenchSuite(ctx context.Context, tx *sql.Tx, suite WABenchSuiteDraft) (string, error) {
	coverage, err := marshalJSON(suite.Coverage)
	if err != nil {
		return "", fmt.Errorf("encode WABench suite coverage: %w", err)
	}
	privacy, err := marshalJSON(suite.Privacy)
	if err != nil {
		return "", fmt.Errorf("encode WABench suite privacy: %w", err)
	}
	warnings, err := marshalJSON(suite.MigrationWarnings)
	if err != nil {
		return "", fmt.Errorf("encode WABench suite warnings: %w", err)
	}
	var id string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO wabench_suites (
			suite_id, schema_version, version, name, description, partition, visibility,
			status, coverage, privacy, legacy_set_id, migration_warnings, created_at, updated_at
		) VALUES ($1, 'wabench.v1', $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10, $11::jsonb, NOW(), NOW())
		ON CONFLICT (legacy_set_id) DO UPDATE SET
			suite_id = EXCLUDED.suite_id, version = EXCLUDED.version, name = EXCLUDED.name,
			description = EXCLUDED.description, partition = EXCLUDED.partition,
			visibility = EXCLUDED.visibility, status = EXCLUDED.status,
			coverage = EXCLUDED.coverage, privacy = EXCLUDED.privacy,
			migration_warnings = EXCLUDED.migration_warnings, updated_at = NOW()
		RETURNING id::text
	`, suite.SuiteID, suite.Version, suite.Name, suite.Description, suite.Partition, suite.Visibility,
		suite.Status, coverage, privacy, suite.LegacySetID, warnings).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert WABench suite for legacy set %s: %w", suite.LegacySetID, err)
	}
	return id, nil
}

func upsertWABenchCase(ctx context.Context, tx *sql.Tx, suitePK string, draft WABenchCaseDraft) error {
	contextJSON, err := marshalJSON(draft.Context)
	if err != nil {
		return fmt.Errorf("encode WABench case context: %w", err)
	}
	weightsJSON, err := marshalJSON(draft.RubricWeights)
	if err != nil {
		return fmt.Errorf("encode WABench case weights: %w", err)
	}
	legacyScoreJSON, err := marshalJSON(draft.LegacyScore)
	if err != nil {
		return fmt.Errorf("encode WABench legacy score: %w", err)
	}
	warningsJSON, err := marshalJSON(draft.MigrationWarnings)
	if err != nil {
		return fmt.Errorf("encode WABench case warnings: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO wabench_cases (
			case_id, suite_pk, schema_version, task_type, difficulty,
			input_storage, input_ref, input_hash, context, source_mode, source_fixture_refs,
			expected_behavior, must_have, must_not_have, hard_gate_ids, rubric_weights,
			capability_tags, risk_tags, rule_profile_refs, privacy_level,
			legacy_sample_id, legacy_score, migration_warnings, created_at, updated_at
		) VALUES (
			$1, $2, 'wabench.v1', $3, $4, $5, $6, $7, $8::jsonb, $9, $10,
			$11, $12, $13, $14, $15::jsonb, $16, $17, $18, $19,
			$20, $21::jsonb, $22::jsonb, NOW(), NOW()
		)
		ON CONFLICT (legacy_sample_id) DO UPDATE SET
			case_id = EXCLUDED.case_id, suite_pk = EXCLUDED.suite_pk,
			task_type = EXCLUDED.task_type, difficulty = EXCLUDED.difficulty,
			input_storage = EXCLUDED.input_storage, input_text = NULL,
			input_ref = EXCLUDED.input_ref, input_hash = EXCLUDED.input_hash,
			context = EXCLUDED.context, source_mode = EXCLUDED.source_mode,
			source_fixture_refs = EXCLUDED.source_fixture_refs,
			expected_behavior = EXCLUDED.expected_behavior, must_have = EXCLUDED.must_have,
			must_not_have = EXCLUDED.must_not_have, hard_gate_ids = EXCLUDED.hard_gate_ids,
			rubric_weights = EXCLUDED.rubric_weights, capability_tags = EXCLUDED.capability_tags,
			risk_tags = EXCLUDED.risk_tags, rule_profile_refs = EXCLUDED.rule_profile_refs,
			privacy_level = EXCLUDED.privacy_level, legacy_score = EXCLUDED.legacy_score,
			migration_warnings = EXCLUDED.migration_warnings, updated_at = NOW()
	`, draft.CaseID, suitePK, draft.TaskType, draft.Difficulty, draft.InputStorage, draft.InputRef,
		draft.InputHash, contextJSON, draft.SourceMode, pq.Array(draft.SourceFixtureRefs), draft.ExpectedBehavior,
		pq.Array(draft.MustHave), pq.Array(draft.MustNotHave), pq.Array(draft.HardGateIDs), weightsJSON, pq.Array(draft.CapabilityTags),
		pq.Array(draft.RiskTags), pq.Array(draft.RuleProfileRefs), draft.PrivacyLevel, draft.LegacySampleID,
		legacyScoreJSON, warningsJSON)
	if err != nil {
		return fmt.Errorf("upsert WABench case for legacy sample %s: %w", draft.LegacySampleID, err)
	}
	return nil
}

func SortedWABenchPartitions() []string {
	partitions := make([]string, 0, len(wabenchPartitions))
	for partition := range wabenchPartitions {
		partitions = append(partitions, partition)
	}
	sort.Strings(partitions)
	return partitions
}
