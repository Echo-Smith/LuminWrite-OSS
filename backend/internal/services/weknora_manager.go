package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// WeKnoraManager manages the WeKnora integration using Scheme B:
// - A single admin account is used to obtain JWT tokens
// - Each user gets their own knowledge base (KB) in WeKnora
// - The mapping is stored locally in user_weknora_mapping table
// - Users never interact with WeKnora directly
type WeKnoraManager struct {
	baseURL       string
	adminEmail    string
	adminPassword string
	adminKBID     string // default KB ID (from legacy config, for admin panel)
	timeout       time.Duration

	// Cached admin JWT token
	adminToken   string
	adminExpiry  time.Time
	tokenMu      sync.RWMutex

	// WeKnora client initialized with admin token
	client *tools.WeKnoraClient

	db *sql.DB
}

// NewWeKnoraManager creates a new WeKnora manager.
func NewWeKnoraManager(baseURL, adminEmail, adminPassword string, timeout time.Duration, db *sql.DB) *WeKnoraManager {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &WeKnoraManager{
		baseURL:       baseURL,
		adminEmail:    adminEmail,
		adminPassword: adminPassword,
		timeout:       timeout,
		db:            db,
	}
}

// SetAdminKBID sets the default KB ID for admin panel operations.
func (m *WeKnoraManager) SetAdminKBID(kbID string) {
	m.adminKBID = kbID
}

// IsConfigured returns true if admin credentials are set.
func (m *WeKnoraManager) IsConfigured() bool {
	return m != nil && m.baseURL != "" && m.adminEmail != "" && m.adminPassword != ""
}

// getAdminToken returns a valid admin JWT token, refreshing if needed.
func (m *WeKnoraManager) getAdminToken(ctx context.Context) (string, error) {
	m.tokenMu.RLock()
	if m.adminToken != "" && time.Now().Before(m.adminExpiry.Add(-5*time.Minute)) {
		token := m.adminToken
		m.tokenMu.RUnlock()
		return token, nil
	}
	m.tokenMu.RUnlock()

	// Need to login
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()

	// Double-check after acquiring write lock
	if m.adminToken != "" && time.Now().Before(m.adminExpiry.Add(-5*time.Minute)) {
		return m.adminToken, nil
	}

	slog.Info("weknora admin token expired, refreshing...", "base_url", m.baseURL)

	token, tenantID, err := tools.WeKnoraLogin(m.baseURL, m.adminEmail, m.adminPassword, m.timeout)
	if err != nil {
		return "", fmt.Errorf("weknora admin login failed: %w", err)
	}

	m.adminToken = token
	m.adminExpiry = time.Now().Add(24 * time.Hour) // WeKnora JWT expires in ~24h
	m.client = tools.NewWeKnoraClient(m.baseURL, token, "", m.timeout)

	slog.Info("weknora admin token refreshed", "tenant_id", tenantID)
	return token, nil
}

// getClient returns a WeKnoraClient with the admin token, refreshing if needed.
func (m *WeKnoraManager) getClient(ctx context.Context) (*tools.WeKnoraClient, error) {
	if _, err := m.getAdminToken(ctx); err != nil {
		return nil, err
	}
	if m.client == nil {
		return nil, fmt.Errorf("weknora client not initialized")
	}
	return m.client, nil
}

// GetAdminClient returns a WeKnoraClient initialized with the admin JWT token.
// The client has no KB ID set — use it for admin-level operations (list/create/delete KBs).
// This is used by the admin panel handlers to proxy WeKnora operations.
func (m *WeKnoraManager) GetAdminClient(ctx context.Context) (*tools.WeKnoraClient, error) {
	return m.getClient(ctx)
}

// GetAdminClientWithKB returns a WeKnoraClient with the admin JWT token and the default KB ID.
// Use this for KB-specific operations (search, add knowledge, etc.) when the legacy client is unavailable.
func (m *WeKnoraManager) GetAdminClientWithKB(ctx context.Context) (*tools.WeKnoraClient, error) {
	token, err := m.getAdminToken(ctx)
	if err != nil {
		return nil, err
	}
	if m.adminKBID == "" {
		return nil, fmt.Errorf("admin KB ID not configured")
	}
	return tools.NewWeKnoraClient(m.baseURL, token, m.adminKBID, m.timeout), nil
}

// GetAdminKBID returns the configured default KB ID (from the legacy config).
// Returns empty string if not set.
func (m *WeKnoraManager) GetAdminKBID() string {
	return m.adminKBID
}

