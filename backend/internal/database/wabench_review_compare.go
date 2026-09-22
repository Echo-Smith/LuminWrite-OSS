package database

import (
	"context"
	"encoding/json"
	"fmt"
)

// ─── Baseline vs candidate review comparison (llm-space pairwise B-A) ─────────
//
// Lets the Eval Center pick two durable runs and compare the reviews they
// captured: unweighted per-dimension means (1-5), the pass/hard-fail rates, the
// weighted-score mean (already frozen per review), an acceptance distribution,
// and a candidate-minus-baseline delta. Historical reviews keep the rubric they
// were scored with, so a later rubric edit never skews an old comparison.

type WABenchRunReviewStats struct {
	RunID             string             `json:"runId"`
	Reviews           int                `json:"reviews"`
	MeanScores        map[string]float64 `json:"meanScores"`
	MeanWeightedScore float64            `json:"meanWeightedScore"`
	PassRate          float64            `json:"passRate"`
	HardFailureRate   float64            `json:"hardFailureRate"`
	Acceptance        map[string]int     `json:"acceptance"`
}

type WABenchReviewDelta struct {
	MeanScores        map[string]float64 `json:"meanScores"`
	MeanWeightedScore float64            `json:"meanWeightedScore"`
	PassRate          float64            `json:"passRate"`
	HardFailureRate   float64            `json:"hardFailureRate"`
}

type WABenchReviewComparison struct {
	BaselineRunID  string                `json:"baselineRunId"`
	CandidateRunID string                `json:"candidateRunId"`
	Baseline       WABenchRunReviewStats `json:"baseline"`
	Candidate      WABenchRunReviewStats `json:"candidate"`
	Delta          WABenchReviewDelta    `json:"delta"`
}

func emptyRunReviewStats(runID string) WABenchRunReviewStats {
	return WABenchRunReviewStats{
		RunID: runID, MeanScores: map[string]float64{}, Acceptance: map[string]int{},
	}
}

// GetRunReviewStats aggregates the reviews captured for one run.
func (r *WABenchRepo) GetRunReviewStats(ctx context.Context, runID string) (WABenchRunReviewStats, error) {
	if r == nil || r.db == nil {
		return WABenchRunReviewStats{}, fmt.Errorf("database not available")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT rv.task_compliance, rv.source_fidelity, rv.structure_reasoning,
		       rv.style_consistency, rv.direct_usability, rv.acceptance_label,
		       COALESCE(array_length(rv.hard_failure_ids, 1), 0) > 0,
		       COALESCE(rv.evidence, '{}'::jsonb)
		FROM wabench_reviews rv
		JOIN wabench_outputs o ON o.id = rv.output_pk
		JOIN wabench_runs run ON run.id = o.run_pk
		WHERE run.run_id = $1
		ORDER BY rv.reviewed_at
	`, runID)
	if err != nil {
		return WABenchRunReviewStats{}, fmt.Errorf("query run reviews: %w", err)
	}
	defer rows.Close()

	stats := emptyRunReviewStats(runID)
	var sumScores map[string]int
	var sumWeighted, passed, hardFailures float64

	for rows.Next() {
		var task, source, structure, style, direct int
		var acceptance string
		var hasHard bool
		var evidenceRaw []byte
		if err := rows.Scan(&task, &source, &structure, &style, &direct, &acceptance, &hasHard, &evidenceRaw); err != nil {
			return WABenchRunReviewStats{}, err
		}
		if sumScores == nil {
			sumScores = map[string]int{}
		}
		sumScores["taskCompliance"] += task
		sumScores["sourceFidelity"] += source
		sumScores["structureReasoning"] += structure
		sumScores["styleConsistency"] += style
		sumScores["directUsability"] += direct

		evidence := map[string]interface{}{}
		_ = json.Unmarshal(evidenceRaw, &evidence)
		if weighted, ok := evidenceWeightedScore(evidence); ok {
			sumWeighted += weighted
			// pass gate mirrors the judge: weighted>=80 && task>=4 && source>=4 && no hard failure
			if weighted >= WABenchRubricPassScore && task >= 4 && source >= 4 && !hasHard {
				passed++
			}
		}
		if hasHard {
			hardFailures++
		}
		if acceptance == "" {
			acceptance = "unknown"
		}
		stats.Acceptance[acceptance]++
		stats.Reviews++
	}
	if err := rows.Err(); err != nil {
		return WABenchRunReviewStats{}, err
	}
	if stats.Reviews == 0 {
		return emptyRunReviewStats(runID), fmt.Errorf("no reviews recorded for run %s", runID)
	}

	n := float64(stats.Reviews)
	for _, dimension := range WABenchRubricDimensions {
		stats.MeanScores[dimension] = float64(sumScores[dimension]) / n
	}
	stats.MeanWeightedScore = sumWeighted / n
	stats.PassRate = passed / n
	stats.HardFailureRate = hardFailures / n
	return stats, nil
}

// CompareRunReviews returns a candidate-minus-baseline review comparison.
func (r *WABenchRepo) CompareRunReviews(ctx context.Context, baselineRunID, candidateRunID string) (*WABenchReviewComparison, error) {
	baseline, err := r.GetRunReviewStats(ctx, baselineRunID)
	if err != nil {
		return nil, fmt.Errorf("baseline: %w", err)
	}
	candidate, err := r.GetRunReviewStats(ctx, candidateRunID)
	if err != nil {
		return nil, fmt.Errorf("candidate: %w", err)
	}
	meanScores := map[string]float64{}
	for _, dimension := range WABenchRubricDimensions {
		meanScores[dimension] = candidate.MeanScores[dimension] - baseline.MeanScores[dimension]
	}
	return &WABenchReviewComparison{
		BaselineRunID:  baselineRunID,
		CandidateRunID: candidateRunID,
		Baseline:       baseline,
		Candidate:      candidate,
		Delta: WABenchReviewDelta{
			MeanScores:        meanScores,
			MeanWeightedScore: candidate.MeanWeightedScore - baseline.MeanWeightedScore,
			PassRate:          candidate.PassRate - baseline.PassRate,
			HardFailureRate:   candidate.HardFailureRate - baseline.HardFailureRate,
		},
	}, nil
}

func evidenceWeightedScore(evidence map[string]interface{}) (float64, bool) {
	value, ok := evidence["weightedScore"]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}
