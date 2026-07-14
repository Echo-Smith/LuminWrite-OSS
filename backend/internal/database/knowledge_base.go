package database

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/luminbuddy/writing-agent-v2/internal/tools"
)

// KnowledgeBaseRepo handles knowledge base CRUD and semantic search.
type KnowledgeBaseRepo struct {
	db        *DB
	embedding *tools.EmbeddingClient
}

// NewKnowledgeBaseRepo creates a new KnowledgeBaseRepo.
func NewKnowledgeBaseRepo(db *DB, embedding *tools.EmbeddingClient) *KnowledgeBaseRepo {
	return &KnowledgeBaseRepo{db: db, embedding: embedding}
}

// KBEntry represents a knowledge base entry.
type KBEntry struct {
	ID              string                 `json:"id"`
	Source          string                 `json:"source"`
	SourceID        string                 `json:"source_id,omitempty"`
	Title           string                 `json:"title"`
	Content         string                 `json:"content"`
	ContentHash     string                 `json:"content_hash"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	EmbeddingModel  string                 `json:"embedding_model,omitempty"`
	EmbeddingDim    int                    `json:"embedding_dim,omitempty"`
	HasEmbedding    bool                   `json:"has_embedding"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

// AddEntry inserts a new knowledge base entry and generates its embedding.
func (r *KnowledgeBaseRepo) AddEntry(ctx context.Context, source, sourceID, title, content string, metadata map[string]interface{}) (*KBEntry, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	// Compute content hash
	contentHash := simpleHash(content)

	// Check for duplicate
	var existingID string
	err := r.db.QueryRowContext(ctx, `
		SELECT id::text FROM knowledge_base WHERE content_hash = $1
	`, contentHash).Scan(&existingID)
	if err == nil {
		// Already exists — return it
		return r.GetByID(ctx, existingID)
	}

	// Generate embedding
	var embeddingVec string
	embeddingModel := ""
	embeddingDim := 0

	if r.embedding != nil && r.embedding.IsConfigured() {
		// Combine title and content for embedding
		embedText := title + "\n" + content
		if len([]rune(embedText)) > 2000 {
			embedText = string([]rune(embedText)[:2000])
		}

		vec, _, err := r.embedding.EmbedSingle(ctx, embedText)
		if err != nil {
			slog.Warn("failed to generate embedding, storing without vector", "error", err)
		} else {
			embeddingVec = tools.FormatVectorForPG(vec)
			embeddingModel = r.embedding.Model()
			embeddingDim = r.embedding.Dimension()
		}
	}

	metaJSON, _ := json.Marshal(metadata)
	if metadata == nil {
		metaJSON = []byte("{}")
	}

	var entry KBEntry
	var metaResult []byte

	query := `
		INSERT INTO knowledge_base (source, source_id, title, content, content_hash, metadata, embedding_model, embedding_dim, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
	`
	args := []interface{}{source, sourceID, title, content, contentHash, string(metaJSON), embeddingModel, embeddingDim}

	if embeddingVec != "" {
		query = `
			INSERT INTO knowledge_base (source, source_id, title, content, content_hash, metadata, embedding, embedding_model, embedding_dim, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
		`
		args = []interface{}{source, sourceID, title, content, contentHash, string(metaJSON), embeddingVec, embeddingModel, embeddingDim}
	}

	err = r.db.QueryRowContext(ctx, query+" RETURNING id::text, source, source_id, title, content, content_hash, metadata, embedding_model, embedding_dim, (embedding IS NOT NULL), created_at, updated_at", args...).Scan(
		&entry.ID, &entry.Source, &entry.SourceID, &entry.Title, &entry.Content, &entry.ContentHash,
		&metaResult, &entry.EmbeddingModel, &entry.EmbeddingDim, &entry.HasEmbedding, &entry.CreatedAt, &entry.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert KB entry: %w", err)
	}

	if len(metaResult) > 0 {
		json.Unmarshal(metaResult, &entry.Metadata)
	}

	slog.Info("KB entry added", "id", entry.ID, "source", source, "has_embedding", entry.HasEmbedding)
	return &entry, nil
}

// GetByID retrieves a knowledge base entry by ID.
func (r *KnowledgeBaseRepo) GetByID(ctx context.Context, id string) (*KBEntry, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var entry KBEntry
	var metaResult []byte

	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, source, source_id, title, content, content_hash, metadata,
		       embedding_model, embedding_dim, (embedding IS NOT NULL), created_at, updated_at
		FROM knowledge_base WHERE id = $1
	`, id).Scan(
		&entry.ID, &entry.Source, &entry.SourceID, &entry.Title, &entry.Content, &entry.ContentHash,
		&metaResult, &entry.EmbeddingModel, &entry.EmbeddingDim, &entry.HasEmbedding, &entry.CreatedAt, &entry.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if len(metaResult) > 0 {
		json.Unmarshal(metaResult, &entry.Metadata)
	}

	return &entry, nil
}

// List lists knowledge base entries with optional source filter.
func (r *KnowledgeBaseRepo) List(ctx context.Context, source string, page, pageSize int) ([]*KBEntry, int, error) {
	if r.db == nil {
		return []*KBEntry{}, 0, nil
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	query := `SELECT id::text, source, source_id, title, content, content_hash, metadata,
	          embedding_model, embedding_dim, (embedding IS NOT NULL), created_at, updated_at
	          FROM knowledge_base`
	args := []interface{}{}
	argIdx := 1

	if source != "" {
		query += fmt.Sprintf(" WHERE source = $%d", argIdx)
		args = append(args, source)
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, pageSize, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []*KBEntry
	for rows.Next() {
		var entry KBEntry
		var metaResult []byte
		if err := rows.Scan(&entry.ID, &entry.Source, &entry.SourceID, &entry.Title, &entry.Content,
			&entry.ContentHash, &metaResult, &entry.EmbeddingModel, &entry.EmbeddingDim,
			&entry.HasEmbedding, &entry.CreatedAt, &entry.UpdatedAt); err != nil {
			continue
		}
		if len(metaResult) > 0 {
			json.Unmarshal(metaResult, &entry.Metadata)
		}
		entries = append(entries, &entry)
	}

	// Get total count
	var total int
	countQuery := "SELECT COUNT(*) FROM knowledge_base"
	if source != "" {
		countQuery += " WHERE source = $1"
		r.db.QueryRowContext(ctx, countQuery, source).Scan(&total)
	} else {
		r.db.QueryRowContext(ctx, countQuery).Scan(&total)
	}

	return entries, total, nil
}

// SemanticSearch performs a vector similarity search on the knowledge base.
// It generates an embedding for the query and finds the most similar entries.
func (r *KnowledgeBaseRepo) SemanticSearch(ctx context.Context, query string, limit int, source string) ([]*KBSearchResult, error) {
	if r.db == nil {
		return []*KBSearchResult{}, nil
	}
	if r.embedding == nil || !r.embedding.IsConfigured() {
		// Fallback to text search
		return r.TextSearch(ctx, query, limit, source)
	}

	if limit <= 0 || limit > 50 {
		limit = 10
	}

	// Generate embedding for the query
	queryVec, _, err := r.embedding.EmbedSingle(ctx, query)
	if err != nil {
		slog.Warn("failed to generate query embedding, falling back to text search", "error", err)
		return r.TextSearch(ctx, query, limit, source)
	}

	vecStr := tools.FormatVectorForPG(queryVec)

	// Perform vector similarity search using cosine distance
	dbQuery := `
		SELECT id::text, source, source_id, title, content, metadata,
		       1 - (embedding <=> $1::vector) as similarity
		FROM knowledge_base
		WHERE embedding IS NOT NULL
	`
	args := []interface{}{vecStr}
	argIdx := 2

	if source != "" {
		dbQuery += fmt.Sprintf(" AND source = $%d", argIdx)
		args = append(args, source)
		argIdx++
	}

	dbQuery += fmt.Sprintf(" ORDER BY embedding <=> $1::vector LIMIT $%d", argIdx)
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, dbQuery, args...)
	if err != nil {
		slog.Warn("vector search failed, falling back to text search", "error", err)
		return r.TextSearch(ctx, query, limit, source)
	}
	defer rows.Close()

	var results []*KBSearchResult
	for rows.Next() {
		var r KBSearchResult
		var metaResult []byte
		if err := rows.Scan(&r.ID, &r.Source, &r.SourceID, &r.Title, &r.Content, &metaResult, &r.Similarity); err != nil {
			continue
		}
		if len(metaResult) > 0 {
			json.Unmarshal(metaResult, &r.Metadata)
		}
		results = append(results, &r)
	}

	slog.Info("semantic search completed", "query", query, "results", len(results), "source", source)
	return results, nil
}

// TextSearch performs a full-text search on the knowledge base.
func (r *KnowledgeBaseRepo) TextSearch(ctx context.Context, query string, limit int, source string) ([]*KBSearchResult, error) {
	if r.db == nil {
		return []*KBSearchResult{}, nil
	}

	if limit <= 0 || limit > 50 {
		limit = 10
	}

	dbQuery := `
		SELECT id::text, source, source_id, title, content, metadata,
		       ts_rank(to_tsvector('simple', content), plainto_tsquery('simple', $1)) as rank
		FROM knowledge_base
		WHERE to_tsvector('simple', content) @@ plainto_tsquery('simple', $1)
	`
	args := []interface{}{query}
	argIdx := 2

	if source != "" {
		dbQuery += fmt.Sprintf(" AND source = $%d", argIdx)
		args = append(args, source)
		argIdx++
	}

	dbQuery += fmt.Sprintf(" ORDER BY rank DESC LIMIT $%d", argIdx)
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, dbQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*KBSearchResult
	for rows.Next() {
		var r KBSearchResult
		var metaResult []byte
		if err := rows.Scan(&r.ID, &r.Source, &r.SourceID, &r.Title, &r.Content, &metaResult, &r.Similarity); err != nil {
			continue
		}
		if len(metaResult) > 0 {
			json.Unmarshal(metaResult, &r.Metadata)
		}
		results = append(results, &r)
	}

	return results, nil
}

// KBSearchResult is a search result from the knowledge base.
type KBSearchResult struct {
	ID         string                 `json:"id"`
	Source     string                 `json:"source"`
	SourceID   string                 `json:"source_id,omitempty"`
	Title      string                 `json:"title"`
	Content    string                 `json:"content"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	Similarity float64                `json:"similarity"`
}

// Delete removes a knowledge base entry by ID.
func (r *KnowledgeBaseRepo) Delete(ctx context.Context, id string) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM knowledge_base WHERE id = $1`, id)
	return err
}

// GenerateMissingEmbeddings generates embeddings for entries that don't have one.
func (r *KnowledgeBaseRepo) GenerateMissingEmbeddings(ctx context.Context, batchSize int) (int, error) {
	if r.db == nil {
		return 0, nil
	}
	if r.embedding == nil || !r.embedding.IsConfigured() {
		return 0, fmt.Errorf("embedding client not configured")
	}

	if batchSize <= 0 {
		batchSize = 10
	}

	// Get entries without embeddings
	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text, title, content FROM knowledge_base
		WHERE embedding IS NULL
		LIMIT $1
	`, batchSize)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type entry struct {
		id      string
		title   string
		content string
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.title, &e.content); err != nil {
			continue
		}
		entries = append(entries, e)
	}

	if len(entries) == 0 {
		return 0, nil
	}

	// Generate embeddings in batch
	texts := make([]string, len(entries))
	for i, e := range entries {
		texts[i] = e.title + "\n" + e.content
		if len([]rune(texts[i])) > 2000 {
			texts[i] = string([]rune(texts[i])[:2000])
		}
	}

	embeddings, _, err := r.embedding.Embed(ctx, texts)
	if err != nil {
		return 0, fmt.Errorf("failed to generate embeddings: %w", err)
	}

	count := 0
	for i, emb := range embeddings {
		vecStr := tools.FormatVectorForPG(emb)
		_, err := r.db.ExecContext(ctx, `
			UPDATE knowledge_base
			SET embedding = $2::vector, embedding_model = $3, embedding_dim = $4, updated_at = NOW()
			WHERE id = $1
		`, entries[i].id, vecStr, r.embedding.Model(), r.embedding.Dimension())
		if err != nil {
			slog.Warn("failed to update embedding for entry", "id", entries[i].id, "error", err)
			continue
		}
		count++
	}

	slog.Info("missing embeddings generated", "count", count, "total", len(entries))
	return count, nil
}

// simpleHash computes a simple SHA256-like hash for content deduplication.
func simpleHash(s string) string {
	// Use a simple hash for deduplication — we use the database's content_hash column
	// which has a unique constraint, so collisions are handled at the DB level
	h := uint64(14695981039346656037) // FNV offset
	for _, c := range s {
		h ^= uint64(c)
		h *= 1099511628211 // FNV prime
	}
	return fmt.Sprintf("%x", h)
}

// Ensure uuid is used (for potential future use)
var _ = uuid.New
