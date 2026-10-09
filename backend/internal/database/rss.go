package database

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ─── RSS Subscriptions ──────────────────────────────────

// ErrRSSSubscriptionNotFound marks a missing or non-owned subscription.
var ErrRSSSubscriptionNotFound = errors.New("rss subscription not found")

// RSSSubscription is one user-subscribed feed. Items land as materials
// (source_type "rss") in TargetFolderID.
type RSSSubscription struct {
	ID               string    `json:"id"`
	UserID           string    `json:"user_id"`
	FeedURL          string    `json:"feed_url"`
	Title            string    `json:"title"`
	SiteURL          string    `json:"site_url"`
	Description      string    `json:"description"`
	TargetFolderID   string    `json:"target_folder_id,omitempty"`
	MaxItemsPerTick  int       `json:"max_items_per_tick"`
	ETag             string    `json:"-"`
	LastModified     string    `json:"-"`
	LastFetchedAt    *time.Time `json:"last_fetched_at,omitempty"`
	LastItemAt       *time.Time `json:"last_item_at,omitempty"`
	FailCount        int       `json:"fail_count"`
	LastError        string    `json:"last_error,omitempty"`
	IsActive         bool      `json:"is_active"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

const rssSubscriptionColumns = `
	id::text, user_id::text, feed_url, title, site_url, description,
	COALESCE(target_folder_id::text, ''), max_items_per_tick, etag, last_modified,
	last_fetched_at, last_item_at, fail_count, last_error, is_active,
	created_at, updated_at
`

func scanRSSSubscription(scan func(dest ...interface{}) error) (*RSSSubscription, error) {
	var s RSSSubscription
	err := scan(&s.ID, &s.UserID, &s.FeedURL, &s.Title, &s.SiteURL, &s.Description,
		&s.TargetFolderID, &s.MaxItemsPerTick, &s.ETag, &s.LastModified,
		&s.LastFetchedAt, &s.LastItemAt, &s.FailCount, &s.LastError, &s.IsActive,
		&s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// CreateRSSSubscription inserts one feed for the user.
func (r *AdminRepo) CreateRSSSubscription(ctx context.Context, s *RSSSubscription) (*RSSSubscription, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if !validUUID(s.UserID) {
		return nil, fmt.Errorf("invalid user id")
	}
	if s.TargetFolderID != "" && !validUUID(s.TargetFolderID) {
		return nil, fmt.Errorf("invalid target folder id")
	}
	if s.MaxItemsPerTick < 1 || s.MaxItemsPerTick > 20 {
		s.MaxItemsPerTick = 3
	}
	created, err := scanRSSSubscription(r.db.QueryRowContext(ctx, `
		INSERT INTO rss_subscriptions
			(user_id, feed_url, title, site_url, description, target_folder_id, max_items_per_tick)
		VALUES ($1::uuid, $2, $3, $4, $5, NULLIF($6, '')::uuid, $7)
		RETURNING `+rssSubscriptionColumns+`
	`, s.UserID, s.FeedURL, s.Title, s.SiteURL, s.Description, s.TargetFolderID, s.MaxItemsPerTick).Scan)
	if err != nil {
		return nil, err
	}
	return created, nil
}

// GetRSSSubscription retrieves one owned subscription.
func (r *AdminRepo) GetRSSSubscription(ctx context.Context, userID, id string) (*RSSSubscription, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if !validUUID(userID) || !validUUID(id) {
		return nil, ErrRSSSubscriptionNotFound
	}
	s, err := scanRSSSubscription(r.db.QueryRowContext(ctx, `
		SELECT `+rssSubscriptionColumns+`
		FROM rss_subscriptions
		WHERE id = $2::uuid AND user_id = $1::uuid
	`, userID, id).Scan)
	if err != nil {
		return nil, ErrRSSSubscriptionNotFound
	}
	return s, nil
}

// ListRSSSubscriptions lists the user's subscriptions (active first, newest
// first), including fetch bookkeeping for the management UI.
func (r *AdminRepo) ListRSSSubscriptions(ctx context.Context, userID string) ([]*RSSSubscription, error) {
	if r.db == nil || !validUUID(userID) {
		return []*RSSSubscription{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+rssSubscriptionColumns+`
		FROM rss_subscriptions
		WHERE user_id = $1::uuid
		ORDER BY is_active DESC, created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RSSSubscription
	for rows.Next() {
		s, err := scanRSSSubscription(rows.Scan)
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// UpdateRSSSubscription updates editable fields of an owned subscription.
func (r *AdminRepo) UpdateRSSSubscription(ctx context.Context, userID, id string, s *RSSSubscription) (*RSSSubscription, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if !validUUID(userID) || !validUUID(id) {
		return nil, ErrRSSSubscriptionNotFound
	}
	if s.MaxItemsPerTick < 1 || s.MaxItemsPerTick > 20 {
		s.MaxItemsPerTick = 3
	}
	updated, err := scanRSSSubscription(r.db.QueryRowContext(ctx, `
		UPDATE rss_subscriptions SET
			title = $3, target_folder_id = NULLIF($4, '')::uuid,
			max_items_per_tick = $5, is_active = $6, updated_at = NOW()
		WHERE id = $2::uuid AND user_id = $1::uuid
		RETURNING `+rssSubscriptionColumns+`
	`, userID, id, s.Title, s.TargetFolderID, s.MaxItemsPerTick, s.IsActive).Scan)
	if err != nil {
		return nil, ErrRSSSubscriptionNotFound
	}
	return updated, nil
}

// DeleteRSSSubscription removes an owned subscription. Imported materials
// are kept (they are the user's own content).
func (r *AdminRepo) DeleteRSSSubscription(ctx context.Context, userID, id string) error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}
	if !validUUID(userID) || !validUUID(id) {
		return ErrRSSSubscriptionNotFound
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM rss_subscriptions WHERE id = $2::uuid AND user_id = $1::uuid`, userID, id)
	return err
}

// MarkRSSFetchSuccess records a completed fetch tick (conditional-GET
// headers + new-item watermark).
func (r *AdminRepo) MarkRSSFetchSuccess(ctx context.Context, id, etag, lastModified string, lastItemAt *time.Time) error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE rss_subscriptions SET
			etag = $2, last_modified = $3, last_fetched_at = NOW(),
			last_item_at = COALESCE($4, last_item_at),
			fail_count = 0, last_error = '', updated_at = NOW()
		WHERE id = $1::uuid
	`, id, etag, lastModified, lastItemAt)
	return err
}

