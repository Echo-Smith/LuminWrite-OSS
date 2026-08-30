package writingstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// CanonicalContentRefPrefix marks content refs served by the canonical
// content store. Governed artifacts reference bodies through
// 'db://canonical/<content_key>'.
const CanonicalContentRefPrefix = "db://canonical/"

var canonicalKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,500}$`)

type CanonicalContentRecord struct {
	Key         string
	MediaType   string
	Body        []byte
	ContentHash string
	CreatedAt   time.Time
}

// PutCanonicalContent stores one canonical artifact body. Writes are
// idempotent per key: replaying the same body succeeds, replaying a
// different body under an existing key fails closed so committed artifact
// references can never be silently rewritten.
func (s *Store) PutCanonicalContent(ctx context.Context, record CanonicalContentRecord) error {
	if s == nil {
		return fmt.Errorf("%w: canonical content store is required", ErrInvalidRecord)
	}
	if err := validateCanonicalContentRecord(record); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO writing_canonical_content (content_key, media_type, body, content_hash, created_at)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (content_key) DO NOTHING
	`, record.Key, record.MediaType, record.Body, record.ContentHash, record.CreatedAt)
	if err != nil {
		return fmt.Errorf("put canonical content: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 1 {
		return nil
	}
	var savedHash, savedMedia string
	if err := s.db.QueryRowContext(ctx, `
		SELECT content_hash, media_type FROM writing_canonical_content WHERE content_key=$1
	`, record.Key).Scan(&savedHash, &savedMedia); err != nil {
		return fmt.Errorf("load replayed canonical content: %w", err)
	}
	if savedHash != record.ContentHash || savedMedia != record.MediaType {
		return fmt.Errorf("%w: canonical key was replayed with different content", ErrImmutableConflict)
	}
	return nil
}

// GetCanonicalContent returns the stored body after re-verifying the stored
// content hash: the recovery boundary for restarts and reloads.
func (s *Store) GetCanonicalContent(ctx context.Context, key string) ([]byte, error) {
	if s == nil || !canonicalKeyPattern.MatchString(key) {
		return nil, fmt.Errorf("%w: invalid canonical content key", ErrInvalidRecord)
	}
	var body []byte
	var hash string
	err := s.db.QueryRowContext(ctx, `
		SELECT body, content_hash FROM writing_canonical_content WHERE content_key=$1
	`, key).Scan(&body, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get canonical content: %w", err)
	}
	if hashShadowBody(body) != hash {
		return nil, fmt.Errorf("%w: stored canonical body hash mismatch", ErrImmutableConflict)
	}
	return append([]byte(nil), body...), nil
}

func validateCanonicalContentRecord(record CanonicalContentRecord) error {
	if !canonicalKeyPattern.MatchString(record.Key) || strings.TrimSpace(record.MediaType) == "" ||
		len(record.Body) == 0 || record.CreatedAt.IsZero() {
		return fmt.Errorf("%w: invalid canonical content record", ErrInvalidRecord)
	}
	if hashShadowBody(record.Body) != record.ContentHash {
		return fmt.Errorf("%w: canonical content hash does not match body", ErrInvalidRecord)
	}
	return nil
}
