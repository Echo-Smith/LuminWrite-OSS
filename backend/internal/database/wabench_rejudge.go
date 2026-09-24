package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// WABenchRejudgeCandidate is one frozen output of a run whose review is
// missing or invalid while the article text is still stored, so blind
// re-judging can run against the frozen text without re-executing the agent.
type WABenchRejudgeCandidate struct {
	OutputPK   string
	OutputID   string
	OutputText string
	Failures   []map[string]interface{}
	Routing    map[string]interface{}
	CaseID     string
	Case       WABenchCase
	Suite      WABenchSuite
	Candidate  WABenchCandidate
}

// WABenchRejudgeMarkerKey is the evidence key stamped on rejudge-sourced
// reviews. Its presence marks a review as produced by the maintenance rejudge
// entry.
const WABenchRejudgeMarkerKey = "rejudge"

// wabenchRejudgeableOutputsQuery selects frozen outputs of a run that still
// lack a valid review:
//
//   - the run matches by run_id;
//   - the frozen article text is available inline (output_text non-empty);
//   - no review with a usable weighted score exists (missing or invalid);
//   - no rejudge-sourced review exists yet (idempotency: once rejudged, an
//     output is never rejudged again even if older invalid reviews remain).
const wabenchRejudgeableOutputsQuery = `
	SELECT
		o.id::text, o.output_id, o.output_text, o.failures, o.routing,
		c.id::text, c.case_id, c.suite_pk::text, c.task_type, c.difficulty,
		c.input_storage, COALESCE(c.input_text, ''), COALESCE(c.input_ref, ''),
		c.input_hash, COALESCE(c.redacted_input_hash, ''),
		c.context, c.source_mode, c.source_fixture_refs, c.expected_behavior,
		c.must_have, c.must_not_have, c.hard_gate_ids, c.rubric_weights,
		c.rule_profile_refs, c.privacy_level,
		s.id::text, s.suite_id, s.name, s.version, s.partition,
		s.visibility, s.status, s.case_count,
		cd.id::text, cd.candidate_id, cd.name, cd.prompt_hash,
		COALESCE(cd.memory_hash, ''), cd.model_manifest, cd.code_hash,
		cd.tool_manifest, cd.feature_flags
	FROM wabench_outputs o
	JOIN wabench_runs r ON r.id = o.run_pk
	JOIN wabench_cases c ON c.id = o.case_pk
	JOIN wabench_suites s ON s.id = r.suite_pk
	JOIN wabench_candidates cd ON cd.id = r.candidate_pk
	WHERE r.run_id = $1
	  AND o.output_text IS NOT NULL AND o.output_text <> ''
	  AND NOT EXISTS (
			SELECT 1 FROM wabench_reviews rv
			WHERE rv.output_pk = o.id AND rv.evidence->>'weightedScore' IS NOT NULL
	  )
	  AND NOT EXISTS (
			SELECT 1 FROM wabench_reviews rj
			WHERE rj.output_pk = o.id AND rj.evidence ? 'rejudge'
	  )
	ORDER BY o.output_id
`

