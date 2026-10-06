package database_test

import (
	"context"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
)

// TestPurposeRouting: a verification-flagged key serves review lanes without
// displacing the user's default writing model, and generation stays put.
func TestPurposeRouting(t *testing.T) {
	_, adminRepo, repo, userA, _ := newBYOKFixture(t)
	seedGlobalDefault(t, repoRawDB(repo, adminRepo), "global-purpose-model", "sk-global")
	ctx := context.Background()

	// The user's default writing model (expensive).
	if _, err := repo.Create(ctx, &database.UserModelKey{
		UserID: userA, Provider: "deepseek", ModelName: "writer-model",
		APIKeyPlain: "sk-writer", IsDefault: true,
	}); err != nil {
		t.Fatalf("seed writer model: %v", err)
	}
	// A cheap verification-flagged key. Create must not let it claim the
	// default slot even if is_default was (incorrectly) set by a client.
	if _, err := repo.Create(ctx, &database.UserModelKey{
		UserID: userA, Provider: "deepseek", ModelName: "cheap-verifier",
		APIKeyPlain: "sk-verifier", Purpose: "verification", IsDefault: true,
	}); err != nil {
		t.Fatalf("seed verifier model: %v", err)
	}

	def, err := repo.GetDefaultForUser(ctx, userA)
	if err != nil {
		t.Fatalf("default lookup: %v", err)
	}
	if def.ModelName != "writer-model" {
		t.Fatalf("verification row stole the default slot: %q", def.ModelName)
	}

	ver, err := repo.GetForUserByPurpose(ctx, userA, "verification")
	if err != nil {
		t.Fatalf("purpose lookup: %v", err)
	}
	if ver.ModelName != "cheap-verifier" {
		t.Fatalf("purpose lookup got %q", ver.ModelName)
	}
	if _, err := repo.GetForUserByPurpose(ctx, userA, "embedding"); err == nil {
		t.Fatal("embedding purpose unexpectedly resolved")
	}

	svc := services.NewLLMService(adminRepo, nil, 0).WithUserKeys(repo)

	// Verification lanes resolve to the cheap verifier.
	c := svc.GetClientForPurpose(ctx, userA, "verification", "")
	if c == nil || c.Model() != "cheap-verifier" {
		t.Fatalf("verification purpose: got %v", c)
	}
	// Writing (default resolution) still uses the expensive writer.
	w := svc.GetClientForUser(ctx, userA, "")
	if w == nil || w.Model() != "writer-model" {
		t.Fatalf("generation resolution changed: got %v", w)
	}
	// Purpose cache is a distinct slot: invalidation and hits do not cross.
	if svc.GetClientForPurpose(ctx, userA, "verification", "") != c {
		t.Fatal("purpose cache did not hit")
	}
	// A user without any keys falls through to the global default.
	c2 := svc.GetClientForPurpose(ctx, "00000000-0000-0000-0000-00000000nope", "verification", "")
	if c2 == nil {
		t.Fatal("fallback client missing")
	}
	// empty user → global default as well.
	if got := svc.GetClientForPurpose(ctx, "", "verification", ""); got == nil || got.Model() != "global-purpose-model" {
		t.Fatalf("system caller: got %v", got)
	}
}
