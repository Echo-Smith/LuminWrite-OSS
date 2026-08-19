package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

const WABenchCenterListLimit = 500

type WABenchCenterOverview struct {
	SuiteCount            int      `json:"suiteCount"`
	CandidateCount        int      `json:"candidateCount"`
	RunCount              int      `json:"runCount"`
	ReviewCount           int      `json:"reviewCount"`
	RunningCount          int      `json:"runningCount"`
	FailedRunCount        int      `json:"failedRunCount"`
	AverageScore          *float64 `json:"averageScore"`
	HardFailureRate       *float64 `json:"hardFailureRate"`
	AcceptanceRate        *float64 `json:"acceptanceRate"`
	ModificationBurden    *float64 `json:"modificationBurden"`
	P50LatencyMs          *float64 `json:"p50LatencyMs"`
	P95LatencyMs          *float64 `json:"p95LatencyMs"`
	SourceBoundaryFailure int      `json:"sourceBoundaryFailureCount"`
	LatestGateDecision    string   `json:"latestGateDecision"`
	CostStatus            string   `json:"costStatus"`
}

type WABenchCenterSuite struct {
	SuiteID      string                 `json:"suiteId"`
	Name         string                 `json:"name"`
	Version      string                 `json:"version"`
	Description  string                 `json:"description"`
	Partition    string                 `json:"partition"`
	Visibility   string                 `json:"visibility"`
	Status       string                 `json:"status"`
	CaseCount    int                    `json:"caseCount"`
	Coverage     map[string]interface{} `json:"coverage"`
	Privacy      map[string]interface{} `json:"privacy"`
	ContentHash  string                 `json:"contentHash,omitempty"`
	TaskCounts   map[string]int         `json:"taskCounts"`
	PrivacyLabel string                 `json:"privacyLabel"`
	CreatedAt    time.Time              `json:"createdAt"`
}

type WABenchCenterCandidate struct {
	CandidateID   string                 `json:"candidateId"`
	Name          string                 `json:"name"`
	PromptHash    string                 `json:"promptHash"`
	MemoryHash    string                 `json:"memoryHash,omitempty"`
	ModelManifest map[string]interface{} `json:"modelManifest"`
	CodeHash      string                 `json:"codeHash"`
	ToolManifest  map[string]interface{} `json:"toolManifest"`
	FeatureFlags  map[string]interface{} `json:"featureFlags"`
	CreatedAt     time.Time              `json:"createdAt"`
}

