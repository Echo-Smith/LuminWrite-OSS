package database

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

type WABenchFrozenBatch struct {
	SchemaVersion string                 `json:"schemaVersion"`
	BatchID       string                 `json:"batchId"`
	Version       string                 `json:"version"`
	Description   string                 `json:"description"`
	Visibility    string                 `json:"visibility"`
	SuiteID       string                 `json:"suiteId"`
	CaseRefs      []WABenchFrozenCaseRef `json:"caseRefs"`
	ContentHash   string                 `json:"contentHash"`
	FrozenAt      time.Time              `json:"frozenAt"`
}

type WABenchFrozenCaseRef struct {
	CaseID            string   `json:"caseId"`
	InputHash         string   `json:"inputHash"`
	OriginInputHash   string   `json:"originInputHash,omitempty"`
	PrivacyLevel      string   `json:"privacyLevel"`
	SourceFixtureRefs []string `json:"sourceFixtureRefs"`
	PrivateCaseRef    string   `json:"privateCaseRef,omitempty"`
}

type WABenchPrivateHoldoutRecord struct {
	HoldoutID         string `json:"holdoutId"`
	InputHash         string `json:"inputHash"`
	RedactedInputHash string `json:"redactedInputHash"`
	InputRedacted     string `json:"inputRedacted"`
	TaskType          string `json:"taskType"`
	SourceTraceHash   string `json:"sourceTraceHash"`
	Route             string `json:"route"`
	Retrieval         struct {
		KnowledgeOnly     bool   `json:"knowledgeOnly"`
		KnowledgeProvider string `json:"knowledgeProvider"`
	} `json:"retrieval"`
}

type WABenchPortableCase struct {
	SchemaVersion     string                 `json:"schemaVersion"`
	CaseID            string                 `json:"caseId"`
	TaskType          string                 `json:"taskType"`
	Difficulty        string                 `json:"difficulty"`
	Input             string                 `json:"input"`
	Context           map[string]interface{} `json:"context"`
	SourceMode        string                 `json:"sourceMode"`
	SourceFixtureRefs []string               `json:"sourceFixtureRefs"`
	ExpectedBehavior  string                 `json:"expectedBehavior"`
	MustHave          []string               `json:"mustHave"`
	MustNotHave       []string               `json:"mustNotHave"`
	HardGateIDs       []string               `json:"hardGateIds"`
	RubricWeights     map[string]int         `json:"rubricWeights"`
	CapabilityTags    []string               `json:"capabilityTags"`
	RiskTags          []string               `json:"riskTags"`
	RuleProfileRefs   []string               `json:"ruleProfileRefs"`
	PrivacyLevel      string                 `json:"privacyLevel"`
	InputHash         string                 `json:"inputHash"`
}

type WABenchPortableFixture struct {
	SchemaVersion string                 `json:"schemaVersion"`
	FixtureID     string                 `json:"fixtureId"`
	SourceType    string                 `json:"sourceType"`
	Provider      string                 `json:"provider"`
	SourceRef     string                 `json:"sourceRef"`
	Title         string                 `json:"title"`
	RetrievedAt   time.Time              `json:"retrievedAt"`
	ContentHash   string                 `json:"contentHash"`
	PrivacyLevel  string                 `json:"privacyLevel"`
	Excerpt       string                 `json:"excerpt"`
	Metadata      map[string]interface{} `json:"metadata"`
}

type WABenchBatchImportResult struct {
	SuiteID      string `json:"suiteId"`
	BatchID      string `json:"batchId"`
	CaseCount    int    `json:"caseCount"`
	FixtureCount int    `json:"fixtureCount"`
	ContentHash  string `json:"contentHash"`
}

func validateFrozenBatchHash(batch WABenchFrozenBatch) error {
	encoded, err := json.Marshal(batch.CaseRefs)
	if err != nil {
		return err
	}
	actual := fmt.Sprintf("sha256:%x", sha256.Sum256(encoded))
	if actual != batch.ContentHash {
		return fmt.Errorf("frozen batch content hash mismatch")
	}
	return nil
}

