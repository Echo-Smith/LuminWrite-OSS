package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var ErrWABenchInputUnavailable = errors.New("WABench case input is unavailable")

type WABenchSuite struct {
	PK         string `json:"-"`
	SuiteID    string `json:"suiteId"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	Partition  string `json:"partition"`
	Visibility string `json:"visibility"`
	Status     string `json:"status"`
	CaseCount  int    `json:"caseCount"`
}

type WABenchCase struct {
	PK                string
	CaseID            string
	SuitePK           string
	TaskType          string
	Difficulty        string
	InputStorage      string
	InputText         string
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
	RuleProfileRefs   []string
	PrivacyLevel      string
}

type WABenchSourceFixture struct {
	FixtureID      string
	SourceType     string
	Provider       string
	SourceRef      string
	Title          string
	ContentHash    string
	PrivacyLevel   string
	ExcerptStorage string
	ExcerptText    string
	PrivateRef     string
}

type WABenchRedTeamSeedCase struct {
	CaseID           string
	Input            string
	ExpectedBehavior string
	MustHave         []string
	MustNotHave      []string
	Context          map[string]interface{}
	CapabilityTags   []string
	RiskTags         []string
}

type WABenchCandidate struct {
	PK            string                 `json:"-"`
	CandidateID   string                 `json:"candidateId"`
	Name          string                 `json:"name"`
	PromptHash    string                 `json:"promptHash"`
	MemoryHash    string                 `json:"memoryHash,omitempty"`
	ModelManifest map[string]interface{} `json:"modelManifest"`
	CodeHash      string                 `json:"codeHash"`
	ToolManifest  map[string]interface{} `json:"toolManifest"`
	FeatureFlags  map[string]interface{} `json:"featureFlags"`
}

type WABenchCandidateDraft struct {
	CandidateID   string
	Name          string
	PromptHash    string
	MemoryHash    string
	ModelManifest map[string]interface{}
	CodeHash      string
	ToolManifest  map[string]interface{}
	FeatureFlags  map[string]interface{}
}

type WABenchRun struct {
	PK              string     `json:"-"`
	RunID           string     `json:"runId"`
	SuitePK         string     `json:"-"`
	CandidatePK     string     `json:"-"`
	AdapterID       string     `json:"adapterId"`
	RunnerVersion   string     `json:"runnerVersion"`
	Environment     string     `json:"environment"`
	TrafficType     string     `json:"trafficType"`
	EvaluationRunID string     `json:"evaluationRunId,omitempty"`
	Status          string     `json:"status"`
	TotalCases      int        `json:"totalCases"`
	CompletedCases  int        `json:"completedCases"`
	FailedCases     int        `json:"failedCases"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
}

type WABenchRunExecution struct {
	Run       WABenchRun
	Suite     WABenchSuite
	Candidate WABenchCandidate
}

type WABenchRunReport struct {
	Run                  WABenchRun             `json:"run"`
	OutputStatusCounts   map[string]int         `json:"outputStatusCounts"`
	ScoredCases          int                    `json:"scoredCases"`
	AverageWeightedScore *float64               `json:"averageWeightedScore"`
	GateDecision         string                 `json:"gateDecision,omitempty"`
	GateEvidence         map[string]interface{} `json:"gateEvidence,omitempty"`
}

type WABenchCheckWrite struct {
	CheckID  string
	Status   string
	Severity string
	Evidence map[string]interface{}
}

type WABenchReviewWrite struct {
	ReviewID            string
	ReviewerID          string
	ReviewerRole        string
	ReviewerType        string
	ReviewMethod        string
	LabelSource         string
	IsBlind             bool
	TaskCompliance      int
	SourceFidelity      int
	StructureReasoning  int
	StyleConsistency    int
	DirectUsability     int
	AcceptanceLabel     string
	ModificationBurden  *int
	HardFailureIDs      []string
	PrimaryRootCause    string
	SecondaryRootCauses []string
	Evidence            map[string]interface{}
	ReviewedAt          time.Time
}

type WABenchOutputWrite struct {
	OutputID    string
	Status      string
	OutputHash  string
	TextStorage string
	OutputText  string
	PrivateRef  string
	Failures    []map[string]interface{}
	Metrics     map[string]interface{}
	Routing     map[string]interface{}
	TraceRef    string
	Checks      []WABenchCheckWrite
	Review      *WABenchReviewWrite
	Failed      bool
}

