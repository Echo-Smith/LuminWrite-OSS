package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

var normalizedWABenchIDChars = regexp.MustCompile(`[^a-z0-9_]+`)
var normalizedWABenchTag = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)+$`)

type WABenchNormalizedRun struct {
	SchemaVersion   string     `json:"schemaVersion"`
	RunID           string     `json:"runId"`
	SuiteID         string     `json:"suiteId"`
	CandidateID     string     `json:"candidateId"`
	AdapterID       string     `json:"adapterId"`
	RunnerVersion   string     `json:"runnerVersion"`
	Environment     string     `json:"environment"`
	TrafficType     string     `json:"trafficType"`
	EvaluationRunID string     `json:"evaluationRunId,omitempty"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"startedAt"`
	CompletedAt     *time.Time `json:"completedAt"`
	OutputRefs      []string   `json:"outputRefs"`
}

type WABenchNormalizedTextStorage struct {
	Mode       string `json:"mode"`
	Text       string `json:"text,omitempty"`
	PrivateRef string `json:"privateRef,omitempty"`
}

type WABenchNormalizedCheck struct {
	CheckID  string                 `json:"checkId"`
	Status   string                 `json:"status"`
	Severity string                 `json:"severity"`
	Evidence map[string]interface{} `json:"evidence"`
}

type WABenchNormalizedFailure struct {
	Stage       string `json:"stage"`
	Type        string `json:"type"`
	Code        string `json:"code"`
	HardFailure bool   `json:"hardFailure"`
	Retryable   bool   `json:"retryable"`
	UserVisible bool   `json:"userVisible"`
}

type WABenchNormalizedMetrics struct {
	LatencyMs        *int64   `json:"latencyMs"`
	InputTokens      *int64   `json:"inputTokens"`
	OutputTokens     *int64   `json:"outputTokens"`
	ToolFailureCount int      `json:"toolFailureCount"`
	CostStatus       string   `json:"costStatus"`
	Cost             *float64 `json:"cost"`
	Currency         *string  `json:"currency"`
}

type WABenchNormalizedRouting struct {
	KnowledgeProvider  *string `json:"knowledgeProvider"`
	KnowledgeOnly      bool    `json:"knowledgeOnly"`
	WebSearchTriggered bool    `json:"webSearchTriggered"`
}

type WABenchNormalizedOutput struct {
	SchemaVersion string                       `json:"schemaVersion"`
	OutputID      string                       `json:"outputId"`
	RunID         string                       `json:"runId"`
	CaseID        string                       `json:"caseId"`
	Status        string                       `json:"status"`
	OutputHash    string                       `json:"outputHash"`
	TextStorage   WABenchNormalizedTextStorage `json:"textStorage"`
	Checks        []WABenchNormalizedCheck     `json:"checks"`
	Failures      []WABenchNormalizedFailure   `json:"failures"`
	Metrics       WABenchNormalizedMetrics     `json:"metrics"`
	Routing       WABenchNormalizedRouting     `json:"routing"`
	TraceRef      *string                      `json:"traceRef"`
	CreatedAt     time.Time                    `json:"createdAt"`
}

type WABenchNormalizedReviewer struct {
	Alias     string `json:"alias"`
	Type      string `json:"type"`
	Role      string `json:"role"`
	Mode      string `json:"mode"`
	TagSource string `json:"tagSource"`
}

type WABenchNormalizedScores struct {
	TaskCompliance     int `json:"taskCompliance"`
	SourceFidelity     int `json:"sourceFidelity"`
	StructureReasoning int `json:"structureReasoning"`
	StyleConsistency   int `json:"styleConsistency"`
	DirectUsability    int `json:"directUsability"`
}

type WABenchNormalizedReview struct {
	SchemaVersion       string                    `json:"schemaVersion"`
	ReviewID            string                    `json:"reviewId"`
	RunID               string                    `json:"runId"`
	CaseID              string                    `json:"caseId"`
	OutputHash          string                    `json:"outputHash"`
	RubricVersion       string                    `json:"rubricVersion"`
	Reviewer            WABenchNormalizedReviewer `json:"reviewer"`
	Scores              WABenchNormalizedScores   `json:"scores"`
	Acceptance          string                    `json:"acceptance"`
	ModificationBurden  *int                      `json:"modificationBurden"`
	HardFailures        []string                  `json:"hardFailures"`
	SymptomTags         []string                  `json:"symptomTags"`
	PrimaryRootCause    *string                   `json:"primaryRootCause"`
	SecondaryRootCauses []string                  `json:"secondaryRootCauses"`
	ReviewedAt          time.Time                 `json:"reviewedAt"`
}

type WABenchNormalizedBundle struct {
	SchemaVersion    string                    `json:"schemaVersion"`
	BundleID         string                    `json:"bundleId"`
	BatchID          string                    `json:"batchId"`
	BatchContentHash string                    `json:"batchContentHash"`
	ReviewStatus     string                    `json:"reviewStatus"`
	Run              WABenchNormalizedRun      `json:"run"`
	Outputs          []WABenchNormalizedOutput `json:"outputs"`
	Reviews          []WABenchNormalizedReview `json:"reviews"`
	GeneratedAt      time.Time                 `json:"generatedAt"`
}

type wabenchBundleOutputRow struct {
	PK, OutputID, CaseID, Status, OutputHash, TextStorage string
	OutputText, PrivateRef, TraceRef                      sql.NullString
	FailuresRaw, MetricsRaw, RoutingRaw, ContextRaw       []byte
	CreatedAt                                             time.Time
}

func normalizedWABenchAlias(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = normalizedWABenchIDChars.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		value = "reviewer_" + value
	}
	if len(value) > 40 {
		value = value[:40]
	}
	return value
}

func normalizedWABenchFailure(raw map[string]interface{}) WABenchNormalizedFailure {
	id, _ := raw["id"].(string)
	stage, _ := raw["stage"].(string)
	switch stage {
	case "capture":
		stage = "input"
	case "routing":
		stage = "retrieval"
	case "safety":
		stage = "generation"
	case "input", "retrieval", "tool", "generation", "judge", "stream", "storage", "unknown":
	default:
		stage = "unknown"
	}
	typeName := id
	if index := strings.LastIndex(typeName, "."); index >= 0 {
		typeName = typeName[index+1:]
	}
	typeName = normalizedWABenchIDChars.ReplaceAllString(strings.ToLower(typeName), "_")
	if typeName == "" {
		typeName = "unknown_failure"
	}
	code := strings.ToUpper(normalizedWABenchIDChars.ReplaceAllString(strings.ReplaceAll(id, ".", "_"), "_"))
	if code == "" || code[0] < 'A' || code[0] > 'Z' {
		code = "UNKNOWN_FAILURE"
	}
	return WABenchNormalizedFailure{Stage: stage, Type: typeName, Code: code, HardFailure: true, Retryable: false, UserVisible: true}
}

func numberFromMap(values map[string]interface{}, key string) *int64 {
	value, ok := values[key].(float64)
	if !ok || value < 0 {
		return nil
	}
	result := int64(value)
	return &result
}

func normalizeWABenchMetrics(raw map[string]interface{}, checks []WABenchNormalizedCheck) WABenchNormalizedMetrics {
	result := WABenchNormalizedMetrics{LatencyMs: numberFromMap(raw, "latencyMs"), CostStatus: "unavailable"}
	for _, check := range checks {
		if strings.HasPrefix(check.CheckID, "tool.") && check.Status == "fail" {
			result.ToolFailureCount++
		}
	}
	if cost, ok := raw["cost"].(map[string]interface{}); ok {
		availability, _ := cost["availability"].(string)
		amount, amountOK := cost["amount"].(float64)
		currency, currencyOK := cost["currency"].(string)
		if (availability == "observed" || availability == "estimated") && amountOK && amount >= 0 {
			result.CostStatus = availability
			result.Cost = &amount
			if currencyOK && currency != "" {
				result.Currency = &currency
			}
		}
	}
	return result
}

func normalizeWABenchRouting(raw, contextData map[string]interface{}) WABenchNormalizedRouting {
	result := WABenchNormalizedRouting{}
	result.KnowledgeOnly, _ = contextData["knowledgeOnly"].(bool)
	result.WebSearchTriggered, _ = raw["webSearchTriggered"].(bool)
	if providers, ok := raw["knowledgeProviders"].([]interface{}); ok && len(providers) > 0 {
		if value, ok := providers[0].(string); ok && value != "" {
			result.KnowledgeProvider = &value
		}
	}
	return result
}

func decodeBundleMap(raw []byte) (map[string]interface{}, error) {
	result := map[string]interface{}{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *WABenchRepo) GetNormalizedRunBundle(ctx context.Context, runID, batchID, batchContentHash string) (*WABenchNormalizedBundle, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if strings.TrimSpace(batchID) == "" || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(batchContentHash) {
		return nil, fmt.Errorf("valid batchId and batchContentHash are required")
	}
	execution, err := r.GetRunExecution(ctx, runID)
	if err != nil {
		return nil, err
	}
	if execution.Run.StartedAt == nil {
		return nil, fmt.Errorf("WABench run %s has not started", runID)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT o.id::text, o.output_id, c.case_id, o.status, o.output_hash, o.text_storage,
		       o.output_text, o.private_ref, o.failures, o.metrics, o.routing, o.trace_ref,
		       c.context, o.created_at
		FROM wabench_outputs o JOIN wabench_cases c ON c.id = o.case_pk
		WHERE o.run_pk = $1 ORDER BY c.case_id
	`, execution.Run.PK)
	if err != nil {
		return nil, fmt.Errorf("load normalized WABench outputs: %w", err)
	}
	defer rows.Close()
	outputRows := []wabenchBundleOutputRow{}
	for rows.Next() {
		var item wabenchBundleOutputRow
		if err := rows.Scan(&item.PK, &item.OutputID, &item.CaseID, &item.Status, &item.OutputHash, &item.TextStorage,
			&item.OutputText, &item.PrivateRef, &item.FailuresRaw, &item.MetricsRaw, &item.RoutingRaw, &item.TraceRef,
			&item.ContextRaw, &item.CreatedAt); err != nil {
			return nil, err
		}
		outputRows = append(outputRows, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(outputRows) != execution.Run.TotalCases {
		return nil, fmt.Errorf("run output count %d does not match frozen total %d", len(outputRows), execution.Run.TotalCases)
	}

	bundle := &WABenchNormalizedBundle{
		SchemaVersion: WABenchSchemaVersion, BundleID: "bundle_" + normalizedWABenchAlias(runID),
		BatchID: batchID, BatchContentHash: batchContentHash, ReviewStatus: "complete",
		Run: WABenchNormalizedRun{SchemaVersion: WABenchSchemaVersion, RunID: execution.Run.RunID, SuiteID: execution.Suite.SuiteID,
			CandidateID: execution.Candidate.CandidateID, AdapterID: execution.Run.AdapterID, RunnerVersion: execution.Run.RunnerVersion,
			Environment: execution.Run.Environment, TrafficType: execution.Run.TrafficType, EvaluationRunID: execution.Run.EvaluationRunID,
			Status: execution.Run.Status, StartedAt: *execution.Run.StartedAt, CompletedAt: execution.Run.CompletedAt, OutputRefs: []string{}},
		Outputs: []WABenchNormalizedOutput{}, Reviews: []WABenchNormalizedReview{}, GeneratedAt: time.Now().UTC(),
	}
	if execution.Run.Status == "completed" && execution.Run.FailedCases > 0 {
		bundle.Run.Status = "completed_with_failures"
	}
	for _, item := range outputRows {
		output, review, err := r.normalizeBundleOutput(ctx, execution.Run.RunID, item)
		if err != nil {
			return nil, err
		}
		bundle.Outputs = append(bundle.Outputs, output)
		bundle.Run.OutputRefs = append(bundle.Run.OutputRefs, output.OutputID)
		if review != nil {
			bundle.Reviews = append(bundle.Reviews, *review)
		} else if output.Status == "complete" {
			bundle.ReviewStatus = "pending"
		}
	}
	return bundle, nil
}

func (r *WABenchRepo) normalizeBundleOutput(ctx context.Context, runID string, item wabenchBundleOutputRow) (WABenchNormalizedOutput, *WABenchNormalizedReview, error) {
	failuresRaw := []map[string]interface{}{}
	if err := json.Unmarshal(item.FailuresRaw, &failuresRaw); err != nil {
		return WABenchNormalizedOutput{}, nil, err
	}
	metricsRaw, err := decodeBundleMap(item.MetricsRaw)
	if err != nil {
		return WABenchNormalizedOutput{}, nil, err
	}
	routingRaw, err := decodeBundleMap(item.RoutingRaw)
	if err != nil {
		return WABenchNormalizedOutput{}, nil, err
	}
	contextData, err := decodeBundleMap(item.ContextRaw)
	if err != nil {
		return WABenchNormalizedOutput{}, nil, err
	}
	checks, err := r.normalizedBundleChecks(ctx, item.PK)
	if err != nil {
		return WABenchNormalizedOutput{}, nil, err
	}
	textStorage := WABenchNormalizedTextStorage{Mode: item.TextStorage}
	if item.TextStorage == "inline_public" {
		textStorage.Text = item.OutputText.String
	} else if item.TextStorage == "private_ref" {
		textStorage.PrivateRef = item.PrivateRef.String
	}
	var traceRef *string
	if item.TraceRef.Valid {
		value := item.TraceRef.String
		traceRef = &value
	}
	output := WABenchNormalizedOutput{SchemaVersion: WABenchSchemaVersion, OutputID: item.OutputID, RunID: runID, CaseID: item.CaseID,
		Status: item.Status, OutputHash: item.OutputHash, TextStorage: textStorage, Checks: checks, Failures: []WABenchNormalizedFailure{},
		Metrics: normalizeWABenchMetrics(metricsRaw, checks), Routing: normalizeWABenchRouting(routingRaw, contextData), TraceRef: traceRef, CreatedAt: item.CreatedAt}
	for _, failure := range failuresRaw {
		output.Failures = append(output.Failures, normalizedWABenchFailure(failure))
	}
	review, err := r.normalizedBundleModelReview(ctx, runID, item)
	return output, review, err
}

func (r *WABenchRepo) normalizedBundleChecks(ctx context.Context, outputPK string) ([]WABenchNormalizedCheck, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT check_id, status, severity, evidence FROM wabench_checks WHERE output_pk = $1 ORDER BY check_id`, outputPK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []WABenchNormalizedCheck{}
	for rows.Next() {
		var item WABenchNormalizedCheck
		var raw []byte
		if err := rows.Scan(&item.CheckID, &item.Status, &item.Severity, &raw); err != nil {
			return nil, err
		}
		if item.Evidence, err = decodeBundleMap(raw); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *WABenchRepo) normalizedBundleModelReview(ctx context.Context, runID string, output wabenchBundleOutputRow) (*WABenchNormalizedReview, error) {
	var review WABenchNormalizedReview
	var reviewerID, reviewerRole, reviewMethod, labelSource string
	var modification sql.NullInt64
	var primaryValue sql.NullString
	var hardFailures, secondary pq.StringArray
	var evidenceRaw []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT review_id, reviewer_id, reviewer_role, review_method, label_source,
		       task_compliance, source_fidelity, structure_reasoning, style_consistency, direct_usability,
		       acceptance_label, modification_burden, hard_failure_ids, primary_root_cause,
		       secondary_root_causes, evidence, reviewed_at
		FROM wabench_reviews WHERE output_pk = $1 AND reviewer_type = 'model'
		ORDER BY reviewed_at DESC LIMIT 1
	`, output.PK).Scan(&review.ReviewID, &reviewerID, &reviewerRole, &reviewMethod, &labelSource,
		&review.Scores.TaskCompliance, &review.Scores.SourceFidelity, &review.Scores.StructureReasoning,
		&review.Scores.StyleConsistency, &review.Scores.DirectUsability, &review.Acceptance, &modification,
		&hardFailures, &primaryValue, &secondary, &evidenceRaw, &review.ReviewedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	_ = reviewMethod
	_ = labelSource
	review.SchemaVersion = WABenchSchemaVersion
	review.RunID = runID
	review.CaseID = output.CaseID
	review.OutputHash = output.OutputHash
	review.RubricVersion = "core-rubric.v1"
	review.Reviewer = WABenchNormalizedReviewer{Alias: normalizedWABenchAlias(reviewerID), Type: "model", Role: reviewerRole, Mode: "independent_blind", TagSource: "model_precheck"}
	if modification.Valid {
		value := int(modification.Int64)
		review.ModificationBurden = &value
	}
	review.HardFailures = append([]string(nil), hardFailures...)
	review.SecondaryRootCauses = append([]string(nil), secondary...)
	if primaryValue.Valid {
		value := primaryValue.String
		review.PrimaryRootCause = &value
	}
	review.SymptomTags = []string{}
	evidence, err := decodeBundleMap(evidenceRaw)
	if err != nil {
		return nil, err
	}
	if symptoms, ok := evidence["symptoms"].([]interface{}); ok {
		for _, value := range symptoms {
			if tag, ok := value.(string); ok && normalizedWABenchTag.MatchString(tag) {
				review.SymptomTags = append(review.SymptomTags, tag)
			}
		}
	}
	sort.Strings(review.SymptomTags)
	return &review, nil
}