// ListRejudgeableOutputs returns the rejudge worklist for one run. Read-only:
// it never mutates outputs.
func (r *WABenchRepo) ListRejudgeableOutputs(ctx context.Context, runID string) ([]WABenchRejudgeCandidate, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	rows, err := r.db.QueryContext(ctx, wabenchRejudgeableOutputsQuery, runID)
	if err != nil {
		return nil, fmt.Errorf("list WABench rejudgeable outputs: %w", err)
	}
	defer rows.Close()
	candidates := []WABenchRejudgeCandidate{}
	for rows.Next() {
		var item WABenchRejudgeCandidate
		var failuresRaw, routingRaw, contextRaw, weightsRaw []byte
		var memoryHash sql.NullString
		var modelRaw, toolsRaw, flagsRaw []byte
		if err := rows.Scan(
			&item.OutputPK, &item.OutputID, &item.OutputText, &failuresRaw, &routingRaw,
			&item.Case.PK, &item.Case.CaseID, &item.Case.SuitePK, &item.Case.TaskType, &item.Case.Difficulty,
			&item.Case.InputStorage, &item.Case.InputText, &item.Case.InputRef,
			&item.Case.InputHash, &item.Case.RedactedInputHash,
			&contextRaw, &item.Case.SourceMode, pq.Array(&item.Case.SourceFixtureRefs), &item.Case.ExpectedBehavior,
			pq.Array(&item.Case.MustHave), pq.Array(&item.Case.MustNotHave), pq.Array(&item.Case.HardGateIDs), &weightsRaw,
			pq.Array(&item.Case.RuleProfileRefs), &item.Case.PrivacyLevel,
			&item.Suite.PK, &item.Suite.SuiteID, &item.Suite.Name, &item.Suite.Version, &item.Suite.Partition,
			&item.Suite.Visibility, &item.Suite.Status, &item.Suite.CaseCount,
			&item.Candidate.PK, &item.Candidate.CandidateID, &item.Candidate.Name, &item.Candidate.PromptHash,
			&memoryHash, &modelRaw, &item.Candidate.CodeHash, &toolsRaw, &flagsRaw,
		); err != nil {
			return nil, fmt.Errorf("scan WABench rejudgeable output: %w", err)
		}
		item.Candidate.MemoryHash = memoryHash.String
		if err := decodeWABenchRejudgeCandidate(&item, failuresRaw, routingRaw, contextRaw, weightsRaw, modelRaw, toolsRaw, flagsRaw); err != nil {
			return nil, err
		}
		candidates = append(candidates, item)
	}
	return candidates, rows.Err()
}

func decodeWABenchRejudgeCandidate(item *WABenchRejudgeCandidate, failuresRaw, routingRaw, contextRaw, weightsRaw, modelRaw, toolsRaw, flagsRaw []byte) error {
	if err := json.Unmarshal(failuresRaw, &item.Failures); err != nil {
		return fmt.Errorf("decode WABench output failures %s: %w", item.OutputID, err)
	}
	if err := json.Unmarshal(routingRaw, &item.Routing); err != nil {
		return fmt.Errorf("decode WABench output routing %s: %w", item.OutputID, err)
	}
	if err := json.Unmarshal(contextRaw, &item.Case.Context); err != nil {
		return fmt.Errorf("decode WABench case context %s: %w", item.Case.CaseID, err)
	}
	if err := json.Unmarshal(weightsRaw, &item.Case.RubricWeights); err != nil {
		return fmt.Errorf("decode WABench case rubric %s: %w", item.Case.CaseID, err)
	}
	var err error
	if item.Candidate.ModelManifest, err = unmarshalWABenchMap(modelRaw); err != nil {
		return fmt.Errorf("decode WABench candidate model manifest: %w", err)
	}
	if item.Candidate.ToolManifest, err = unmarshalWABenchMap(toolsRaw); err != nil {
		return fmt.Errorf("decode WABench candidate tool manifest: %w", err)
	}
	if item.Candidate.FeatureFlags, err = unmarshalWABenchMap(flagsRaw); err != nil {
		return fmt.Errorf("decode WABench candidate feature flags: %w", err)
	}
	return nil
}

// SaveRejudgeReview inserts a rejudge-sourced review for one output inside a
// transaction. Any earlier rejudge-sourced reviews for the same output are
// removed first, so re-running the entry is idempotent; original run reviews
// and the output row itself are never modified.
func (r *WABenchRepo) SaveRejudgeReview(ctx context.Context, outputPK string, review WABenchReviewWrite) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("database not available")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin WABench rejudge review transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `
		DELETE FROM wabench_reviews WHERE output_pk = $1 AND evidence ? 'rejudge'
	`, outputPK); err != nil {
		return fmt.Errorf("clear previous WABench rejudge reviews: %w", err)
	}
	evidence, err := marshalWABenchJSON(review.Evidence)
	if err != nil {
		return err
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
		return fmt.Errorf("insert WABench rejudge review: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit WABench rejudge review: %w", err)
	}
	return nil
}

// WABenchRejudgeEvidence builds the metadata block stamped into the evidence
// jsonb of a rejudge-sourced review.
func WABenchRejudgeEvidence(runID string) map[string]interface{} {
	return map[string]interface{}{
		"source":     "judge_failure_rejudge",
		"runId":      runID,
		"rejudgedAt": time.Now().UTC().Format(time.RFC3339),
	}
}