type WABenchGateDecisionWrite struct {
	DecisionID         string
	Decision           string
	Evidence           map[string]interface{}
	Exceptions         []map[string]interface{}
	RollbackConditions []map[string]interface{}
	OwnerRef           string
	DecidedAt          time.Time
}

func marshalWABenchJSON(value interface{}) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func unmarshalWABenchMap(raw []byte) (map[string]interface{}, error) {
	result := map[string]interface{}{}
	if len(raw) == 0 {
		return result, nil
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *WABenchRepo) UpsertCandidate(ctx context.Context, draft WABenchCandidateDraft) (*WABenchCandidate, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	modelName, _ := draft.ModelManifest["model"].(string)
	if strings.TrimSpace(modelName) == "" {
		modelName, _ = draft.ModelManifest["modelName"].(string)
	}
	if strings.TrimSpace(modelName) == "" {
		return nil, fmt.Errorf("model manifest must freeze model or modelName")
	}
	if memoryEnabled, _ := draft.FeatureFlags["memoryEnabled"].(bool); memoryEnabled && draft.MemoryHash == "" {
		return nil, fmt.Errorf("memoryEnabled candidate must include memoryHash")
	}
	model, err := marshalWABenchJSON(draft.ModelManifest)
	if err != nil {
		return nil, fmt.Errorf("encode model manifest: %w", err)
	}
	tools, err := marshalWABenchJSON(draft.ToolManifest)
	if err != nil {
		return nil, fmt.Errorf("encode tool manifest: %w", err)
	}
	flags, err := marshalWABenchJSON(draft.FeatureFlags)
	if err != nil {
		return nil, fmt.Errorf("encode feature flags: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO wabench_candidates (
			candidate_id, schema_version, name, prompt_hash, memory_hash,
			model_manifest, code_hash, tool_manifest, feature_flags
		) VALUES ($1, 'wabench.v1', $2, $3, NULLIF($4, ''), $5, $6, $7, $8)
		ON CONFLICT (candidate_id) DO NOTHING
	`, draft.CandidateID, draft.Name, draft.PromptHash, draft.MemoryHash, model, draft.CodeHash, tools, flags)
	if err != nil {
		return nil, fmt.Errorf("create WABench candidate: %w", err)
	}
	candidate, err := r.GetCandidate(ctx, draft.CandidateID)
	if err != nil {
		return nil, err
	}
	if inserted, _ := result.RowsAffected(); inserted == 0 {
		modelMatches := canonicalJSONEqual(candidate.ModelManifest, draft.ModelManifest)
		toolsMatch := canonicalJSONEqual(candidate.ToolManifest, draft.ToolManifest)
		flagsMatch := canonicalJSONEqual(candidate.FeatureFlags, draft.FeatureFlags)
		if candidate.Name != draft.Name || candidate.PromptHash != draft.PromptHash ||
			candidate.MemoryHash != draft.MemoryHash || candidate.CodeHash != draft.CodeHash ||
			!modelMatches || !toolsMatch || !flagsMatch {
			return nil, fmt.Errorf("candidate %s is immutable; create a new candidate ID for changed hashes or manifests", draft.CandidateID)
		}
	}
	return candidate, nil
}

func canonicalJSONEqual(left, right interface{}) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func (r *WABenchRepo) GetCandidate(ctx context.Context, candidateID string) (*WABenchCandidate, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	var candidate WABenchCandidate
	var memoryHash sql.NullString
	var modelRaw, toolsRaw, flagsRaw []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, candidate_id, name, prompt_hash, memory_hash,
		       model_manifest, code_hash, tool_manifest, feature_flags
		FROM wabench_candidates WHERE candidate_id = $1
	`, candidateID).Scan(
		&candidate.PK, &candidate.CandidateID, &candidate.Name, &candidate.PromptHash, &memoryHash,
		&modelRaw, &candidate.CodeHash, &toolsRaw, &flagsRaw,
	)
	if err != nil {
		return nil, fmt.Errorf("get WABench candidate: %w", err)
	}
	candidate.MemoryHash = memoryHash.String
	if candidate.ModelManifest, err = unmarshalWABenchMap(modelRaw); err != nil {
		return nil, fmt.Errorf("decode model manifest: %w", err)
	}
	if candidate.ToolManifest, err = unmarshalWABenchMap(toolsRaw); err != nil {
		return nil, fmt.Errorf("decode tool manifest: %w", err)
	}
	if candidate.FeatureFlags, err = unmarshalWABenchMap(flagsRaw); err != nil {
		return nil, fmt.Errorf("decode feature flags: %w", err)
	}
	return &candidate, nil
}

func (r *WABenchRepo) GetSuite(ctx context.Context, suiteID string) (*WABenchSuite, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	var suite WABenchSuite
	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, suite_id, name, version, partition, visibility, status, case_count
		FROM wabench_suites WHERE suite_id = $1
	`, suiteID).Scan(&suite.PK, &suite.SuiteID, &suite.Name, &suite.Version, &suite.Partition,
		&suite.Visibility, &suite.Status, &suite.CaseCount)
	if err != nil {
		return nil, fmt.Errorf("get WABench suite: %w", err)
	}
	return &suite, nil
}

func (r *WABenchRepo) EnsureRedTeamSuite(ctx context.Context, cases []WABenchRedTeamSeedCase) (*WABenchSuite, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("red-team suite requires at least one case")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin red-team seed: %w", err)
	}
	defer tx.Rollback()
	const suiteID = "luminbuddy.private.red-team.v1"
	var suitePK string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO wabench_suites (
			suite_id, schema_version, version, name, description, partition,
			visibility, status, case_count, coverage, privacy
		) VALUES (
			$1, 'wabench.v1', 'v1.0.0', 'Luminbuddy Red Team',
			'独立红队发布门禁；不参与普通写作质量均分。', 'red_team',
			'private', 'active', $2,
			'{"taskTypes":["abnormal"],"capabilities":["prompt_injection_defense","tool_safety","privacy_boundary"]}',
			'{"allowsRawText":true,"publicationPolicy":"aggregate_only"}'
		)
		ON CONFLICT (suite_id) DO UPDATE SET suite_id = EXCLUDED.suite_id
		RETURNING id::text
	`, suiteID, len(cases)).Scan(&suitePK)
	if err != nil {
		return nil, fmt.Errorf("upsert red-team suite: %w", err)
	}
	var partition, visibility string
	var existingCaseCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT partition, visibility, case_count FROM wabench_suites WHERE id = $1
	`, suitePK).Scan(&partition, &visibility, &existingCaseCount); err != nil {
		return nil, fmt.Errorf("verify red-team suite: %w", err)
	}
	if partition != "red_team" || visibility != "private" || existingCaseCount != len(cases) {
		return nil, fmt.Errorf("red-team suite is immutable; bump its version before changing metadata or case count")
	}
	weights, _ := json.Marshal(map[string]int{
		"taskCompliance": 25, "sourceFidelity": 25, "structureReasoning": 15,
		"styleConsistency": 15, "directUsability": 20,
	})
	for _, seed := range cases {
		if seed.CaseID == "" || seed.Input == "" {
			return nil, fmt.Errorf("red-team case id and input are required")
		}
		if seed.ExpectedBehavior != "answer" && seed.ExpectedBehavior != "clarify" && seed.ExpectedBehavior != "refuse" && seed.ExpectedBehavior != "degrade" {
			return nil, fmt.Errorf("invalid red-team expected behavior %q", seed.ExpectedBehavior)
		}
		contextJSON, marshalErr := json.Marshal(seed.Context)
		if marshalErr != nil {
			return nil, marshalErr
		}
		caseID := "redteam_" + safeWABenchID(seed.CaseID, "case")
		result, execErr := tx.ExecContext(ctx, `
			INSERT INTO wabench_cases (
				case_id, suite_pk, schema_version, task_type, difficulty,
				input_storage, input_text, input_hash, context, source_mode,
				expected_behavior, must_have, must_not_have, hard_gate_ids,
				rubric_weights, capability_tags, risk_tags, rule_profile_refs,
				privacy_level, migration_warnings
			) VALUES (
				$1, $2, 'wabench.v1', 'abnormal', 'L3',
				'inline_public', $3, $4, $5::jsonb, 'none',
				$6, $7, $8, ARRAY['redteam.compromised'],
				$9::jsonb, $10, $11, ARRAY['luminbuddy.builtin-style.yinyue'],
				'synthetic', '[]'
			)
			ON CONFLICT (case_id) DO NOTHING
		`, caseID, suitePK, seed.Input, sha256String(seed.Input), string(contextJSON),
			seed.ExpectedBehavior, pq.Array(seed.MustHave), pq.Array(seed.MustNotHave), string(weights),
			pq.Array(seed.CapabilityTags), pq.Array(seed.RiskTags))
		if execErr != nil {
			return nil, fmt.Errorf("create red-team case %s: %w", seed.CaseID, execErr)
		}
		if inserted, _ := result.RowsAffected(); inserted == 0 {
			var existingHash, existingSuitePK string
			if err := tx.QueryRowContext(ctx, `SELECT input_hash, suite_pk::text FROM wabench_cases WHERE case_id = $1`, caseID).Scan(&existingHash, &existingSuitePK); err != nil {
				return nil, fmt.Errorf("verify red-team case %s: %w", seed.CaseID, err)
			}
			if existingHash != sha256String(seed.Input) || existingSuitePK != suitePK {
				return nil, fmt.Errorf("red-team case %s is immutable; bump the suite version before changing it", seed.CaseID)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit red-team seed: %w", err)
	}
	return r.GetSuite(ctx, suiteID)
}

func (r *WABenchRepo) ListCases(ctx context.Context, suitePK string) ([]WABenchCase, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text, case_id, suite_pk::text, task_type, difficulty,
		       input_storage, COALESCE(input_text, ''), COALESCE(input_ref, ''), input_hash,
		       context, source_mode, source_fixture_refs, expected_behavior,
		       must_have, must_not_have, hard_gate_ids, rubric_weights,
		       rule_profile_refs, privacy_level
		FROM wabench_cases WHERE suite_pk = $1 ORDER BY case_id
	`, suitePK)
	if err != nil {
		return nil, fmt.Errorf("list WABench cases: %w", err)
	}
	defer rows.Close()
	cases := []WABenchCase{}
	for rows.Next() {
		var item WABenchCase
		var contextRaw, weightsRaw []byte
		if err := rows.Scan(
			&item.PK, &item.CaseID, &item.SuitePK, &item.TaskType, &item.Difficulty,
			&item.InputStorage, &item.InputText, &item.InputRef, &item.InputHash,
			&contextRaw, &item.SourceMode, pq.Array(&item.SourceFixtureRefs), &item.ExpectedBehavior,
			pq.Array(&item.MustHave), pq.Array(&item.MustNotHave), pq.Array(&item.HardGateIDs), &weightsRaw,
			pq.Array(&item.RuleProfileRefs), &item.PrivacyLevel,
		); err != nil {
			return nil, fmt.Errorf("scan WABench case: %w", err)
		}
		if err := json.Unmarshal(contextRaw, &item.Context); err != nil {
			return nil, fmt.Errorf("decode WABench case context %s: %w", item.CaseID, err)
		}
		if err := json.Unmarshal(weightsRaw, &item.RubricWeights); err != nil {
			return nil, fmt.Errorf("decode WABench case rubric %s: %w", item.CaseID, err)
		}
		cases = append(cases, item)
	}
	return cases, rows.Err()
}

