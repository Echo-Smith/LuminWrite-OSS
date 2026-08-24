package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/crypto"
)

// ─── Model Configs ───────────────────────────────────────

// ModelConfig represents a model configuration row.
// APIKeyEncrypted is stored encrypted in DB, never returned in JSON.
// Use HasAPIKey (computed) to indicate whether a key is configured.
type ModelConfig struct {
	ID              string                 `json:"id"`
	Provider        string                 `json:"provider"`
	ModelName       string                 `json:"model_name"`
	DisplayName     string                 `json:"display_name"`
	BaseURL         string                 `json:"base_url"`
	APIKeyID        *string                `json:"api_key_id,omitempty"` // legacy, deprecated
	APIKeyEncrypted string                 `json:"-"`                     // encrypted in DB, never serialized
	APIKeyPlain     string                 `json:"api_key,omitempty"`     // write-only: set by admin, encrypted before storage
	HasAPIKey       bool                   `json:"has_api_key"`           // read-only: true if api_key_encrypted is non-empty
	MaxTokens       int                    `json:"max_tokens"`
	Temperature     float64                `json:"temperature"`
	ReasoningEffort string                 `json:"reasoning_effort"`       // low | medium | high | max (thinking mode only)
	IsDefault       bool                   `json:"is_default"`
	IsActive        bool                   `json:"is_active"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	Metadata        map[string]interface{} `json:"metadata"`
	CustomHeaders   map[string]string      `json:"custom_headers"`        // custom HTTP headers for API requests
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

// ListModelConfigs returns all model configs.
func (r *AdminRepo) ListModelConfigs(ctx context.Context) ([]*ModelConfig, error) {
	if r.db == nil {
		return []*ModelConfig{}, nil
	}

rows, err := r.db.QueryContext(ctx, `
	SELECT id::text, provider, model_name, display_name, base_url,
	       api_key_id::text, api_key_encrypted, max_tokens, temperature, reasoning_effort, is_default, is_active,
	       capabilities, metadata, custom_headers, created_at, updated_at
	FROM model_configs
	ORDER BY provider, is_default DESC, model_name
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var configs []*ModelConfig
	for rows.Next() {
		var c ModelConfig
		var capJSON, metaJSON, hdrJSON []byte
		if err := rows.Scan(&c.ID, &c.Provider, &c.ModelName, &c.DisplayName, &c.BaseURL,
			&c.APIKeyID, &c.APIKeyEncrypted, &c.MaxTokens, &c.Temperature, &c.ReasoningEffort, &c.IsDefault, &c.IsActive,
			&capJSON, &metaJSON, &hdrJSON, &c.CreatedAt, &c.UpdatedAt); err != nil {
			continue
		}
		c.HasAPIKey = c.APIKeyEncrypted != ""
		c.APIKeyEncrypted = "" // never expose encrypted value
		if len(capJSON) > 0 {
			json.Unmarshal(capJSON, &c.Capabilities)
		}
		if len(metaJSON) > 0 {
			json.Unmarshal(metaJSON, &c.Metadata)
		}
		if len(hdrJSON) > 0 {
			json.Unmarshal(hdrJSON, &c.CustomHeaders)
		}
		configs = append(configs, &c)
	}
	return configs, nil
}