// MarkRSSFetchFailure records a failed fetch tick with bounded error text.
func (r *AdminRepo) MarkRSSFetchFailure(ctx context.Context, id string, fetchErr error) error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}
	msg := ""
	if fetchErr != nil {
		msg = fetchErr.Error()
		if len(msg) > 500 {
			msg = msg[:500]
		}
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE rss_subscriptions SET
			fail_count = fail_count + 1, last_error = $2,
			last_fetched_at = NOW(), updated_at = NOW()
		WHERE id = $1::uuid
	`, id, msg)
	return err
}

// ListDueRSSSubscriptions returns active subscriptions whose next tick is
// due: never-fetched first, then oldest fetch. The per-tick global budget is
// enforced by the caller (LIMIT).
func (r *AdminRepo) ListDueRSSSubscriptions(ctx context.Context, limit int) ([]*RSSSubscription, error) {
	if r.db == nil || limit < 1 {
		return []*RSSSubscription{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+rssSubscriptionColumns+`
		FROM rss_subscriptions
		WHERE is_active
		ORDER BY last_fetched_at NULLS FIRST
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RSSSubscription
	for rows.Next() {
		s, err := scanRSSSubscription(rows.Scan)
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// HasMaterialSourceURL reports whether the user already imported this URL
// (dedup backstop for feeds without stable GUIDs).
func (r *AdminRepo) HasMaterialSourceURL(ctx context.Context, userID, sourceURL string) (bool, error) {
	if r.db == nil || sourceURL == "" {
		return false, nil
	}
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM knowledge_base
		WHERE user_id = $1 AND metadata->>'source_url' = $2
	`, userID, sourceURL).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
