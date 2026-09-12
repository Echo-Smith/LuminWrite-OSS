package writingstore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/projectmemory"
)

// forgetSweepFixture stages one of each forgettable object plus the
// survivors the sweep must leave alone: a stale fact-lane candidate, a fresh
// one (committed to canon), a stale open claim, a stale terminology
// candidate (raw-inserted because its created_at is trigger-protected), a
// cold superseded decision, and a warm superseded decision.
func forgetSweepFixture(t *testing.T, store *Store, ctx context.Context, projectID string, user Actor, old time.Time) {
	t.Helper()
	// Fact-lane candidates: stale (swept) and fresh (committed to canon).
	stale := projectmemory.Candidate{CandidateID: "cand_sweep_stale", BatchID: "bat_sweep", ProjectID: projectID,
		Subject: "主体", Predicate: "state", Object: "过期候选", AsOf: old,
		SourceRefs: []string{"doc_store"}, SubmittedByType: string(ActorModel)}
	fresh := stale
	fresh.CandidateID = "cand_sweep_fresh"
	fresh.Object = "新鲜候选"
	if err := store.StageMemoryCandidates(ctx, []projectmemory.Candidate{stale, fresh}); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
		UPDATE project_memory_candidates SET created_at=$1 WHERE candidate_id='cand_sweep_stale'
	`, old); err != nil {
		t.Fatalf("backdate stale candidate: %v", err)
	}
	// A stale open claim the sweep decays.
	claim := projectmemory.Claim{ClaimID: "claim_sweep_stale", BatchID: "bat_sweep", ProjectID: projectID,
		Subject: "主体", Predicate: "state", Object: "过期佐证", AsOf: old,
		SourceRefs: []string{"doc_store"}, RaisedByType: string(ActorModel)}
	if err := store.StageMemoryClaims(ctx, []projectmemory.Claim{claim}); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
		UPDATE project_claims SET updated_at=$1 WHERE claim_id='claim_sweep_stale'
	`, old); err != nil {
		t.Fatalf("backdate stale claim: %v", err)
	}
	// A stale terminology candidate: created_at is trigger-protected, so the
	// fixture raw-inserts the aged row (the staging API always stamps now).
	if _, err := integrationDB.ExecContext(ctx, `
		INSERT INTO project_terminology (terminology_id, project_id, term, definition, aliases, forbidden,
			status, raised_by_type, created_at, updated_at)
		VALUES ('term_sweep_stale', $1, '遗忘测试术语', '候选态', '[]'::jsonb, '[]'::jsonb,
			'candidate', 'model', $2, $2)
	`, projectID, old); err != nil {
		t.Fatalf("stage stale terminology: %v", err)
	}
	// Decisions: first is superseded by second. The supersede stamps
	// updated_at=NOW(); backdate the cold one, leave the warm one fresh.
	first := projectmemory.Decision{DecisionID: "dec_sweep_cold", ProjectID: projectID,
		Statement: "冷决策", SourceRefs: []string{"doc_store"}, RaisedByType: string(ActorModel)}
	second := projectmemory.Decision{DecisionID: "dec_sweep_second", ProjectID: projectID,
		Statement: "第二版决策", SourceRefs: []string{"doc_store"}, RaisedByType: string(ActorModel), Supersedes: first.DecisionID}
	third := projectmemory.Decision{DecisionID: "dec_sweep_third", ProjectID: projectID,
		Statement: "第三版决策", SourceRefs: []string{"doc_store"}, RaisedByType: string(ActorModel), Supersedes: second.DecisionID}
	for _, decision := range []*projectmemory.Decision{&first, &second, &third} {
		if err := store.StageDecision(ctx, decision); err != nil {
			t.Fatal(err)
		}
		if err := store.PromoteDecision(ctx, decision.DecisionID, user); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := integrationDB.ExecContext(ctx, `
		UPDATE project_decisions SET updated_at=$1 WHERE decision_id='dec_sweep_cold'
	`, old); err != nil {
		t.Fatalf("backdate cold decision: %v", err)
	}
}

