package database

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// EvaluationRepo handles evaluation set/sample/run persistence.
type EvaluationRepo struct {
	db *DB
}

// NewEvaluationRepo creates a new EvaluationRepo.
func NewEvaluationRepo(db *DB) *EvaluationRepo {
	return &EvaluationRepo{db: db}
}

// ─── Evaluation Sets ─────────────────────────────────────

type EvaluationSet struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	StyleSlug   string    `json:"style_slug"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	SampleCount int       `json:"sample_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (r *EvaluationRepo) CreateSet(ctx context.Context, name, styleSlug, description string) (*EvaluationSet, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var set EvaluationSet
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO evaluation_sets (name, style_slug, description, status, sample_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'draft', 0, NOW(), NOW())
		RETURNING id::text, name, style_slug, description, status, sample_count, created_at, updated_at
	`, name, styleSlug, description).Scan(
		&set.ID, &set.Name, &set.StyleSlug, &set.Description,
		&set.Status, &set.SampleCount, &set.CreatedAt, &set.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &set, nil
}

func (r *EvaluationRepo) ListSets(ctx context.Context, styleSlug string, page, pageSize int) ([]*EvaluationSet, int, error) {
	if r.db == nil {
		return []*EvaluationSet{}, 0, nil
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	query := `SELECT id::text, name, style_slug, description, status, sample_count, created_at, updated_at FROM evaluation_sets`
	args := []interface{}{}
	argIdx := 1
	if styleSlug != "" {
		query += fmt.Sprintf(" WHERE style_slug = $%d", argIdx)
		args = append(args, styleSlug)
		argIdx++
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, pageSize, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var sets []*EvaluationSet
	for rows.Next() {
		var s EvaluationSet
		if err := rows.Scan(&s.ID, &s.Name, &s.StyleSlug, &s.Description, &s.Status, &s.SampleCount, &s.CreatedAt, &s.UpdatedAt); err != nil {
			continue
		}
		sets = append(sets, &s)
	}

	var total int
	countQuery := "SELECT COUNT(*) FROM evaluation_sets"
	if styleSlug != "" {
		countQuery += " WHERE style_slug = $1"
		r.db.QueryRowContext(ctx, countQuery, styleSlug).Scan(&total)
	} else {
		r.db.QueryRowContext(ctx, countQuery).Scan(&total)
	}

	return sets, total, nil
}

func (r *EvaluationRepo) GetSet(ctx context.Context, id string) (*EvaluationSet, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var s EvaluationSet
	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, name, style_slug, description, status, sample_count, created_at, updated_at
		FROM evaluation_sets WHERE id = $1
	`, id).Scan(&s.ID, &s.Name, &s.StyleSlug, &s.Description, &s.Status, &s.SampleCount, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ─── Evaluation Samples ──────────────────────────────────

type EvaluationSample struct {
	ID               string                 `json:"id"`
	SetID            string                 `json:"set_id"`
	Topic            string                 `json:"topic"`
	InputPrompt      string                 `json:"input_prompt"`
	StyleSlug        string                 `json:"style_slug"`
	ExpectedStructure map[string]interface{} `json:"expected_structure,omitempty"`
	ExpectedKeywords  []string               `json:"expected_keywords,omitempty"`
	ExpectedLength    *int                   `json:"expected_length,omitempty"`
	RedFlags          []string               `json:"red_flags,omitempty"`
	ScoringCriteria   map[string]interface{} `json:"scoring_criteria"`
	Status           string                 `json:"status"`
	CreatedAt        time.Time              `json:"created_at"`
}

func (r *EvaluationRepo) AddSample(ctx context.Context, setID, topic, inputPrompt, styleSlug string, scoringCriteria map[string]interface{}) (*EvaluationSample, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	criteriaJSON, _ := json.Marshal(scoringCriteria)

	var s EvaluationSample
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO evaluation_samples (set_id, topic, input_prompt, style_slug, scoring_criteria, status, created_at)
		VALUES ($1, $2, $3, $4, $5, 'pending', NOW())
		RETURNING id::text, set_id::text, topic, input_prompt, style_slug, scoring_criteria, status, created_at
	`, setID, topic, inputPrompt, styleSlug, string(criteriaJSON)).Scan(
		&s.ID, &s.SetID, &s.Topic, &s.InputPrompt, &s.StyleSlug,
		&criteriaJSON, &s.Status, &s.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	json.Unmarshal(criteriaJSON, &s.ScoringCriteria)

	// Update sample count
	r.db.ExecContext(ctx, `UPDATE evaluation_sets SET sample_count = sample_count + 1, updated_at = NOW() WHERE id = $1`, setID)

	return &s, nil
}

func (r *EvaluationRepo) AddSamples(ctx context.Context, setID string, samples []map[string]interface{}) (int, error) {
	if r.db == nil {
		return 0, fmt.Errorf("database not available")
	}

	count := 0
	for _, sample := range samples {
		topic, _ := sample["topic"].(string)
		inputPrompt, _ := sample["input_prompt"].(string)
		styleSlug, _ := sample["style_slug"].(string)
		if topic == "" || inputPrompt == "" {
			continue
		}
		if styleSlug == "" {
			styleSlug = "yinyue"
		}

		criteria, _ := sample["scoring_criteria"].(map[string]interface{})
		if criteria == nil {
			criteria = map[string]interface{}{
				"factuality": 0.3,
				"structure":  0.2,
				"style":      0.2,
				"relevance":  0.2,
				"risk":       0.1,
			}
		}

		_, err := r.AddSample(ctx, setID, topic, inputPrompt, styleSlug, criteria)
		if err != nil {
			slog.Warn("failed to add sample", "error", err)
			continue
		}
		count++
	}

	return count, nil
}

func (r *EvaluationRepo) ListSamples(ctx context.Context, setID string) ([]*EvaluationSample, error) {
	if r.db == nil {
		return []*EvaluationSample{}, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text, set_id::text, topic, input_prompt, style_slug,
		       expected_structure, expected_keywords, expected_length,
		       red_flags, scoring_criteria, status, created_at
		FROM evaluation_samples WHERE set_id = $1 ORDER BY created_at
	`, setID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var samples []*EvaluationSample
	for rows.Next() {
		var s EvaluationSample
		var (
			structureJSON []byte
			keywords      []string
			length        *int
			redFlags      []string
			criteriaJSON  []byte
		)
		if err := rows.Scan(&s.ID, &s.SetID, &s.Topic, &s.InputPrompt, &s.StyleSlug,
			&structureJSON, &keywords, &length, &redFlags, &criteriaJSON, &s.Status, &s.CreatedAt); err != nil {
			continue
		}
		if len(structureJSON) > 0 {
			json.Unmarshal(structureJSON, &s.ExpectedStructure)
		}
		s.ExpectedKeywords = keywords
		s.ExpectedLength = length
		s.RedFlags = redFlags
		if len(criteriaJSON) > 0 {
			json.Unmarshal(criteriaJSON, &s.ScoringCriteria)
		}
		samples = append(samples, &s)
	}

	return samples, nil
}

// ─── Evaluation Runs ─────────────────────────────────────

type EvaluationRun struct {
	ID             string                 `json:"id"`
	SetID          string                 `json:"set_id"`
	ProfileSlug    string                 `json:"profile_slug"`
	ProfileVersion int                    `json:"profile_version"`
	TriggerType    string                 `json:"trigger_type"`
	TriggerDetail  string                 `json:"trigger_detail"`
	Status         string                 `json:"status"`
	TotalSamples   int                    `json:"total_samples"`
	CompletedCount int                    `json:"completed_count"`
	Results        []map[string]interface{} `json:"results"`
	OverallScore   float64                `json:"overall_score"`
	DimensionScores map[string]float64    `json:"dimension_scores"`
	StartedAt      *time.Time             `json:"started_at,omitempty"`
	CompletedAt    *time.Time             `json:"completed_at,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
}

func (r *EvaluationRepo) CreateRun(ctx context.Context, setID, profileSlug string, profileVersion int, triggerType, triggerDetail string) (*EvaluationRun, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	// Count samples
	var totalSamples int
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM evaluation_samples WHERE set_id = $1`, setID).Scan(&totalSamples)

	id := uuid.New().String()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO evaluation_runs (id, set_id, profile_slug, profile_version, trigger_type, trigger_detail,
			status, total_samples, completed_count, results, overall_score, dimension_scores, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, 0, '[]', 0, '{}', NOW())
	`,
		id, setID, profileSlug, profileVersion, triggerType, triggerDetail, totalSamples,
	)
	if err != nil {
		return nil, err
	}

	return r.GetRun(ctx, id)
}

func (r *EvaluationRepo) GetRun(ctx context.Context, id string) (*EvaluationRun, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var run EvaluationRun
	var (
		resultsJSON    []byte
		dimScoresJSON  []byte
		overallScore   *float64
	)

	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, set_id::text, profile_slug, profile_version, trigger_type, trigger_detail,
		       status, total_samples, completed_count, results, overall_score, dimension_scores,
		       started_at, completed_at, created_at
		FROM evaluation_runs WHERE id = $1
	`, id).Scan(
		&run.ID, &run.SetID, &run.ProfileSlug, &run.ProfileVersion, &run.TriggerType, &run.TriggerDetail,
		&run.Status, &run.TotalSamples, &run.CompletedCount, &resultsJSON, &overallScore, &dimScoresJSON,
		&run.StartedAt, &run.CompletedAt, &run.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if len(resultsJSON) > 0 {
		json.Unmarshal(resultsJSON, &run.Results)
	}
	if len(dimScoresJSON) > 0 {
		json.Unmarshal(dimScoresJSON, &run.DimensionScores)
	}
	if overallScore != nil {
		run.OverallScore = *overallScore
	}

	return &run, nil
}