type WABenchCenterRun struct {
	RunID                string     `json:"runId"`
	SuiteID              string     `json:"suiteId"`
	SuiteName            string     `json:"suiteName"`
	CandidateID          string     `json:"candidateId"`
	CandidateName        string     `json:"candidateName"`
	AdapterID            string     `json:"adapterId"`
	RunnerVersion        string     `json:"runnerVersion"`
	Environment          string     `json:"environment"`
	TrafficType          string     `json:"trafficType"`
	Status               string     `json:"status"`
	TotalCases           int        `json:"totalCases"`
	CompletedCases       int        `json:"completedCases"`
	FailedCases          int        `json:"failedCases"`
	ScoredCases          int        `json:"scoredCases"`
	AverageWeightedScore *float64   `json:"averageWeightedScore"`
	GateDecision         string     `json:"gateDecision,omitempty"`
	StartedAt            *time.Time `json:"startedAt,omitempty"`
	CompletedAt          *time.Time `json:"completedAt,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
}

type WABenchCenterReview struct {
	ReviewID            string    `json:"reviewId"`
	RunID               string    `json:"runId"`
	OutputID            string    `json:"outputId"`
	CaseID              string    `json:"caseId"`
	OutputStatus        string    `json:"outputStatus"`
	TextStorage         string    `json:"textStorage"`
	PrivacyLevel        string    `json:"privacyLevel"`
	ContentAvailable    bool      `json:"contentAvailable"`
	ReviewerID          string    `json:"reviewerId"`
	ReviewerRole        string    `json:"reviewerRole"`
	ReviewerType        string    `json:"reviewerType"`
	ReviewMethod        string    `json:"reviewMethod"`
	LabelSource         string    `json:"labelSource"`
	IsBlind             bool      `json:"isBlind"`
	TaskCompliance      int       `json:"taskCompliance"`
	SourceFidelity      int       `json:"sourceFidelity"`
	StructureReasoning  int       `json:"structureReasoning"`
	StyleConsistency    int       `json:"styleConsistency"`
	DirectUsability     int       `json:"directUsability"`
	AcceptanceLabel     string    `json:"acceptanceLabel"`
	ModificationBurden  *int      `json:"modificationBurden,omitempty"`
	HardFailureIDs      []string  `json:"hardFailureIds"`
	PrimaryRootCause    string    `json:"primaryRootCause,omitempty"`
	SecondaryRootCauses []string  `json:"secondaryRootCauses"`
	ArbitrationStatus   string    `json:"arbitrationStatus"`
	IsArbitration       bool      `json:"isArbitration"`
	ReviewedAt          time.Time `json:"reviewedAt"`
}

type WABenchCenterBadcase struct {
	RunID            string                   `json:"runId"`
	OutputID         string                   `json:"outputId"`
	CaseID           string                   `json:"caseId"`
	OutputStatus     string                   `json:"outputStatus"`
	Symptoms         []map[string]interface{} `json:"symptoms"`
	PrimaryRootCause string                   `json:"primaryRootCause,omitempty"`
	HardFailureIDs   []string                 `json:"hardFailureIds"`
	Owner            string                   `json:"owner,omitempty"`
	FixVersion       string                   `json:"fixVersion,omitempty"`
	RegressionStatus string                   `json:"regressionStatus"`
	PrivacyLevel     string                   `json:"privacyLevel"`
	TraceRef         string                   `json:"traceRef,omitempty"`
	CreatedAt        time.Time                `json:"createdAt"`
}

type WABenchCenterRelease struct {
	DecisionID         string                   `json:"decisionId"`
	RunID              string                   `json:"runId"`
	CandidateID        string                   `json:"candidateId"`
	SuiteID            string                   `json:"suiteId"`
	Decision           string                   `json:"decision"`
	Evidence           map[string]interface{}   `json:"evidence"`
	Exceptions         []map[string]interface{} `json:"exceptions"`
	RollbackConditions []map[string]interface{} `json:"rollbackConditions"`
	OwnerRef           string                   `json:"ownerRef"`
	DecidedAt          time.Time                `json:"decidedAt"`
}

type WABenchHumanReviewImport struct {
	ReviewID            string
	OutputID            string
	ReviewerID          string
	ReviewerRole        string
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

func clampWABenchCenterLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > WABenchCenterListLimit {
		return WABenchCenterListLimit
	}
	return limit
}

func scanNullableFloat(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	result := value.Float64
	return &result
}

func decodeCenterMap(raw []byte) (map[string]interface{}, error) {
	result := map[string]interface{}{}
	if len(raw) == 0 {
		return result, nil
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func decodeCenterObjects(raw []byte) ([]map[string]interface{}, error) {
	result := []map[string]interface{}{}
	if len(raw) == 0 {
		return result, nil
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func WABenchPrivacyLabel(visibility, privacyLevel string) string {
	if visibility == "public" && privacyLevel == "synthetic" {
		return "public"
	}
	if privacyLevel == "anonymized" || privacyLevel == "redacted" {
		return "redacted"
	}
	return "private"
}

func WABenchContentAvailable(textStorage, privacyLevel string) bool {
	return textStorage == "inline_public" && privacyLevel == "synthetic"
}

func (r *WABenchRepo) GetCenterOverview(ctx context.Context) (*WABenchCenterOverview, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	var result WABenchCenterOverview
	var averageScore, hardFailureRate, acceptanceRate, burden, p50, p95 sql.NullFloat64
	var latestGate sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM wabench_suites),
			(SELECT COUNT(*) FROM wabench_candidates),
			(SELECT COUNT(*) FROM wabench_runs),
			(SELECT COUNT(*) FROM wabench_reviews),
			(SELECT COUNT(*) FROM wabench_runs WHERE status IN ('pending', 'running')),
			(SELECT COUNT(*) FROM wabench_runs WHERE status = 'failed'),
			(SELECT AVG((task_compliance + source_fidelity + structure_reasoning + style_consistency + direct_usability) * 4.0) FROM wabench_reviews WHERE reviewer_type = 'model'),
			(SELECT COUNT(DISTINCT output_pk) FILTER (WHERE cardinality(hard_failure_ids) > 0)::FLOAT / NULLIF(COUNT(DISTINCT output_pk), 0) FROM wabench_reviews),
			(SELECT COUNT(*) FILTER (WHERE acceptance_label IN ('direct_use', 'light_edit'))::FLOAT / NULLIF(COUNT(*) FILTER (WHERE acceptance_label <> 'unknown'), 0) FROM wabench_reviews WHERE reviewer_type = 'human'),
			(SELECT AVG(modification_burden::FLOAT) FROM wabench_reviews WHERE reviewer_type = 'human' AND modification_burden IS NOT NULL),
			(SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY (metrics->>'latencyMs')::FLOAT) FROM wabench_outputs WHERE metrics ? 'latencyMs'),
			(SELECT percentile_cont(0.95) WITHIN GROUP (ORDER BY (metrics->>'latencyMs')::FLOAT) FROM wabench_outputs WHERE metrics ? 'latencyMs'),
			(SELECT COUNT(*) FROM wabench_checks WHERE check_id LIKE 'routing.%' AND status = 'fail'),
			(SELECT decision FROM wabench_gate_decisions ORDER BY decided_at DESC LIMIT 1)
	`).Scan(
		&result.SuiteCount, &result.CandidateCount, &result.RunCount, &result.ReviewCount,
		&result.RunningCount, &result.FailedRunCount, &averageScore, &hardFailureRate,
		&acceptanceRate, &burden, &p50, &p95, &result.SourceBoundaryFailure, &latestGate,
	)
	if err != nil {
		return nil, err
	}
	result.AverageScore = scanNullableFloat(averageScore)
	result.HardFailureRate = scanNullableFloat(hardFailureRate)
	result.AcceptanceRate = scanNullableFloat(acceptanceRate)
	result.ModificationBurden = scanNullableFloat(burden)
	result.P50LatencyMs = scanNullableFloat(p50)
	result.P95LatencyMs = scanNullableFloat(p95)
	result.LatestGateDecision = latestGate.String
	result.CostStatus = "unavailable"
	return &result, nil
}