func TestForgettingSweepForgetsStaleAndPreservesCanon(t *testing.T) {
	if integrationDB == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	if _, err := integrationDB.ExecContext(ctx, `TRUNCATE project_memory_forgetting_log, project_facts, project_memory_candidates, writing_projects CASCADE`); err != nil {
		t.Fatalf("reset project tables: %v", err)
	}
	store, fixture := newIntegrationFixture(t, false)
	var userID string
	if err := integrationDB.QueryRowContext(ctx, `SELECT owner_user_id::text FROM writing_documents WHERE document_id=$1`, fixture.documentID).Scan(&userID); err != nil {
		t.Fatalf("load fixture user: %v", err)
	}
	user := Actor{Type: ActorUser, ID: userID}
	policyActor := Actor{Type: ActorPolicy, ID: "memory_forgetting"}
	projectID := "prj_forget"
	if err := store.CreateProject(ctx, ProjectRecord{ProjectID: projectID, OwnerUserID: userID, Title: "forgetting", Actor: user}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	old := now.Add(-120 * 24 * time.Hour)
	forgetSweepFixture(t, store, ctx, projectID, user, old)

	// Canon is established before the sweep and must survive it untouched.
	if _, committed, err := store.CommitMemoryCandidate(ctx, "cand_sweep_fresh", user, "fact_sweep_canon"); err != nil || !committed {
		t.Fatalf("commit canon fact: committed=%v err=%v", committed, err)
	}

	policy := projectmemory.DefaultForgetPolicy()
	hash, err := policy.Hash()
	if err != nil {
		t.Fatal(err)
	}

	// Preview counts, touches nothing.
	preview, err := store.PreviewForgettingPolicy(ctx, projectID, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CandidatesRejected != 1 || preview.ClaimsRejected != 1 || preview.TerminologyArchived != 1 || preview.DecisionsArchived != 1 {
		t.Fatalf("preview=%#v", preview)
	}
	if preview.SweepID != "" {
		t.Fatalf("preview carried a sweep id: %q", preview.SweepID)
	}
	if preview.PolicyHash != hash {
		t.Fatalf("preview hash %s != policy hash %s", preview.PolicyHash, hash)
	}

	// Model actors may never sweep.
	if _, err := store.ApplyForgettingPolicy(ctx, projectID, policy, now, Actor{Type: ActorModel, ID: "m"}); err == nil || !strings.Contains(err.Error(), "policy or user actor") {
		t.Fatalf("model actor swept: %v", err)
	}

	applied, err := store.ApplyForgettingPolicy(ctx, projectID, policy, now, policyActor)
	if err != nil {
		t.Fatal(err)
	}
	if applied.CandidatesRejected != 1 || applied.ClaimsRejected != 1 || applied.TerminologyArchived != 1 || applied.DecisionsArchived != 1 {
		t.Fatalf("applied=%#v", applied)
	}
	if applied.Logged() != 4 {
		t.Fatalf("logged=%d, want one row per transition", applied.Logged())
	}
	if applied.SweepID == "" || !strings.HasPrefix(applied.SweepID, "swp_") {
		t.Fatalf("sweep id=%q", applied.SweepID)
	}

	// The stale objects transitioned; the survivors stayed.
	if status := forgetStatus(t, ctx, "project_memory_candidates", "candidate_id", "cand_sweep_stale"); status != "rejected" {
		t.Fatalf("stale candidate status=%s", status)
	}
	if status := forgetStatus(t, ctx, "project_memory_candidates", "candidate_id", "cand_sweep_fresh"); status != "committed" {
		t.Fatalf("committed candidate status=%s", status)
	}
	if status := forgetStatus(t, ctx, "project_claims", "claim_id", "claim_sweep_stale"); status != "rejected" {
		t.Fatalf("stale claim status=%s", status)
	}
	if status := forgetStatus(t, ctx, "project_terminology", "terminology_id", "term_sweep_stale"); status != "archived" {
		t.Fatalf("stale terminology status=%s", status)
	}
	if status := forgetStatus(t, ctx, "project_decisions", "decision_id", "dec_sweep_cold"); status != "archived" {
		t.Fatalf("cold superseded status=%s", status)
	}
	if status := forgetStatus(t, ctx, "project_decisions", "decision_id", "dec_sweep_second"); status != "superseded" {
		t.Fatalf("warm superseded moved: %s", status)
	}
	if status := forgetStatus(t, ctx, "project_decisions", "decision_id", "dec_sweep_third"); status != "active" {
		t.Fatalf("active decision moved: %s", status)
	}
	canon, err := store.GetFact(ctx, "fact_sweep_canon")
	if err != nil || canon.ValidTo != nil || canon.SupersededBy != "" {
		t.Fatalf("canon fact disturbed: fact=%#v err=%v", canon, err)
	}
	if active, err := store.ListActiveFacts(ctx, projectID, ""); err != nil || len(active) != 1 {
		t.Fatalf("canon fact left the active set: %d err=%v", len(active), err)
	}

	// The log records exactly what happened, with the real from-statuses.
	log, err := store.ListForgettingLog(ctx, projectID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 4 {
		t.Fatalf("log rows=%d", len(log))
	}
	seen := map[string]ForgettingLogRecord{}
	for _, record := range log {
		seen[record.ObjectID] = record
		if record.SweepID != applied.SweepID || record.PolicyHash != hash || record.PolicyVersion != projectmemory.ForgetPolicyVersion {
			t.Fatalf("log row unattributed: %#v", record)
		}
		if record.AppliedByType != string(ActorPolicy) {
			t.Fatalf("log row actor=%s", record.AppliedByType)
		}
	}
	if seen["cand_sweep_stale"].FromStatus != "staged" || seen["cand_sweep_stale"].Rule != projectmemory.RuleStaleCandidate {
		t.Fatalf("candidate log row=%#v", seen["cand_sweep_stale"])
	}
	if seen["claim_sweep_stale"].FromStatus != "open" || seen["claim_sweep_stale"].Rule != projectmemory.RuleClaimDecay {
		t.Fatalf("claim log row=%#v", seen["claim_sweep_stale"])
	}
	if seen["term_sweep_stale"].FromStatus != "candidate" || seen["term_sweep_stale"].ToStatus != "archived" {
		t.Fatalf("terminology log row=%#v", seen["term_sweep_stale"])
	}
	if seen["dec_sweep_cold"].FromStatus != "superseded" || seen["dec_sweep_cold"].Rule != projectmemory.RuleColdSuperseded {
		t.Fatalf("decision log row=%#v", seen["dec_sweep_cold"])
	}

	// Idempotent by construction: a second sweep forgets nothing new and
	// writes no rows.
	second, err := store.ApplyForgettingPolicy(ctx, projectID, policy, now.Add(time.Hour), policyActor)
	if err != nil {
		t.Fatal(err)
	}
	if second.Logged() != 0 {
		t.Fatalf("second sweep transitioned %#v", second)
	}
	again, err := store.ListForgettingLog(ctx, projectID, 100)
	if err != nil || len(again) != 4 {
		t.Fatalf("log grew on replay: rows=%d err=%v", len(again), err)
	}

	// Canon stays canon after the second sweep too.
	if _, err := store.GetFact(ctx, "fact_sweep_canon"); err != nil {
		t.Fatalf("canon fact vanished: %v", err)
	}
}

// TestForgettingSweepFreesTerminologySlot pins the operational payoff of
// archiving stale candidates: the partial unique index holds one live entry
// per term, and the sweep releases the slot so the term can be re-staged.
func TestForgettingSweepFreesTerminologySlot(t *testing.T) {
	if integrationDB == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	if _, err := integrationDB.ExecContext(ctx, `TRUNCATE project_memory_forgetting_log, project_facts, project_memory_candidates, writing_projects CASCADE`); err != nil {
		t.Fatalf("reset project tables: %v", err)
	}
	store, fixture := newIntegrationFixture(t, false)
	var userID string
	if err := integrationDB.QueryRowContext(ctx, `SELECT owner_user_id::text FROM writing_documents WHERE document_id=$1`, fixture.documentID).Scan(&userID); err != nil {
		t.Fatalf("load fixture user: %v", err)
	}
	user := Actor{Type: ActorUser, ID: userID}
	projectID := "prj_slot"
	if err := store.CreateProject(ctx, ProjectRecord{ProjectID: projectID, OwnerUserID: userID, Title: "slot", Actor: user}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour)
	if _, err := integrationDB.ExecContext(ctx, `
		INSERT INTO project_terminology (terminology_id, project_id, term, definition, aliases, forbidden,
			status, raised_by_type, created_at, updated_at)
		VALUES ('term_slot_stale', $1, '占位术语', '旧定义', '[]'::jsonb, '[]'::jsonb,
			'candidate', 'model', $2, $2)
	`, projectID, old); err != nil {
		t.Fatalf("stage stale terminology: %v", err)
	}
	// The stale candidate holds the term slot.
	blocked := projectmemory.Terminology{TerminologyID: "term_slot_new", ProjectID: projectID,
		Term: "占位术语", Definition: "新定义", SourceRefs: []string{"doc_store"}, RaisedByType: string(ActorModel)}
	if err := store.StageTerminology(ctx, &blocked); err == nil {
		t.Fatal("stale candidate did not hold the term slot")
	}
	policy := projectmemory.ForgetPolicy{CandidateHorizon: 30 * 24 * time.Hour}
	applied, err := store.ApplyForgettingPolicy(ctx, projectID, policy, now, Actor{Type: ActorPolicy, ID: "memory_forgetting"})
	if err != nil {
		t.Fatal(err)
	}
	if applied.TerminologyArchived != 1 {
		t.Fatalf("applied=%#v", applied)
	}
	if err := store.StageTerminology(ctx, &blocked); err != nil {
		t.Fatalf("slot not freed after sweep: %v", err)
	}
}

func forgetStatus(t *testing.T, ctx context.Context, table, idColumn, id string) string {
	t.Helper()
	if !forgettingSweepTables[table] {
		t.Fatalf("table %s is not sweepable", table)
	}
	var status string
	if err := integrationDB.QueryRowContext(ctx, `SELECT status FROM `+table+` WHERE `+idColumn+`=$1`, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("object %s missing", id)
		}
		t.Fatalf("query %s %s: %v", table, id, err)
	}
	return status
}