func (r *EvaluationRepo) ListRuns(ctx context.Context, setID string, page, pageSize int) ([]*EvaluationRun, int, error) {
	if r.db == nil {
		return []*EvaluationRun{}, 0, nil
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	query := `SELECT id::text, set_id::text, profile_slug, profile_version, trigger_type, trigger_detail,
		       status, total_samples, completed_count, overall_score, started_at, completed_at, created_at
		       FROM evaluation_runs`
	args := []interface{}{}
	argIdx := 1
	if setID != "" {
		query += fmt.Sprintf(" WHERE set_id = $%d", argIdx)
		args = append(args, setID)
		argIdx++
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, pageSize, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var runs []*EvaluationRun
	for rows.Next() {
		var run EvaluationRun
		var overallScore *float64
		if err := rows.Scan(&run.ID, &run.SetID, &run.ProfileSlug, &run.ProfileVersion,
			&run.TriggerType, &run.TriggerDetail, &run.Status, &run.TotalSamples,
			&run.CompletedCount, &overallScore, &run.StartedAt, &run.CompletedAt, &run.CreatedAt); err != nil {
			continue
		}
		if overallScore != nil {
			run.OverallScore = *overallScore
		}
		runs = append(runs, &run)
	}

	var total int
	countQuery := "SELECT COUNT(*) FROM evaluation_runs"
	if setID != "" {
		countQuery += " WHERE set_id = $1"
		r.db.QueryRowContext(ctx, countQuery, setID).Scan(&total)
	} else {
		r.db.QueryRowContext(ctx, countQuery).Scan(&total)
	}

	return runs, total, nil
}

// UpdateRunProgress updates the run with a new result entry and increments completed count.
func (r *EvaluationRepo) UpdateRunProgress(ctx context.Context, runID string, result map[string]interface{}) error {
	if r.db == nil {
		return nil
	}

	resultJSON, _ := json.Marshal(result)

	// Append result to results array and increment completed_count
	_, err := r.db.ExecContext(ctx, `
		UPDATE evaluation_runs
		SET results = results || $2::jsonb,
		    completed_count = completed_count + 1
		WHERE id = $1
	`, runID, string(resultJSON))
	return err
}

// CompleteRun finalizes the run with overall scores.
func (r *EvaluationRepo) CompleteRun(ctx context.Context, runID string, overallScore float64, dimensionScores map[string]float64) error {
	if r.db == nil {
		return nil
	}

	dimJSON, _ := json.Marshal(dimensionScores)

	_, err := r.db.ExecContext(ctx, `
		UPDATE evaluation_runs
		SET status = 'completed', overall_score = $2, dimension_scores = $3, completed_at = NOW()
		WHERE id = $1
	`, runID, overallScore, string(dimJSON))
	return err
}

// StartRun marks a run as running.
func (r *EvaluationRepo) StartRun(ctx context.Context, runID string) error {
	if r.db == nil {
		return nil
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE evaluation_runs SET status = 'running', started_at = NOW() WHERE id = $1
	`, runID)
	return err
}

// FailRun marks a run as failed.
func (r *EvaluationRepo) FailRun(ctx context.Context, runID, errMsg string) error {
	if r.db == nil {
		return nil
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE evaluation_runs SET status = 'failed', completed_at = NOW() WHERE id = $1
	`, runID)
	return err
}
