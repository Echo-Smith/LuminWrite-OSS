// Governed artifact content store (V3.0 M0b-2c, migration 104). Content-
// addressed, immutable, canonical counterpart to the shadow content store:
// the governed runtime's canonical ContentGateway persists artifact bodies
// here keyed by their sha256, so identical bytes dedupe and nothing rewrites
// a stored body.
package writingstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
)

var artifactContentHash = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// PutArtifactContent stores one content-addressed body. Re-staging identical
// bytes is a no-op (ON CONFLICT DO NOTHING); the immutable trigger forbids
// rewriting a stored body.
func (s *Store) PutArtifactContent(ctx context.Context, contentHash, mediaType string, body []byte) error {
	if !artifactContentHash.MatchString(contentHash) {
		return fmt.Errorf("%w: artifact content hash must be sha256:<64 hex>", ErrInvalidRecord)
	}
	if mediaType == "" || len(body) == 0 {
		return fmt.Errorf("%w: artifact content requires media type and non-empty body", ErrInvalidRecord)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO writing_artifact_contents (content_hash, media_type, body)
		VALUES ($1, $2, $3)
		ON CONFLICT (content_hash) DO NOTHING
	`, contentHash, mediaType, body); err != nil {
		return fmt.Errorf("put artifact content: %w", err)
	}
	return nil
}

// GetArtifactContent loads a stored body by its content hash.
func (s *Store) GetArtifactContent(ctx context.Context, contentHash string) (mediaType string, body []byte, err error) {
	if err := s.db.QueryRowContext(ctx, `
		SELECT media_type, body FROM writing_artifact_contents WHERE content_hash=$1
	`, contentHash).Scan(&mediaType, &body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, ErrNotFound
		}
		return "", nil, fmt.Errorf("get artifact content: %w", err)
	}
	return mediaType, body, nil
}
