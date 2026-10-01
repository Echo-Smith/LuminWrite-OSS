package database_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database/dbtest"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/crypto"
)

// newBYOKFixture opens an isolated migrated database and seeds two users.
func newBYOKFixture(t *testing.T) (*database.DB, *database.AdminRepo, *database.UserModelKeyRepo, string, string) {
	t.Helper()
	db, cleanup, err := dbtest.Open(os.Getenv("TEST_DATABASE_URL"), 4, 2)
	if errors.Is(err, dbtest.ErrNoDatabaseURL) {
		t.Skip("TEST_DATABASE_URL not set")
	}
	if err != nil {
		t.Fatalf("open isolated test database: %v", err)
	}
	t.Cleanup(cleanup)

	ctx := context.Background()
	var userA, userB string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (uid, name, role) VALUES ('byok-a', 'byok-a', 'user') RETURNING id::text`,
	).Scan(&userA); err != nil {
		t.Fatalf("seed user a: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (uid, name, role) VALUES ('byok-b', 'byok-b', 'user') RETURNING id::text`,
	).Scan(&userB); err != nil {
		t.Fatalf("seed user b: %v", err)
	}

	adminRepo := database.NewAdminRepo(db).WithEncryptionKey(crypto.DeriveKey("byok-test-encryption-key"))
	userRepo := database.NewUserModelKeyRepo(db, crypto.DeriveKey("byok-test-encryption-key"))
	return db, adminRepo, userRepo, userA, userB
}

func TestUserModelKeyCRUDAndOwnership(t *testing.T) {
	_, _, repo, userA, userB := newBYOKFixture(t)
	ctx := context.Background()

	created, err := repo.Create(ctx, &database.UserModelKey{
		UserID: userA, Provider: "deepseek", ModelName: "user-a-model",
		Name: "A 的模型", APIKeyPlain: "sk-user-a-secret", MaxTokens: 4096, Temperature: 0.5,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// APIKeyEncrypted is retained in-process for LLMService (json:"-" guards
	// the wire); APIKeyPlain must be wiped after create.
	if !created.HasAPIKey || created.APIKeyEncrypted == "" || created.APIKeyPlain != "" {
		t.Fatalf("unexpected created key state: %+v", created)
	}

	// Ownership: user B cannot read/update/delete/default A's key.
	if _, err := repo.GetForUser(ctx, userB, created.ID); !errors.Is(err, database.ErrUserModelKeyNotFound) {
		t.Fatalf("user B read A's key: got %v, want ErrUserModelKeyNotFound", err)
	}
	if _, err := repo.Update(ctx, userB, created.ID, &database.UserModelKey{Provider: "openai", ModelName: "hijack"}); !errors.Is(err, database.ErrUserModelKeyNotFound) {
		t.Fatalf("user B updated A's key: got %v", err)
	}
	if err := repo.Delete(ctx, userB, created.ID); !errors.Is(err, database.ErrUserModelKeyNotFound) {
		t.Fatalf("user B deleted A's key: got %v", err)
	}
	if err := repo.SetDefault(ctx, userB, created.ID); !errors.Is(err, database.ErrUserModelKeyNotFound) {
		t.Fatalf("user B defaulted A's key: got %v", err)
	}

	// Malformed ids surface as not-found (404), not Postgres cast errors (500).
	badID := "not-a-uuid"
	if _, err := repo.GetForUser(ctx, userA, badID); !errors.Is(err, database.ErrUserModelKeyNotFound) {
		t.Fatalf("malformed id read: got %v", err)
	}
	if err := repo.SetDefault(ctx, userA, badID); !errors.Is(err, database.ErrUserModelKeyNotFound) {
		t.Fatalf("malformed id default: got %v", err)
	}

	// Update preserving key: empty api_key keeps the stored one.
	updated, err := repo.Update(ctx, userA, created.ID, &database.UserModelKey{
		Provider: "deepseek", ModelName: "user-a-model", Name: "改名", MaxTokens: 2048, Temperature: 0.5,
	})
	if err != nil {
		t.Fatalf("update preserving key: %v", err)
	}
	if !updated.HasAPIKey {
		t.Fatal("update with empty api_key lost the stored key")
	}
	if got := repo.DecryptAPIKey(updated.APIKeyEncrypted); got != "sk-user-a-secret" {
		t.Fatalf("stored key changed across update: %q", got)
	}

	// Default uniqueness: two defaults collapse to one.
	if _, err := repo.Create(ctx, &database.UserModelKey{
		UserID: userA, Provider: "openai", ModelName: "user-a-second",
		APIKeyPlain: "sk-second", IsDefault: true,
	}); err != nil {
		t.Fatalf("create second with default: %v", err)
	}
	list, err := repo.ListForUser(ctx, userA)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defaults := 0
	for _, k := range list {
		if k.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("user A has %d defaults, want 1", defaults)
	}

	// Duplicate model_name for the same user is rejected.
	if _, err := repo.Create(ctx, &database.UserModelKey{
		UserID: userA, Provider: "deepseek", ModelName: "user-a-model", APIKeyPlain: "sk-dup",
	}); err == nil {
		t.Fatal("duplicate (user, model_name) accepted")
	}

	// Delete by owner works.
	if err := repo.Delete(ctx, userA, created.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
}