// GetModelConfig retrieves a single model config by ID.
func (r *AdminRepo) GetModelConfig(ctx context.Context, id string) (*ModelConfig, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var c ModelConfig
	var capJSON, metaJSON, hdrJSON []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, provider, model_name, display_name, base_url,
		       api_key_id::text, api_key_encrypted, max_tokens, temperature, reasoning_effort, is_default, is_active,
		       capabilities, metadata, custom_headers, created_at, updated_at
		FROM model_configs WHERE id = $1
	`, id).Scan(&c.ID, &c.Provider, &c.ModelName, &c.DisplayName, &c.BaseURL,
		&c.APIKeyID, &c.APIKeyEncrypted, &c.MaxTokens, &c.Temperature, &c.ReasoningEffort, &c.IsDefault, &c.IsActive,
		&capJSON, &metaJSON, &hdrJSON, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.HasAPIKey = c.APIKeyEncrypted != ""
	c.APIKeyEncrypted = "" // never expose
	if len(capJSON) > 0 {
		json.Unmarshal(capJSON, &c.Capabilities)
	}
	if len(metaJSON) > 0 {
		json.Unmarshal(metaJSON, &c.Metadata)
	}
	if len(hdrJSON) > 0 {
		json.Unmarshal(hdrJSON, &c.CustomHeaders)
	}
	return &c, nil
}

// CreateModelConfig inserts a new model config.
// If c.APIKeyPlain is non-empty, it is encrypted and stored in api_key_encrypted.
func (r *AdminRepo) CreateModelConfig(ctx context.Context, c *ModelConfig) (*ModelConfig, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	capJSON, _ := json.Marshal(c.Capabilities)
	if c.Capabilities == nil {
		capJSON = []byte("{}")
	}
	metaJSON, _ := json.Marshal(c.Metadata)
	if c.Metadata == nil {
		metaJSON = []byte("{}")
	}
	hdrJSON, _ := json.Marshal(c.CustomHeaders)
	if c.CustomHeaders == nil {
		hdrJSON = []byte("{}")
	}

	// Encrypt API key if provided
	storedAPIKey := ""
	if c.APIKeyPlain != "" {
		if len(r.encKey) > 0 {
			encrypted, err := crypto.Encrypt(c.APIKeyPlain, r.encKey)
			if err != nil {
				return nil, fmt.Errorf("failed to encrypt API key: %w", err)
			}
			storedAPIKey = encrypted
		} else {
			storedAPIKey = c.APIKeyPlain // no encryption key configured
		}
	}

	// If this is default, unset other defaults for same provider
	if c.IsDefault {
		r.db.ExecContext(ctx, `UPDATE model_configs SET is_default = FALSE WHERE provider = $1`, c.Provider)
	}

	var result ModelConfig
	var rCapJSON, rMetaJSON, rHdrJSON []byte
	var apiKeyID interface{}
	if c.APIKeyID != nil && *c.APIKeyID != "" {
		apiKeyID = *c.APIKeyID
	}

	err := r.db.QueryRowContext(ctx, `
		INSERT INTO model_configs (provider, model_name, display_name, base_url, api_key_id, api_key_encrypted,
			max_tokens, temperature, reasoning_effort, is_default, is_active, capabilities, metadata, custom_headers)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id::text, provider, model_name, display_name, base_url,
		          api_key_id::text, api_key_encrypted, max_tokens, temperature, reasoning_effort, is_default, is_active,
		          capabilities, metadata, custom_headers, created_at, updated_at
	`, c.Provider, c.ModelName, c.DisplayName, c.BaseURL, apiKeyID, storedAPIKey,
		c.MaxTokens, c.Temperature, c.ReasoningEffort, c.IsDefault, c.IsActive, string(capJSON), string(metaJSON), string(hdrJSON)).Scan(
		&result.ID, &result.Provider, &result.ModelName, &result.DisplayName, &result.BaseURL,
		&result.APIKeyID, &result.APIKeyEncrypted, &result.MaxTokens, &result.Temperature, &result.ReasoningEffort, &result.IsDefault, &result.IsActive,
		&rCapJSON, &rMetaJSON, &rHdrJSON, &result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return nil, err
	}
	result.HasAPIKey = result.APIKeyEncrypted != ""
	result.APIKeyEncrypted = ""
	result.APIKeyPlain = ""
	if len(rCapJSON) > 0 {
		json.Unmarshal(rCapJSON, &result.Capabilities)
	}
	if len(rMetaJSON) > 0 {
		json.Unmarshal(rMetaJSON, &result.Metadata)
	}
	if len(rHdrJSON) > 0 {
		json.Unmarshal(rHdrJSON, &result.CustomHeaders)
	}
	return &result, nil
}

// UpdateModelConfig updates a model config.
// If c.APIKeyPlain is non-empty, it is encrypted and stored. If empty, existing key is preserved.
func (r *AdminRepo) UpdateModelConfig(ctx context.Context, id string, c *ModelConfig) (*ModelConfig, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	capJSON, _ := json.Marshal(c.Capabilities)
	if c.Capabilities == nil {
		capJSON = []byte("{}")
	}
	metaJSON, _ := json.Marshal(c.Metadata)
	if c.Metadata == nil {
		metaJSON = []byte("{}")
	}
	hdrJSON, _ := json.Marshal(c.CustomHeaders)
	if c.CustomHeaders == nil {
		hdrJSON = []byte("{}")
	}

	if c.IsDefault {
		r.db.ExecContext(ctx, `UPDATE model_configs SET is_default = FALSE WHERE provider = $1 AND id != $2`, c.Provider, id)
	}

	var result ModelConfig
	var rCapJSON, rMetaJSON, rHdrJSON []byte
	var apiKeyID interface{}
	if c.APIKeyID != nil && *c.APIKeyID != "" {
		apiKeyID = *c.APIKeyID
	}

	var query string
	var args []interface{}

	if c.APIKeyPlain != "" {
		// Encrypt new key
		storedAPIKey := c.APIKeyPlain
		if len(r.encKey) > 0 {
			encrypted, err := crypto.Encrypt(c.APIKeyPlain, r.encKey)
			if err != nil {
				return nil, fmt.Errorf("failed to encrypt API key: %w", err)
			}
			storedAPIKey = encrypted
		}
		query = `
			UPDATE model_configs SET
				provider = $2, model_name = $3, display_name = $4, base_url = $5,
				api_key_id = $6, api_key_encrypted = $7, max_tokens = $8, temperature = $9,
				reasoning_effort = $10, is_default = $11, is_active = $12, capabilities = $13, metadata = $14, custom_headers = $15, updated_at = NOW()
			WHERE id = $1
			RETURNING id::text, provider, model_name, display_name, base_url,
			          api_key_id::text, api_key_encrypted, max_tokens, temperature, reasoning_effort, is_default, is_active,
			          capabilities, metadata, custom_headers, created_at, updated_at
		`
		args = []interface{}{id, c.Provider, c.ModelName, c.DisplayName, c.BaseURL,
			apiKeyID, storedAPIKey, c.MaxTokens, c.Temperature, c.ReasoningEffort,
			c.IsDefault, c.IsActive, string(capJSON), string(metaJSON), string(hdrJSON)}
	} else {
		// Keep existing api_key_encrypted
		query = `
			UPDATE model_configs SET
				provider = $2, model_name = $3, display_name = $4, base_url = $5,
				api_key_id = $6, max_tokens = $7, temperature = $8,
				reasoning_effort = $9, is_default = $10, is_active = $11, capabilities = $12, metadata = $13, custom_headers = $14, updated_at = NOW()
			WHERE id = $1
			RETURNING id::text, provider, model_name, display_name, base_url,
			          api_key_id::text, api_key_encrypted, max_tokens, temperature, reasoning_effort, is_default, is_active,
			          capabilities, metadata, custom_headers, created_at, updated_at
		`
		args = []interface{}{id, c.Provider, c.ModelName, c.DisplayName, c.BaseURL,
			apiKeyID, c.MaxTokens, c.Temperature, c.ReasoningEffort,
			c.IsDefault, c.IsActive, string(capJSON), string(metaJSON), string(hdrJSON)}
	}

	err := r.db.QueryRowContext(ctx, query, args...).Scan(
		&result.ID, &result.Provider, &result.ModelName, &result.DisplayName, &result.BaseURL,
		&result.APIKeyID, &result.APIKeyEncrypted, &result.MaxTokens, &result.Temperature, &result.ReasoningEffort, &result.IsDefault, &result.IsActive,
		&rCapJSON, &rMetaJSON, &rHdrJSON, &result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return nil, err
	}
	result.HasAPIKey = result.APIKeyEncrypted != ""
	result.APIKeyEncrypted = ""
	result.APIKeyPlain = ""
	if len(rCapJSON) > 0 {
		json.Unmarshal(rCapJSON, &result.Capabilities)
	}
	if len(rMetaJSON) > 0 {
		json.Unmarshal(rMetaJSON, &result.Metadata)
	}
	if len(rHdrJSON) > 0 {
		json.Unmarshal(rHdrJSON, &result.CustomHeaders)
	}
	return &result, nil
}

// DeleteModelConfig deletes a model config.
func (r *AdminRepo) DeleteModelConfig(ctx context.Context, id string) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM model_configs WHERE id = $1`, id)
	return err
}