// GetOrCreateUserKB returns the WeKnora KB ID for the given user.
// If no KB exists yet, a new one is created in WeKnora and the mapping is stored locally.
func (m *WeKnoraManager) GetOrCreateUserKB(ctx context.Context, userID string) (string, error) {
	if !m.IsConfigured() {
		return "", fmt.Errorf("weknora manager not configured")
	}

	// Check local mapping first
	kbID, err := m.getUserKBMapping(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("failed to check user KB mapping: %w", err)
	}
	if kbID != "" {
		return kbID, nil
	}

	// Need to create a new KB
	client, err := m.getClient(ctx)
	if err != nil {
		return "", err
	}

	kbName := fmt.Sprintf("user_%s_materials", userID)
	kbDesc := fmt.Sprintf("个人素材库 — 用户 %s", userID)

	newKBID, err := client.CreateKnowledgeBase(ctx, kbName, kbDesc)
	if err != nil {
		return "", fmt.Errorf("failed to create WeKnora KB for user %s: %w", userID, err)
	}

	// Save mapping
	if err := m.saveUserKBMapping(ctx, userID, newKBID); err != nil {
		// KB was created but mapping failed — log and return the KB ID anyway
		slog.Error("failed to save user KB mapping", "user_id", userID, "kb_id", newKBID, "error", err)
	}

	slog.Info("weknora KB created for user", "user_id", userID, "kb_id", newKBID)
	return newKBID, nil
}

// SearchInUserKB performs a hybrid search in the user's WeKnora KB.
func (m *WeKnoraManager) SearchInUserKB(ctx context.Context, userID, query string, limit int) ([]tools.WeKnoraSearchResult, error) {
	kbID, err := m.GetOrCreateUserKB(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Create a temporary client with the user's KB ID
	userClient := tools.NewWeKnoraClient(m.baseURL, m.adminToken, kbID, m.timeout)
	results, err := userClient.SearchRaw(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("weknora search failed for user %s: %w", userID, err)
	}

	return results, nil
}

// AddKnowledgeToUserKB adds a text/markdown knowledge entry to the user's KB.
func (m *WeKnoraManager) AddKnowledgeToUserKB(ctx context.Context, userID, title, content string) (string, error) {
	kbID, err := m.GetOrCreateUserKB(ctx, userID)
	if err != nil {
		return "", err
	}

	userClient := tools.NewWeKnoraClient(m.baseURL, m.adminToken, kbID, m.timeout)
	docID, err := userClient.CreateKnowledge(ctx, title, content, nil)
	if err != nil {
		return "", fmt.Errorf("failed to add knowledge to user KB: %w", err)
	}

	return docID, nil
}

// UploadFileToUserKB uploads a file to the user's KB.
func (m *WeKnoraManager) UploadFileToUserKB(ctx context.Context, userID, filename string, fileContent io.Reader, title string) (string, error) {
	kbID, err := m.GetOrCreateUserKB(ctx, userID)
	if err != nil {
		return "", err
	}

	userClient := tools.NewWeKnoraClient(m.baseURL, m.adminToken, kbID, m.timeout)

	docID, err := userClient.UploadFile(ctx, filename, fileContent, title)
	if err != nil {
		return "", fmt.Errorf("failed to upload file to user KB: %w", err)
	}

	return docID, nil
}

// DeleteKnowledgeFromUserKB deletes a knowledge entry from the user's KB.
func (m *WeKnoraManager) DeleteKnowledgeFromUserKB(ctx context.Context, userID, knowledgeID string) error {
	kbID, err := m.GetOrCreateUserKB(ctx, userID)
	if err != nil {
		return err
	}

	userClient := tools.NewWeKnoraClient(m.baseURL, m.adminToken, kbID, m.timeout)
	if err := userClient.DeleteKnowledge(ctx, knowledgeID); err != nil {
		return fmt.Errorf("failed to delete knowledge from user KB: %w", err)
	}

	return nil
}

// ListUserKnowledge lists knowledge entries in the user's KB.
func (m *WeKnoraManager) ListUserKnowledge(ctx context.Context, userID string, page, pageSize int) ([]tools.WeKnoraKnowledge, int, error) {
	kbID, err := m.GetOrCreateUserKB(ctx, userID)
	if err != nil {
		return nil, 0, err
	}

	userClient := tools.NewWeKnoraClient(m.baseURL, m.adminToken, kbID, m.timeout)
	entries, total, err := userClient.ListKnowledge(ctx, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list user knowledge: %w", err)
	}

	return entries, total, nil
}

// ─── Database helpers ────────────────────────────────────

func (m *WeKnoraManager) getUserKBMapping(ctx context.Context, userID string) (string, error) {
	if m.db == nil {
		return "", fmt.Errorf("database not available")
	}

	var kbID string
	err := m.db.QueryRowContext(ctx,
		`SELECT weknora_kb_id FROM user_weknora_mapping WHERE user_id = $1`,
		userID,
	).Scan(&kbID)

	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return kbID, nil
}

func (m *WeKnoraManager) saveUserKBMapping(ctx context.Context, userID, kbID string) error {
	if m.db == nil {
		return fmt.Errorf("database not available")
	}

	_, err := m.db.ExecContext(ctx,
		`INSERT INTO user_weknora_mapping (user_id, weknora_kb_id, kb_name, created_at, updated_at)
		 VALUES ($1, $2, $3, NOW(), NOW())
		 ON CONFLICT (user_id) DO UPDATE SET weknora_kb_id = $2, updated_at = NOW()`,
		userID, kbID, fmt.Sprintf("user_%s_materials", userID),
	)
	return err
}

// ─── User Material Metadata ──────────────────────────────

// UserMaterial represents a user's material entry in the local database.
type UserMaterial struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Title          string    `json:"title"`
	ContentPreview string    `json:"content_preview"`
	SourceType     string    `json:"source_type"`
	SourceURL      string    `json:"source_url,omitempty"`
	FileName       string    `json:"file_name,omitempty"`
	FileSize       int64     `json:"file_size,omitempty"`
	WeKnoraDocID   string    `json:"weknora_doc_id"`
	WeKnoraKBID    string    `json:"weknora_kb_id"`
	Metadata       any       `json:"metadata,omitempty"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// SaveMaterial saves a user material entry to the local database.
func (m *WeKnoraManager) SaveMaterial(ctx context.Context, mat *UserMaterial) error {
	metadataJSON, _ := json.Marshal(mat.Metadata)
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO user_materials (id, user_id, title, content_preview, source_type, source_url, file_name, file_size, weknora_doc_id, weknora_kb_id, metadata, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW())`,
		mat.ID, mat.UserID, mat.Title, mat.ContentPreview, mat.SourceType,
		mat.SourceURL, mat.FileName, mat.FileSize, mat.WeKnoraDocID, mat.WeKnoraKBID,
		metadataJSON, mat.Status,
	)
	return err
}

