package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

type EvidenceRepo struct {
	db *DB
}

type Evidence struct {
	ID           string                 `json:"id"`
	TraceID      string                 `json:"trace_id"`
	EvidenceType string                 `json:"evidence_type"`
	SourceURL    string                 `json:"source_url"`
	SourceDomain string                 `json:"source_domain"`
	ContentHash  string                 `json:"content_hash"`
	Snippet      string                 `json:"snippet"`
	TrustLevel   string                 `json:"trust_level"`
	Confidence   float64                `json:"confidence"`
	Metadata     map[string]interface{} `json:"metadata"`
	ExpiresAt    *time.Time             `json:"expires_at,omitempty"`
	CreatedAt    time.Time              `json:"created_at"`
}

func NewEvidenceRepo(db *DB) *EvidenceRepo {
	return &EvidenceRepo{db: db}
}

func (r *EvidenceRepo) SaveEvidence(ctx context.Context, ev *Evidence) error {
	if r.db == nil {
		return nil
	}
	metadataJSON, _ := json.Marshal(ev.Metadata)
	var expiresAt interface{}
	if ev.ExpiresAt != nil {
		expiresAt = ev.ExpiresAt
	} else {
		expiresAt = nil
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO trace_evidence (trace_id, evidence_type, source_url, source_domain, content_hash, snippet, trust_level, confidence, metadata, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, ev.TraceID, ev.EvidenceType, ev.SourceURL, ev.SourceDomain, ev.ContentHash, ev.Snippet, ev.TrustLevel, ev.Confidence, string(metadataJSON), expiresAt)
	return err
}

func (r *EvidenceRepo) GetEvidence(ctx context.Context, traceID string) ([]Evidence, error) {
	if r.db == nil {
		return []Evidence{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, trace_id, evidence_type, source_url, source_domain, content_hash, snippet, trust_level, confidence, metadata, expires_at, created_at
		FROM trace_evidence WHERE trace_id = $1 ORDER BY created_at
	`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evidence []Evidence
	for rows.Next() {
		var ev Evidence
		var expiresAt sql.NullTime
		var metadataJSON []byte
		if err := rows.Scan(&ev.ID, &ev.TraceID, &ev.EvidenceType, &ev.SourceURL, &ev.SourceDomain, &ev.ContentHash, &ev.Snippet, &ev.TrustLevel, &ev.Confidence, &metadataJSON, &expiresAt, &ev.CreatedAt); err != nil {
			continue
		}
		json.Unmarshal(metadataJSON, &ev.Metadata)
		if expiresAt.Valid {
			ev.ExpiresAt = &expiresAt.Time
		}
		evidence = append(evidence, ev)
	}
	return evidence, nil
}

func (r *EvidenceRepo) SaveSearchEvidence(ctx context.Context, traceID string, results []engine.SearchResult) error {
	for _, sr := range results {
		hash := sha256.Sum256([]byte(sr.Snippet))
		ev := Evidence{
			TraceID:      traceID,
			EvidenceType: "search_result",
			SourceURL:    sr.URL,
			SourceDomain: extractDomain(sr.URL),
			ContentHash:  hex.EncodeToString(hash[:]),
			Snippet:      sr.Snippet,
			TrustLevel:   "unverified",
			Confidence:   sr.CredibilityScore,
			Metadata:     map[string]interface{}{"score": sr.Score, "relevance": sr.Relevance},
		}
		if err := r.SaveEvidence(ctx, &ev); err != nil {
			slog.Warn("failed to save evidence", "error", err)
		}
	}
	return nil
}

func extractDomain(url string) string {
	if len(url) == 0 {
		return ""
	}
	for i, c := range url {
		if i > 7 && c == '/' {
			return url[:i]
		}
	}
	return url
}