// GetDefaultModelConfig returns the default active model config.
func (r *AdminRepo) GetDefaultModelConfig(ctx context.Context) (*ModelConfig, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var c ModelConfig
	var capJSON, metaJSON, hdrJSON []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, provider, model_name, display_name, base_url,
		       api_key_id::text, api_key_encrypted, max_tokens, temperature, reasoning_effort, is_default, is_active,
		       capabilities, metadata, custom_headers, created_at, updated_at
		FROM model_configs
		WHERE is_default = TRUE AND is_active = TRUE
		ORDER BY updated_at DESC
		LIMIT 1
	`).Scan(&c.ID, &c.Provider, &c.ModelName, &c.DisplayName, &c.BaseURL,
		&c.APIKeyID, &c.APIKeyEncrypted, &c.MaxTokens, &c.Temperature, &c.ReasoningEffort, &c.IsDefault, &c.IsActive,
		&capJSON, &metaJSON, &hdrJSON, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.HasAPIKey = c.APIKeyEncrypted != ""
	if len(capJSON) > 0 {
		json.Unmarshal(capJSON, &c.Capabilities)
	}
	if len(metaJSON) > 0 {
		json.Unmarshal(metaJSON, &c.Metadata)
	}
	if len(hdrJSON) > 0 {
		json.Unmarshal(hdrJSON, &c.CustomHeaders)
	}
	return &c, nil
}

// GetModelConfigByName retrieves a model config by model_name.
func (r *AdminRepo) GetModelConfigByName(ctx context.Context, modelName string) (*ModelConfig, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var c ModelConfig
	var capJSON, metaJSON, hdrJSON []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, provider, model_name, display_name, base_url,
		       api_key_id::text, api_key_encrypted, max_tokens, temperature, reasoning_effort, is_default, is_active,
		       capabilities, metadata, custom_headers, created_at, updated_at
		FROM model_configs
		WHERE model_name = $1 AND is_active = TRUE
		LIMIT 1
	`, modelName).Scan(&c.ID, &c.Provider, &c.ModelName, &c.DisplayName, &c.BaseURL,
		&c.APIKeyID, &c.APIKeyEncrypted, &c.MaxTokens, &c.Temperature, &c.ReasoningEffort, &c.IsDefault, &c.IsActive,
		&capJSON, &metaJSON, &hdrJSON, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.HasAPIKey = c.APIKeyEncrypted != ""
	if len(capJSON) > 0 {
		json.Unmarshal(capJSON, &c.Capabilities)
	}
	if len(metaJSON) > 0 {
		json.Unmarshal(metaJSON, &c.Metadata)
	}
	if len(hdrJSON) > 0 {
		json.Unmarshal(hdrJSON, &c.CustomHeaders)
	}
	return &c, nil
}