func (r *WABenchRepo) ResolveCaseInput(ctx context.Context, item WABenchCase) (string, error) {
	switch item.InputStorage {
	case "inline_public":
		if item.InputText == "" {
			return "", fmt.Errorf("%w: inline input is empty for %s", ErrWABenchInputUnavailable, item.CaseID)
		}
		return item.InputText, nil
	case "private_ref":
		const legacyPrefix = "legacy:evaluation_samples/"
		if strings.HasPrefix(item.InputRef, legacyPrefix) {
			legacyID := strings.TrimPrefix(item.InputRef, legacyPrefix)
			var input string
			if err := r.db.QueryRowContext(ctx, `SELECT input_prompt FROM evaluation_samples WHERE id = $1`, legacyID).Scan(&input); err != nil {
				return "", fmt.Errorf("%w: resolve legacy ref for %s: %v", ErrWABenchInputUnavailable, item.CaseID, err)
			}
			return input, nil
		}
		return "", fmt.Errorf("%w: unsupported private ref for %s", ErrWABenchInputUnavailable, item.CaseID)
	case "hash_only":
		return "", fmt.Errorf("%w: hash-only case %s needs an external private resolver", ErrWABenchInputUnavailable, item.CaseID)
	default:
		return "", fmt.Errorf("%w: unknown input storage %q", ErrWABenchInputUnavailable, item.InputStorage)
	}
}

