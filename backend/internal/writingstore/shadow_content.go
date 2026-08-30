package writingstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var shadowKeyPattern = regexp.MustCompile(`^[0-9a-f]{64}/[A-Za-z0-9._-]+/[0-9a-f]{64}$`)

type ShadowContentRecord struct {
	Key         string
	PolicyHash  string
	RunID       string
	MediaType   string
	Body        []byte
	ContentHash string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

func (s *Store) PutShadowContent(ctx context.Context, record ShadowContentRecord) error {
	if s == nil {
		return fmt.Errorf("%w: shadow content store is required", ErrInvalidRecord)
	}
	if err := validateShadowContentRecord(record); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO writing_shadow_content (
			shadow_key, policy_hash, run_id, media_type, body, content_hash, created_at, expires_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (shadow_key) DO NOTHING
	`, record.Key, record.PolicyHash, record.RunID, record.MediaType, record.Body,
		record.ContentHash, record.CreatedAt, record.ExpiresAt)
	if err != nil {
		return fmt.Errorf("put shadow content: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 1 {
		return nil
	}
	var savedHash, savedPolicy, savedRun, savedMedia string
	if err := s.db.QueryRowContext(ctx, `
		SELECT content_hash, policy_hash, run_id, media_type
		FROM writing_shadow_content WHERE shadow_key=$1
	`, record.Key).Scan(&savedHash, &savedPolicy, &savedRun, &savedMedia); err != nil {
		return fmt.Errorf("load replayed shadow content: %w", err)
	}
	if savedHash != record.ContentHash || savedPolicy != record.PolicyHash || savedRun != record.RunID || savedMedia != record.MediaType {
		return fmt.Errorf("%w: shadow key was replayed with different content", ErrImmutableConflict)
	}
	return nil
}

func (s *Store) GetShadowContent(ctx context.Context, key string) ([]byte, error) {
	if s == nil || !shadowKeyPattern.MatchString(key) {
		return nil, fmt.Errorf("%w: invalid shadow key", ErrInvalidRecord)
	}
	var body []byte
	var hash string
	err := s.db.QueryRowContext(ctx, `
		SELECT body, content_hash FROM writing_shadow_content
		WHERE shadow_key=$1 AND expires_at > CURRENT_TIMESTAMP
	`, key).Scan(&body, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get shadow content: %w", err)
	}
	if hashShadowBody(body) != hash {
		return nil, fmt.Errorf("%w: stored shadow body hash mismatch", ErrImmutableConflict)
	}
	return append([]byte(nil), body...), nil
}

func (s *Store) DeleteShadowContentPrefix(ctx context.Context, prefix string) (int, error) {
	if s == nil || !validShadowPrefix(prefix) {
		return 0, fmt.Errorf("%w: invalid shadow prefix", ErrInvalidRecord)
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM writing_shadow_content WHERE LEFT(shadow_key, LENGTH($1))=$1`, prefix)
	if err != nil {
		return 0, fmt.Errorf("delete shadow content prefix: %w", err)
	}
	rows, err := result.RowsAffected()
	return int(rows), err
}

func (s *Store) DeleteShadowContentBefore(ctx context.Context, cutoff time.Time) (int, error) {
	if s == nil || cutoff.IsZero() {
		return 0, fmt.Errorf("%w: shadow cutoff is required", ErrInvalidRecord)
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM writing_shadow_content WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete expired shadow content: %w", err)
	}
	rows, err := result.RowsAffected()
	return int(rows), err
}

func validateShadowContentRecord(record ShadowContentRecord) error {
	if !shadowKeyPattern.MatchString(record.Key) || !sha256Pattern.MatchString(record.PolicyHash) ||
		!sha256Pattern.MatchString(record.ContentHash) || validateID(record.RunID, "run_", "run_id") != nil ||
		strings.TrimSpace(record.MediaType) == "" || len(record.Body) == 0 || record.CreatedAt.IsZero() ||
		!record.ExpiresAt.After(record.CreatedAt) {
		return fmt.Errorf("%w: invalid shadow content record", ErrInvalidRecord)
	}
	parts := strings.Split(record.Key, "/")
	if record.PolicyHash != "sha256:"+parts[0] || record.ContentHash != "sha256:"+parts[2] ||
		!strings.HasPrefix(parts[1], record.RunID+"-") || hashShadowBody(record.Body) != record.ContentHash {
		return fmt.Errorf("%w: shadow content identity or hash mismatch", ErrInvalidRecord)
	}
	return nil
}

func validShadowPrefix(prefix string) bool {
	parts := strings.Split(prefix, "/")
	if len(parts) != 2 || len(parts[0]) != 64 || !strings.HasSuffix(parts[1], "-") {
		return false
	}
	return shadowKeyPattern.MatchString(parts[0] + "/" + strings.TrimSuffix(parts[1], "-") + "-/" + strings.Repeat("0", 64))
}

func hashShadowBody(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