// DecryptModelAPIKey returns the decrypted API key for a model config.
// If encryption is not configured, returns the raw value.
func (r *AdminRepo) DecryptModelAPIKey(encryptedKey string) string {
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

// GetAPIKeyByID retrieves an API key by ID with the actual (decrypted) key value.
func (r *AdminRepo) GetAPIKeyByID(ctx context.Context, id string) (*APIKey, string, error) {
	if r.db == nil {
		return nil, "", fmt.Errorf("database not available")
	}

	var k APIKey
	var metaJSON []byte
	var keyValue string
	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, name, provider, key_value, base_url, is_active,
		       last_used_at, last_check, last_status, last_error,
		       metadata, created_at, updated_at
		FROM api_keys WHERE id = $1
	`, id).Scan(&k.ID, &k.Name, &k.Provider, &keyValue, &k.BaseURL, &k.IsActive,
		&k.LastUsedAt, &k.LastCheck, &k.LastStatus, &k.LastError,
		&metaJSON, &k.CreatedAt, &k.UpdatedAt)
	if err != nil {
		return nil, "", err
	}
	if len(metaJSON) > 0 {
		json.Unmarshal(metaJSON, &k.Metadata)
	}

	// Decrypt if encryption key is set
	if len(r.encKey) > 0 && keyValue != "" {
		decrypted, err := crypto.Decrypt(keyValue, r.encKey)
		if err == nil {
			keyValue = decrypted
		}
	}

	k.KeyValue = maskKey(keyValue)
	return &k, keyValue, nil
}

// ─── API Keys ────────────────────────────────────────────

// APIKey represents an API key row (key_value is masked in JSON).
type APIKey struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Provider    string                 `json:"provider"`
	Category    string                 `json:"category"` // "llm" or "mcp"
	KeyValue    string                 `json:"key_value,omitempty"` // masked in list responses
	BaseURL     string                 `json:"base_url"`
	IsActive    bool                   `json:"is_active"`
	LastUsedAt  *time.Time             `json:"last_used_at,omitempty"`
	LastCheck   *time.Time             `json:"last_check,omitempty"`
	LastStatus  string                 `json:"last_status"`
	LastError   *string                `json:"last_error,omitempty"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// ListAPIKeys returns all API keys (with masked key values).
// If category is non-empty, filters by category (e.g. "mcp", "llm").
func (r *AdminRepo) ListAPIKeys(ctx context.Context, category string) ([]*APIKey, error) {
	if r.db == nil {
		return []*APIKey{}, nil
	}

	var rows *sql.Rows
	var err error
	if category != "" {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id::text, name, provider, category, base_url, is_active,
			       last_used_at, last_check, last_status, last_error,
			       metadata, created_at, updated_at
			FROM api_keys
			WHERE category = $1
			ORDER BY provider, created_at DESC
		`, category)
	} else {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id::text, name, provider, category, base_url, is_active,
			       last_used_at, last_check, last_status, last_error,
			       metadata, created_at, updated_at
			FROM api_keys
			ORDER BY provider, created_at DESC
		`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*APIKey
	for rows.Next() {
		var k APIKey
		var metaJSON []byte
		if err := rows.Scan(&k.ID, &k.Name, &k.Provider, &k.Category, &k.BaseURL, &k.IsActive,
			&k.LastUsedAt, &k.LastCheck, &k.LastStatus, &k.LastError,
			&metaJSON, &k.CreatedAt, &k.UpdatedAt); err != nil {
			continue
		}
		if len(metaJSON) > 0 {
			json.Unmarshal(metaJSON, &k.Metadata)
		}
		k.KeyValue = maskKey("")
		keys = append(keys, &k)
	}
	return keys, nil
}

// CreateAPIKey inserts a new API key.
// Category defaults to "mcp" if empty.
func (r *AdminRepo) CreateAPIKey(ctx context.Context, k *APIKey) (*APIKey, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	if k.Category == "" {
		k.Category = "mcp"
	}

	metaJSON, _ := json.Marshal(k.Metadata)
	if k.Metadata == nil {
		metaJSON = []byte("{}")
	}

	// Encrypt key value if encryption key is set
	storedKey := k.KeyValue
	if len(r.encKey) > 0 && k.KeyValue != "" {
		encrypted, err := crypto.Encrypt(k.KeyValue, r.encKey)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt API key: %w", err)
		}
		storedKey = encrypted
	}

	// Compute hash for lookup
	keyHash := hashAPIKey(k.KeyValue)

	var result APIKey
	var rMetaJSON []byte
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO api_keys (name, provider, key_value, key_hash, base_url, is_active, metadata, category)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id::text, name, provider, category, base_url, is_active,
		          last_used_at, last_check, last_status, last_error,
		          metadata, created_at, updated_at
	`, k.Name, k.Provider, storedKey, keyHash, k.BaseURL, k.IsActive, string(metaJSON), k.Category).Scan(
		&result.ID, &result.Name, &result.Provider, &result.Category, &result.BaseURL, &result.IsActive,
		&result.LastUsedAt, &result.LastCheck, &result.LastStatus, &result.LastError,
		&rMetaJSON, &result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if len(rMetaJSON) > 0 {
		json.Unmarshal(rMetaJSON, &result.Metadata)
	}
	result.KeyValue = maskKey(k.KeyValue)
	return &result, nil
}

// UpdateAPIKey updates an API key. If key_value is empty, keeps the existing value.
func (r *AdminRepo) UpdateAPIKey(ctx context.Context, id string, k *APIKey) (*APIKey, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	if k.Category == "" {
		k.Category = "mcp"
	}

	metaJSON, _ := json.Marshal(k.Metadata)
	if k.Metadata == nil {
		metaJSON = []byte("{}")
	}

	var query string
	var args []interface{}

	if k.KeyValue != "" {
		query = `
			UPDATE api_keys SET name = $2, provider = $3, key_value = $4, base_url = $5,
				is_active = $6, metadata = $7, category = $8, updated_at = NOW()
			WHERE id = $1
			RETURNING id::text, name, provider, category, base_url, is_active,
			          last_used_at, last_check, last_status, last_error,
			          metadata, created_at, updated_at
		`
		args = []interface{}{id, k.Name, k.Provider, k.KeyValue, k.BaseURL, k.IsActive, string(metaJSON), k.Category}
	} else {
		query = `
			UPDATE api_keys SET name = $2, provider = $3, base_url = $4,
				is_active = $5, metadata = $6, category = $7, updated_at = NOW()
			WHERE id = $1
			RETURNING id::text, name, provider, category, base_url, is_active,
			          last_used_at, last_check, last_status, last_error,
			          metadata, created_at, updated_at
		`
		args = []interface{}{id, k.Name, k.Provider, k.BaseURL, k.IsActive, string(metaJSON), k.Category}
	}

	var result APIKey
	var rMetaJSON []byte
	err := r.db.QueryRowContext(ctx, query, args...).Scan(
		&result.ID, &result.Name, &result.Provider, &result.Category, &result.BaseURL, &result.IsActive,
		&result.LastUsedAt, &result.LastCheck, &result.LastStatus, &result.LastError,
		&rMetaJSON, &result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if len(rMetaJSON) > 0 {
		json.Unmarshal(rMetaJSON, &result.Metadata)
	}
	result.KeyValue = maskKey("")
	return &result, nil
}

// DeleteAPIKey deletes an API key.
func (r *AdminRepo) DeleteAPIKey(ctx context.Context, id string) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = $1`, id)
	return err
}

// GetAPIKeyValue retrieves the actual key value for a given provider (internal use).
func (r *AdminRepo) GetAPIKeyValue(ctx context.Context, provider string) (string, string, error) {
	if r.db == nil {
		return "", "", fmt.Errorf("database not available")
	}

	var keyValue, baseURL string
	err := r.db.QueryRowContext(ctx, `
		SELECT key_value, base_url FROM api_keys
		WHERE provider = $1 AND is_active = TRUE
		ORDER BY created_at DESC LIMIT 1
	`, provider).Scan(&keyValue, &baseURL)
	if err != nil {
		return "", "", err
	}

	// Decrypt if encryption key is set
	if len(r.encKey) > 0 && keyValue != "" {
		if decrypted, err := crypto.Decrypt(keyValue, r.encKey); err == nil {
			keyValue = decrypted
		}
	}

	// Update last_used_at
	r.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = NOW() WHERE provider = $1 AND is_active = TRUE`, provider)

	return keyValue, baseURL, nil
}

// UpdateAPIKeyStatus updates the health check status of an API key.
func (r *AdminRepo) UpdateAPIKeyStatus(ctx context.Context, id, status, errMsg string) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE api_keys SET last_check = NOW(), last_status = $2, last_error = $3, updated_at = NOW()
		WHERE id = $1
	`, id, status, errMsg)
	return err
}

