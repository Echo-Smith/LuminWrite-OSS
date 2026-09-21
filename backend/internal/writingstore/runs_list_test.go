package writingstore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

// TestListRunsByOwner verifies the sidebar's governed history query: runs are
// scoped by their document's owner (the same ownership GetRun authorizes
// through, not the run row's created_by actor), ordered newest first, and
// paginated with a total count.
func TestListRunsByOwner(t *testing.T) {
	if integrationDB == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, fixture := newIntegrationFixture(t, true)
	ctx := context.Background()

	// A second owner with their own document + contract + run chain.
	var user2 string
	uid := StableID("writingstore_", strings.ToLower(t.Name()), fmt.Sprint(time.Now().UnixNano()))
	if err := integrationDB.QueryRowContext(ctx, `
		INSERT INTO users (uid, name) VALUES ($1, 'writingstore owner 2') RETURNING id::text
	`, uid).Scan(&user2); err != nil {
		t.Fatalf("create second fixture user: %v", err)
	}
	if err := store.CreateDocument(ctx, DocumentRecord{DocumentID: "doc_owner2",
		OwnerUserID: user2, Title: "Other owner", Actor: testTrace().Actor}); err != nil {
		t.Fatal(err)
	}
	// Contract identity is global (contract_id, version), so the second owner
	// needs their own resealed contract.
	contract2 := fixture.contract
	contract2.ContractID = "ctr_owner2"
	contract2 = resealContract(t, contract2)
	if err := store.PutContract(ctx, ContractRecord{DocumentID: "doc_owner2",
		Contract: contract2, Trace: testTrace()}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRun(ctx, RunRecord{RunID: "run_owner2", DocumentID: "doc_owner2",
		ContractID: contract2.ContractID, ContractVersion: contract2.Version,
		ContractHash: contract2.ContractHash, StyleSlug: "yinyue",
		Status: "planned", ApprovalMode: writingkernel.ApprovalModeAuto,
		RequestedAssurance: writingkernel.AssuranceLevelStandard,
		Budget:             writingplan.PlanBudget{MaxCostUSD: 10, MaxDurationMS: 10000, MaxConcurrency: 1, MaxNodes: 2, MaxItems: 1},
		Permissions:        []writingplan.Permission{"model.invoke"}, Trace: testTrace()}); err != nil {
		t.Fatal(err)
	}

	// A second run for the fixture owner, created after run_store so the
	// ordering assertion is deterministic.
	if err := store.CreateRun(ctx, RunRecord{RunID: "run_owner1_newer", DocumentID: fixture.documentID,
		ContractID: fixture.contract.ContractID, ContractVersion: fixture.contract.Version,
		ContractHash: fixture.contract.ContractHash, StyleSlug: "yinyue",
		Status: "planned", ApprovalMode: writingkernel.ApprovalModeAuto,
		RequestedAssurance: writingkernel.AssuranceLevelStandard,
		Budget:             writingplan.PlanBudget{MaxCostUSD: 10, MaxDurationMS: 10000, MaxConcurrency: 1, MaxNodes: 2, MaxItems: 1},
		Permissions:        []writingplan.Permission{"model.invoke"}, Trace: testTrace()}); err != nil {
		t.Fatal(err)
	}
	owner1, err := store.LoadRuntimeRun(ctx, fixture.runID)
	if err != nil {
		t.Fatal(err)
	}

	// completed_at round-trips for terminal runs.
	completedAt := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := integrationDB.ExecContext(ctx,
		`UPDATE writing_runs SET completed_at=$2 WHERE run_id=$1`, "run_owner1_newer", completedAt); err != nil {
		t.Fatal(err)
	}

	// Full page for the fixture owner: both runs, newest first, the other
	// owner's run excluded.
	items, total, err := store.ListRunsByOwner(ctx, owner1.OwnerUserID, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("total=%d items=%#v", total, items)
	}
	if items[0].RunID != "run_owner1_newer" || items[1].RunID != fixture.runID {
		t.Fatalf("unexpected order: %s, %s", items[0].RunID, items[1].RunID)
	}
	first := items[0]
	if first.DocumentID != fixture.documentID || first.Title != "Store test" ||
		first.StyleSlug != "yinyue" || first.Status != "planned" {
		t.Fatalf("projection mismatch: %#v", first)
	}
	if first.CompletedAt == nil || !first.CompletedAt.Equal(completedAt) {
		t.Fatalf("completed_at=%v want %v", first.CompletedAt, completedAt)
	}
	if items[1].CompletedAt != nil {
		t.Fatalf("running run reported completed_at=%v", items[1].CompletedAt)
	}

	// Pagination windows.
	page1, total, err := store.ListRunsByOwner(ctx, owner1.OwnerUserID, 1, 0)
	if err != nil || total != 2 || len(page1) != 1 || page1[0].RunID != "run_owner1_newer" {
		t.Fatalf("page1=%#v total=%d err=%v", page1, total, err)
	}
	page2, _, err := store.ListRunsByOwner(ctx, owner1.OwnerUserID, 1, 1)
	if err != nil || len(page2) != 1 || page2[0].RunID != fixture.runID {
		t.Fatalf("page2=%#v err=%v", page2, err)
	}
	beyond, _, err := store.ListRunsByOwner(ctx, owner1.OwnerUserID, 1, 5)
	if err != nil || len(beyond) != 0 {
		t.Fatalf("beyond=%#v err=%v", beyond, err)
	}

	// The other owner sees only their own run.
	items2, total2, err := store.ListRunsByOwner(ctx, user2, 50, 0)
	if err != nil || total2 != 1 || len(items2) != 1 || items2[0].RunID != "run_owner2" || items2[0].Title != "Other owner" {
		t.Fatalf("owner2 items=%#v total=%d err=%v", items2, total2, err)
	}

	// An owner with no runs gets an empty page, not an error.
	empty, totalEmpty, err := store.ListRunsByOwner(ctx, "00000000-0000-0000-0000-000000000000", 50, 0)
	if err != nil || totalEmpty != 0 || len(empty) != 0 {
		t.Fatalf("empty owner items=%#v total=%d err=%v", empty, totalEmpty, err)
	}
}
