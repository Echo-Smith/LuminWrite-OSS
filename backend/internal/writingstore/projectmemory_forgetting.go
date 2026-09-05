// Forgetting sweeps (roadmap §12, V2.9 item 12): applying the versioned
// lifecycle policy to one project's memory pools. Every transition lands in
// the append-only project_memory_forgetting_log (migration 103) with the
// rule and policy hash that produced it, so "what was forgotten, and why" is
// replayable evidence — forgetting is governed hygiene, never silent
// deletion.
//
// The sweep never references project_facts: canon leaves memory only through
// validity intervals and user-driven supersede, both of which already exist.
// That invariant is pinned by the projectmemory policy tests, by the sweep
// table list below, and by the store-level sweep test.
package writingstore

import (
	"context"
	"fmt"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/projectmemory"
)

// ForgettingSummary reports what one sweep found (preview) or did (apply).
// Preview summaries carry a zero SweepID: nothing was applied.
type ForgettingSummary struct {
	SweepID             string `json:"sweep_id,omitempty"`
	PolicyVersion       int    `json:"policy_version"`
	PolicyHash          string `json:"policy_hash"`
	CandidatesRejected  int    `json:"candidates_rejected"`
	ClaimsRejected      int    `json:"claims_rejected"`
	TerminologyArchived int    `json:"terminology_archived"`
	DecisionsArchived   int    `json:"decisions_archived"`
	ThreadsArchived     int    `json:"threads_archived"`
	EntitiesArchived    int    `json:"entities_archived"`
}

// Logged is the number of log rows a sweep writes — equal to the sum of its
// transitions.
func (summary ForgettingSummary) Logged() int {
	return summary.CandidatesRejected + summary.ClaimsRejected + summary.TerminologyArchived +
		summary.DecisionsArchived + summary.ThreadsArchived + summary.EntitiesArchived
}

// ForgettingLogRecord is one append-only audit row.
type ForgettingLogRecord struct {
	LogID         string    `json:"log_id"`
	SweepID       string    `json:"sweep_id"`
	ProjectID     string    `json:"project_id"`
	ObjectTable   string    `json:"object_table"`
	ObjectID      string    `json:"object_id"`
	FromStatus    string    `json:"from_status"`
	ToStatus      string    `json:"to_status"`
	Rule          string    `json:"rule"`
	PolicyVersion int       `json:"policy_version"`
	PolicyHash    string    `json:"policy_hash"`
	AppliedByType string    `json:"applied_by_type"`
	AppliedByID   string    `json:"applied_by_id,omitempty"`
	AppliedAt     time.Time `json:"applied_at"`
}

// forgettingSweepTables pins the object tables a sweep may touch. Canon
// (project_facts) is structurally absent — that absence is the invariant.
var forgettingSweepTables = map[string]bool{
	"project_memory_candidates": true,
	"project_claims":            true,
	"project_terminology":       true,
	"project_decisions":         true,
	"project_threads":           true,
	"project_entities":          true,
}

// forgetSweep describes one homogeneous transition the policy prescribes.
// Table and column names are package constants — never caller input.
type forgetSweep struct {
	table      string
	idColumn   string
	fromStatus []string
	toStatus   string
	rule       string
	timeColumn string
	horizon    time.Duration
	// touchUpdated marks tables whose lifecycle carries updated_at; the
	// fact-lane candidate table (migration 099) has only created_at.
	touchUpdated bool
	count        *int
}

func forgetSweeps(policy projectmemory.ForgetPolicy) []forgetSweep {
	sweeps := []forgetSweep{}
	if policy.CandidateHorizon > 0 {
		sweeps = append(sweeps,
			forgetSweep{table: "project_memory_candidates", idColumn: "candidate_id", fromStatus: []string{"staged"},
				toStatus: "rejected", rule: projectmemory.RuleStaleCandidate, timeColumn: "created_at", horizon: policy.CandidateHorizon},
			forgetSweep{table: "project_terminology", idColumn: "terminology_id", fromStatus: []string{"candidate"},
				toStatus: "archived", rule: projectmemory.RuleStaleCandidate, timeColumn: "created_at", horizon: policy.CandidateHorizon, touchUpdated: true},
			forgetSweep{table: "project_threads", idColumn: "thread_id", fromStatus: []string{"candidate"},
				toStatus: "archived", rule: projectmemory.RuleStaleCandidate, timeColumn: "created_at", horizon: policy.CandidateHorizon, touchUpdated: true},
			forgetSweep{table: "project_entities", idColumn: "entity_id", fromStatus: []string{"candidate"},
				toStatus: "archived", rule: projectmemory.RuleStaleCandidate, timeColumn: "created_at", horizon: policy.CandidateHorizon, touchUpdated: true},
			forgetSweep{table: "project_decisions", idColumn: "decision_id", fromStatus: []string{"candidate"},
				toStatus: "archived", rule: projectmemory.RuleStaleCandidate, timeColumn: "created_at", horizon: policy.CandidateHorizon, touchUpdated: true},
		)
	}
	if policy.DecisionColdHorizon > 0 {
		sweeps = append(sweeps, forgetSweep{table: "project_decisions", idColumn: "decision_id", fromStatus: []string{"superseded"},
			toStatus: "archived", rule: projectmemory.RuleColdSuperseded, timeColumn: "updated_at", horizon: policy.DecisionColdHorizon, touchUpdated: true})
	}
	if policy.ClaimDecayHorizon > 0 {
		sweeps = append(sweeps, forgetSweep{table: "project_claims", idColumn: "claim_id", fromStatus: []string{"open", "supported"},
			toStatus: "rejected", rule: projectmemory.RuleClaimDecay, timeColumn: "updated_at", horizon: policy.ClaimDecayHorizon, touchUpdated: true})
	}
	return sweeps
}