// ValidateAPIKey looks up an API key by its hash and returns the key ID and provider if valid.
func (r *AdminRepo) ValidateAPIKey(ctx context.Context, keyValue string) (id, provider string, ok bool, err error) {
	if r.db == nil {
		return "", "", false, fmt.Errorf("database not available")
	}

	keyHash := hashAPIKey(keyValue)
	err = r.db.QueryRowContext(ctx, `
		SELECT id::text, provider FROM api_keys
		WHERE key_hash = $1 AND is_active = TRUE
		LIMIT 1
	`, keyHash).Scan(&id, &provider)
	if err != nil {
		// Fallback: try direct key_value match (backward compatibility with unencrypted keys)
		err = r.db.QueryRowContext(ctx, `
			SELECT id::text, provider FROM api_keys
			WHERE key_value = $1 AND is_active = TRUE
			LIMIT 1
		`, keyValue).Scan(&id, &provider)
		if err != nil {
			return "", "", false, nil // not found = not an error for caller
		}
	}

	// Update last_used_at
	r.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = NOW() WHERE id = $1`, id)

	return id, provider, true, nil
}

// maskKey returns a masked version of the key for display.
func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// hashAPIKey returns the SHA256 hex-encoded hash of an API key for lookup.
func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// ─── Token Usage ─────────────────────────────────────────

// TokenUsageRecord represents a token usage row.
type TokenUsageRecord struct {
	ID               string    `json:"id"`
	TraceID          string    `json:"trace_id,omitempty"`
	UserID           string    `json:"user_id,omitempty"`
	ModelName        string    `json:"model_name"`
	Provider         string    `json:"provider"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	EstimatedCost    float64   `json:"estimated_cost"`
	APIKeyID         *string   `json:"api_key_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// TokenUsageStats holds aggregated usage statistics.
type TokenUsageStats struct {
	TotalTokens      int64                `json:"total_tokens"`
	TotalPrompt      int64                `json:"total_prompt_tokens"`
	TotalCompletion  int64                `json:"total_completion_tokens"`
	TotalCost        float64              `json:"total_estimated_cost"`
	TodayTokens      int64                `json:"today_tokens"`
	ByModel          []ModelUsageBreakdown `json:"by_model"`
	ByProvider       []ProviderUsageBreakdown `json:"by_provider"`
	DailyTokens      []DailyCount         `json:"daily_tokens"`
}

// ModelUsageBreakdown is usage stats per model.
type ModelUsageBreakdown struct {
	ModelName    string  `json:"model_name"`
	Provider     string  `json:"provider"`
	TotalTokens  int64   `json:"total_tokens"`
	CallCount    int64   `json:"call_count"`
	EstimatedCost float64 `json:"estimated_cost"`
}

// ProviderUsageBreakdown is usage stats per provider.
type ProviderUsageBreakdown struct {
	Provider     string  `json:"provider"`
	TotalTokens  int64   `json:"total_tokens"`
	CallCount    int64   `json:"call_count"`
	EstimatedCost float64 `json:"estimated_cost"`
}

// RecordTokenUsage inserts a token usage record.
func (r *AdminRepo) RecordTokenUsage(ctx context.Context, rec *TokenUsageRecord) error {
	if r.db == nil {
		return nil
	}

	apiKeyID := uuid.Nil.String()
	if rec.APIKeyID != nil && *rec.APIKeyID != "" {
		apiKeyID = *rec.APIKeyID
	}

	traceID := rec.TraceID
	if traceID == "" {
		traceID = "unknown"
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO token_usage (trace_id, user_id, model_name, provider,
			prompt_tokens, completion_tokens, total_tokens, estimated_cost, api_key_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`,
		traceID, rec.UserID, rec.ModelName, rec.Provider,
		rec.PromptTokens, rec.CompletionTokens, rec.TotalTokens, rec.EstimatedCost, apiKeyID)
	return err
}

