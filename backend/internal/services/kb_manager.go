package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ─── Knowledge Manager ─────────────────────────────────
// KbManager replaces WeKnoraManager. Instead of proxying to an external
// WeKnora API, it operates directly on the local PostgreSQL database.
//
// Key differences from WeKnoraManager:
//   - No HTTP client, no JWT token management, no external API calls
//   - All operations are local SQL queries on knowledge_base + knowledge_chunks
//   - Multi-tenant isolation via user_id column (no separate KB per user)
//   - Hybrid search uses paradedb BM25 + pgvector Dense locally
//   - GraphRAG uses kb_entities + kb_relations tables locally

// KbConfig holds chunking and search configuration for a knowledge base.
type KbConfig struct {
	ChunkSize    int
	ChunkOverlap int
	SplitMarkers []string
	BM25Weight   float64
	DenseWeight  float64
	GraphWeight  float64
}

// DefaultKbConfig returns sensible defaults for knowledge base operations.
func DefaultKbConfig() KbConfig {
	return KbConfig{
		ChunkSize:    512,
		ChunkOverlap: 50,
		SplitMarkers: []string{"\n\n", "\n", "。"},
		BM25Weight:   0.3,
		DenseWeight:  0.5,
		GraphWeight:  0.2,
	}
}

// KbManager manages knowledge base operations directly on local PostgreSQL.
// It replaces WeKnoraManager and eliminates the need for an external WeKnora API.
type KbManager struct {
	db         *sql.DB
	embedding  *tools.EmbeddingClient
	config     KbConfig
	graphRAG   *GraphRAGManager // optional, for entity extraction
}

// NewKbManager creates a new Knowledge Manager.
func NewKbManager(db *sql.DB, embedding *tools.EmbeddingClient) *KbManager {
	return &KbManager{
		db:        db,
		embedding: embedding,
		config:    DefaultKbConfig(),
	}
}

// SetGraphRAG attaches a GraphRAG manager for entity extraction.
// When set, AddChunk will asynchronously extract entities and relations
// from each chunk and store them in kb_entities/kb_relations tables.
func (m *KbManager) SetGraphRAG(g *GraphRAGManager) {
	m.graphRAG = g
}

// IsConfigured returns true if the database is available.
func (m *KbManager) IsConfigured() bool {
	return m != nil && m.db != nil
}

// ─── Document Management ────────────────────────────────

// KbDocument represents a document in the knowledge base.
type KbDocument struct {
	ID           string                 `json:"id"`
	UserID       string                 `json:"user_id"`
	Source       string                 `json:"source"`
	SourceType   string                 `json:"source_type"`
	Title        string                 `json:"title"`
	Content      string                 `json:"content"`
	ContentPreview string               `json:"content_preview"`
	SourceURL    string                 `json:"source_url,omitempty"`
	FileName     string                 `json:"file_name,omitempty"`
	FileSize     int64                  `json:"file_size,omitempty"`
	ChunkCount   int                    `json:"chunk_count"`
	Status       string                 `json:"status"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	HasEmbedding bool                   `json:"has_embedding"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
}

// AddDocument creates a new document in the knowledge base.
// If userID is empty, the document is shared globally.
func (m *KbManager) AddDocument(ctx context.Context, userID, title, content, sourceType string, metadata map[string]interface{}) (*KbDocument, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	contentHash := simpleContentHash(content)
	preview := truncateContent(content, 500)

	metaJSON, _ := json.Marshal(metadata)
	if metadata == nil {
		metaJSON = []byte("{}")
	}

	var doc KbDocument
	var metaResult []byte

	// Try to generate embedding for the document
	var embeddingVec string
	embeddingModel := ""
	embeddingDim := 0

	if m.embedding != nil && m.embedding.IsConfigured() {
		embedText := title + "\n" + content
		if len([]rune(embedText)) > 2000 {
			embedText = string([]rune(embedText)[:2000])
		}
		vec, _, err := m.embedding.EmbedSingle(ctx, embedText)
		if err == nil {
			embeddingVec = tools.FormatVectorForPG(vec)
			embeddingModel = m.embedding.Model()
			embeddingDim = m.embedding.Dimension()
		}
	}

	query := `
		INSERT INTO knowledge_base (user_id, source, source_type, title, content, content_hash, metadata, status, embedding_model, embedding_dim, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', $8, $9, NOW(), NOW())
	`
	args := []interface{}{nullIfEmpty(userID), sourceType, sourceType, title, content, contentHash, string(metaJSON), embeddingModel, embeddingDim}

	if embeddingVec != "" {
		query = `
			INSERT INTO knowledge_base (user_id, source, source_type, title, content, content_hash, metadata, status, embedding, embedding_model, embedding_dim, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', $8, $9, $10, NOW(), NOW())
		`
		args = []interface{}{nullIfEmpty(userID), sourceType, sourceType, title, content, contentHash, string(metaJSON), embeddingVec, embeddingModel, embeddingDim}
	}

	err := m.db.QueryRowContext(ctx, query+" RETURNING id::text, COALESCE(user_id, ''), source, source_type, title, content, metadata, (embedding IS NOT NULL), created_at, updated_at", args...).Scan(
		&doc.ID, &doc.UserID, &doc.Source, &doc.SourceType, &doc.Title, &doc.Content,
		&metaResult, &doc.HasEmbedding, &doc.CreatedAt, &doc.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert document: %w", err)
	}

	doc.ContentPreview = preview
	doc.Status = "active"
	if len(metaResult) > 0 {
		json.Unmarshal(metaResult, &doc.Metadata)
	}

	slog.Info("KB document added", "id", doc.ID, "user_id", userID, "title", title)
	return &doc, nil
}