// ListMaterials lists a user's materials with pagination.
func (m *WeKnoraManager) ListMaterials(ctx context.Context, userID string, page, pageSize int) ([]UserMaterial, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var total int
	err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_materials WHERE user_id = $1 AND status != 'deleted'`,
		userID,
	).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, user_id, title, content_preview, source_type, source_url, file_name, file_size, weknora_doc_id, weknora_kb_id, metadata, status, created_at, updated_at
		 FROM user_materials
		 WHERE user_id = $1 AND status != 'deleted'
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		userID, pageSize, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var materials []UserMaterial
	for rows.Next() {
		var mat UserMaterial
		var metadataBytes []byte
		var sourceURL, fileName sql.NullString
		var fileSize sql.NullInt64

		if err := rows.Scan(
			&mat.ID, &mat.UserID, &mat.Title, &mat.ContentPreview,
			&mat.SourceType, &sourceURL, &fileName, &fileSize,
			&mat.WeKnoraDocID, &mat.WeKnoraKBID, &metadataBytes,
			&mat.Status, &mat.CreatedAt, &mat.UpdatedAt,
		); err != nil {
			return nil, 0, err
		}

		mat.SourceURL = sourceURL.String
		mat.FileName = fileName.String
		mat.FileSize = fileSize.Int64
		if len(metadataBytes) > 0 {
			json.Unmarshal(metadataBytes, &mat.Metadata)
		}
		materials = append(materials, mat)
	}

	return materials, total, nil
}

// GetMaterial retrieves a single material by ID.
func (m *WeKnoraManager) GetMaterial(ctx context.Context, userID, materialID string) (*UserMaterial, error) {
	var mat UserMaterial
	var metadataBytes []byte
	var sourceURL, fileName sql.NullString
	var fileSize sql.NullInt64

	err := m.db.QueryRowContext(ctx,
		`SELECT id, user_id, title, content_preview, source_type, source_url, file_name, file_size, weknora_doc_id, weknora_kb_id, metadata, status, created_at, updated_at
		 FROM user_materials
		 WHERE id = $1 AND user_id = $2 AND status != 'deleted'`,
		materialID, userID,
	).Scan(
		&mat.ID, &mat.UserID, &mat.Title, &mat.ContentPreview,
		&mat.SourceType, &sourceURL, &fileName, &fileSize,
		&mat.WeKnoraDocID, &mat.WeKnoraKBID, &metadataBytes,
		&mat.Status, &mat.CreatedAt, &mat.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	mat.SourceURL = sourceURL.String
	mat.FileName = fileName.String
	mat.FileSize = fileSize.Int64
	if len(metadataBytes) > 0 {
		json.Unmarshal(metadataBytes, &mat.Metadata)
	}

	return &mat, nil
}

// GetMaterialByDocID retrieves a material by its WeKnora document ID.
func (m *WeKnoraManager) GetMaterialByDocID(ctx context.Context, userID, docID string) (*UserMaterial, error) {
	var mat UserMaterial
	var metadataBytes []byte
	var sourceURL, fileName sql.NullString
	var fileSize sql.NullInt64

	err := m.db.QueryRowContext(ctx,
		`SELECT id, user_id, title, content_preview, source_type, source_url, file_name, file_size, weknora_doc_id, weknora_kb_id, metadata, status, created_at, updated_at
		 FROM user_materials
		 WHERE weknora_doc_id = $1 AND user_id = $2 AND status != 'deleted'`,
		docID, userID,
	).Scan(
		&mat.ID, &mat.UserID, &mat.Title, &mat.ContentPreview,
		&mat.SourceType, &sourceURL, &fileName, &fileSize,
		&mat.WeKnoraDocID, &mat.WeKnoraKBID, &metadataBytes,
		&mat.Status, &mat.CreatedAt, &mat.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	mat.SourceURL = sourceURL.String
	mat.FileName = fileName.String
	mat.FileSize = fileSize.Int64
	if len(metadataBytes) > 0 {
		json.Unmarshal(metadataBytes, &mat.Metadata)
	}

	return &mat, nil
}

// DeleteMaterial soft-deletes a material entry and removes it from WeKnora.
func (m *WeKnoraManager) DeleteMaterial(ctx context.Context, userID, materialID string) error {
	// Get the material to find the WeKnora doc ID
	mat, err := m.GetMaterial(ctx, userID, materialID)
	if err != nil {
		return err
	}
	if mat == nil {
		return fmt.Errorf("material not found")
	}

	// Delete from WeKnora (best effort)
	if mat.WeKnoraDocID != "" {
		if err := m.DeleteKnowledgeFromUserKB(ctx, userID, mat.WeKnoraDocID); err != nil {
			slog.Warn("failed to delete material from weknora", "doc_id", mat.WeKnoraDocID, "error", err)
		}
	}

	// Soft-delete locally
	_, err = m.db.ExecContext(ctx,
		`UPDATE user_materials SET status = 'deleted', updated_at = NOW() WHERE id = $1 AND user_id = $2`,
		materialID, userID,
	)
	return err
}

// ─── Topic-Material Association ──────────────────────────

// TopicMaterial represents a topic-material association.
type TopicMaterial struct {
	ID              string    `json:"id"`
	TopicID         string    `json:"topic_id"`
	MaterialID      string    `json:"material_id"`
	UserID          string    `json:"user_id"`
	AssociationType string    `json:"association_type"`
	RelevanceScore  float64   `json:"relevance_score,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// AssociateMaterialWithTopic creates a manual association between a topic and a material.
func (m *WeKnoraManager) AssociateMaterialWithTopic(ctx context.Context, topicID, materialID, userID string, associationType string, score float64) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO topic_materials (topic_id, material_id, user_id, association_type, relevance_score, created_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())
		 ON CONFLICT (topic_id, material_id) DO UPDATE SET association_type = $4, relevance_score = $5`,
		topicID, materialID, userID, associationType, score,
	)
	return err
}

// ListTopicMaterials lists all materials associated with a topic.
func (m *WeKnoraManager) ListTopicMaterials(ctx context.Context, topicID, userID string) ([]TopicMaterial, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT tm.id, tm.topic_id, tm.material_id, tm.user_id, tm.association_type, tm.relevance_score, tm.created_at
		 FROM topic_materials tm
		 WHERE tm.topic_id = $1 AND tm.user_id = $2
		 ORDER BY tm.association_type DESC, tm.relevance_score DESC, tm.created_at DESC`,
		topicID, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []TopicMaterial
	for rows.Next() {
		var tm TopicMaterial
		if err := rows.Scan(&tm.ID, &tm.TopicID, &tm.MaterialID, &tm.UserID, &tm.AssociationType, &tm.RelevanceScore, &tm.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, tm)
	}
	return items, nil
}

// RemoveTopicMaterial removes a topic-material association.
func (m *WeKnoraManager) RemoveTopicMaterial(ctx context.Context, topicID, materialID, userID string) error {
	_, err := m.db.ExecContext(ctx,
		`DELETE FROM topic_materials WHERE topic_id = $1 AND material_id = $2 AND user_id = $3`,
		topicID, materialID, userID,
	)
	return err
}