// GetTokenUsageStats retrieves aggregated token usage stats.
func (r *AdminRepo) GetTokenUsageStats(ctx context.Context, days int) (*TokenUsageStats, error) {
	if r.db == nil {
		return &TokenUsageStats{}, nil
	}

	if days <= 0 {
		days = 30
	}

	stats := &TokenUsageStats{}

	// Overall totals
	r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(total_tokens), 0), COALESCE(SUM(prompt_tokens), 0),
		       COALESCE(SUM(completion_tokens), 0), COALESCE(SUM(estimated_cost), 0)
		FROM token_usage
		WHERE created_at >= NOW() - INTERVAL '%d days'
	`, days).Scan(&stats.TotalTokens, &stats.TotalPrompt, &stats.TotalCompletion, &stats.TotalCost)

	// Today's tokens
	r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(total_tokens), 0) FROM token_usage WHERE created_at >= CURRENT_DATE
	`).Scan(&stats.TodayTokens)

	// By model
	rows, err := r.db.QueryContext(ctx, `
		SELECT model_name, provider, COALESCE(SUM(total_tokens), 0), COUNT(*), COALESCE(SUM(estimated_cost), 0)
		FROM token_usage
		WHERE created_at >= NOW() - INTERVAL '%d days'
		GROUP BY model_name, provider
		ORDER BY SUM(total_tokens) DESC
	`, days)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var m ModelUsageBreakdown
			if err := rows.Scan(&m.ModelName, &m.Provider, &m.TotalTokens, &m.CallCount, &m.EstimatedCost); err != nil {
				continue
			}
			stats.ByModel = append(stats.ByModel, m)
		}
	}

	// By provider
	provRows, err := r.db.QueryContext(ctx, `
		SELECT provider, COALESCE(SUM(total_tokens), 0), COUNT(*), COALESCE(SUM(estimated_cost), 0)
		FROM token_usage
		WHERE created_at >= NOW() - INTERVAL '%d days'
		GROUP BY provider
		ORDER BY SUM(total_tokens) DESC
	`, days)
	if err == nil {
		defer provRows.Close()
		for provRows.Next() {
			var p ProviderUsageBreakdown
			if err := provRows.Scan(&p.Provider, &p.TotalTokens, &p.CallCount, &p.EstimatedCost); err != nil {
				continue
			}
			stats.ByProvider = append(stats.ByProvider, p)
		}
	}

	// Daily tokens (last N days)
	dailyRows, err := r.db.QueryContext(ctx, `
		SELECT DATE(created_at) as d, COALESCE(SUM(total_tokens), 0) as cnt
		FROM token_usage
		WHERE created_at >= CURRENT_DATE - INTERVAL '%d days'
		GROUP BY d ORDER BY d
	`, days-1)
	if err == nil {
		defer dailyRows.Close()
		for dailyRows.Next() {
			var dc DailyCount
			var d time.Time
			if err := dailyRows.Scan(&d, &dc.Count); err != nil {
				continue
			}
			dc.Date = d.Format("2006-01-02")
			stats.DailyTokens = append(stats.DailyTokens, dc)
		}
	}

	return stats, nil
}

// ─── Cron Jobs ───────────────────────────────────────────

