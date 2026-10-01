package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/crypto"
)

// ─── User Model Keys (BYOK) ──────────────────────────────

// ErrUserModelKeyNotFound marks a missing or non-owned user model key.
// Handlers map it to 404 without leaking ownership state.
var ErrUserModelKeyNotFound = errors.New("user model key not found")

// UserModelKey represents one BYOK model endpoint owned by a user.
// APIKeyEncrypted follows the model_configs inline convention: encrypted at
// rest with the deployment's AES key, never serialized. APIKeyPlain is the
// write-only field set by the user; HasAPIKey is the read-only marker.
type UserModelKey struct {
	ID              string            `json:"id"`
	UserID          string            `json:"user_id"`
	Name            string            `json:"name"`
	Provider        string            `json:"provider"`
	ModelName       string            `json:"model_name"`
	BaseURL         string            `json:"base_url"`
	APIKeyEncrypted string            `json:"-"`
	APIKeyPlain     string            `json:"api_key,omitempty"` // write-only
	HasAPIKey       bool              `json:"has_api_key"`       // read-only
	MaxTokens       int               `json:"max_tokens"`
	Temperature     float64           `json:"temperature"`
	ReasoningEffort string            `json:"reasoning_effort"`
	Purpose         string            `json:"purpose"` // storage-only this iteration; not routed
	IsDefault       bool              `json:"is_default"`
	IsActive        bool              `json:"is_active"`
	CustomHeaders   map[string]string `json:"custom_headers"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// UserModelKeyRepo owns the user_model_keys table (BYOK storage).
// Resolution semantics live in services.LLMService: user config first,
// global model_configs fallback, env as last resort.
type UserModelKeyRepo struct {
	db     *DB
	encKey []byte
}

// NewUserModelKeyRepo creates a new repo; encKey may be nil (plaintext mode,
// which BYOK write handlers must refuse — see EncryptionEnabled).
func NewUserModelKeyRepo(db *DB, encKey []byte) *UserModelKeyRepo {
	return &UserModelKeyRepo{db: db, encKey: encKey}
}

// EncryptionEnabled reports whether the deployment configured the AES key.
// BYOK endpoints must refuse writes when this returns false.
func (r *UserModelKeyRepo) EncryptionEnabled() bool {
	return r.db != nil && len(r.encKey) > 0
}

// DecryptAPIKey mirrors AdminRepo.DecryptModelAPIKey: raw value passthrough
// when encryption is not configured (legacy plaintext rows).
func (r *UserModelKeyRepo) DecryptAPIKey(encryptedKey string) string {
	if encryptedKey == "" {
		return ""
	}
	if len(r.encKey) > 0 {
		if decrypted, err := crypto.Decrypt(encryptedKey, r.encKey); err == nil {
			return decrypted
		}
	}
	return encryptedKey // not encrypted
}

const userModelKeyColumns = `
	id::text, user_id::text, name, provider, model_name, base_url,
	api_key_encrypted, max_tokens, temperature, reasoning_effort, purpose,
	is_default, is_active, custom_headers, created_at, updated_at
`

func scanUserModelKey(scan func(dest ...interface{}) error) (*UserModelKey, error) {
	var k UserModelKey
	var hdrJSON []byte
	if err := scan(&k.ID, &k.UserID, &k.Name, &k.Provider, &k.ModelName, &k.BaseURL,
		&k.APIKeyEncrypted, &k.MaxTokens, &k.Temperature, &k.ReasoningEffort, &k.Purpose,
		&k.IsDefault, &k.IsActive, &hdrJSON, &k.CreatedAt, &k.UpdatedAt); err != nil {
		return nil, err
	}
	k.HasAPIKey = k.APIKeyEncrypted != ""
	// APIKeyEncrypted is retained for LLMService decryption; the `json:"-"`
	// tag (not clearing) is the exposure boundary toward API responses.
	if len(hdrJSON) > 0 {
		json.Unmarshal(hdrJSON, &k.CustomHeaders)
	}
	return &k, nil
}

// ListForUser returns all model keys owned by userID.
func (r *UserModelKeyRepo) ListForUser(ctx context.Context, userID string) ([]*UserModelKey, error) {
	if r.db == nil {
		return []*UserModelKey{}, nil
	}
	if _, err := uuid.Parse(userID); err != nil {
		return []*UserModelKey{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+userModelKeyColumns+`
		FROM user_model_keys
		WHERE user_id = $1::uuid
		ORDER BY is_default DESC, created_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*UserModelKey
	for rows.Next() {
		k, err := scanUserModelKey(rows.Scan)
		if err != nil {
			continue
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// GetForUser retrieves one key by ID, enforcing ownership in the WHERE clause.
func (r *UserModelKeyRepo) GetForUser(ctx context.Context, userID, id string) (*UserModelKey, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if _, err := uuid.Parse(userID); err != nil {
		return nil, ErrUserModelKeyNotFound
	}
	if !validUUID(id) {
		return nil, ErrUserModelKeyNotFound
	}
	k, err := scanUserModelKey(r.db.QueryRowContext(ctx, `
		SELECT `+userModelKeyColumns+`
		FROM user_model_keys
		WHERE id = $2::uuid AND user_id = $1::uuid
	`, userID, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserModelKeyNotFound
	}
	return k, err
}

// validUUID guards the $n::uuid casts: a malformed id must surface as
// ErrUserModelKeyNotFound (404), not a Postgres cast error (500).
func validUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

// encryptStoredKey encrypts APIKeyPlain when set. Empty plain means "keep
// existing" for updates and "none" for creates.
func (r *UserModelKeyRepo) encryptStoredKey(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if len(r.encKey) == 0 {
		return "", fmt.Errorf("API_KEY_ENCRYPTION_KEY not configured; refusing plaintext BYOK storage")
	}
	encrypted, err := crypto.Encrypt(plain, r.encKey)
	if err != nil {
		return "", fmt.Errorf("encrypt api key: %w", err)
	}
	return encrypted, nil
}

func (r *UserModelKeyRepo) marshalHeaders(k *UserModelKey) string {
	hdrJSON, _ := json.Marshal(k.CustomHeaders)
	if k.CustomHeaders == nil {
		hdrJSON = []byte("null")
	}
	return string(hdrJSON)
}

// clearUserDefault drops the user's current default marker. The partial
// unique index allows at most one default per user.
func (r *UserModelKeyRepo) clearUserDefault(ctx context.Context, tx *sql.Tx, userID string) error {
	if tx != nil {
		_, err := tx.ExecContext(ctx, `UPDATE user_model_keys SET is_default = FALSE WHERE user_id = $1::uuid AND is_default`, userID)
		return err
	}
	_, err := r.db.ExecContext(ctx, `UPDATE user_model_keys SET is_default = FALSE WHERE user_id = $1::uuid AND is_default`, userID)
	return err
}

// Create inserts a new key for the user. If k.IsDefault, the previous default
// is cleared first. Requires EncryptionEnabled (rejects plaintext storage).
func (r *UserModelKeyRepo) Create(ctx context.Context, k *UserModelKey) (*UserModelKey, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if _, err := uuid.Parse(k.UserID); err != nil {
		return nil, fmt.Errorf("invalid user id")
	}
	stored, err := r.encryptStoredKey(k.APIKeyPlain)
	if err != nil {
		return nil, err
	}
	if k.Purpose == "" {
		k.Purpose = "generation"
	}
	// IsActive has no user-facing toggle: creates are always active (the Go
	// zero value false must not override the column's intended TRUE).
	k.IsActive = true
	if k.IsDefault {
		if err := r.clearUserDefault(ctx, nil, k.UserID); err != nil {
			return nil, err
		}
	}
	created, err := scanUserModelKey(r.db.QueryRowContext(ctx, `
		INSERT INTO user_model_keys (user_id, name, provider, model_name, base_url, api_key_encrypted,
			max_tokens, temperature, reasoning_effort, purpose, is_default, is_active, custom_headers)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb)
		RETURNING `+userModelKeyColumns+`
	`, k.UserID, k.Name, k.Provider, k.ModelName, k.BaseURL, stored,
		k.MaxTokens, k.Temperature, k.ReasoningEffort, k.Purpose, k.IsDefault, k.IsActive, r.marshalHeaders(k)).Scan)
	if err != nil {
		return nil, err
	}
	created.APIKeyPlain = ""
	return created, nil
}

// Update modifies a key owned by userID. Empty APIKeyPlain preserves the
// stored key; a non-empty value re-encrypts and replaces it.
func (r *UserModelKeyRepo) Update(ctx context.Context, userID, id string, k *UserModelKey) (*UserModelKey, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if _, err := uuid.Parse(userID); err != nil {
		return nil, ErrUserModelKeyNotFound
	}
	if !validUUID(id) {
		return nil, ErrUserModelKeyNotFound
	}
	stored, err := r.encryptStoredKey(k.APIKeyPlain)
	if err != nil {
		return nil, err
	}
	if k.Purpose == "" {
		k.Purpose = "generation"
	}
	if k.IsDefault {
		if err := r.clearUserDefault(ctx, nil, userID); err != nil {
			return nil, err
		}
	}
	// is_active is deliberately untouched by updates: the field has no UI
	// surface and a JSON-omitted false must not deactivate the entry.
	updated, err := scanUserModelKey(r.db.QueryRowContext(ctx, `
		UPDATE user_model_keys SET
			name = $3, provider = $4, model_name = $5, base_url = $6,
			api_key_encrypted = CASE WHEN $7 <> '' THEN $7 ELSE api_key_encrypted END,
			max_tokens = $8, temperature = $9, reasoning_effort = $10, purpose = $11,
			is_default = $12, custom_headers = $13::jsonb,
			updated_at = NOW()
		WHERE id = $2::uuid AND user_id = $1::uuid
		RETURNING `+userModelKeyColumns+`
	`, userID, id, k.Name, k.Provider, k.ModelName, k.BaseURL, stored,
		k.MaxTokens, k.Temperature, k.ReasoningEffort, k.Purpose, k.IsDefault, r.marshalHeaders(k)).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserModelKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	updated.APIKeyPlain = ""
	return updated, nil
}

// SetDefault marks one key as the user's default, clearing the previous one
// in a single transaction.
func (r *UserModelKeyRepo) SetDefault(ctx context.Context, userID, id string) error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}
	if _, err := uuid.Parse(userID); err != nil {
		return ErrUserModelKeyNotFound
	}
	if !validUUID(id) {
		return ErrUserModelKeyNotFound
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := r.clearUserDefault(ctx, tx, userID); err != nil {
		return err
	}
	tag, err := tx.ExecContext(ctx, `
		UPDATE user_model_keys SET is_default = TRUE, updated_at = NOW()
		WHERE id = $2::uuid AND user_id = $1::uuid AND is_active
	`, userID, id)
	if err != nil {
		return err
	}
	if n, _ := tag.RowsAffected(); n == 0 {
		return ErrUserModelKeyNotFound
	}
	return tx.Commit()
}

// Delete removes a key owned by userID.
func (r *UserModelKeyRepo) Delete(ctx context.Context, userID, id string) error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}
	if _, err := uuid.Parse(userID); err != nil {
		return ErrUserModelKeyNotFound
	}
	if !validUUID(id) {
		return ErrUserModelKeyNotFound
	}
	tag, err := r.db.ExecContext(ctx, `DELETE FROM user_model_keys WHERE id = $2::uuid AND user_id = $1::uuid`, userID, id)
	if err != nil {
		return err
	}
	if n, _ := tag.RowsAffected(); n == 0 {
		return ErrUserModelKeyNotFound
	}
	return nil
}

// GetForUserByName returns the user's active key matching model_name exactly.
func (r *UserModelKeyRepo) GetForUserByName(ctx context.Context, userID, modelName string) (*UserModelKey, error) {
	if r.db == nil || userID == "" || modelName == "" {
		return nil, sql.ErrNoRows
	}
	if _, err := uuid.Parse(userID); err != nil {
		return nil, sql.ErrNoRows
	}
	k, err := scanUserModelKey(r.db.QueryRowContext(ctx, `
		SELECT `+userModelKeyColumns+`
		FROM user_model_keys
		WHERE user_id = $1::uuid AND model_name = $2 AND is_active
		LIMIT 1
	`, userID, modelName).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	return k, err
}

// GetDefaultForUser returns the user's default active key.
func (r *UserModelKeyRepo) GetDefaultForUser(ctx context.Context, userID string) (*UserModelKey, error) {
	if r.db == nil || userID == "" {
		return nil, sql.ErrNoRows
	}
	if _, err := uuid.Parse(userID); err != nil {
		return nil, sql.ErrNoRows
	}
	k, err := scanUserModelKey(r.db.QueryRowContext(ctx, `
		SELECT `+userModelKeyColumns+`
		FROM user_model_keys
		WHERE user_id = $1::uuid AND is_default AND is_active
		LIMIT 1
	`, userID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	return k, err
}
