package database_test

import (
	"context"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/crypto"
)

// seedGlobalDefault inserts the deployment's default model config.
// A dedicated provider sidesteps the migrated-in deepseek default row
// (partial unique (provider, purpose) WHERE is_default).
func seedGlobalDefault(t *testing.T, db *database.DB, modelName, apiKey string) {
	t.Helper()
	encrypted, err := crypto.Encrypt(apiKey, crypto.DeriveKey("byok-test-encryption-key"))
	if err != nil {
		t.Fatalf("encrypt global key: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs (provider, model_name, display_name, base_url, api_key_encrypted, max_tokens, temperature, is_default, is_active)
		VALUES ('testglobal', $1, $1, 'https://global.example.com', $2, 8192, 0.7, TRUE, TRUE)
	`, modelName, encrypted); err != nil {
		t.Fatalf("seed global default: %v", err)
	}
}

// repoRawDB reaches the underlying *DB through the admin repo.
func repoRawDB(repo *database.UserModelKeyRepo, adminRepo *database.AdminRepo) *database.DB {
	_ = repo
	return adminRepo.DB()
}

func TestLLMServiceUserResolutionOrder(t *testing.T) {
	_, adminRepo, repo, userA, userB := newBYOKFixture(t)
	seedGlobalDefault(t, repoRawDB(repo, adminRepo), "global-default-model", "sk-global")
	ctx := context.Background()

	if _, err := repo.Create(ctx, &database.UserModelKey{
		UserID: userA, Provider: "deepseek", ModelName: "user-a-model", APIKeyPlain: "sk-a", IsDefault: true,
	}); err != nil {
		t.Fatalf("seed user a key: %v", err)
	}
	if _, err := repo.Create(ctx, &database.UserModelKey{
		UserID: userB, Provider: "qwen", ModelName: "user-b-model", APIKeyPlain: "sk-b",
	}); err != nil {
		t.Fatalf("seed user b key: %v", err)
	}

	svc := services.NewLLMService(adminRepo, nil, 0).WithUserKeys(repo)
	model := func(userID, name string) string {
		t.Helper()
		c := svc.GetClientForUser(ctx, userID, name)
		if c == nil {
			t.Fatalf("no client for user=%q model=%q", userID, name)
		}
		return c.Model()
	}

	// Unspecified request → the user's own default when one exists.
	if got := model(userA, ""); got != "user-a-model" {
		t.Fatalf("user A default: got %q, want user-a-model", got)
	}
	// User B has keys but no default → global default.
	if got := model(userB, ""); got != "global-default-model" {
		t.Fatalf("user B default: got %q, want global-default-model", got)
	}
	// Explicit global model name keeps the platform key even for BYOK users.
	if got := model(userA, "global-default-model"); got != "global-default-model" {
		t.Fatalf("user A explicit global model: got %q", got)
	}
	// Explicit user model name uses the user's entry.
	if got := model(userA, "user-a-model"); got != "user-a-model" {
		t.Fatalf("user A explicit own model: got %q", got)
	}
	// System caller (empty userID) → global layer only.
	if got := model("", ""); got != "global-default-model" {
		t.Fatalf("system caller: got %q, want global-default-model", got)
	}
}

func TestLLMServiceUserCacheIsolation(t *testing.T) {
	_, adminRepo, repo, userA, userB := newBYOKFixture(t)
	seedGlobalDefault(t, repoRawDB(repo, adminRepo), "global-cache-model", "sk-global")
	ctx := context.Background()

	for _, u := range []struct{ id, model string }{{userA, "shared-name"}, {userB, "shared-name"}} {
		if _, err := repo.Create(ctx, &database.UserModelKey{
			UserID: u.id, Provider: "deepseek", ModelName: u.model, APIKeyPlain: "sk-" + u.id[:8], IsDefault: true,
		}); err != nil {
			t.Fatalf("seed key for %s: %v", u.id, err)
		}
	}

	svc := services.NewLLMService(adminRepo, nil, 0).WithUserKeys(repo)
	ca := svc.GetClientForUser(ctx, userA, "shared-name")
	cb := svc.GetClientForUser(ctx, userB, "shared-name")
	if ca == nil || cb == nil {
		t.Fatal("expected clients for both users")
	}
	if ca == cb {
		t.Fatal("cache collision: two users sharing a model name got the same client")
	}

	// User-key invalidation is scoped: only the target user's cached clients drop.
	before := svc.GetClientForUser(ctx, userB, "shared-name")
	svc.InvalidateUserCache(userA)
	afterB := svc.GetClientForUser(ctx, userB, "shared-name")
	if before != afterB {
		t.Fatal("InvalidateUserCache(userA) evicted user B's cached client")
	}
}