func assignSweepCounts(sweeps []forgetSweep, summary *ForgettingSummary) {
	for i := range sweeps {
		switch sweeps[i].table {
		case "project_memory_candidates":
			sweeps[i].count = &summary.CandidatesRejected
		case "project_claims":
			sweeps[i].count = &summary.ClaimsRejected
		case "project_terminology":
			sweeps[i].count = &summary.TerminologyArchived
		case "project_decisions":
			sweeps[i].count = &summary.DecisionsArchived
		case "project_threads":
			sweeps[i].count = &summary.ThreadsArchived
		case "project_entities":
			sweeps[i].count = &summary.EntitiesArchived
		}
	}
}

// PreviewForgettingPolicy counts what a sweep would transition, without
// mutating anything: the dry-run path the runbook's sweep procedure and the
// memory-forget CLI's default mode consume.
func (s *Store) PreviewForgettingPolicy(ctx context.Context, projectID string, policy projectmemory.ForgetPolicy, now time.Time) (ForgettingSummary, error) {
	if err := validateID(projectID, "prj_", "project_id"); err != nil {
		return ForgettingSummary{}, err
	}
	hash, err := policy.Hash()
	if err != nil {
		return ForgettingSummary{}, err
	}
	summary := ForgettingSummary{PolicyVersion: projectmemory.ForgetPolicyVersion, PolicyHash: hash}
	sweeps := forgetSweeps(policy)
	assignSweepCounts(sweeps, &summary)
	for _, sweep := range sweeps {
		var count int
		if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`
			SELECT COUNT(*) FROM %s
			WHERE project_id=$1 AND status = ANY($2) AND %s < $3
		`, sweep.table, sweep.timeColumn), projectID, sweep.fromStatus, now.Add(-sweep.horizon)).Scan(&count); err != nil {
			return ForgettingSummary{}, fmt.Errorf("preview forgetting %s: %w", sweep.table, err)
		}
		*sweep.count = count
	}
	return summary, nil
}

