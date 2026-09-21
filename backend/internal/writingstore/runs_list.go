package writingstore

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RunListItem is one row of an owner's governed run history: the sidebar's
// primary history source after the source-of-truth migration (governed
// writing_runs first, legacy agent_traces demoted to read-only history).
type RunListItem struct {
	RunID       string
	DocumentID  string
	Title       string // from writing_documents.title
	Status      string
	StyleSlug   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
}

// ListRunsByOwner pages the governed runs whose document belongs to ownerID,
// newest first, with the total count for pagination.
//
// Ownership follows the run's document (writing_documents.owner_user_id): the
// runtime projection already resolves run.OwnerUserID from that column, and
// the API's GetRun authorizes through document ownership — comparing the
// owner as text here matches exactly that semantics.
func (s *Store) ListRunsByOwner(ctx context.Context, ownerID string, limit, offset int) ([]RunListItem, int, error) {
	if s == nil {
		return nil, 0, fmt.Errorf("%w: store is required", ErrInvalidRecord)
	}
	if strings.TrimSpace(ownerID) == "" {
		return nil, 0, fmt.Errorf("%w: owner_user_id is required", ErrInvalidRecord)
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM writing_runs r
		JOIN writing_documents d ON d.document_id = r.document_id
		WHERE d.owner_user_id::text = $1
	`, ownerID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count writing runs by owner: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.run_id, r.document_id, d.title, r.status,
		       COALESCE(r.style_slug, ''), r.created_at, r.updated_at, r.completed_at
		FROM writing_runs r
		JOIN writing_documents d ON d.document_id = r.document_id
		WHERE d.owner_user_id::text = $1
		ORDER BY r.created_at DESC, r.run_id DESC
		LIMIT $2 OFFSET $3
	`, ownerID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list writing runs by owner: %w", err)
	}
	defer rows.Close()
	items := make([]RunListItem, 0)
	for rows.Next() {
		var item RunListItem
		if err := rows.Scan(&item.RunID, &item.DocumentID, &item.Title, &item.Status,
			&item.StyleSlug, &item.CreatedAt, &item.UpdatedAt, &item.CompletedAt); err != nil {
			return nil, 0, fmt.Errorf("scan writing run list item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate writing runs by owner: %w", err)
	}
	return items, total, nil
}