func (r *WABenchRepo) ResolveCaseSources(ctx context.Context, item WABenchCase) ([]WABenchSourceFixture, error) {
	if len(item.SourceFixtureRefs) == 0 {
		return []WABenchSourceFixture{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT fixture_id, source_type, COALESCE(provider, ''), COALESCE(source_ref, ''),
		       title, content_hash, privacy_level, excerpt_storage,
		       COALESCE(excerpt_text, ''), COALESCE(private_ref, '')
		FROM wabench_source_fixtures
		WHERE fixture_id = ANY($1)
		ORDER BY fixture_id
	`, pq.Array(item.SourceFixtureRefs))
	if err != nil {
		return nil, fmt.Errorf("resolve WABench source fixtures: %w", err)
	}
	defer rows.Close()
	fixtures := []WABenchSourceFixture{}
	for rows.Next() {
		var fixture WABenchSourceFixture
		if err := rows.Scan(
			&fixture.FixtureID, &fixture.SourceType, &fixture.Provider, &fixture.SourceRef,
			&fixture.Title, &fixture.ContentHash, &fixture.PrivacyLevel, &fixture.ExcerptStorage,
			&fixture.ExcerptText, &fixture.PrivateRef,
		); err != nil {
			return nil, fmt.Errorf("scan WABench source fixture: %w", err)
		}
		if fixture.ExcerptStorage != "inline_public" || fixture.ExcerptText == "" {
			return nil, fmt.Errorf("%w: fixture %s requires an external private resolver", ErrWABenchInputUnavailable, fixture.FixtureID)
		}
		fixtures = append(fixtures, fixture)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(fixtures) != len(item.SourceFixtureRefs) {
		return nil, fmt.Errorf("%w: expected %d source fixtures, resolved %d", ErrWABenchInputUnavailable, len(item.SourceFixtureRefs), len(fixtures))
	}
	return fixtures, nil
}

func (r *WABenchRepo) CreateRun(ctx context.Context, suiteID, candidateID, environment, evaluationRunID string) (*WABenchRun, error) {
	suite, err := r.GetSuite(ctx, suiteID)
	if err != nil {
		return nil, err
	}
	candidate, err := r.GetCandidate(ctx, candidateID)
	if err != nil {
		return nil, err
	}
	runID := "wabench_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO wabench_runs (
			run_id, schema_version, suite_pk, candidate_pk, adapter_id, runner_version,
			environment, traffic_type, evaluation_run_id, status, total_cases
		) VALUES ($1, 'wabench.v1', $2, $3, 'luminbuddy-v2', 'wabench.v1', $4, 'replay', NULLIF($5, ''), 'pending',
		          (SELECT COUNT(*) FROM wabench_cases WHERE suite_pk = $2))
	`, runID, suite.PK, candidate.PK, environment, evaluationRunID)
	if err != nil {
		return nil, fmt.Errorf("create WABench run: %w", err)
	}
	return r.GetRun(ctx, runID)
}

func (r *WABenchRepo) GetRun(ctx context.Context, runID string) (*WABenchRun, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	var run WABenchRun
	var evaluationRunID sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, run_id, suite_pk::text, candidate_pk::text, adapter_id, runner_version,
		       environment, traffic_type, evaluation_run_id, status, total_cases,
		       completed_cases, failed_cases, started_at, completed_at
		FROM wabench_runs WHERE run_id = $1
	`, runID).Scan(
		&run.PK, &run.RunID, &run.SuitePK, &run.CandidatePK, &run.AdapterID, &run.RunnerVersion,
		&run.Environment, &run.TrafficType, &evaluationRunID, &run.Status, &run.TotalCases,
		&run.CompletedCases, &run.FailedCases, &run.StartedAt, &run.CompletedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get WABench run: %w", err)
	}
	run.EvaluationRunID = evaluationRunID.String
	return &run, nil
}

func (r *WABenchRepo) GetRunExecution(ctx context.Context, runID string) (*WABenchRunExecution, error) {
	run, err := r.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	var suite WABenchSuite
	err = r.db.QueryRowContext(ctx, `
		SELECT id::text, suite_id, name, version, partition, visibility, status, case_count
		FROM wabench_suites WHERE id = $1
	`, run.SuitePK).Scan(&suite.PK, &suite.SuiteID, &suite.Name, &suite.Version, &suite.Partition,
		&suite.Visibility, &suite.Status, &suite.CaseCount)
	if err != nil {
		return nil, fmt.Errorf("load WABench run suite: %w", err)
	}
	var candidate WABenchCandidate
	var memoryHash sql.NullString
	var modelRaw, toolsRaw, flagsRaw []byte
	err = r.db.QueryRowContext(ctx, `
		SELECT id::text, candidate_id, name, prompt_hash, memory_hash,
		       model_manifest, code_hash, tool_manifest, feature_flags
		FROM wabench_candidates WHERE id = $1
	`, run.CandidatePK).Scan(
		&candidate.PK, &candidate.CandidateID, &candidate.Name, &candidate.PromptHash, &memoryHash,
		&modelRaw, &candidate.CodeHash, &toolsRaw, &flagsRaw,
	)
	if err != nil {
		return nil, fmt.Errorf("load WABench run candidate: %w", err)
	}
	candidate.MemoryHash = memoryHash.String
	if candidate.ModelManifest, err = unmarshalWABenchMap(modelRaw); err != nil {
		return nil, err
	}
	if candidate.ToolManifest, err = unmarshalWABenchMap(toolsRaw); err != nil {
		return nil, err
	}
	if candidate.FeatureFlags, err = unmarshalWABenchMap(flagsRaw); err != nil {
		return nil, err
	}
	return &WABenchRunExecution{Run: *run, Suite: suite, Candidate: candidate}, nil
}

func (r *WABenchRepo) GetRunReport(ctx context.Context, runID string) (*WABenchRunReport, error) {
	run, err := r.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	report := &WABenchRunReport{Run: *run, OutputStatusCounts: map[string]int{}}
	rows, err := r.db.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM wabench_outputs WHERE run_pk = $1 GROUP BY status
	`, run.PK)
	if err != nil {
		return nil, fmt.Errorf("load WABench output summary: %w", err)
	}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			return nil, err
		}
		report.OutputStatusCounts[status] = count
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var average sql.NullFloat64
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*), AVG((rv.evidence->>'weightedScore')::double precision)
		FROM wabench_reviews rv
		JOIN wabench_outputs o ON o.id = rv.output_pk
		WHERE o.run_pk = $1
	`, run.PK).Scan(&report.ScoredCases, &average); err != nil {
		return nil, fmt.Errorf("load WABench score summary: %w", err)
	}
	if average.Valid {
		report.AverageWeightedScore = &average.Float64
	}
	var gateEvidence []byte
	err = r.db.QueryRowContext(ctx, `
		SELECT decision, evidence FROM wabench_gate_decisions
		WHERE run_pk = $1 ORDER BY created_at DESC LIMIT 1
	`, run.PK).Scan(&report.GateDecision, &gateEvidence)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load WABench gate decision: %w", err)
	}
	if len(gateEvidence) > 0 {
		report.GateEvidence, err = unmarshalWABenchMap(gateEvidence)
		if err != nil {
			return nil, err
		}
	}
	return report, nil
}

func (r *WABenchRepo) StartRun(ctx context.Context, runID string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE wabench_runs SET status = 'running', started_at = NOW()
		WHERE run_id = $1 AND status = 'pending'
	`, runID)
	if err != nil {
		return fmt.Errorf("start WABench run: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return fmt.Errorf("WABench run %s is not pending", runID)
	}
	return nil
}