// ListDocuments lists documents for a user with pagination.
func (m *KbManager) ListDocuments(ctx context.Context, userID string, page, pageSize int) ([]*KbDocument, int, error) {
	if m.db == nil {
		return []*KbDocument{}, 0, nil
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	// Count total
	var total int
	countQuery := "SELECT COUNT(*) FROM knowledge_base WHERE user_id = $1 AND status != 'deleted'"
	if err := m.db.QueryRowContext(ctx, countQuery, userID).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	rows, err := m.db.QueryContext(ctx, `
		SELECT id::text, COALESCE(user_id, ''), source, source_type, title,
		       LEFT(content, 500) as content_preview, status, chunk_count,
		       COALESCE(source_url, ''), COALESCE(file_name, ''), COALESCE(file_size, 0),
		       metadata, (embedding IS NOT NULL), created_at, updated_at
		FROM knowledge_base
		WHERE user_id = $1 AND status != 'deleted'
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var docs []*KbDocument
	for rows.Next() {
		var doc KbDocument
		var metaResult []byte
		if err := rows.Scan(
			&doc.ID, &doc.UserID, &doc.Source, &doc.SourceType, &doc.Title,
			&doc.ContentPreview, &doc.Status, &doc.ChunkCount,
			&doc.SourceURL, &doc.FileName, &doc.FileSize,
			&metaResult, &doc.HasEmbedding, &doc.CreatedAt, &doc.UpdatedAt,
		); err != nil {
			continue
		}
		if len(metaResult) > 0 {
			json.Unmarshal(metaResult, &doc.Metadata)
		}
		docs = append(docs, &doc)
	}

	return docs, total, nil
}

// GetDocument retrieves a single document by ID.
func (m *KbManager) GetDocument(ctx context.Context, userID, docID string) (*KbDocument, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var doc KbDocument
	var metaResult []byte
	var userIDFromDB sql.NullString

	err := m.db.QueryRowContext(ctx, `
		SELECT id::text, COALESCE(user_id, ''), source, source_type, title,
		       content, LEFT(content, 500) as content_preview, status, chunk_count,
		       COALESCE(source_url, ''), COALESCE(file_name, ''), COALESCE(file_size, 0),
		       metadata, (embedding IS NOT NULL), created_at, updated_at
		FROM knowledge_base
		WHERE id = $1 AND user_id = $2 AND status != 'deleted'
	`, docID, userID).Scan(
		&doc.ID, &userIDFromDB, &doc.Source, &doc.SourceType, &doc.Title,
		&doc.Content, &doc.ContentPreview, &doc.Status, &doc.ChunkCount,
		&doc.SourceURL, &doc.FileName, &doc.FileSize,
		&metaResult, &doc.HasEmbedding, &doc.CreatedAt, &doc.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	doc.UserID = userIDFromDB.String
	if len(metaResult) > 0 {
		json.Unmarshal(metaResult, &doc.Metadata)
	}

	return &doc, nil
}

// DeleteDocument soft-deletes a document and its chunks.
func (m *KbManager) DeleteDocument(ctx context.Context, userID, docID string) error {
	if m.db == nil {
		return fmt.Errorf("database not available")
	}

	// Delete chunks first (hard delete — they cascade)
	_, err := m.db.ExecContext(ctx, `DELETE FROM knowledge_chunks WHERE doc_id = $1`, docID)
	if err != nil {
		slog.Warn("failed to delete chunks", "doc_id", docID, "error", err)
	}

	// Soft-delete document
	_, err = m.db.ExecContext(ctx, `
		UPDATE knowledge_base SET status = 'deleted', updated_at = NOW()
		WHERE id = $1 AND user_id = $2
	`, docID, userID)
	return err
}

// ─── Chunk Management ───────────────────────────────────

// KbChunk represents a content chunk within a document.
type KbChunk struct {
	ID           string    `json:"id"`
	DocID        string    `json:"doc_id"`
	UserID       string    `json:"user_id"`
	ChunkIndex   int       `json:"chunk_index"`
	Title        string    `json:"title,omitempty"`
	Content      string    `json:"content"`
	HasEmbedding bool      `json:"has_embedding"`
	CreatedAt    time.Time `json:"created_at"`
}

// AddChunk creates a new chunk for a document and generates its embedding.
func (m *KbManager) AddChunk(ctx context.Context, docID, userID string, chunkIndex int, title, content string, metadata map[string]interface{}) (*KbChunk, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	metaJSON, _ := json.Marshal(metadata)
	if metadata == nil {
		metaJSON = []byte("{}")
	}

	// Generate embedding for chunk
	var embeddingVec string
	embeddingModel := ""
	embeddingDim := 0

	if m.embedding != nil && m.embedding.IsConfigured() {
		embedText := content
		if len([]rune(embedText)) > 2000 {
			embedText = string([]rune(embedText)[:2000])
		}
		vec, _, err := m.embedding.EmbedSingle(ctx, embedText)
		if err == nil {
			embeddingVec = tools.FormatVectorForPG(vec)
			embeddingModel = m.embedding.Model()
			embeddingDim = m.embedding.Dimension()
		}
	}

	var chunk KbChunk
	// Use a subquery to inherit kb_id from the parent document.
	// This ensures KB-scoped searches (bm25SearchInKB / denseSearchInKB)
	// can find newly created chunks without an extra round-trip.
	query := `
		INSERT INTO knowledge_chunks (doc_id, user_id, kb_id, chunk_index, title, content, chunk_metadata, embedding_model, embedding_dim, created_at, updated_at)
		VALUES ($1, $2, (SELECT kb_id FROM knowledge_base WHERE id = $1), $3, $4, $5, $6, $7, $8, NOW(), NOW())
	`
	args := []interface{}{docID, nullIfEmpty(userID), chunkIndex, title, content, string(metaJSON), embeddingModel, embeddingDim}

	if embeddingVec != "" {
		query = `
			INSERT INTO knowledge_chunks (doc_id, user_id, kb_id, chunk_index, title, content, chunk_metadata, embedding, embedding_model, embedding_dim, created_at, updated_at)
			VALUES ($1, $2, (SELECT kb_id FROM knowledge_base WHERE id = $1), $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
		`
		args = []interface{}{docID, nullIfEmpty(userID), chunkIndex, title, content, string(metaJSON), embeddingVec, embeddingModel, embeddingDim}
	}

	err := m.db.QueryRowContext(ctx, query+" RETURNING id::text, doc_id::text, COALESCE(user_id, ''), chunk_index, COALESCE(title, ''), content, (embedding IS NOT NULL), created_at", args...).Scan(
		&chunk.ID, &chunk.DocID, &chunk.UserID, &chunk.ChunkIndex, &chunk.Title, &chunk.Content, &chunk.HasEmbedding, &chunk.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert chunk: %w", err)
	}

	// Async GraphRAG extraction — extract entities and relations from this chunk
	if m.graphRAG != nil && m.graphRAG.IsConfigured() {
		chunkID := chunk.ID
		chunkContent := chunk.Content
		go func() {
			grafCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := m.graphRAG.ExtractAndStore(grafCtx, docID, chunkID, userID, chunkContent); err != nil {
				slog.Debug("GraphRAG async extraction skipped", "chunk_id", chunkID, "error", err)
			}
		}()
	}

	return &chunk, nil
}

// ListChunks lists all chunks for a document.
func (m *KbManager) ListChunks(ctx context.Context, docID string) ([]*KbChunk, error) {
	if m.db == nil {
		return []*KbChunk{}, nil
	}

	rows, err := m.db.QueryContext(ctx, `
		SELECT id::text, doc_id::text, COALESCE(user_id, ''), chunk_index,
		       COALESCE(title, ''), content, (embedding IS NOT NULL), created_at
		FROM knowledge_chunks
		WHERE doc_id = $1
		ORDER BY chunk_index ASC
	`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []*KbChunk
	for rows.Next() {
		var chunk KbChunk
		if err := rows.Scan(&chunk.ID, &chunk.DocID, &chunk.UserID, &chunk.ChunkIndex, &chunk.Title, &chunk.Content, &chunk.HasEmbedding, &chunk.CreatedAt); err != nil {
			continue
		}
		chunks = append(chunks, &chunk)
	}

	return chunks, nil
}

// UpdateChunkCount updates the chunk_count on the parent document.
func (m *KbManager) UpdateChunkCount(ctx context.Context, docID string, count int) error {
	if m.db == nil {
		return nil
	}
	_, err := m.db.ExecContext(ctx, `UPDATE knowledge_base SET chunk_count = $2, updated_at = NOW() WHERE id = $1`, docID, count)
	return err
}

// DeleteChunksByDocID deletes all chunks for a document.
func (m *KbManager) DeleteChunksByDocID(ctx context.Context, docID string) error {
	if m.db == nil {
		return nil
	}
	_, err := m.db.ExecContext(ctx, `DELETE FROM knowledge_chunks WHERE doc_id = $1`, docID)
	return err
}

// RechunkResult holds the result of re-chunking a single document.
type RechunkResult struct {
	DocID       string `json:"doc_id"`
	Title       string `json:"title"`
	OldChunks   int    `json:"old_chunks"`
	NewChunks   int    `json:"new_chunks"`
	ContentLen  int    `json:"content_len"`
}

// RechunkAll re-chunks all active documents in the knowledge base.
// It deletes existing chunks for each document and re-creates them
// using the provided chunk config. This is used for cleaning up
// data that was chunked with the old, buggy chunker.
func (m *KbManager) RechunkAll(ctx context.Context, config ChunkConfig) ([]RechunkResult, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	// Get all active documents with content
	rows, err := m.db.QueryContext(ctx, `
		SELECT id::text, COALESCE(user_id, ''), title, content, chunk_count
		FROM knowledge_base
		WHERE status = 'active'
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query documents: %w", err)
	}
	defer rows.Close()

	var results []RechunkResult
	for rows.Next() {
		var docID, userID, title, content string
		var oldChunkCount int
		if err := rows.Scan(&docID, &userID, &title, &content, &oldChunkCount); err != nil {
			continue
		}

		// Clean the content using whitelist extraction (only author + body text)
		cleaned := cleanContentForRechunk(content)

		// If cleaning produced empty content, skip this document
		if strings.TrimSpace(cleaned) == "" {
			slog.Warn("rechunk: cleaned content is empty, skipping", "doc_id", docID, "title", title)
			// Still delete old chunks for this doc
			m.DeleteChunksByDocID(ctx, docID)
			m.UpdateChunkCount(ctx, docID, 0)
			results = append(results, RechunkResult{
				DocID:     docID,
				Title:     title,
				OldChunks: oldChunkCount,
				NewChunks: 0,
			})
			continue
		}

		// Update the document's content column with cleaned content
		// Note: we don't update content_hash to avoid unique constraint violations
		// when multiple documents have similar cleaned content
		if _, err := m.db.ExecContext(ctx, `UPDATE knowledge_base SET content = $2, updated_at = NOW() WHERE id = $1`, docID, cleaned); err != nil {
			slog.Warn("rechunk: failed to update content", "doc_id", docID, "error", err)
		}

		// Re-chunk with the new config
		chunks := ChunkText(cleaned, config)

		// Delete old chunks
		if err := m.DeleteChunksByDocID(ctx, docID); err != nil {
			slog.Warn("rechunk: failed to delete old chunks", "doc_id", docID, "error", err)
			continue
		}

		// Insert new chunks
		for _, chunk := range chunks {
			_, err := m.AddChunk(ctx, docID, userID, chunk.Index, chunk.Title, chunk.Content, map[string]interface{}{
				"start_pos": chunk.StartPos,
				"end_pos":   chunk.EndPos,
				"rechunked": true,
			})
			if err != nil {
				slog.Warn("rechunk: failed to add chunk", "doc_id", docID, "index", chunk.Index, "error", err)
			}
		}

		// Update chunk count
		m.UpdateChunkCount(ctx, docID, len(chunks))

		results = append(results, RechunkResult{
			DocID:      docID,
			Title:      title,
			OldChunks:  oldChunkCount,
			NewChunks:  len(chunks),
			ContentLen: len([]rune(cleaned)),
		})

		slog.Info("rechunk: document re-chunked",
			"doc_id", docID, "title", title,
			"old_chunks", oldChunkCount, "new_chunks", len(chunks),
			"content_len", len([]rune(cleaned)),
		)
	}

	return results, nil
}

// cleanContentForRechunk applies whitelist article extraction to existing content.
// This is used when re-chunking old documents that were imported with
// the old (noisy) URL extractor. Only extracts author and body text.
func cleanContentForRechunk(content string) string {
	// Use the whitelist approach: only keep author + body paragraphs
	return extractArticleContent(content)
}

// ReimportAllURLs re-fetches all URL-imported documents from their original URLs,
// updates the content with the improved extractor, and re-chunks them.
// This is used to recover data lost by overly-aggressive cleaning.
func (m *KbManager) ReimportAllURLs(ctx context.Context, config ChunkConfig) ([]RechunkResult, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	// Get all active URL documents with their source URLs
	rows, err := m.db.QueryContext(ctx, `
		SELECT id::text, COALESCE(user_id, ''), title, metadata->>'source_url'
		FROM knowledge_base
		WHERE status = 'active' AND source_type = 'url'
		  AND metadata->>'source_url' IS NOT NULL
		  AND metadata->>'source_url' != ''
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query URL documents: %w", err)
	}
	defer rows.Close()

	importer := NewURLImporter(m, config)
	var results []RechunkResult

	for rows.Next() {
		var docID, userID, title, url string
		if err := rows.Scan(&docID, &userID, &title, &url); err != nil {
			continue
		}

		// Re-fetch the URL
		content, err := importer.fetchAndExtract(ctx, url)
		if err != nil {
			slog.Warn("reimport: failed to fetch URL", "doc_id", docID, "url", url, "error", err)
			continue
		}

		if len([]rune(content)) < 50 {
			slog.Warn("reimport: content too short", "doc_id", docID, "url", url, "len", len([]rune(content)))
			continue
		}

		// Update the document's content
		if _, err := m.db.ExecContext(ctx, `UPDATE knowledge_base SET content = $2, updated_at = NOW() WHERE id = $1`, docID, content); err != nil {
			slog.Warn("reimport: failed to update content", "doc_id", docID, "error", err)
			continue
		}

		// Re-chunk with the new config
		chunks := ChunkText(content, config)

		// Delete old chunks
		m.DeleteChunksByDocID(ctx, docID)

		// Insert new chunks
		for _, chunk := range chunks {
			_, err := m.AddChunk(ctx, docID, userID, chunk.Index, chunk.Title, chunk.Content, map[string]interface{}{
				"start_pos":   chunk.StartPos,
				"end_pos":     chunk.EndPos,
				"source_url":  url,
				"reimported":  true,
			})
			if err != nil {
				slog.Warn("reimport: failed to add chunk", "doc_id", docID, "index", chunk.Index, "error", err)
			}
		}

		m.UpdateChunkCount(ctx, docID, len(chunks))

		results = append(results, RechunkResult{
			DocID:      docID,
			Title:      title,
			NewChunks:  len(chunks),
			ContentLen: len([]rune(content)),
		})

		slog.Info("reimport: document re-imported",
			"doc_id", docID, "title", title, "url", url,
			"chunks", len(chunks), "content_len", len([]rune(content)),
		)
	}

	return results, nil
}

// ─── Hybrid Search (BM25 + Dense) ──────────────────────

// KbSearchResult is a single hybrid search result.
type KbSearchResult struct {
	DocID     string  `json:"doc_id"`
	ChunkID   string  `json:"chunk_id,omitempty"`
	Title     string  `json:"title"`
	Content   string  `json:"content"`
	Score     float64  `json:"score"`
	BM25Score float64  `json:"bm25_score,omitempty"`
	DenseScore float64 `json:"dense_score,omitempty"`
	Source    string  `json:"source"`
	UserID    string  `json:"user_id,omitempty"`
}

// HybridSearch performs a BM25 + Dense hybrid search on the knowledge base.
// It runs BM25 full-text search and pgvector cosine similarity in parallel,
// then combines the scores using reciprocal rank fusion (RRF).
func (m *KbManager) HybridSearch(ctx context.Context, userID, query string, limit int) ([]*KbSearchResult, error) {
	if m.db == nil {
		return []*KbSearchResult{}, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	// Generate query embedding
	var queryVec string
	if m.embedding != nil && m.embedding.IsConfigured() {
		vec, _, err := m.embedding.EmbedSingle(ctx, query)
		if err == nil {
			queryVec = tools.FormatVectorForPG(vec)
		}
	}

	// BM25 search (paradedb) — run on chunk level
	bm25Results := m.bm25Search(ctx, userID, query, limit*2)

	// Dense search (pgvector) — run on chunk level
	denseResults := m.denseSearch(ctx, userID, queryVec, limit*2)

	// Combine using RRF (Reciprocal Rank Fusion)
	combined := m.combineRRF(bm25Results, denseResults, m.config.BM25Weight, m.config.DenseWeight)

	// Also search at document level for fallback
	if len(combined) < limit {
		docResults := m.docLevelSearch(ctx, userID, query, queryVec, limit)
		combined = m.mergeDocResults(combined, docResults)
	}

	// The lower-level retrieval variants intentionally fall back among ParadeDB,
	// PostgreSQL FTS, and document-level search. If all of them return no rows,
	// distinguish a healthy empty knowledge base from an unavailable database;
	// callers must be able to degrade explicitly instead of presenting an outage
	// as a normal zero-result lookup.
	if len(combined) == 0 {
		if err := m.db.PingContext(ctx); err != nil {
			return nil, fmt.Errorf("local knowledge base unavailable: %w", err)
		}
	}

	// Sort by combined score and limit
	if len(combined) > limit {
		combined = combined[:limit]
	}

	return combined, nil
}

// SearchMode controls which retrieval method(s) to use.
type SearchMode string

const (
	SearchModeHybrid SearchMode = "hybrid" // BM25 + Dense + RRF (default)
	SearchModeBM25   SearchMode = "bm25"   // BM25 only
	SearchModeDense  SearchMode = "dense"  // Dense (vector) only
)

// HybridSearchWithMode performs a search with a specified mode and optional weight overrides.
// mode: "hybrid" (default), "bm25", or "dense"
// bm25Weight / denseWeight: if > 0, overrides the default config weights
func (m *KbManager) HybridSearchWithMode(ctx context.Context, userID, query string, limit int, mode SearchMode, bm25Weight, denseWeight float64) ([]*KbSearchResult, error) {
	if m.db == nil {
		return []*KbSearchResult{}, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	// Use default weights if not overridden
	if bm25Weight <= 0 {
		bm25Weight = m.config.BM25Weight
	}
	if denseWeight <= 0 {
		denseWeight = m.config.DenseWeight
	}

	// Generate query embedding (needed for dense and hybrid modes)
	var queryVec string
	if mode == SearchModeDense || mode == SearchModeHybrid {
		if m.embedding != nil && m.embedding.IsConfigured() {
			vec, _, err := m.embedding.EmbedSingle(ctx, query)
			if err == nil {
				queryVec = tools.FormatVectorForPG(vec)
			}
		}
	}

	switch mode {
	case SearchModeBM25:
		results := m.bm25Search(ctx, userID, query, limit)
		if len(results) > limit {
			results = results[:limit]
		}
		return results, nil

	case SearchModeDense:
		results := m.denseSearch(ctx, userID, queryVec, limit)
		if len(results) > limit {
			results = results[:limit]
		}
		return results, nil

	default: // hybrid
		bm25Results := m.bm25Search(ctx, userID, query, limit*2)
		denseResults := m.denseSearch(ctx, userID, queryVec, limit*2)
		combined := m.combineRRF(bm25Results, denseResults, bm25Weight, denseWeight)

		if len(combined) < limit {
			docResults := m.docLevelSearch(ctx, userID, query, queryVec, limit)
			combined = m.mergeDocResults(combined, docResults)
		}

		if len(combined) > limit {
			combined = combined[:limit]
		}
		return combined, nil
	}
}
func (m *KbManager) bm25Search(ctx context.Context, userID, query string, limit int) []*KbSearchResult {
	if m.db == nil {
		return nil
	}

	// Try paradedb BM25 search first
	q := `
		SELECT kc.id::text, kc.doc_id::text, COALESCE(kc.title, ''),
		       LEFT(kc.content, 500), paradedb.score(kc),
		       COALESCE(kb.source, ''), COALESCE(kc.user_id, '')
		FROM knowledge_chunks kc
		JOIN knowledge_base kb ON kc.doc_id = kb.id
		WHERE kc.content @@@ $1
		  AND (kc.user_id = $2 OR kc.user_id IS NULL)
		  AND kb.status != 'deleted'
		ORDER BY paradedb.score(kc) DESC
		LIMIT $3
	`
	rows, err := m.db.QueryContext(ctx, q, query, userID, limit)
	if err != nil {
		// Fallback to PostgreSQL full-text search
		slog.Debug("paradedb BM25 search failed, falling back to FTS", "error", err)
		return m.ftsSearch(ctx, userID, query, limit)
	}
	defer rows.Close()

	var results []*KbSearchResult
	for rows.Next() {
		var r KbSearchResult
		if err := rows.Scan(&r.ChunkID, &r.DocID, &r.Title, &r.Content, &r.BM25Score, &r.Source, &r.UserID); err != nil {
			continue
		}
		r.Score = r.BM25Score
		results = append(results, &r)
	}

	return results
}

// ftsSearch is a fallback to PostgreSQL built-in full-text search.
func (m *KbManager) ftsSearch(ctx context.Context, userID, query string, limit int) []*KbSearchResult {
	if m.db == nil {
		return nil
	}

	q := `
		SELECT kc.id::text, kc.doc_id::text, COALESCE(kc.title, ''),
		       LEFT(kc.content, 500),
		       ts_rank(to_tsvector('simple', kc.content), plainto_tsquery('simple', $1)) as rank,
		       COALESCE(kb.source, ''), COALESCE(kc.user_id, '')
		FROM knowledge_chunks kc
		JOIN knowledge_base kb ON kc.doc_id = kb.id
		WHERE to_tsvector('simple', kc.content) @@ plainto_tsquery('simple', $1)
		  AND (kc.user_id = $2 OR kc.user_id IS NULL)
		  AND kb.status != 'deleted'
		ORDER BY rank DESC
		LIMIT $3
	`
	rows, err := m.db.QueryContext(ctx, q, query, userID, limit)
	if err != nil {
		slog.Warn("FTS search also failed", "error", err)
		return nil
	}
	defer rows.Close()

	var results []*KbSearchResult
	for rows.Next() {
		var r KbSearchResult
		if err := rows.Scan(&r.ChunkID, &r.DocID, &r.Title, &r.Content, &r.BM25Score, &r.Source, &r.UserID); err != nil {
			continue
		}
		r.Score = r.BM25Score
		results = append(results, &r)
	}

	return results
}

// denseSearch performs a pgvector cosine similarity search on knowledge_chunks.
func (m *KbManager) denseSearch(ctx context.Context, userID, queryVec string, limit int) []*KbSearchResult {
	if m.db == nil || queryVec == "" {
		return nil
	}

	q := `
		SELECT kc.id::text, kc.doc_id::text, COALESCE(kc.title, ''),
		       LEFT(kc.content, 500),
		       1 - (kc.embedding <=> $1::vector) as similarity,
		       COALESCE(kb.source, ''), COALESCE(kc.user_id, '')
		FROM knowledge_chunks kc
		JOIN knowledge_base kb ON kc.doc_id = kb.id
		WHERE kc.embedding IS NOT NULL
		  AND (kc.user_id = $2 OR kc.user_id IS NULL)
		  AND kb.status != 'deleted'
		ORDER BY kc.embedding <=> $1::vector
		LIMIT $3
	`
	rows, err := m.db.QueryContext(ctx, q, queryVec, userID, limit)
	if err != nil {
		slog.Warn("dense vector search failed", "error", err)
		return nil
	}
	defer rows.Close()

	var results []*KbSearchResult
	for rows.Next() {
		var r KbSearchResult
		if err := rows.Scan(&r.ChunkID, &r.DocID, &r.Title, &r.Content, &r.DenseScore, &r.Source, &r.UserID); err != nil {
			continue
		}
		r.Score = r.DenseScore
		results = append(results, &r)
	}

	return results
}

// docLevelSearch performs a search at the document level (no chunks).
func (m *KbManager) docLevelSearch(ctx context.Context, userID, query, queryVec string, limit int) []*KbSearchResult {
	if m.db == nil {
		return nil
	}

	var results []*KbSearchResult

	// Doc-level BM25/FTS
	q := `
		SELECT id::text, '', title, LEFT(content, 500),
		       ts_rank(to_tsvector('simple', content), plainto_tsquery('simple', $1)) as rank,
		       COALESCE(source, ''), COALESCE(user_id, '')
		FROM knowledge_base
		WHERE to_tsvector('simple', content) @@ plainto_tsquery('simple', $1)
		  AND (user_id = $2 OR user_id IS NULL)
		  AND status != 'deleted'
		ORDER BY rank DESC
		LIMIT $3
	`
	rows, err := m.db.QueryContext(ctx, q, query, userID, limit)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var r KbSearchResult
			if err := rows.Scan(&r.DocID, &r.ChunkID, &r.Title, &r.Content, &r.BM25Score, &r.Source, &r.UserID); err != nil {
				continue
			}
			r.Score = r.BM25Score
			results = append(results, &r)
		}
	}

	// Doc-level vector search
	if queryVec != "" {
		qVec := `
			SELECT id::text, '', title, LEFT(content, 500),
			       1 - (embedding <=> $1::vector) as similarity,
			       COALESCE(source, ''), COALESCE(user_id, '')
			FROM knowledge_base
			WHERE embedding IS NOT NULL
			  AND (user_id = $2 OR user_id IS NULL)
			  AND status != 'deleted'
			ORDER BY embedding <=> $1::vector
			LIMIT $3
		`
		rows2, err := m.db.QueryContext(ctx, qVec, queryVec, userID, limit)
		if err == nil {
			defer rows2.Close()
			for rows2.Next() {
				var r KbSearchResult
				if err := rows2.Scan(&r.DocID, &r.ChunkID, &r.Title, &r.Content, &r.DenseScore, &r.Source, &r.UserID); err != nil {
					continue
				}
				r.Score = r.DenseScore
				results = append(results, &r)
			}
		}
	}

	return results
}

// combineRRF combines BM25 and Dense search results using Reciprocal Rank Fusion.
// RRF formula: score = sum(weight_i / (k + rank_i)) for each result list i
// where k=60 (standard RRF constant) and rank starts from 1.
func (m *KbManager) combineRRF(bm25Results, denseResults []*KbSearchResult, bm25Weight, denseWeight float64) []*KbSearchResult {
	const rrfK = 60.0

	scoreMap := make(map[string]*KbSearchResult)

	// Process BM25 results
	for i, r := range bm25Results {
		key := r.ChunkID
		if key == "" {
			key = r.DocID
		}
		if existing, ok := scoreMap[key]; ok {
			existing.Score += bm25Weight / (rrfK + float64(i+1))
			existing.BM25Score = r.BM25Score
		} else {
			r.Score = bm25Weight / (rrfK + float64(i+1))
			scoreMap[key] = r
		}
	}

	// Process Dense results
	for i, r := range denseResults {
		key := r.ChunkID
		if key == "" {
			key = r.DocID
		}
		if existing, ok := scoreMap[key]; ok {
			existing.Score += denseWeight / (rrfK + float64(i+1))
			existing.DenseScore = r.DenseScore
		} else {
			r.Score = denseWeight / (rrfK + float64(i+1))
			scoreMap[key] = r
		}
	}

	// Convert to slice and sort
	results := make([]*KbSearchResult, 0, len(scoreMap))
	for _, r := range scoreMap {
		results = append(results, r)
	}

	// Sort by score descending
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	return results
}

// mergeDocResults merges document-level results into chunk-level results
// when chunk-level search didn't return enough results.
func (m *KbManager) mergeDocResults(chunkResults, docResults []*KbSearchResult) []*KbSearchResult {
	seen := make(map[string]bool)
	for _, r := range chunkResults {
		key := r.ChunkID
		if key == "" {
			key = r.DocID
		}
		seen[key] = true
	}

	for _, r := range docResults {
		key := r.DocID
		if seen[key] {
			continue
		}
		chunkResults = append(chunkResults, r)
		seen[key] = true
	}

	return chunkResults
}

// ─── User Material Management (compat layer) ───────────

// UserMaterial is kept for backward compatibility with existing handlers.
// It maps to the same user_materials table but now uses doc_id instead of weknora_doc_id.
type UserMaterial struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Title          string    `json:"title"`
	ContentPreview string    `json:"content_preview"`
	SourceType     string    `json:"source_type"`
	SourceURL      string    `json:"source_url,omitempty"`
	FileName       string    `json:"file_name,omitempty"`
	FileSize       int64     `json:"file_size,omitempty"`
	DocID          string    `json:"doc_id"`
	ChunkCount     int       `json:"chunk_count"`
	FolderID       string    `json:"folder_id,omitempty"`
	Metadata       any       `json:"metadata,omitempty"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Governance     *MaterialGovernance `json:"governance,omitempty"`
}

// MaterialGovernance truthfully describes whether a library item can be
// captured by MaterialAdapter. It is not itself a Run Artifact.
type MaterialGovernance struct {
	Eligible        bool   `json:"eligible"`
	ArtifactType    string `json:"artifact_type"`
	SnapshotStatus  string `json:"snapshot_status"`
	IntegrityStatus string `json:"integrity_status"`
	SourceRef       string `json:"source_ref,omitempty"`
}

// MaterialFolder represents a user's material folder for organization.
type MaterialFolder struct {
	ID            string    `json:"id"`
	UserID        string    `json:"user_id"`
	Name          string    `json:"name"`
	ParentID      string    `json:"parent_id,omitempty"`
	SortOrder     int       `json:"sort_order"`
	Description   string    `json:"description,omitempty"`
	MaterialCount int       `json:"material_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// SaveMaterial saves a user material entry (now points to local KB document).
func (m *KbManager) SaveMaterial(ctx context.Context, mat *UserMaterial) error {
	if m.db == nil {
		return fmt.Errorf("database not available")
	}
	metadataJSON, _ := json.Marshal(mat.Metadata)

	var folderID any
	if mat.FolderID != "" {
		folderID = mat.FolderID
	}

	_, err := m.db.ExecContext(ctx, `
		INSERT INTO user_materials (id, user_id, title, content_preview, source_type, source_url, file_name, file_size, doc_id, chunk_count, folder_id, metadata, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW(), NOW())
		ON CONFLICT (id) DO UPDATE SET title = $3, content_preview = $4, doc_id = $9, chunk_count = $10, folder_id = $11, updated_at = NOW()
	`, mat.ID, mat.UserID, mat.Title, mat.ContentPreview, mat.SourceType,
		mat.SourceURL, mat.FileName, mat.FileSize, mat.DocID, mat.ChunkCount,
		folderID, metadataJSON, mat.Status)
	return err
}

// ListMaterials lists user materials with pagination.
func (m *KbManager) ListMaterials(ctx context.Context, userID string, page, pageSize int) ([]*UserMaterial, int, error) {
	return m.ListMaterialsInFolder(ctx, userID, "", page, pageSize)
}

// ListMaterialsInFolder lists user materials in a specific folder (empty = root, "all" = all folders).
func (m *KbManager) ListMaterialsInFolder(ctx context.Context, userID, folderID string, page, pageSize int) ([]*UserMaterial, int, error) {
	if m.db == nil {
		return nil, 0, fmt.Errorf("database not available")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var countQuery, dataQuery string
	var countArgs, dataArgs []interface{}

	if folderID == "all" {
		// All folders
		countQuery = `SELECT COUNT(*) FROM user_materials WHERE user_id = $1 AND status != 'deleted'`
		countArgs = []interface{}{userID}
		dataQuery = `
			SELECT id, user_id, title, content_preview, source_type,
			       COALESCE(source_url, ''), COALESCE(file_name, ''), COALESCE(file_size, 0),
			       COALESCE(doc_id::text, ''), COALESCE(chunk_count, 0),
			   COALESCE(folder_id::text, ''),
			   metadata, status, created_at, updated_at
			FROM user_materials
			WHERE user_id = $1 AND status != 'deleted'
			ORDER BY created_at DESC
			LIMIT $2 OFFSET $3`
		dataArgs = []interface{}{userID, pageSize, (page - 1) * pageSize}
	} else if folderID == "" {
		// Root folder (folder_id IS NULL)
		countQuery = `SELECT COUNT(*) FROM user_materials WHERE user_id = $1 AND status != 'deleted' AND folder_id IS NULL`
		countArgs = []interface{}{userID}
		dataQuery = `
			SELECT id, user_id, title, content_preview, source_type,
			       COALESCE(source_url, ''), COALESCE(file_name, ''), COALESCE(file_size, 0),
			       COALESCE(doc_id::text, ''), COALESCE(chunk_count, 0),
			   COALESCE(folder_id::text, ''),
			   metadata, status, created_at, updated_at
			FROM user_materials
			WHERE user_id = $1 AND status != 'deleted' AND folder_id IS NULL
			ORDER BY created_at DESC
			LIMIT $2 OFFSET $3`
		dataArgs = []interface{}{userID, pageSize, (page - 1) * pageSize}
	} else {
		// Specific folder
		countQuery = `SELECT COUNT(*) FROM user_materials WHERE user_id = $1 AND status != 'deleted' AND folder_id = $2`
		countArgs = []interface{}{userID, folderID}
		dataQuery = `
			SELECT id, user_id, title, content_preview, source_type,
			       COALESCE(source_url, ''), COALESCE(file_name, ''), COALESCE(file_size, 0),
			       COALESCE(doc_id::text, ''), COALESCE(chunk_count, 0),
			   COALESCE(folder_id::text, ''),
			   metadata, status, created_at, updated_at
			FROM user_materials
			WHERE user_id = $1 AND status != 'deleted' AND folder_id = $2
			ORDER BY created_at DESC
			LIMIT $3 OFFSET $4`
		dataArgs = []interface{}{userID, folderID, pageSize, (page - 1) * pageSize}
	}

	var total int
	if err := m.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := m.db.QueryContext(ctx, dataQuery, dataArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var materials []*UserMaterial
	for rows.Next() {
		var mat UserMaterial
		var metadataBytes []byte
		var sourceURL, fileName, docID, folderIDStr sql.NullString
		var fileSize sql.NullInt64
		var chunkCount sql.NullInt64

		if err := rows.Scan(
			&mat.ID, &mat.UserID, &mat.Title, &mat.ContentPreview, &mat.SourceType,
			&sourceURL, &fileName, &fileSize, &docID, &chunkCount,
			&folderIDStr,
			&metadataBytes, &mat.Status, &mat.CreatedAt, &mat.UpdatedAt,
		); err != nil {
			continue
		}
		mat.SourceURL = sourceURL.String
		mat.FileName = fileName.String
		mat.FileSize = fileSize.Int64
		mat.DocID = docID.String
		mat.ChunkCount = int(chunkCount.Int64)
		mat.FolderID = folderIDStr.String
		if len(metadataBytes) > 0 {
			json.Unmarshal(metadataBytes, &mat.Metadata)
		}
		materials = append(materials, &mat)
	}

	return materials, total, nil
}

// GetMaterial retrieves a single material by ID.
func (m *KbManager) GetMaterial(ctx context.Context, userID, materialID string) (*UserMaterial, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var mat UserMaterial
	var metadataBytes []byte
	var sourceURL, fileName, docID, folderIDStr sql.NullString
	var fileSize sql.NullInt64
	var chunkCount sql.NullInt64

	err := m.db.QueryRowContext(ctx, `
		SELECT id, user_id, title, content_preview, source_type,
		       COALESCE(source_url, ''), COALESCE(file_name, ''), COALESCE(file_size, 0),
		       COALESCE(doc_id::text, ''), COALESCE(chunk_count, 0),
		       COALESCE(folder_id::text, ''),
		       metadata, status, created_at, updated_at
		FROM user_materials
		WHERE id = $1 AND user_id = $2 AND status != 'deleted'
	`, materialID, userID).Scan(
		&mat.ID, &mat.UserID, &mat.Title, &mat.ContentPreview, &mat.SourceType,
		&sourceURL, &fileName, &fileSize, &docID, &chunkCount,
		&folderIDStr,
		&metadataBytes, &mat.Status, &mat.CreatedAt, &mat.UpdatedAt,
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
	mat.DocID = docID.String
	mat.ChunkCount = int(chunkCount.Int64)
	mat.FolderID = folderIDStr.String
	if len(metadataBytes) > 0 {
		json.Unmarshal(metadataBytes, &mat.Metadata)
	}

	return &mat, nil
}

// DeleteMaterial soft-deletes a material and its associated KB document.
func (m *KbManager) DeleteMaterial(ctx context.Context, userID, materialID string) error {
	if m.db == nil {
		return fmt.Errorf("database not available")
	}

	// Get the material to find the doc_id
	mat, err := m.GetMaterial(ctx, userID, materialID)
	if err != nil {
		return err
	}
	if mat == nil {
		return fmt.Errorf("material not found")
	}

	// Delete KB document (and cascading chunks) if local doc_id exists
	if mat.DocID != "" {
		if err := m.DeleteDocument(ctx, userID, mat.DocID); err != nil {
			slog.Warn("failed to delete KB document", "doc_id", mat.DocID, "error", err)
		}
	}

	// Soft-delete locally
	_, err = m.db.ExecContext(ctx, `
		UPDATE user_materials SET status = 'deleted', updated_at = NOW()
		WHERE id = $1 AND user_id = $2
	`, materialID, userID)
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
func (m *KbManager) AssociateMaterialWithTopic(ctx context.Context, topicID, materialID, userID string, associationType string, score float64) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO topic_materials (topic_id, material_id, user_id, association_type, relevance_score, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (topic_id, material_id) DO UPDATE SET association_type = $4, relevance_score = $5
	`, topicID, materialID, userID, associationType, score)
	return err
}

// ListTopicMaterials lists all materials associated with a topic.
func (m *KbManager) ListTopicMaterials(ctx context.Context, topicID, userID string) ([]*TopicMaterial, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, topic_id, material_id, user_id, association_type, relevance_score, created_at
		FROM topic_materials
		WHERE topic_id = $1 AND user_id = $2
		ORDER BY association_type DESC, relevance_score DESC, created_at DESC
	`, topicID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*TopicMaterial
	for rows.Next() {
		var tm TopicMaterial
		if err := rows.Scan(&tm.ID, &tm.TopicID, &tm.MaterialID, &tm.UserID, &tm.AssociationType, &tm.RelevanceScore, &tm.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, &tm)
	}
	return items, nil
}

// RemoveTopicMaterial removes a topic-material association.
func (m *KbManager) RemoveTopicMaterial(ctx context.Context, topicID, materialID, userID string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM topic_materials WHERE topic_id = $1 AND material_id = $2 AND user_id = $3`, topicID, materialID, userID)
	return err
}

// ─── Helpers ─────────────────────────────────────────────

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func truncateContent(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

func simpleContentHash(s string) string {
	h := uint64(14695981039346656037)
	for _, c := range s {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return fmt.Sprintf("%x", h)
}

// Ensure uuid is used
var _ = uuid.New

// ─── Stats ──────────────────────────────────────────────

// KbStats holds aggregate statistics about the knowledge base.
type KbStats struct {
	DocCount         int `json:"doc_count"`
	ChunkCount       int `json:"chunk_count"`
	ChunkWithEmbed   int `json:"chunk_with_embedding"`
	EntityCount      int `json:"entity_count"`
	RelationCount    int `json:"relation_count"`
	SourceBreakdown  map[string]int `json:"source_breakdown"`
}

// GetStats returns aggregate statistics about the knowledge base.
func (m *KbManager) GetStats(ctx context.Context) (*KbStats, error) {
	if m.db == nil {
		return &KbStats{}, nil
	}

	stats := &KbStats{SourceBreakdown: map[string]int{}}

	// Document count + source breakdown
	rows, err := m.db.QueryContext(ctx, `
		SELECT COALESCE(source_type, 'unknown'), COUNT(*)::int
		FROM knowledge_base
		WHERE user_id IS NULL OR user_id = ''
		GROUP BY source_type
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var src string
			var cnt int
			if rows.Scan(&src, &cnt) == nil {
				stats.SourceBreakdown[src] = cnt
				stats.DocCount += cnt
			}
		}
	}

	// Chunk count + embedding coverage
	m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)::int, COUNT(CASE WHEN embedding IS NOT NULL THEN 1 END)::int
		FROM knowledge_chunks
		WHERE user_id IS NULL OR user_id = ''
	`).Scan(&stats.ChunkCount, &stats.ChunkWithEmbed)

	// Entity count
	m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)::int FROM kb_entities
	`).Scan(&stats.EntityCount)

	// Relation count
	m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)::int FROM kb_relations
	`).Scan(&stats.RelationCount)

	return stats, nil
}

// GetDocEntities returns all entities extracted from a given document.
func (m *KbManager) GetDocEntities(ctx context.Context, docID string) ([]GraphEntity, error) {
	if m.graphRAG != nil && m.graphRAG.IsConfigured() {
		return m.graphRAG.GetDocEntities(ctx, docID)
	}
	return nil, nil
}

// GetDocRelations returns all relations for a given document.
func (m *KbManager) GetDocRelations(ctx context.Context, docID string) ([]map[string]interface{}, error) {
	if m.graphRAG != nil && m.graphRAG.IsConfigured() {
		return m.graphRAG.GetDocRelations(ctx, docID)
	}
	return nil, nil
}

// GetGlobalGraph returns the top entities and their relations for graph visualization.
func (m *KbManager) GetGlobalGraph(ctx context.Context, limit int) ([]GraphEntity, []GraphRelation, error) {
	if m.graphRAG != nil && m.graphRAG.IsConfigured() {
		nodes, edges, err := m.graphRAG.GetGlobalGraph(ctx, limit)
		if err != nil {
			return nil, nil, err
		}
		// Convert to GraphEntity/GraphRelation for JSON serialization
		entities := make([]GraphEntity, len(nodes))
		for i, n := range nodes {
			entities[i] = GraphEntity{
				ID:         n.ID,
				EntityName: n.Name,
				EntityType: n.Type,
				Attributes: map[string]interface{}{"doc_count": n.DocCount},
			}
		}
		relations := make([]GraphRelation, len(edges))
		for i, e := range edges {
			relations[i] = GraphRelation{
				SourceEntity: e.Source,
				TargetEntity: e.Target,
				RelationType: e.RelType,
			}
		}
		return entities, relations, nil
	}
	return nil, nil, nil
}

// ─── Multi-KB Management ────────────────────────────────

// KnowledgeBase represents a named knowledge base.
type KnowledgeBase struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	UserID      string     `json:"user_id,omitempty"`
	DocCount    int        `json:"doc_count"`
	ChunkCount  int        `json:"chunk_count"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// CreateKB creates a new named knowledge base.
func (m *KbManager) CreateKB(ctx context.Context, id, name, description, userID string) (*KnowledgeBase, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if id == "" {
		id = uuid.NewString()
	}

	var kb KnowledgeBase
	err := m.db.QueryRowContext(ctx, `
		INSERT INTO knowledge_bases (id, name, description, user_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (id) DO UPDATE SET name = $2, description = $3, updated_at = NOW()
		RETURNING id, name, COALESCE(description, ''), COALESCE(user_id, ''), doc_count, chunk_count, created_at, updated_at
	`, id, name, description, nullIfEmpty(userID)).Scan(
		&kb.ID, &kb.Name, &kb.Description, &kb.UserID, &kb.DocCount, &kb.ChunkCount, &kb.CreatedAt, &kb.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create KB: %w", err)
	}
	return &kb, nil
}

// ListKBs lists all knowledge bases.
func (m *KbManager) ListKBs(ctx context.Context, userID string) ([]*KnowledgeBase, error) {
	if m.db == nil {
		return []*KnowledgeBase{}, nil
	}

	rows, err := m.db.QueryContext(ctx, `
		SELECT kb.id, kb.name, COALESCE(kb.description, ''), COALESCE(kb.user_id, ''),
		       kb.doc_count, kb.chunk_count, kb.created_at, kb.updated_at
		FROM knowledge_bases kb
		WHERE ($1 = '' OR kb.user_id IS NULL OR kb.user_id = $1)
		ORDER BY kb.created_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var kbs []*KnowledgeBase
	for rows.Next() {
		var kb KnowledgeBase
		if err := rows.Scan(&kb.ID, &kb.Name, &kb.Description, &kb.UserID, &kb.DocCount, &kb.ChunkCount, &kb.CreatedAt, &kb.UpdatedAt); err != nil {
			continue
		}
		kbs = append(kbs, &kb)
	}
	return kbs, nil
}

// DeleteKB deletes a knowledge base and all its documents.
// The 'default' KB cannot be deleted.
func (m *KbManager) DeleteKB(ctx context.Context, kbID string) error {
	if m.db == nil {
		return fmt.Errorf("database not available")
	}
	if kbID == "" || kbID == "default" {
		return fmt.Errorf("cannot delete the default knowledge base")
	}

	// Delete all documents in this KB (cascades to chunks)
	_, err := m.db.ExecContext(ctx, "DELETE FROM knowledge_base WHERE kb_id = $1", kbID)
	if err != nil {
		return fmt.Errorf("failed to delete KB documents: %w", err)
	}

	// Delete the KB metadata
	_, err = m.db.ExecContext(ctx, "DELETE FROM knowledge_bases WHERE id = $1", kbID)
	if err != nil {
		return fmt.Errorf("failed to delete KB: %w", err)
	}
	return nil
}

// UpdateKB updates a knowledge base name and description.
func (m *KbManager) UpdateKB(ctx context.Context, kbID, name, description string) (*KnowledgeBase, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var kb KnowledgeBase
	err := m.db.QueryRowContext(ctx, `
		UPDATE knowledge_bases SET name = $2, description = $3, updated_at = NOW()
		WHERE id = $1
		RETURNING id, name, COALESCE(description, ''), COALESCE(user_id, ''), doc_count, chunk_count, created_at, updated_at
	`, kbID, name, description).Scan(
		&kb.ID, &kb.Name, &kb.Description, &kb.UserID, &kb.DocCount, &kb.ChunkCount, &kb.CreatedAt, &kb.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update KB: %w", err)
	}
	return &kb, nil
}

// ─── KB-scoped operations ────────────────────────────────

// AddDocumentToKB creates a document in a specific knowledge base.
func (m *KbManager) AddDocumentToKB(ctx context.Context, userID, kbID, title, content, sourceType string, metadata map[string]interface{}) (*KbDocument, error) {
	if kbID == "" {
		kbID = "default"
	}
	// Call AddDocument then set kb_id
	doc, err := m.AddDocument(ctx, userID, title, content, sourceType, metadata)
	if err != nil {
		return nil, err
	}
	// Update kb_id
	_, err = m.db.ExecContext(ctx, "UPDATE knowledge_base SET kb_id = $1 WHERE id = $2", kbID, doc.ID)
	if err != nil {
		slog.Warn("failed to set kb_id on document", "doc_id", doc.ID, "kb_id", kbID, "error", err)
	}
	doc.Metadata = map[string]interface{}{"kb_id": kbID}
	return doc, nil
}

// ListDocumentsInKB lists documents in a specific knowledge base.
func (m *KbManager) ListDocumentsInKB(ctx context.Context, userID, kbID string, page, pageSize int) ([]*KbDocument, int, error) {
	if m.db == nil {
		return []*KbDocument{}, 0, nil
	}
	if kbID == "" {
		kbID = "default"
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var total int
	err := m.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM knowledge_base WHERE kb_id = $1 AND (user_id = $2 OR user_id IS NULL) AND status != 'deleted'",
		kbID, userID,
	).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	rows, err := m.db.QueryContext(ctx, `
		SELECT id::text, COALESCE(user_id, ''), source, source_type, title,
		       LEFT(content, 500) as content_preview, status, chunk_count,
		       COALESCE(source_url, ''), COALESCE(file_name, ''), COALESCE(file_size, 0),
		       metadata, (embedding IS NOT NULL), created_at, updated_at
		FROM knowledge_base
		WHERE kb_id = $1 AND (user_id = $2 OR user_id IS NULL) AND status != 'deleted'
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4
	`, kbID, userID, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var docs []*KbDocument
	for rows.Next() {
		var doc KbDocument
		var metaResult []byte
		if err := rows.Scan(
			&doc.ID, &doc.UserID, &doc.Source, &doc.SourceType, &doc.Title,
			&doc.ContentPreview, &doc.Status, &doc.ChunkCount,
			&doc.SourceURL, &doc.FileName, &doc.FileSize,
			&metaResult, &doc.HasEmbedding, &doc.CreatedAt, &doc.UpdatedAt,
		); err != nil {
			continue
		}
		if len(metaResult) > 0 {
			json.Unmarshal(metaResult, &doc.Metadata)
		}
		docs = append(docs, &doc)
	}

	return docs, total, nil
}

// HybridSearchInKB performs a search scoped to a specific knowledge base.
func (m *KbManager) HybridSearchInKB(ctx context.Context, userID, kbID, query string, limit int, mode SearchMode, bm25Weight, denseWeight float64) ([]*KbSearchResult, error) {
	if m.db == nil {
		return []*KbSearchResult{}, nil
	}
	if kbID == "" {
		kbID = "default"
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	if bm25Weight <= 0 {
		bm25Weight = m.config.BM25Weight
	}
	if denseWeight <= 0 {
		denseWeight = m.config.DenseWeight
	}

	// Generate query embedding
	var queryVec string
	if mode == SearchModeDense || mode == SearchModeHybrid {
		if m.embedding != nil && m.embedding.IsConfigured() {
			vec, _, err := m.embedding.EmbedSingle(ctx, query)
			if err != nil {
				slog.Warn("failed to generate query embedding for KB search", "error", err, "query", query)
			} else {
				queryVec = tools.FormatVectorForPG(vec)
			}
		} else {
			slog.Warn("embedding client not configured for KB search")
		}
	}

	switch mode {
	case SearchModeBM25:
		return m.bm25SearchInKB(ctx, userID, kbID, query, limit), nil
	case SearchModeDense:
		return m.denseSearchInKB(ctx, userID, kbID, queryVec, limit), nil
	default:
		bm25Results := m.bm25SearchInKB(ctx, userID, kbID, query, limit*2)
		denseResults := m.denseSearchInKB(ctx, userID, kbID, queryVec, limit*2)
		combined := m.combineRRF(bm25Results, denseResults, bm25Weight, denseWeight)
		if len(combined) > limit {
			combined = combined[:limit]
		}
		return combined, nil
	}
}

// bm25SearchInKB performs BM25 search scoped to a knowledge base.
// Falls back to PostgreSQL FTS if paradedb BM25 index is not available.
func (m *KbManager) bm25SearchInKB(ctx context.Context, userID, kbID, query string, limit int) []*KbSearchResult {
	if m.db == nil {
		return nil
	}

	// Try paradedb BM25 search first (only if index exists)
	q := `
		SELECT kc.id::text, kc.doc_id::text, COALESCE(kc.title, ''),
		       LEFT(kc.content, 500), paradedb.score(kc),
		       COALESCE(kb.source, ''), COALESCE(kc.user_id, '')
		FROM knowledge_chunks kc
		JOIN knowledge_base kb ON kc.doc_id = kb.id
		WHERE kc.content @@@ $1
		  AND kc.kb_id = $2
		  AND (kc.user_id = $3 OR kc.user_id IS NULL)
		  AND kb.status != 'deleted'
		ORDER BY paradedb.score(kc) DESC
		LIMIT $4
	`
	rows, err := m.db.QueryContext(ctx, q, query, kbID, userID, limit)
	if err != nil {
		slog.Debug("BM25 search in KB failed, falling back to FTS", "error", err)
		return m.ftsSearchInKB(ctx, userID, kbID, query, limit)
	}
	defer rows.Close()

	var results []*KbSearchResult
	for rows.Next() {
		var r KbSearchResult
		if err := rows.Scan(&r.ChunkID, &r.DocID, &r.Title, &r.Content, &r.BM25Score, &r.Source, &r.UserID); err != nil {
			continue
		}
		r.Score = r.BM25Score
		results = append(results, &r)
	}
	return results
}

// ftsSearchInKB is a fallback full-text search scoped to a KB.
func (m *KbManager) ftsSearchInKB(ctx context.Context, userID, kbID, query string, limit int) []*KbSearchResult {
	rows, err := m.db.QueryContext(ctx, `
		SELECT kc.id::text, kc.doc_id::text, COALESCE(kc.title, ''),
		       LEFT(kc.content, 500),
		       ts_rank(to_tsvector('simple', kc.content), plainto_tsquery('simple', $1)) as score,
		       COALESCE(kb.source, ''), COALESCE(kc.user_id, '')
		FROM knowledge_chunks kc
		JOIN knowledge_base kb ON kc.doc_id = kb.id
		WHERE to_tsvector('simple', kc.content) @@ plainto_tsquery('simple', $1)
		  AND kc.kb_id = $2
		  AND (kc.user_id = $3 OR kc.user_id IS NULL)
		ORDER BY score DESC
		LIMIT $4
	`, query, kbID, userID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var results []*KbSearchResult
	for rows.Next() {
		var r KbSearchResult
		if err := rows.Scan(&r.ChunkID, &r.DocID, &r.Title, &r.Content, &r.BM25Score, &r.Source, &r.UserID); err != nil {
			continue
		}
		r.Score = r.BM25Score
		results = append(results, &r)
	}
	return results
}

// denseSearchInKB performs dense vector search scoped to a KB.
func (m *KbManager) denseSearchInKB(ctx context.Context, userID, kbID, queryVec string, limit int) []*KbSearchResult {
	if queryVec == "" {
		return nil
	}

	rows, err := m.db.QueryContext(ctx, `
		SELECT kc.id::text, kc.doc_id::text, COALESCE(kc.title, ''),
		       LEFT(kc.content, 500),
		       1 - (kc.embedding <=> $1::vector) as score,
		       COALESCE(kb.source, ''), COALESCE(kc.user_id, '')
		FROM knowledge_chunks kc
		JOIN knowledge_base kb ON kc.doc_id = kb.id
		WHERE kc.embedding IS NOT NULL
		  AND kc.kb_id = $2
		  AND (kc.user_id = $3 OR kc.user_id IS NULL)
		  AND kb.status != 'deleted'
		ORDER BY kc.embedding <=> $1::vector
		LIMIT $4
	`, queryVec, kbID, userID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var results []*KbSearchResult
	for rows.Next() {
		var r KbSearchResult
		if err := rows.Scan(&r.ChunkID, &r.DocID, &r.Title, &r.Content, &r.DenseScore, &r.Source, &r.UserID); err != nil {
			continue
		}
		r.Score = r.DenseScore
		results = append(results, &r)
	}
	return results
}

// GetStatsForKB returns statistics for a specific knowledge base.
func (m *KbManager) GetStatsForKB(ctx context.Context, kbID string) (*KbStats, error) {
	if m.db == nil {
		return &KbStats{}, nil
	}
	if kbID == "" {
		kbID = "default"
	}

	stats := &KbStats{SourceBreakdown: map[string]int{}}

	// Document count + source breakdown
	rows, err := m.db.QueryContext(ctx, `
		SELECT COALESCE(source_type, 'unknown'), COUNT(*)::int
		FROM knowledge_base
		WHERE kb_id = $1 AND (user_id IS NULL OR user_id = '')
		GROUP BY source_type
	`, kbID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var src string
			var cnt int
			if rows.Scan(&src, &cnt) == nil {
				stats.SourceBreakdown[src] = cnt
				stats.DocCount += cnt
			}
		}
	}

	// Chunk count + embedding coverage
	m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)::int, COUNT(CASE WHEN embedding IS NOT NULL THEN 1 END)::int
		FROM knowledge_chunks
		WHERE kb_id = $1 AND (user_id IS NULL OR user_id = '')
	`, kbID).Scan(&stats.ChunkCount, &stats.ChunkWithEmbed)

	// Entity count
	m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)::int FROM kb_entities e
		JOIN knowledge_base kb ON e.doc_id = kb.id
		WHERE kb.kb_id = $1
	`, kbID).Scan(&stats.EntityCount)

	// Relation count
	m.db.QueryRowContext(ctx, `
		SELECT COUNT(*)::int FROM kb_relations r
		JOIN knowledge_base kb ON r.doc_id = kb.id
		WHERE kb.kb_id = $1
	`, kbID).Scan(&stats.RelationCount)

	return stats, nil
}

// ─── Material Folder Management ──────────────────────────

// ListFolders lists all material folders for a user.
func (m *KbManager) ListFolders(ctx context.Context, userID string) ([]*MaterialFolder, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	rows, err := m.db.QueryContext(ctx, `
		SELECT f.id::text, f.user_id, f.name,
		       COALESCE(f.parent_id::text, ''),
		       f.sort_order, COALESCE(f.description, ''),
		       COALESCE((
		       		SELECT COUNT(*) FROM user_materials um
		       		WHERE um.folder_id = f.id AND um.status != 'deleted'
		       ), 0),
		       f.created_at, f.updated_at
		FROM material_folders f
		WHERE f.user_id = $1
		ORDER BY f.sort_order ASC, f.created_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []*MaterialFolder
	for rows.Next() {
		var f MaterialFolder
		if err := rows.Scan(
			&f.ID, &f.UserID, &f.Name, &f.ParentID,
			&f.SortOrder, &f.Description, &f.MaterialCount,
			&f.CreatedAt, &f.UpdatedAt,
		); err != nil {
			continue
		}
		folders = append(folders, &f)
	}

	return folders, nil
}

// CreateFolder creates a new material folder.
func (m *KbManager) CreateFolder(ctx context.Context, userID, name, parentID, description string) (*MaterialFolder, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if name == "" {
		return nil, fmt.Errorf("folder name is required")
	}

	var parentArg any
	if parentID != "" {
		parentArg = parentID
	}

	var f MaterialFolder
	err := m.db.QueryRowContext(ctx, `
		INSERT INTO material_folders (user_id, name, parent_id, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text, user_id, name, COALESCE(parent_id::text, ''), sort_order, COALESCE(description, ''), 0, created_at, updated_at
	`, userID, name, parentArg, description).Scan(
		&f.ID, &f.UserID, &f.Name, &f.ParentID,
		&f.SortOrder, &f.Description, &f.MaterialCount,
		&f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create folder: %w", err)
	}

	return &f, nil
}

// UpdateFolder updates a material folder's name and/or description.
func (m *KbManager) UpdateFolder(ctx context.Context, userID, folderID, name, description string) (*MaterialFolder, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if folderID == "" {
		return nil, fmt.Errorf("folder ID is required")
	}

	var f MaterialFolder
	err := m.db.QueryRowContext(ctx, `
		UPDATE material_folders
		SET name = COALESCE(NULLIF($3, ''), name),
		    description = COALESCE($4, description),
		    updated_at = NOW()
		WHERE id = $1 AND user_id = $2
		RETURNING id::text, user_id, name, COALESCE(parent_id::text, ''), sort_order, COALESCE(description, ''),
		          COALESCE((SELECT COUNT(*) FROM user_materials um WHERE um.folder_id = f.id AND um.status != 'deleted'), 0),
		          created_at, updated_at
	`, folderID, userID, name, description).Scan(
		&f.ID, &f.UserID, &f.Name, &f.ParentID,
		&f.SortOrder, &f.Description, &f.MaterialCount,
		&f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update folder: %w", err)
	}

	return &f, nil
}

// DeleteFolder deletes a material folder. Materials in it are moved to root (folder_id = NULL).
func (m *KbManager) DeleteFolder(ctx context.Context, userID, folderID string) error {
	if m.db == nil {
		return fmt.Errorf("database not available")
	}
	if folderID == "" {
		return fmt.Errorf("folder ID is required")
	}

	// Move materials to root first
	_, err := m.db.ExecContext(ctx, `
		UPDATE user_materials SET folder_id = NULL WHERE folder_id = $1 AND user_id = $2
	`, folderID, userID)
	if err != nil {
		return fmt.Errorf("failed to move materials: %w", err)
	}

	// Delete the folder
	_, err = m.db.ExecContext(ctx, `
		DELETE FROM material_folders WHERE id = $1 AND user_id = $2
	`, folderID, userID)
	if err != nil {
		return fmt.Errorf("failed to delete folder: %w", err)
	}

	return nil
}

// MoveMaterialToFolder moves a material to a different folder (empty = root).
func (m *KbManager) MoveMaterialToFolder(ctx context.Context, userID, materialID, folderID string) error {
	if m.db == nil {
		return fmt.Errorf("database not available")
	}
	if materialID == "" {
		return fmt.Errorf("material ID is required")
	}

	var folderArg any
	if folderID != "" {
		folderArg = folderID
	}

	_, err := m.db.ExecContext(ctx, `
		UPDATE user_materials SET folder_id = $3, updated_at = NOW()
		WHERE id = $1 AND user_id = $2 AND status != 'deleted'
	`, materialID, userID, folderArg)
	if err != nil {
		return fmt.Errorf("failed to move material: %w", err)
	}

	return nil
}
