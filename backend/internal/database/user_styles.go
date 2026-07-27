package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ─── Types ───────────────────────────────────────────────

// UserStyleProfile is the user-owned style profile record.
type UserStyleProfile struct {
	ID            string    `json:"id"`
	OwnerUserID   string    `json:"owner_user_id"`
	Slug          string    `json:"slug"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Status        string    `json:"status"` // draft | pending_review | approved | rejected
	CurrentVersion int      `json:"current_version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// UserStyleProfileVersion is an immutable snapshot of a style config.
type UserStyleProfileVersion struct {
	ID        string    `json:"id"`
	ProfileID string    `json:"profile_id"`
	Version   int       `json:"version"`
	Config    string    `json:"config"` // JSON string
	Changelog string    `json:"changelog"`
	CreatedAt time.Time `json:"created_at"`
}

// StyleReviewRequest is a review request bound to a specific version.
type StyleReviewRequest struct {
	ID                string     `json:"id"`
	ProfileID         string     `json:"profile_id"`
	SubmittedVersionID string    `json:"submitted_version_id"`
	Status            string     `json:"status"` // pending | approved | rejected
	ReviewNote        string     `json:"review_note"`
	ReviewedBy        string     `json:"reviewed_by"`
	ReviewedAt        *time.Time `json:"reviewed_at"`
	CreatedAt         time.Time  `json:"created_at"`

	// Joined fields (optional)
	ProfileName    string `json:"profile_name,omitempty"`
	ProfileSlug    string `json:"profile_slug,omitempty"`
	OwnerUserID    string `json:"owner_user_id,omitempty"`
	VersionNumber  int    `json:"version_number,omitempty"`
	VersionConfig  string `json:"version_config,omitempty"`
}

// ─── Store ───────────────────────────────────────────────

// UserStyleStore handles user style profile CRUD and review workflow.
type UserStyleStore struct {
	db *DB
}

// NewUserStyleStore creates a new UserStyleStore.
func NewUserStyleStore(db *DB) *UserStyleStore {
	return &UserStyleStore{db: db}
}

// ─── Profile CRUD ────────────────────────────────────────

// CreateProfile creates a new user style profile (status=draft).
func (s *UserStyleStore) CreateProfile(ctx context.Context, ownerUserID, slug, name, description string) (*UserStyleProfile, error) {
	var p UserStyleProfile
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO user_style_profiles (owner_user_id, slug, name, description, status, current_version)
		VALUES ($1, $2, $3, $4, 'draft', 0)
		RETURNING id, owner_user_id::text, slug, name, description, status, current_version, created_at, updated_at
	`, ownerUserID, slug, name, description).Scan(
		&p.ID, &p.OwnerUserID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CurrentVersion, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create user style profile: %w", err)
	}
	return &p, nil
}

// GetProfile returns a single user style profile by ID.
func (s *UserStyleStore) GetProfile(ctx context.Context, id string) (*UserStyleProfile, error) {
	var p UserStyleProfile
	err := s.db.QueryRowContext(ctx, `
		SELECT id, owner_user_id::text, slug, name, description, status, current_version, created_at, updated_at
		FROM user_style_profiles WHERE id = $1
	`, id).Scan(
		&p.ID, &p.OwnerUserID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CurrentVersion, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get user style profile: %w", err)
	}
	return &p, nil
}

// ListProfilesByOwner returns all style profiles for a user.
func (s *UserStyleStore) ListProfilesByOwner(ctx context.Context, ownerUserID string) ([]UserStyleProfile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, owner_user_id::text, slug, name, description, status, current_version, created_at, updated_at
		FROM user_style_profiles
		WHERE owner_user_id = $1
		ORDER BY updated_at DESC
	`, ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("list user style profiles: %w", err)
	}
	defer rows.Close()

	var profiles []UserStyleProfile
	for rows.Next() {
		var p UserStyleProfile
		if err := rows.Scan(&p.ID, &p.OwnerUserID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CurrentVersion, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	return profiles, nil
}

// UpdateProfile updates the mutable fields of a user style profile.
func (s *UserStyleStore) UpdateProfile(ctx context.Context, id, name, description string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE user_style_profiles SET name = $2, description = $3, updated_at = NOW()
		WHERE id = $1
	`, id, name, description)
	if err != nil {
		return fmt.Errorf("update user style profile: %w", err)
	}
	return nil
}

// DeleteProfile deletes a user style profile and its versions (cascade).
func (s *UserStyleStore) DeleteProfile(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM user_style_profiles WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete user style profile: %w", err)
	}
	return nil
}

// ─── Version Management ──────────────────────────────────

// SaveVersion creates a new immutable version snapshot and bumps current_version.
func (s *UserStyleStore) SaveVersion(ctx context.Context, profileID string, config json.RawMessage, changelog string) (*UserStyleProfileVersion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Lock and get current version
	var currentVersion int
	err = tx.QueryRowContext(ctx, `
		SELECT current_version FROM user_style_profiles WHERE id = $1 FOR UPDATE
	`, profileID).Scan(&currentVersion)
	if err != nil {
		return nil, fmt.Errorf("lock profile: %w", err)
	}

	newVersion := currentVersion + 1
	versionID := uuid.NewString()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_style_profile_versions (id, profile_id, version, config, changelog)
		VALUES ($1, $2, $3, $4, $5)
	`, versionID, profileID, newVersion, []byte(config), changelog)
	if err != nil {
		return nil, fmt.Errorf("insert version: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE user_style_profiles SET current_version = $2, updated_at = NOW() WHERE id = $1
	`, profileID, newVersion)
	if err != nil {
		return nil, fmt.Errorf("update current_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return &UserStyleProfileVersion{
		ID:        versionID,
		ProfileID: profileID,
		Version:   newVersion,
		Config:    string(config),
		Changelog: changelog,
	}, nil
}

// GetLatestVersion returns the latest version snapshot for a profile.
func (s *UserStyleStore) GetLatestVersion(ctx context.Context, profileID string) (*UserStyleProfileVersion, error) {
	var v UserStyleProfileVersion
	err := s.db.QueryRowContext(ctx, `
		SELECT id, profile_id, version, config::text, changelog, created_at
		FROM user_style_profile_versions
		WHERE profile_id = $1
		ORDER BY version DESC
		LIMIT 1
	`, profileID).Scan(&v.ID, &v.ProfileID, &v.Version, &v.Config, &v.Changelog, &v.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get latest version: %w", err)
	}
	return &v, nil
}

// GetVersion returns a specific version by ID.
func (s *UserStyleStore) GetVersion(ctx context.Context, versionID string) (*UserStyleProfileVersion, error) {
	var v UserStyleProfileVersion
	err := s.db.QueryRowContext(ctx, `
		SELECT id, profile_id, version, config::text, changelog, created_at
		FROM user_style_profile_versions WHERE id = $1
	`, versionID).Scan(&v.ID, &v.ProfileID, &v.Version, &v.Config, &v.Changelog, &v.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get version: %w", err)
	}
	return &v, nil
}

// ListVersions returns all versions for a profile.
func (s *UserStyleStore) ListVersions(ctx context.Context, profileID string) ([]UserStyleProfileVersion, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, profile_id, version, config::text, changelog, created_at
		FROM user_style_profile_versions
		WHERE profile_id = $1
		ORDER BY version DESC
	`, profileID)
	if err != nil {
		return nil, fmt.Errorf("list versions: %w", err)
	}
	defer rows.Close()

	var versions []UserStyleProfileVersion
	for rows.Next() {
		var v UserStyleProfileVersion
		if err := rows.Scan(&v.ID, &v.ProfileID, &v.Version, &v.Config, &v.Changelog, &v.CreatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, nil
}

// ─── Review Workflow ─────────────────────────────────────

// SubmitForReview creates a review request bound to the latest version snapshot.
// The profile status changes to pending_review.
func (s *UserStyleStore) SubmitForReview(ctx context.Context, profileID string) (*StyleReviewRequest, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Get latest version
	var versionID string
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM user_style_profile_versions
		WHERE profile_id = $1
		ORDER BY version DESC LIMIT 1
	`, profileID).Scan(&versionID)
	if err != nil {
		return nil, fmt.Errorf("get latest version for review: %w", err)
	}

	// Update profile status
	_, err = tx.ExecContext(ctx, `
		UPDATE user_style_profiles SET status = 'pending_review', updated_at = NOW() WHERE id = $1
	`, profileID)
	if err != nil {
		return nil, fmt.Errorf("update profile status: %w", err)
	}

	// Create review request
	reqID := uuid.NewString()
	var r StyleReviewRequest
	err = tx.QueryRowContext(ctx, `
		INSERT INTO style_review_requests (id, profile_id, submitted_version_id, status)
		VALUES ($1, $2, $3, 'pending')
		RETURNING id, profile_id, submitted_version_id, status, review_note, reviewed_by, reviewed_at, created_at
	`, reqID, profileID, versionID).Scan(
		&r.ID, &r.ProfileID, &r.SubmittedVersionID, &r.Status, &r.ReviewNote, &r.ReviewedBy, &r.ReviewedAt, &r.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create review request: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return &r, nil
}

// ListPendingReviews returns all pending review requests with joined profile info.
func (s *UserStyleStore) ListPendingReviews(ctx context.Context) ([]StyleReviewRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.profile_id, r.submitted_version_id, r.status, r.review_note,
		       r.reviewed_by, r.reviewed_at, r.created_at,
		       p.name, p.slug, p.owner_user_id::text,
		       v.version, v.config::text
		FROM style_review_requests r
		JOIN user_style_profiles p ON p.id = r.profile_id
		JOIN user_style_profile_versions v ON v.id = r.submitted_version_id
		WHERE r.status = 'pending'
		ORDER BY r.created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list pending reviews: %w", err)
	}
	defer rows.Close()

	var reviews []StyleReviewRequest
	for rows.Next() {
		var r StyleReviewRequest
		if err := rows.Scan(
			&r.ID, &r.ProfileID, &r.SubmittedVersionID, &r.Status, &r.ReviewNote,
			&r.ReviewedBy, &r.ReviewedAt, &r.CreatedAt,
			&r.ProfileName, &r.ProfileSlug, &r.OwnerUserID,
			&r.VersionNumber, &r.VersionConfig,
		); err != nil {
			return nil, err
		}
		reviews = append(reviews, r)
	}
	return reviews, nil
}

// ApproveReview marks a review request as approved, updates the profile status,
// and copies the version snapshot into the global style_profiles table.
func (s *UserStyleStore) ApproveReview(ctx context.Context, reviewID, reviewedBy string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Get review request with joined info
	var (
		profileID        string
		submittedVersionID string
		profileSlug      string
		profileName      string
		profileDesc      string
		ownerUserID      string
		versionConfig    []byte
	)
	err = tx.QueryRowContext(ctx, `
		SELECT r.profile_id, r.submitted_version_id, p.slug, p.name, p.description,
		       p.owner_user_id::text, v.config
		FROM style_review_requests r
		JOIN user_style_profiles p ON p.id = r.profile_id
		JOIN user_style_profile_versions v ON v.id = r.submitted_version_id
		WHERE r.id = $1 AND r.status = 'pending'
	`, reviewID).Scan(&profileID, &submittedVersionID, &profileSlug, &profileName, &profileDesc, &ownerUserID, &versionConfig)
	if err != nil {
		return fmt.Errorf("get review request for approval: %w", err)
	}

	// Update review request
	_, err = tx.ExecContext(ctx, `
		UPDATE style_review_requests
		SET status = 'approved', reviewed_by = $2, reviewed_at = NOW()
		WHERE id = $1
	`, reviewID, reviewedBy)
	if err != nil {
		return fmt.Errorf("update review status: %w", err)
	}

	// Update user profile status
	_, err = tx.ExecContext(ctx, `
		UPDATE user_style_profiles SET status = 'approved', updated_at = NOW() WHERE id = $1
	`, profileID)
	if err != nil {
		return fmt.Errorf("update profile status: %w", err)
	}

	// Insert into global style_profiles (or update if slug already exists)
	// Use a community-scoped slug to avoid collision with builtin styles
	globalSlug := "community_" + profileSlug
	_, err = tx.ExecContext(ctx, `
		INSERT INTO style_profiles (slug, name, description, version, status, config,
			rollout_type, whitelist_uids, rollout_percent,
			published_at, published_by, source_type, source_user_profile_id, author_user_id,
			created_at, updated_at)
		VALUES ($1, $2, $3, 1, 'published', $4,
			'full', '{}', 100,
			NOW(), $5, 'community', $6, $7::uuid,
			NOW(), NOW())
		ON CONFLICT (slug) DO UPDATE SET
			name = EXCLUDED.name,
			description = EXCLUDED.description,
			config = EXCLUDED.config,
			updated_at = NOW(),
			source_type = 'community',
			source_user_profile_id = EXCLUDED.source_user_profile_id,
			author_user_id = EXCLUDED.author_user_id
	`, globalSlug, profileName, profileDesc, versionConfig, reviewedBy, profileID, ownerUserID)
	if err != nil {
		return fmt.Errorf("insert into global style_profiles: %w", err)
	}

	// Also create a profile_versions record
	_, _ = tx.ExecContext(ctx, `
		INSERT INTO profile_versions (id, profile_slug, version, config, changelog, status, published_at, created_at, created_by)
		VALUES (uuid_generate_v4(), $1, 1, $2, 'community contribution', 'published', NOW(), NOW(), $3)
		ON CONFLICT (profile_slug, version) DO NOTHING
	`, globalSlug, versionConfig, reviewedBy)

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// RejectReview marks a review request as rejected and updates the profile status.
func (s *UserStyleStore) RejectReview(ctx context.Context, reviewID, reviewedBy, note string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Get profile_id
	var profileID string
	err = tx.QueryRowContext(ctx, `
		SELECT profile_id FROM style_review_requests WHERE id = $1 AND status = 'pending'
	`, reviewID).Scan(&profileID)
	if err != nil {
		return fmt.Errorf("get review request for rejection: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE style_review_requests
		SET status = 'rejected', review_note = $3, reviewed_by = $2, reviewed_at = NOW()
		WHERE id = $1
	`, reviewID, reviewedBy, note)
	if err != nil {
		return fmt.Errorf("update review status: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE user_style_profiles SET status = 'rejected', updated_at = NOW() WHERE id = $1
	`, profileID)
	if err != nil {
		return fmt.Errorf("update profile status: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}