func validatePortableBatch(batch WABenchFrozenBatch, cases []WABenchPortableCase, fixtures []WABenchPortableFixture) error {
	if batch.SchemaVersion != WABenchSchemaVersion || batch.Visibility != "public" || batch.SuiteID == "" || batch.ContentHash == "" {
		return fmt.Errorf("only a frozen public WABench v1 batch can be imported")
	}
	if err := validateFrozenBatchHash(batch); err != nil {
		return err
	}
	casesByID := map[string]WABenchPortableCase{}
	for _, item := range cases {
		casesByID[item.CaseID] = item
	}
	fixturesByID := map[string]WABenchPortableFixture{}
	for _, item := range fixtures {
		fixturesByID[item.FixtureID] = item
	}
	seen := map[string]bool{}
	for _, ref := range batch.CaseRefs {
		if seen[ref.CaseID] {
			return fmt.Errorf("duplicate frozen case %s", ref.CaseID)
		}
		seen[ref.CaseID] = true
		item, ok := casesByID[ref.CaseID]
		if !ok || item.InputHash != ref.InputHash || item.PrivacyLevel != "synthetic" {
			return fmt.Errorf("frozen case identity mismatch for %s", ref.CaseID)
		}
		if len(item.SourceFixtureRefs) != len(ref.SourceFixtureRefs) {
			return fmt.Errorf("frozen source refs mismatch for %s", ref.CaseID)
		}
		for index, fixtureID := range ref.SourceFixtureRefs {
			if item.SourceFixtureRefs[index] != fixtureID {
				return fmt.Errorf("frozen source refs mismatch for %s", ref.CaseID)
			}
			fixture, ok := fixturesByID[fixtureID]
			if !ok || fixture.PrivacyLevel != "public" || fixture.Excerpt == "" {
				return fmt.Errorf("public fixture %s is unavailable", fixtureID)
			}
		}
	}
	return nil
}