func (r *WABenchRepo) ListCenterSuites(ctx context.Context, limit int) ([]WABenchCenterSuite, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.suite_id, s.name, s.version, s.description, s.partition, s.visibility,
		       s.status, s.case_count, s.coverage, s.privacy, COALESCE(s.content_hash, ''),
		       s.created_at,
		       COALESCE(jsonb_object_agg(c.task_type, c.task_count) FILTER (WHERE c.task_type IS NOT NULL), '{}'::jsonb)
		FROM wabench_suites s
		LEFT JOIN (
			SELECT suite_pk, task_type, COUNT(*)::INTEGER AS task_count
			FROM wabench_cases GROUP BY suite_pk, task_type
		) c ON c.suite_pk = s.id
		GROUP BY s.id
		ORDER BY s.created_at DESC LIMIT $1
	`, clampWABenchCenterLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []WABenchCenterSuite{}
	for rows.Next() {
		var item WABenchCenterSuite
		var coverageRaw, privacyRaw, tasksRaw []byte
		if err := rows.Scan(&item.SuiteID, &item.Name, &item.Version, &item.Description, &item.Partition,
			&item.Visibility, &item.Status, &item.CaseCount, &coverageRaw, &privacyRaw, &item.ContentHash,
			&item.CreatedAt, &tasksRaw); err != nil {
			return nil, err
		}
		if item.Coverage, err = decodeCenterMap(coverageRaw); err != nil {
			return nil, err
		}
		if item.Privacy, err = decodeCenterMap(privacyRaw); err != nil {
			return nil, err
		}
		var taskNumbers map[string]int
		if err := json.Unmarshal(tasksRaw, &taskNumbers); err != nil {
			return nil, err
		}
		item.TaskCounts = taskNumbers
		privacyLevel, _ := item.Privacy["privacyLevel"].(string)
		item.PrivacyLabel = WABenchPrivacyLabel(item.Visibility, privacyLevel)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *WABenchRepo) ListCenterCandidates(ctx context.Context, limit int) ([]WABenchCenterCandidate, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT candidate_id, name, prompt_hash, COALESCE(memory_hash, ''), model_manifest,
		       code_hash, tool_manifest, feature_flags, created_at
		FROM wabench_candidates ORDER BY created_at DESC LIMIT $1
	`, clampWABenchCenterLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []WABenchCenterCandidate{}
	for rows.Next() {
		var item WABenchCenterCandidate
		var modelRaw, toolsRaw, flagsRaw []byte
		if err := rows.Scan(&item.CandidateID, &item.Name, &item.PromptHash, &item.MemoryHash,
			&modelRaw, &item.CodeHash, &toolsRaw, &flagsRaw, &item.CreatedAt); err != nil {
			return nil, err
		}
		if item.ModelManifest, err = decodeCenterMap(modelRaw); err != nil {
			return nil, err
		}
		if item.ToolManifest, err = decodeCenterMap(toolsRaw); err != nil {
			return nil, err
		}
		if item.FeatureFlags, err = decodeCenterMap(flagsRaw); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *WABenchRepo) ListCenterRuns(ctx context.Context, limit int) ([]WABenchCenterRun, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT r.run_id, s.suite_id, s.name, c.candidate_id, c.name, r.adapter_id,
		       r.runner_version, r.environment, r.traffic_type, r.status, r.total_cases,
		       r.completed_cases, r.failed_cases,
		       COUNT(DISTINCT o.id) FILTER (WHERE rv.reviewer_type = 'model')::INTEGER,
		       AVG((rv.task_compliance + rv.source_fidelity + rv.structure_reasoning + rv.style_consistency + rv.direct_usability) * 4.0)
		           FILTER (WHERE rv.reviewer_type = 'model'),
		       COALESCE((SELECT gd.decision FROM wabench_gate_decisions gd WHERE gd.run_pk = r.id ORDER BY gd.decided_at DESC LIMIT 1), ''),
		       r.started_at, r.completed_at, r.created_at
		FROM wabench_runs r
		JOIN wabench_suites s ON s.id = r.suite_pk
		JOIN wabench_candidates c ON c.id = r.candidate_pk
		LEFT JOIN wabench_outputs o ON o.run_pk = r.id
		LEFT JOIN wabench_reviews rv ON rv.output_pk = o.id
		GROUP BY r.id, s.id, c.id
		ORDER BY r.created_at DESC LIMIT $1
	`, clampWABenchCenterLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []WABenchCenterRun{}
	for rows.Next() {
		var item WABenchCenterRun
		var average sql.NullFloat64
		if err := rows.Scan(&item.RunID, &item.SuiteID, &item.SuiteName, &item.CandidateID,
			&item.CandidateName, &item.AdapterID, &item.RunnerVersion, &item.Environment,
			&item.TrafficType, &item.Status, &item.TotalCases, &item.CompletedCases,
			&item.FailedCases, &item.ScoredCases, &average, &item.GateDecision,
			&item.StartedAt, &item.CompletedAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.AverageWeightedScore = scanNullableFloat(average)
		result = append(result, item)
	}
	return result, rows.Err()
}

func centerReviewIsArbitration(evidence map[string]interface{}) bool {
	value, _ := evidence["isArbitration"].(bool)
	return value
}

func centerReviewScores(item WABenchCenterReview) [5]int {
	return [5]int{item.TaskCompliance, item.SourceFidelity, item.StructureReasoning, item.StyleConsistency, item.DirectUsability}
}

func applyCenterArbitrationStatus(items []WABenchCenterReview) {
	groups := map[string][]int{}
	for index := range items {
		groups[items[index].OutputID] = append(groups[items[index].OutputID], index)
	}
	for _, indexes := range groups {
		humans := []WABenchCenterReview{}
		initialReviewerIDs := map[string]bool{}
		arbitratorIDs := map[string]bool{}
		for _, index := range indexes {
			item := items[index]
			if item.ReviewerType != "human" {
				continue
			}
			reviewerID := strings.TrimSpace(item.ReviewerID)
			if item.IsArbitration {
				arbitratorIDs[reviewerID] = true
				continue
			}
			if !initialReviewerIDs[reviewerID] {
				initialReviewerIDs[reviewerID] = true
				humans = append(humans, item)
			}
		}
		status := "not_required"
		if len(humans) >= 2 {
			base := centerReviewScores(humans[0])
			disagrees := false
			for _, candidate := range humans[1:] {
				if centerReviewScores(candidate) != base || candidate.AcceptanceLabel != humans[0].AcceptanceLabel {
					disagrees = true
					break
				}
			}
			if disagrees {
				status = "pending"
				for arbitratorID := range arbitratorIDs {
					if arbitratorID != "" && !initialReviewerIDs[arbitratorID] {
						status = "resolved"
						break
					}
				}
			}
		}
		for _, index := range indexes {
			items[index].ArbitrationStatus = status
		}
	}
}

func (r *WABenchRepo) ListCenterReviews(ctx context.Context, runID string, limit int) ([]WABenchCenterReview, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	query := `
		SELECT rv.review_id, run.run_id, o.output_id, c.case_id, o.status, o.text_storage,
		       c.privacy_level, rv.reviewer_id, rv.reviewer_role, rv.reviewer_type,
		       rv.review_method, rv.label_source, rv.is_blind, rv.task_compliance,
		       rv.source_fidelity, rv.structure_reasoning, rv.style_consistency,
		       rv.direct_usability, rv.acceptance_label, rv.modification_burden,
		       rv.hard_failure_ids, COALESCE(rv.primary_root_cause, ''),
		       rv.secondary_root_causes, rv.evidence, rv.reviewed_at
		FROM wabench_reviews rv
		JOIN wabench_outputs o ON o.id = rv.output_pk
		JOIN wabench_runs run ON run.id = o.run_pk
		JOIN wabench_cases c ON c.id = o.case_pk
		WHERE ($1 = '' OR run.run_id = $1)
		ORDER BY rv.reviewed_at DESC LIMIT $2
	`
	rows, err := r.db.QueryContext(ctx, query, strings.TrimSpace(runID), clampWABenchCenterLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []WABenchCenterReview{}
	for rows.Next() {
		var item WABenchCenterReview
		var burden sql.NullInt64
		var evidenceRaw []byte
		if err := rows.Scan(&item.ReviewID, &item.RunID, &item.OutputID, &item.CaseID,
			&item.OutputStatus, &item.TextStorage, &item.PrivacyLevel, &item.ReviewerID,
			&item.ReviewerRole, &item.ReviewerType, &item.ReviewMethod, &item.LabelSource,
			&item.IsBlind, &item.TaskCompliance, &item.SourceFidelity, &item.StructureReasoning,
			&item.StyleConsistency, &item.DirectUsability, &item.AcceptanceLabel, &burden,
			pq.Array(&item.HardFailureIDs), &item.PrimaryRootCause, pq.Array(&item.SecondaryRootCauses),
			&evidenceRaw, &item.ReviewedAt); err != nil {
			return nil, err
		}
		if burden.Valid {
			value := int(burden.Int64)
			item.ModificationBurden = &value
		}
		evidence, err := decodeCenterMap(evidenceRaw)
		if err != nil {
			return nil, err
		}
		item.IsArbitration = centerReviewIsArbitration(evidence)
		item.ContentAvailable = WABenchContentAvailable(item.TextStorage, item.PrivacyLevel)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	applyCenterArbitrationStatus(result)
	return result, nil
}

func (r *WABenchRepo) ListCenterBadcases(ctx context.Context, limit int) ([]WABenchCenterBadcase, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT run.run_id, o.output_id, c.case_id, o.status, o.failures,
		       COALESCE(rv.primary_root_cause, ''), COALESCE(rv.hard_failure_ids, '{}'),
		       COALESCE(rv.evidence, '{}'::jsonb), c.privacy_level, COALESCE(o.trace_ref, ''), o.created_at
		FROM wabench_outputs o
		JOIN wabench_runs run ON run.id = o.run_pk
		JOIN wabench_cases c ON c.id = o.case_pk
		LEFT JOIN LATERAL (
			SELECT primary_root_cause, hard_failure_ids, evidence
			FROM wabench_reviews WHERE output_pk = o.id
			ORDER BY reviewed_at DESC LIMIT 1
		) rv ON TRUE
		WHERE o.status <> 'complete'
		   OR jsonb_array_length(o.failures) > 0
		   OR cardinality(COALESCE(rv.hard_failure_ids, '{}')) > 0
		   OR COALESCE(rv.primary_root_cause, '') <> ''
		ORDER BY o.created_at DESC LIMIT $1
	`, clampWABenchCenterLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []WABenchCenterBadcase{}
	for rows.Next() {
		var item WABenchCenterBadcase
		var failuresRaw, evidenceRaw []byte
		if err := rows.Scan(&item.RunID, &item.OutputID, &item.CaseID, &item.OutputStatus,
			&failuresRaw, &item.PrimaryRootCause, pq.Array(&item.HardFailureIDs), &evidenceRaw,
			&item.PrivacyLevel, &item.TraceRef, &item.CreatedAt); err != nil {
			return nil, err
		}
		if item.Symptoms, err = decodeCenterObjects(failuresRaw); err != nil {
			return nil, err
		}
		evidence, err := decodeCenterMap(evidenceRaw)
		if err != nil {
			return nil, err
		}
		item.Owner, _ = evidence["owner"].(string)
		item.FixVersion, _ = evidence["fixVersion"].(string)
		item.RegressionStatus, _ = evidence["regressionStatus"].(string)
		if item.RegressionStatus == "" {
			item.RegressionStatus = "open"
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *WABenchRepo) ListCenterReleases(ctx context.Context, limit int) ([]WABenchCenterRelease, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT gd.decision_id, run.run_id, c.candidate_id, s.suite_id, gd.decision,
		       gd.evidence, gd.exceptions, gd.rollback_conditions, gd.owner_ref, gd.decided_at
		FROM wabench_gate_decisions gd
		JOIN wabench_runs run ON run.id = gd.run_pk
		JOIN wabench_candidates c ON c.id = run.candidate_pk
		JOIN wabench_suites s ON s.id = run.suite_pk
		ORDER BY gd.decided_at DESC LIMIT $1
	`, clampWABenchCenterLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []WABenchCenterRelease{}
	for rows.Next() {
		var item WABenchCenterRelease
		var evidenceRaw, exceptionsRaw, rollbackRaw []byte
		if err := rows.Scan(&item.DecisionID, &item.RunID, &item.CandidateID, &item.SuiteID,
			&item.Decision, &evidenceRaw, &exceptionsRaw, &rollbackRaw, &item.OwnerRef,
			&item.DecidedAt); err != nil {
			return nil, err
		}
		if item.Evidence, err = decodeCenterMap(evidenceRaw); err != nil {
			return nil, err
		}
		if item.Exceptions, err = decodeCenterObjects(exceptionsRaw); err != nil {
			return nil, err
		}
		if item.RollbackConditions, err = decodeCenterObjects(rollbackRaw); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func validateImportedHumanReview(review WABenchHumanReviewImport) error {
	if strings.TrimSpace(review.ReviewID) == "" || strings.TrimSpace(review.OutputID) == "" {
		return fmt.Errorf("reviewId and outputId are required")
	}
	if strings.TrimSpace(review.ReviewerID) == "" || strings.TrimSpace(review.ReviewerRole) == "" || strings.TrimSpace(review.ReviewMethod) == "" || strings.TrimSpace(review.LabelSource) == "" {
		return fmt.Errorf("reviewerId, reviewerRole, reviewMethod and labelSource are required")
	}
	for name, score := range map[string]int{
		"taskCompliance": review.TaskCompliance, "sourceFidelity": review.SourceFidelity,
		"structureReasoning": review.StructureReasoning, "styleConsistency": review.StyleConsistency,
		"directUsability": review.DirectUsability,
	} {
		if score < 1 || score > 5 {
			return fmt.Errorf("%s must be between 1 and 5", name)
		}
	}
	validAcceptance := map[string]bool{"direct_use": true, "light_edit": true, "heavy_edit": true, "reject": true, "unknown": true}
	if !validAcceptance[review.AcceptanceLabel] {
		return fmt.Errorf("invalid acceptance label %q", review.AcceptanceLabel)
	}
	if review.ModificationBurden != nil && (*review.ModificationBurden < 0 || *review.ModificationBurden > 3) {
		return fmt.Errorf("modification burden must be between 0 and 3")
	}
	validRoot := map[string]bool{"": true, "input": true, "retrieval": true, "prompt": true, "memory": true, "tool": true, "model": true, "interaction": true}
	if !validRoot[review.PrimaryRootCause] {
		return fmt.Errorf("invalid primary root cause %q", review.PrimaryRootCause)
	}
	for _, root := range review.SecondaryRootCauses {
		if !validRoot[root] || root == "" {
			return fmt.Errorf("invalid secondary root cause %q", root)
		}
	}
	if review.ReviewedAt.IsZero() {
		return fmt.Errorf("reviewedAt is required")
	}
	return nil
}

func (r *WABenchRepo) InsertHumanReviews(ctx context.Context, reviews []WABenchHumanReviewImport) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("database not available")
	}
	if len(reviews) == 0 {
		return fmt.Errorf("at least one review is required")
	}
	seen := map[string]bool{}
	for _, review := range reviews {
		if err := validateImportedHumanReview(review); err != nil {
			return fmt.Errorf("review %q: %w", review.ReviewID, err)
		}
		if seen[review.ReviewID] {
			return fmt.Errorf("duplicate reviewId %q in workbook", review.ReviewID)
		}
		seen[review.ReviewID] = true
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, review := range reviews {
		hardFailureIDs := review.HardFailureIDs
		if hardFailureIDs == nil {
			hardFailureIDs = []string{}
		}
		secondaryRootCauses := review.SecondaryRootCauses
		if secondaryRootCauses == nil {
			secondaryRootCauses = []string{}
		}
		evidence, err := json.Marshal(review.Evidence)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO wabench_reviews (
				review_id, output_pk, reviewer_id, reviewer_role, reviewer_type,
				review_method, label_source, is_blind, task_compliance, source_fidelity,
				structure_reasoning, style_consistency, direct_usability, acceptance_label,
				modification_burden, hard_failure_ids, primary_root_cause,
				secondary_root_causes, evidence, reviewed_at
			)
			SELECT $1, o.id, $3, $4, 'human', $5, $6, $7, $8, $9, $10, $11,
			       $12, $13, $14, $15, NULLIF($16, ''), $17, $18, $19
			FROM wabench_outputs o WHERE o.output_id = $2
			ON CONFLICT (review_id) DO NOTHING
		`, review.ReviewID, review.OutputID, review.ReviewerID, review.ReviewerRole,
			review.ReviewMethod, review.LabelSource, review.IsBlind, review.TaskCompliance,
			review.SourceFidelity, review.StructureReasoning, review.StyleConsistency,
			review.DirectUsability, review.AcceptanceLabel, review.ModificationBurden,
			pq.Array(hardFailureIDs), review.PrimaryRootCause,
			pq.Array(secondaryRootCauses), evidence, review.ReviewedAt)
		if err != nil {
			return fmt.Errorf("insert review %s: %w", review.ReviewID, err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("review %s was not inserted: outputId is unknown or reviewId already exists", review.ReviewID)
		}
	}
	return tx.Commit()
}

func SortedWABenchRootCauses() []string {
	result := []string{"input", "retrieval", "prompt", "memory", "tool", "model", "interaction"}
	sort.Strings(result)
	return result
}