// CronJob represents a cron job row.
type CronJob struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Schedule    string                 `json:"schedule"`
	TaskType    string                 `json:"task_type"`
	TaskConfig  map[string]interface{} `json:"task_config"`
	IsActive    bool                   `json:"is_active"`
	LastRunAt   *time.Time             `json:"last_run_at,omitempty"`
	NextRunAt   *time.Time             `json:"next_run_at,omitempty"`
	LastStatus  string                 `json:"last_status"`
	LastError   *string                `json:"last_error,omitempty"`
	RunCount    int                    `json:"run_count"`
	FailCount   int                    `json:"fail_count"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// ListCronJobs returns all cron jobs.
func (r *AdminRepo) ListCronJobs(ctx context.Context) ([]*CronJob, error) {
	if r.db == nil {
		return []*CronJob{}, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text, name, description, schedule, task_type, task_config,
		       is_active, last_run_at, next_run_at, last_status, last_error,
		       run_count, fail_count, created_at, updated_at
		FROM cron_jobs
		ORDER BY is_active DESC, created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*CronJob
	for rows.Next() {
		var j CronJob
		var cfgJSON []byte
		if err := rows.Scan(&j.ID, &j.Name, &j.Description, &j.Schedule, &j.TaskType, &cfgJSON,
			&j.IsActive, &j.LastRunAt, &j.NextRunAt, &j.LastStatus, &j.LastError,
			&j.RunCount, &j.FailCount, &j.CreatedAt, &j.UpdatedAt); err != nil {
			continue
		}
		if len(cfgJSON) > 0 {
			json.Unmarshal(cfgJSON, &j.TaskConfig)
		}
		jobs = append(jobs, &j)
	}
	return jobs, nil
}

// CreateCronJob inserts a new cron job.
func (r *AdminRepo) CreateCronJob(ctx context.Context, j *CronJob) (*CronJob, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	cfgJSON, _ := json.Marshal(j.TaskConfig)
	if j.TaskConfig == nil {
		cfgJSON = []byte("{}")
	}

	var result CronJob
	var rCfgJSON []byte
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO cron_jobs (name, description, schedule, task_type, task_config, is_active)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text, name, description, schedule, task_type, task_config,
		          is_active, last_run_at, next_run_at, last_status, last_error,
		          run_count, fail_count, created_at, updated_at
	`, j.Name, j.Description, j.Schedule, j.TaskType, string(cfgJSON), j.IsActive).Scan(
		&result.ID, &result.Name, &result.Description, &result.Schedule, &result.TaskType, &rCfgJSON,
		&result.IsActive, &result.LastRunAt, &result.NextRunAt, &result.LastStatus, &result.LastError,
		&result.RunCount, &result.FailCount, &result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if len(rCfgJSON) > 0 {
		json.Unmarshal(rCfgJSON, &result.TaskConfig)
	}
	return &result, nil
}

// UpdateCronJob updates a cron job.
func (r *AdminRepo) UpdateCronJob(ctx context.Context, id string, j *CronJob) (*CronJob, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	cfgJSON, _ := json.Marshal(j.TaskConfig)
	if j.TaskConfig == nil {
		cfgJSON = []byte("{}")
	}

	var result CronJob
	var rCfgJSON []byte
	err := r.db.QueryRowContext(ctx, `
		UPDATE cron_jobs SET
			name = $2, description = $3, schedule = $4, task_type = $5,
			task_config = $6, is_active = $7, updated_at = NOW()
		WHERE id = $1
		RETURNING id::text, name, description, schedule, task_type, task_config,
		          is_active, last_run_at, next_run_at, last_status, last_error,
		          run_count, fail_count, created_at, updated_at
	`, id, j.Name, j.Description, j.Schedule, j.TaskType, string(cfgJSON), j.IsActive).Scan(
		&result.ID, &result.Name, &result.Description, &result.Schedule, &result.TaskType, &rCfgJSON,
		&result.IsActive, &result.LastRunAt, &result.NextRunAt, &result.LastStatus, &result.LastError,
		&result.RunCount, &result.FailCount, &result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if len(rCfgJSON) > 0 {
		json.Unmarshal(rCfgJSON, &result.TaskConfig)
	}
	return &result, nil
}

// DeleteCronJob deletes a cron job.
func (r *AdminRepo) DeleteCronJob(ctx context.Context, id string) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM cron_jobs WHERE id = $1`, id)
	return err
}

// UpdateCronJobStatus updates the run status after execution.
func (r *AdminRepo) UpdateCronJobStatus(ctx context.Context, id, status, errMsg string) error {
	if r.db == nil {
		return nil
	}

	if status == "success" {
		_, err := r.db.ExecContext(ctx, `
			UPDATE cron_jobs SET last_run_at = NOW(), last_status = $2, last_error = '',
				run_count = run_count + 1, updated_at = NOW()
			WHERE id = $1
		`, id, status)
		return err
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE cron_jobs SET last_run_at = NOW(), last_status = $2, last_error = $3,
			run_count = run_count + 1, fail_count = fail_count + 1, updated_at = NOW()
		WHERE id = $1
	`, id, status, errMsg)
	return err
}

// UpdateCronJobNextRun sets the next_run_at timestamp for a cron job.
func (r *AdminRepo) UpdateCronJobNextRun(ctx context.Context, id string, nextRun time.Time) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE cron_jobs SET next_run_at = $2, updated_at = NOW()
		WHERE id = $1
	`, id, nextRun)
	return err
}

// GetPendingCronJobs returns jobs that are due for execution.
func (r *AdminRepo) GetPendingCronJobs(ctx context.Context) ([]*CronJob, error) {
	if r.db == nil {
		return []*CronJob{}, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text, name, description, schedule, task_type, task_config,
		       is_active, last_run_at, next_run_at, last_status, last_error,
		       run_count, fail_count, created_at, updated_at
		FROM cron_jobs
		WHERE is_active = TRUE
		  AND (next_run_at IS NULL OR next_run_at <= NOW())
		  AND (last_run_at IS NULL OR last_run_at < NOW() - INTERVAL '1 minute')
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*CronJob
	for rows.Next() {
		var j CronJob
		var cfgJSON []byte
		if err := rows.Scan(&j.ID, &j.Name, &j.Description, &j.Schedule, &j.TaskType, &cfgJSON,
			&j.IsActive, &j.LastRunAt, &j.NextRunAt, &j.LastStatus, &j.LastError,
			&j.RunCount, &j.FailCount, &j.CreatedAt, &j.UpdatedAt); err != nil {
			continue
		}
		if len(cfgJSON) > 0 {
			json.Unmarshal(cfgJSON, &j.TaskConfig)
		}
		jobs = append(jobs, &j)
	}
	return jobs, nil
}

// ─── MCP Servers ─────────────────────────────────────────

// MCPServerConfig represents a row in the mcp_servers table.
type MCPServerConfig struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Transport       string     `json:"transport"`
	Command         string     `json:"command,omitempty"`
	Args            []string   `json:"args,omitempty"`
	Env             []string   `json:"env,omitempty"`
	URL             string     `json:"url,omitempty"`
	IsActive        bool       `json:"is_active"`
	Description     string     `json:"description"`
	LastStatus      string     `json:"last_status"`
	LastError       *string    `json:"last_error,omitempty"`
	LastConnectedAt *time.Time `json:"last_connected_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// ListMCPServers returns all MCP server configurations.