func (r *WABenchRepo) ImportFrozenPublicBatch(ctx context.Context, batch WABenchFrozenBatch, cases []WABenchPortableCase, fixtures []WABenchPortableFixture) (*WABenchBatchImportResult, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if err := validatePortableBatch(batch, cases, fixtures); err != nil {
		return nil, err
	}
	casesByID := map[string]WABenchPortableCase{}
	for _, item := range cases {
		casesByID[item.CaseID] = item
	}
	fixturesByID := map[string]WABenchPortableFixture{}
	for _, item := range fixtures {
		fixturesByID[item.FixtureID] = item
	}
	referencedFixtures := map[string]bool{}
	for _, ref := range batch.CaseRefs {
		for _, fixtureID := range ref.SourceFixtureRefs {
			referencedFixtures[fixtureID] = true
		}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	coverage, _ := json.Marshal(map[string]interface{}{"batchId": batch.BatchID, "frozenAt": batch.FrozenAt})
	privacy, _ := json.Marshal(map[string]interface{}{"allowsRawText": true, "publicationPolicy": "full", "privacyLevel": "synthetic"})
	var suitePK string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO wabench_suites (suite_id, schema_version, version, name, description, partition, visibility, status, case_count, coverage, privacy, content_hash)
		VALUES ($1, 'wabench.v1', $2, $3, $4, 'public_holdout', 'public', 'active', $5, $6, $7, $8)
		ON CONFLICT (suite_id) DO UPDATE SET suite_id = EXCLUDED.suite_id
		RETURNING id::text
	`, batch.SuiteID, batch.Version, "Task 11 Public Same-batch", batch.Description, len(batch.CaseRefs), coverage, privacy, batch.ContentHash).Scan(&suitePK)
	if err != nil {
		return nil, fmt.Errorf("upsert public batch suite: %w", err)
	}
	var storedHash string
	var storedCount int
	if err := tx.QueryRowContext(ctx, `SELECT content_hash, case_count FROM wabench_suites WHERE id = $1`, suitePK).Scan(&storedHash, &storedCount); err != nil {
		return nil, err
	}
	if storedHash != batch.ContentHash || storedCount != len(batch.CaseRefs) {
		return nil, fmt.Errorf("public batch suite is immutable; use a new suite id/version")
	}

	fixtureIDs := make([]string, 0, len(referencedFixtures))
	for id := range referencedFixtures {
		fixtureIDs = append(fixtureIDs, id)
	}
	sort.Strings(fixtureIDs)
	for _, fixtureID := range fixtureIDs {
		item := fixturesByID[fixtureID]
		metadata, _ := json.Marshal(item.Metadata)
		result, err := tx.ExecContext(ctx, `
			INSERT INTO wabench_source_fixtures (fixture_id, schema_version, source_type, provider, source_ref, title, retrieved_at, content_hash, privacy_level, excerpt_storage, excerpt_text, metadata)
			VALUES ($1, 'wabench.v1', $2, NULLIF($3,''), NULLIF($4,''), $5, $6, $7, 'public', 'inline_public', $8, $9)
			ON CONFLICT (fixture_id) DO NOTHING
		`, item.FixtureID, item.SourceType, item.Provider, item.SourceRef, item.Title, item.RetrievedAt, item.ContentHash, item.Excerpt, metadata)
		if err != nil {
			return nil, fmt.Errorf("insert public fixture %s: %w", fixtureID, err)
		}
		_ = result
	}
	for _, ref := range batch.CaseRefs {
		item := casesByID[ref.CaseID]
		contextJSON, _ := json.Marshal(item.Context)
		weights, _ := json.Marshal(item.RubricWeights)
		result, err := tx.ExecContext(ctx, `
			INSERT INTO wabench_cases (case_id, suite_pk, schema_version, task_type, difficulty, input_storage, input_text, input_hash, context, source_mode, source_fixture_refs, expected_behavior, must_have, must_not_have, hard_gate_ids, rubric_weights, capability_tags, risk_tags, rule_profile_refs, privacy_level)
			VALUES ($1, $2, 'wabench.v1', $3, $4, 'inline_public', $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, 'synthetic')
			ON CONFLICT (case_id) DO NOTHING
		`, item.CaseID, suitePK, item.TaskType, item.Difficulty, item.Input, item.InputHash, contextJSON, item.SourceMode, pq.Array(item.SourceFixtureRefs), item.ExpectedBehavior,
			pq.Array(item.MustHave), pq.Array(item.MustNotHave), pq.Array(item.HardGateIDs), weights, pq.Array(item.CapabilityTags), pq.Array(item.RiskTags), pq.Array(item.RuleProfileRefs))
		if err != nil {
			return nil, fmt.Errorf("insert public case %s: %w", item.CaseID, err)
		}
		_ = result
	}
	var actual int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM wabench_cases WHERE suite_pk = $1`, suitePK).Scan(&actual); err != nil {
		return nil, err
	}
	if actual != len(batch.CaseRefs) {
		return nil, fmt.Errorf("stored public batch has %d cases, expected %d", actual, len(batch.CaseRefs))
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &WABenchBatchImportResult{SuiteID: batch.SuiteID, BatchID: batch.BatchID, CaseCount: actual, FixtureCount: len(fixtureIDs), ContentHash: batch.ContentHash}, nil
}

func validatePrivateBatch(batch WABenchFrozenBatch, rows []WABenchPrivateHoldoutRecord) error {
	if batch.SchemaVersion != WABenchSchemaVersion || batch.Visibility != "private" || batch.SuiteID == "" || batch.ContentHash == "" {
		return fmt.Errorf("only a frozen private WABench v1 batch can be imported")
	}
	if err := validateFrozenBatchHash(batch); err != nil {
		return err
	}
	rowsByID := map[string]WABenchPrivateHoldoutRecord{}
	for _, row := range rows {
		if _, exists := rowsByID[row.HoldoutID]; exists {
			return fmt.Errorf("duplicate private holdout record %s", row.HoldoutID)
		}
		rowsByID[row.HoldoutID] = row
	}
	seen := map[string]bool{}
	for _, ref := range batch.CaseRefs {
		if seen[ref.CaseID] {
			return fmt.Errorf("duplicate frozen case %s", ref.CaseID)
		}
		seen[ref.CaseID] = true
		row, ok := rowsByID[ref.CaseID]
		if !ok || ref.PrivacyLevel != "anonymized" || ref.InputHash != row.RedactedInputHash || ref.OriginInputHash != row.InputHash {
			return fmt.Errorf("private frozen case identity mismatch for %s", ref.CaseID)
		}
		if strings.TrimSpace(row.InputRedacted) == "" || !strings.HasSuffix(ref.PrivateCaseRef, "#"+ref.CaseID) {
			return fmt.Errorf("private frozen case source unavailable for %s", ref.CaseID)
		}
		actual := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(row.InputRedacted)))
		if actual != ref.InputHash {
			return fmt.Errorf("private frozen redacted hash mismatch for %s", ref.CaseID)
		}
	}
	return nil
}