func (r *WABenchRepo) SaveCaseResult(ctx context.Context, runPK, casePK string, output WABenchOutputWrite) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin WABench result transaction: %w", err)
	}
	defer tx.Rollback()
	failures, err := marshalWABenchJSON(output.Failures)
	if err != nil {
		return err
	}
	metrics, err := marshalWABenchJSON(output.Metrics)
	if err != nil {
		return err
	}
	routing, err := marshalWABenchJSON(output.Routing)
	if err != nil {
		return err
	}
	var outputPK string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO wabench_outputs (
			output_id, schema_version, run_pk, case_pk, status, output_hash,
			text_storage, output_text, private_ref, failures, metrics, routing, trace_ref
		) VALUES ($1, 'wabench.v1', $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9, $10, $11, NULLIF($12, ''))
		RETURNING id::text
	`, output.OutputID, runPK, casePK, output.Status, output.OutputHash, output.TextStorage,
		output.OutputText, output.PrivateRef, failures, metrics, routing, output.TraceRef).Scan(&outputPK)
	if err != nil {
		return fmt.Errorf("insert WABench output: %w", err)
	}
	for _, check := range output.Checks {
		evidence, marshalErr := marshalWABenchJSON(check.Evidence)
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO wabench_checks (output_pk, check_id, status, severity, evidence)
			VALUES ($1, $2, $3, $4, $5)
		`, outputPK, check.CheckID, check.Status, check.Severity, evidence); err != nil {
			return fmt.Errorf("insert WABench check %s: %w", check.CheckID, err)
		}
	}
	if output.Review != nil {
		review := output.Review
		evidence, marshalErr := marshalWABenchJSON(review.Evidence)
		if marshalErr != nil {
			return marshalErr
		}
		var primary interface{}
		if review.PrimaryRootCause != "" {
			primary = review.PrimaryRootCause
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO wabench_reviews (
				review_id, schema_version, output_pk, reviewer_id, reviewer_role,
				reviewer_type, review_method, label_source, is_blind,
				task_compliance, source_fidelity, structure_reasoning, style_consistency, direct_usability,
				acceptance_label, modification_burden, hard_failure_ids,
				primary_root_cause, secondary_root_causes, evidence, reviewed_at
			) VALUES (
				$1, 'wabench.v1', $2, $3, $4, $5, $6, $7, $8,
				$9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20
			)
		`, review.ReviewID, outputPK, review.ReviewerID, review.ReviewerRole,
			review.ReviewerType, review.ReviewMethod, review.LabelSource, review.IsBlind,
			review.TaskCompliance, review.SourceFidelity, review.StructureReasoning,
			review.StyleConsistency, review.DirectUsability, review.AcceptanceLabel,
			review.ModificationBurden, pq.Array(review.HardFailureIDs), primary,
			pq.Array(review.SecondaryRootCauses), evidence, review.ReviewedAt); err != nil {
			return fmt.Errorf("insert WABench review: %w", err)
		}
	}
	failedIncrement := 0
	if output.Failed {
		failedIncrement = 1
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE wabench_runs
		SET completed_cases = completed_cases + 1,
		    failed_cases = failed_cases + $2
		WHERE id = $1
	`, runPK, failedIncrement); err != nil {
		return fmt.Errorf("update WABench run progress: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit WABench result: %w", err)
	}
	return nil
}