func (r *AdminRepo) ListMCPServers(ctx context.Context) ([]*MCPServerConfig, error) {
	if r == nil || r.db == nil {
		return []*MCPServerConfig{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text, name, transport, command, args, env, url,
		       is_active, description, last_status, last_error, last_connected_at,
		       created_at, updated_at
		FROM mcp_servers ORDER BY is_active DESC, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var servers []*MCPServerConfig
	for rows.Next() {
		var s MCPServerConfig
		var argsJSON, envJSON []byte
		if err := rows.Scan(&s.ID, &s.Name, &s.Transport, &s.Command, &argsJSON, &envJSON, &s.URL,
			&s.IsActive, &s.Description, &s.LastStatus, &s.LastError, &s.LastConnectedAt,
			&s.CreatedAt, &s.UpdatedAt); err != nil {
			continue
		}
		if len(argsJSON) > 0 { json.Unmarshal(argsJSON, &s.Args) }
		if len(envJSON) > 0  { json.Unmarshal(envJSON, &s.Env) }
		servers = append(servers, &s)
	}
	return servers, nil
}

// CreateMCPServer inserts a new MCP server configuration.
func (r *AdminRepo) CreateMCPServer(ctx context.Context, s *MCPServerConfig) (*MCPServerConfig, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	argsJSON, _ := json.Marshal(s.Args)
	if s.Args == nil { argsJSON = []byte("[]") }
	envJSON, _ := json.Marshal(s.Env)
	if s.Env == nil { envJSON = []byte("[]") }

	var result MCPServerConfig
	var rArgsJSON, rEnvJSON []byte
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO mcp_servers (name, transport, command, args, env, url, is_active, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id::text, name, transport, command, args, env, url,
		          is_active, description, last_status, last_error, last_connected_at,
		          created_at, updated_at
	`, s.Name, s.Transport, s.Command, string(argsJSON), string(envJSON), s.URL, s.IsActive, s.Description).Scan(
		&result.ID, &result.Name, &result.Transport, &result.Command, &rArgsJSON, &rEnvJSON, &result.URL,
		&result.IsActive, &result.Description, &result.LastStatus, &result.LastError, &result.LastConnectedAt,
		&result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if len(rArgsJSON) > 0 { json.Unmarshal(rArgsJSON, &result.Args) }
	if len(rEnvJSON) > 0  { json.Unmarshal(rEnvJSON, &result.Env) }
	return &result, nil
}

// UpdateMCPServer updates an MCP server configuration.
func (r *AdminRepo) UpdateMCPServer(ctx context.Context, id string, s *MCPServerConfig) (*MCPServerConfig, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	argsJSON, _ := json.Marshal(s.Args)
	if s.Args == nil { argsJSON = []byte("[]") }
	envJSON, _ := json.Marshal(s.Env)
	if s.Env == nil { envJSON = []byte("[]") }

	var result MCPServerConfig
	var rArgsJSON, rEnvJSON []byte
	err := r.db.QueryRowContext(ctx, `
		UPDATE mcp_servers SET
			name = $2, transport = $3, command = $4, args = $5, env = $6, url = $7,
			is_active = $8, description = $9, updated_at = NOW()
		WHERE id = $1
		RETURNING id::text, name, transport, command, args, env, url,
		          is_active, description, last_status, last_error, last_connected_at,
		          created_at, updated_at
	`, id, s.Name, s.Transport, s.Command, string(argsJSON), string(envJSON), s.URL, s.IsActive, s.Description).Scan(
		&result.ID, &result.Name, &result.Transport, &result.Command, &rArgsJSON, &rEnvJSON, &result.URL,
		&result.IsActive, &result.Description, &result.LastStatus, &result.LastError, &result.LastConnectedAt,
		&result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if len(rArgsJSON) > 0 { json.Unmarshal(rArgsJSON, &result.Args) }
	if len(rEnvJSON) > 0  { json.Unmarshal(rEnvJSON, &result.Env) }
	return &result, nil
}

// DeleteMCPServer deletes an MCP server configuration.
func (r *AdminRepo) DeleteMCPServer(ctx context.Context, id string) error {
	if r == nil || r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM mcp_servers WHERE id = $1`, id)
	return err
}

// UpdateMCPServerStatus updates the connection status of an MCP server.
func (r *AdminRepo) UpdateMCPServerStatus(ctx context.Context, id, status, errMsg string) error {
	if r == nil || r.db == nil {
		return nil
	}
	if status == "connected" {
		_, err := r.db.ExecContext(ctx, `
			UPDATE mcp_servers SET last_status = $2, last_error = '', last_connected_at = NOW(), updated_at = NOW()
			WHERE id = $1
		`, id, status)
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE mcp_servers SET last_status = $2, last_error = $3, updated_at = NOW()
		WHERE id = $1
	`, id, status, errMsg)
	return err
}