func (r *WABenchRepo) ImportFrozenPrivateBatch(ctx context.Context, batch WABenchFrozenBatch, rows []WABenchPrivateHoldoutRecord) (*WABenchBatchImportResult, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if err := validatePrivateBatch(batch, rows); err != nil {
		return nil, err
	}
	rowsByID := map[string]WABenchPrivateHoldoutRecord{}
	for _, row := range rows {
		rowsByID[row.HoldoutID] = row
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	coverage, _ := json.Marshal(map[string]interface{}{"batchId": batch.BatchID, "frozenAt": batch.FrozenAt})
	privacy, _ := json.Marshal(map[string]interface{}{"allowsRawText": false, "publicationPolicy": "aggregate_only", "privacyLevel": "anonymized"})
	var suitePK string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO wabench_suites (suite_id, schema_version, version, name, description, partition, visibility, status, case_count, coverage, privacy, content_hash)
		VALUES ($1, 'wabench.v1', $2, $3, $4, 'private_holdout', 'private', 'active', $5, $6, $7, $8)
		ON CONFLICT (suite_id) DO UPDATE SET suite_id = EXCLUDED.suite_id
		RETURNING id::text
	`, batch.SuiteID, batch.Version, "Task 11 Private Same-batch", batch.Description, len(batch.CaseRefs), coverage, privacy, batch.ContentHash).Scan(&suitePK)
	if err != nil {
		return nil, fmt.Errorf("upsert private batch suite: %w", err)
	}
	var storedHash string
	var storedCount int
	if err := tx.QueryRowContext(ctx, `SELECT content_hash, case_count FROM wabench_suites WHERE id = $1`, suitePK).Scan(&storedHash, &storedCount); err != nil {
		return nil, err
	}
	if storedHash != batch.ContentHash || storedCount != len(batch.CaseRefs) {
		return nil, fmt.Errorf("private batch suite is immutable; use a new suite id/version")
	}
	weights, _ := json.Marshal(map[string]int{"taskCompliance": 25, "sourceFidelity": 25, "structureReasoning": 15, "styleConsistency": 15, "directUsability": 20})
	for _, ref := range batch.CaseRefs {
		row := rowsByID[ref.CaseID]
		contextJSON, _ := json.Marshal(map[string]interface{}{
			"knowledgeOnly": row.Retrieval.KnowledgeOnly, "expectedKnowledgeProvider": row.Retrieval.KnowledgeProvider, "sourceTraceHash": row.SourceTraceHash,
		})
		taskType := row.TaskType
		if taskType != "topic" && taskType != "writing" && taskType != "polish" && taskType != "dedupe" && taskType != "abnormal" {
			taskType = "writing"
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO wabench_cases (case_id, suite_pk, schema_version, task_type, difficulty, input_storage, input_ref, input_hash, redacted_input_hash, context, source_mode, source_fixture_refs, expected_behavior, must_have, must_not_have, hard_gate_ids, rubric_weights, capability_tags, risk_tags, rule_profile_refs, privacy_level)
			VALUES ($1, $2, 'wabench.v1', $3, 'L2', 'private_ref', $4, $5, $6, $7, 'none', '{}', 'answer', '{}', '{}', '{}', $8, ARRAY['real_business_fit'], ARRAY['privacy.real_user'], ARRAY['luminbuddy.private.general-writing'], 'anonymized')
			ON CONFLICT (case_id) DO NOTHING
		`, ref.CaseID, suitePK, taskType, wabenchPrivateHoldoutPrefix+ref.CaseID, ref.OriginInputHash, ref.InputHash, contextJSON, weights)
		if err != nil {
			return nil, fmt.Errorf("insert private case %s: %w", ref.CaseID, err)
		}
	}
	var actual int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM wabench_cases WHERE suite_pk = $1`, suitePK).Scan(&actual); err != nil {
		return nil, err
	}
	if actual != len(batch.CaseRefs) {
		return nil, fmt.Errorf("stored private batch has %d cases, expected %d", actual, len(batch.CaseRefs))
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &WABenchBatchImportResult{SuiteID: batch.SuiteID, BatchID: batch.BatchID, CaseCount: actual, FixtureCount: 0, ContentHash: batch.ContentHash}, nil
}
