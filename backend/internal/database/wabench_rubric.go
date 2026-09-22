package database

import (
	"context"
	"encoding/json"
	"fmt"
)

// ─── Canonical WABench rubric + immutable snapshot ───────────────────────────
//
// Single source of truth for the 5-dimension rubric, mirrored from llm-space's
// "rubric snapshot" idea: every review freezes the rubric that produced it, so
// editing/deleting the reusable rubric definition never rewrites historical
// evaluations, and baseline-vs-candidate comparison always uses the weights that
// actually scored each run.

// WABenchRubricVersion identifies the canonical rubric frozen into reviews.
const WABenchRubricVersion = "core-rubric.v1"

// WABenchRubricPassScore is the gate threshold on the 0-100 weighted score.
const WABenchRubricPassScore = 80.0

// WABenchRubricDimensions are the five scored dimensions (each 1-5).
var WABenchRubricDimensions = []string{
	"taskCompliance", "sourceFidelity", "structureReasoning", "styleConsistency", "directUsability",
}

// RubricSnapshot is the frozen rubric carried inside a review's evidence JSONB.
type RubricSnapshot struct {
	Version     string         `json:"version"`
	Scale       string         `json:"scale"`
	Weights     map[string]int `json:"weights"`
	PassScore   float64        `json:"passScore"`
	WeightTotal int            `json:"weightTotal"`
}

// NewRubricSnapshot freezes the given per-dimension weights (must total 100).
func NewRubricSnapshot(weights map[string]int) RubricSnapshot {
	frozen := make(map[string]int, len(WABenchRubricDimensions))
	total := 0
	for _, dimension := range WABenchRubricDimensions {
		weight := weights[dimension]
		frozen[dimension] = weight
		total += weight
	}
	return RubricSnapshot{
		Version: WABenchRubricVersion, Scale: "1-5", Weights: frozen,
		PassScore: WABenchRubricPassScore, WeightTotal: total,
	}
}

// RubricSnapshotFromEvidence reads the frozen rubric back out of review evidence.
func RubricSnapshotFromEvidence(evidence map[string]interface{}) (RubricSnapshot, bool) {
	raw, ok := evidence["rubricSnapshot"]
	if !ok || raw == nil {
		return RubricSnapshot{}, false
	}
	// When the value was unmarshalled generically it arrives as a map.
	if direct, ok := raw.(RubricSnapshot); ok {
		return direct, true
	}
	asMap, ok := raw.(map[string]interface{})
	if !ok {
		return RubricSnapshot{}, false
	}
	snapshot := RubricSnapshot{}
	snapshot.Version, _ = asMap["version"].(string)
	snapshot.Scale, _ = asMap["scale"].(string)
	if pass, ok := asMap["passScore"].(float64); ok {
		snapshot.PassScore = pass
	}
	if total, ok := asMap["weightTotal"].(float64); ok {
		snapshot.WeightTotal = int(total)
	}
	if weights, ok := asMap["weights"].(map[string]interface{}); ok {
		snapshot.Weights = make(map[string]int, len(weights))
		for dimension, value := range weights {
			if number, ok := value.(float64); ok {
				snapshot.Weights[dimension] = int(number)
			}
		}
	}
	return snapshot, snapshot.WeightTotal == 100 || len(snapshot.Weights) == len(WABenchRubricDimensions)
}

// caseRubricWeightsForOutput reads the current rubric weights of the case
// behind an output, used to freeze a snapshot onto human-imported reviews that
// arrive without one.
func (r *WABenchRepo) caseRubricWeightsForOutput(ctx context.Context, outputID string) (map[string]int, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	var raw []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT c.rubric_weights
		FROM wabench_outputs o
		JOIN wabench_cases c ON c.id = o.case_pk
		WHERE o.output_id = $1
	`, outputID).Scan(&raw)
	if err != nil {
		return nil, err
	}
	weights := map[string]int{}
	if err := json.Unmarshal(raw, &weights); err != nil {
		return nil, err
	}
	return weights, nil
}