// ApplyForgettingPolicy runs the sweep in one transaction. Each transition
// is conditional on its source status, so a second run is a no-op (idempotent
// by construction), and every transitioned row gets an append-only log entry
// naming the exact from-status, rule, policy version, and policy hash.
//
// The actor gate is policy-or-user: model, capability, and validator actors
// may never forget — memory lifecycle is governance territory, not model
// territory.
func (s *Store) ApplyForgettingPolicy(ctx context.Context, projectID string, policy projectmemory.ForgetPolicy, now time.Time, actor Actor) (ForgettingSummary, error) {
	if err := validateID(projectID, "prj_", "project_id"); err != nil {
		return ForgettingSummary{}, err
	}
	if err := actor.Validate(); err != nil {
		return ForgettingSummary{}, err
	}
	if actor.Type != ActorPolicy && actor.Type != ActorUser {
		return ForgettingSummary{}, fmt.Errorf("%w: forgetting sweep requires a policy or user actor, got %q", ErrInvalidRecord, actor.Type)
	}
	hash, err := policy.Hash()
	if err != nil {
		return ForgettingSummary{}, err
	}
	summary := ForgettingSummary{PolicyVersion: projectmemory.ForgetPolicyVersion, PolicyHash: hash}
	sweeps := forgetSweeps(policy)
	assignSweepCounts(sweeps, &summary)
	sweepID := StableID("swp_", projectID, fmt.Sprint(now.UnixNano()))

	err = s.InTransaction(ctx, func(tx *Tx) error {
		for _, sweep := range sweeps {
			cutoff := now.Add(-sweep.horizon)
			// Capture each eligible row's current status first so the log
			// records the real from-status (claims sweep two statuses).
			selected, err := tx.tx.QueryContext(ctx, fmt.Sprintf(`
				SELECT %s, status FROM %s
				WHERE project_id=$1 AND status = ANY($2) AND %s < $3
			`, sweep.idColumn, sweep.table, sweep.timeColumn), projectID, sweep.fromStatus, cutoff)
			if err != nil {
				return fmt.Errorf("select forgetting %s: %w", sweep.table, err)
			}
			fromStatus := map[string]string{}
			for selected.Next() {
				var id, status string
				if err := selected.Scan(&id, &status); err != nil {
					_ = selected.Close()
					return fmt.Errorf("scan forgetting %s: %w", sweep.table, err)
				}
				fromStatus[id] = status
			}
			if err := selected.Err(); err != nil {
				_ = selected.Close()
				return fmt.Errorf("forgetting %s rows: %w", sweep.table, err)
			}
			_ = selected.Close()
			if len(fromStatus) == 0 {
				continue
			}
			// The UPDATE re-checks the source status and horizon, so only
			// rows that actually transitioned are logged — a concurrent
			// interactive archive cannot be double-counted.
			setClause, cutoffParam := "status=$3", "$4"
			args := []any{projectID, sweep.fromStatus, sweep.toStatus, cutoff}
			if sweep.touchUpdated {
				setClause, cutoffParam = "status=$3, updated_at=$4", "$5"
				args = []any{projectID, sweep.fromStatus, sweep.toStatus, now, cutoff}
			}
			updated, err := tx.tx.QueryContext(ctx, fmt.Sprintf(`
				UPDATE %s SET %s
				WHERE project_id=$1 AND status = ANY($2) AND %s < %s
				RETURNING %s
			`, sweep.table, setClause, sweep.timeColumn, cutoffParam, sweep.idColumn), args...)
			if err != nil {
				return fmt.Errorf("sweep %s: %w", sweep.table, err)
			}
			transitioned := []string{}
			for updated.Next() {
				var id string
				if err := updated.Scan(&id); err != nil {
					_ = updated.Close()
					return fmt.Errorf("sweep %s scan: %w", sweep.table, err)
				}
				transitioned = append(transitioned, id)
			}
			if err := updated.Err(); err != nil {
				_ = updated.Close()
				return fmt.Errorf("sweep %s rows: %w", sweep.table, err)
			}
			_ = updated.Close()
			*sweep.count = len(transitioned)
			for _, id := range transitioned {
				if _, err := tx.tx.ExecContext(ctx, `
					INSERT INTO project_memory_forgetting_log (
						log_id, sweep_id, project_id, object_table, object_id,
						from_status, to_status, rule, policy_version, policy_hash,
						applied_by_type, applied_by_id, applied_at
					) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
					ON CONFLICT (log_id) DO NOTHING
				`, StableID("fgl_", sweepID, sweep.table, id), sweepID, projectID, sweep.table, id,
					fromStatus[id], sweep.toStatus, sweep.rule, projectmemory.ForgetPolicyVersion, hash,
					string(actor.Type), nullString(actor.ID), now); err != nil {
					return fmt.Errorf("log forgetting %s %s: %w", sweep.table, id, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return ForgettingSummary{}, err
	}
	summary.SweepID = sweepID
	return summary, nil
}

// ListForgettingLog returns one project's forgetting history, newest first,
// for runbook audits and the CLI's report mode.
func (s *Store) ListForgettingLog(ctx context.Context, projectID string, limit int) ([]ForgettingLogRecord, error) {
	if err := validateID(projectID, "prj_", "project_id"); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT log_id, sweep_id, project_id, object_table, object_id,
		       from_status, to_status, rule, policy_version, policy_hash,
		       applied_by_type, applied_by_id, applied_at
		FROM project_memory_forgetting_log
		WHERE project_id=$1
		ORDER BY applied_at DESC, log_id
		LIMIT $2
	`, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("list forgetting log: %w", err)
	}
	defer rows.Close()
	records := []ForgettingLogRecord{}
	for rows.Next() {
		record := ForgettingLogRecord{}
		var appliedByID *string
		if err := rows.Scan(&record.LogID, &record.SweepID, &record.ProjectID, &record.ObjectTable, &record.ObjectID,
			&record.FromStatus, &record.ToStatus, &record.Rule, &record.PolicyVersion, &record.PolicyHash,
			&record.AppliedByType, &appliedByID, &record.AppliedAt); err != nil {
			return nil, fmt.Errorf("scan forgetting log: %w", err)
		}
		if appliedByID != nil {
			record.AppliedByID = *appliedByID
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("forgetting log rows: %w", err)
	}
	return records, nil
}

// ForgettingSweepTables exposes the sweepable table set for audits and
// tests: project_facts is absent by design.
func ForgettingSweepTables() []string {
	tables := make([]string, 0, len(forgettingSweepTables))
	for table := range forgettingSweepTables {
		tables = append(tables, table)
	}
	return tables
}