func (r *WABenchRepo) CompleteRun(ctx context.Context, runID, status string) error {
	if status != "completed" && status != "failed" {
		return fmt.Errorf("invalid final WABench run status %q", status)
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE wabench_runs SET status = $2, completed_at = NOW() WHERE run_id = $1
	`, runID, status)
	if err != nil {
		return fmt.Errorf("complete WABench run: %w", err)
	}
	return nil
}

func (r *WABenchRepo) SaveGateDecision(ctx context.Context, runPK string, decision WABenchGateDecisionWrite) error {
	evidence, err := marshalWABenchJSON(decision.Evidence)
	if err != nil {
		return err
	}
	exceptions, err := marshalWABenchJSON(decision.Exceptions)
	if err != nil {
		return err
	}
	rollback, err := marshalWABenchJSON(decision.RollbackConditions)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO wabench_gate_decisions (
			decision_id, schema_version, run_pk, decision, evidence,
			exceptions, rollback_conditions, owner_ref, decided_at
		) VALUES ($1, 'wabench.v1', $2, $3, $4, $5, $6, $7, $8)
	`, decision.DecisionID, runPK, decision.Decision, evidence, exceptions,
		rollback, decision.OwnerRef, decision.DecidedAt)
	if err != nil {
		return fmt.Errorf("save WABench gate decision: %w", err)
	}
	return nil
}
