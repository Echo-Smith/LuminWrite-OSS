package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// ─── Errors ──────────────────────────────────────────────

// ErrUserStyleNotFound is returned when a user style profile or version is not found.
var ErrUserStyleNotFound = errors.New("user style not found")

// ─── Types ───────────────────────────────────────────────

// UserStyleProfile is the user-owned style profile record.
type UserStyleProfile struct {
	ID             string    `json:"id"`
	OwnerUserID    string    `json:"owner_user_id"`
	Slug           string    `json:"slug"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Status         string    `json:"status"` // draft | pending_review | approved | rejected
	CurrentVersion int       `json:"current_version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
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
	ID                 string     `json:"id"`
	ProfileID          string     `json:"profile_id"`
	SubmittedVersionID string     `json:"submitted_version_id"`
	Status             string     `json:"status"` // pending | approved | rejected
	ReviewNote         string     `json:"review_note"`
	ReviewedBy         string     `json:"reviewed_by"`
	ReviewedAt         *time.Time `json:"reviewed_at"`
	CreatedAt          time.Time  `json:"created_at"`

	// Joined fields (populated by ListPendingReviews)
	ProfileName   string `json:"profile_name,omitempty"`
	ProfileSlug   string `json:"profile_slug,omitempty"`
	OwnerUserID   string `json:"owner_user_id,omitempty"`
	VersionNumber int    `json:"version_number,omitempty"`
	VersionConfig string `json:"version_config,omitempty"`
}

// ─── SQL Constants ───────────────────────────────────────

const userProfileColumns = `id, owner_user_id::text, slug, name, description, status, current_version, created_at, updated_at`

const versionColumns = `id, profile_id, version, config::text, changelog, created_at`

// scanProfile scans a full UserStyleProfile from a scanner.
func scanProfile(s interface{ Scan(...any) error }, p *UserStyleProfile) error {
	return s.Scan(&p.ID, &p.OwnerUserID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CurrentVersion, &p.CreatedAt, &p.UpdatedAt)
}

// scanVersion scans a full UserStyleProfileVersion from a scanner.
func scanVersion(s interface{ Scan(...any) error }, v *UserStyleProfileVersion) error {
	return s.Scan(&v.ID, &v.ProfileID, &v.Version, &v.Config, &v.Changelog, &v.CreatedAt)
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
		RETURNING `+userProfileColumns,
		ownerUserID, slug, name, description,
	).Scan(&p.ID, &p.OwnerUserID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CurrentVersion, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create user style profile: %w", err)
	}
	return &p, nil
}

// GetProfile returns a single user style profile by ID.
func (s *UserStyleStore) GetProfile(ctx context.Context, id string) (*UserStyleProfile, error) {
	var p UserStyleProfile
	err := s.db.QueryRowContext(ctx, `
		SELECT `+userProfileColumns+`
		FROM user_style_profiles WHERE id = $1
	`, id).Scan(&p.ID, &p.OwnerUserID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CurrentVersion, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get user style profile: %w", err)
	}
	return &p, nil
}

// GetProfileBySlugAndOwner returns a single user style profile by slug + owner.
// This is used when the agent.start handler needs to resolve a "my_<slug>"
// style slug to the actual StyleProfile config.
func (s *UserStyleStore) GetProfileBySlugAndOwner(ctx context.Context, slug, ownerUserID string) (*UserStyleProfile, error) {
	var p UserStyleProfile
	err := s.db.QueryRowContext(ctx, `
		SELECT `+userProfileColumns+`
		FROM user_style_profiles
		WHERE slug = $1 AND owner_user_id = $2
	`, slug, ownerUserID).Scan(&p.ID, &p.OwnerUserID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CurrentVersion, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get user style profile by slug: %w", err)
	}
	return &p, nil
}

// ListProfilesByOwner returns all style profiles for a user.
func (s *UserStyleStore) ListProfilesByOwner(ctx context.Context, ownerUserID string) ([]UserStyleProfile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+userProfileColumns+`
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
		if err := scanProfile(rows, &p); err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

// ProfileWithConfig pairs a user style profile with its latest version config JSON.
type ProfileWithConfig struct {
	UserStyleProfile
	ConfigJSON string // empty if no version saved
}

// ListProfilesWithLatestVersion returns all profiles for a user with their latest
// version config in a single query (LATERAL JOIN), avoiding N+1.
func (s *UserStyleStore) ListProfilesWithLatestVersion(ctx context.Context, ownerUserID string) ([]ProfileWithConfig, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.`+userProfileColumns+`,
		       COALESCE(v.config::text, '') AS latest_config
		FROM user_style_profiles p
		LEFT JOIN LATERAL (
			SELECT config FROM user_style_profile_versions
			WHERE profile_id = p.id
			ORDER BY version DESC LIMIT 1
		) v ON true
		WHERE p.owner_user_id = $1
		ORDER BY p.updated_at DESC
	`, ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("list profiles with version: %w", err)
	}
	defer rows.Close()

	var result []ProfileWithConfig
	for rows.Next() {
		var pwc ProfileWithConfig
		if err := rows.Scan(
			&pwc.ID, &pwc.OwnerUserID, &pwc.Slug, &pwc.Name, &pwc.Description,
			&pwc.Status, &pwc.CurrentVersion, &pwc.CreatedAt, &pwc.UpdatedAt,
			&pwc.ConfigJSON,
		); err != nil {
			return nil, err
		}
		result = append(result, pwc)
	}
	return result, rows.Err()
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
	`, versionID, profileID, newVersion, config, changelog)
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
		SELECT `+versionColumns+`
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
		SELECT `+versionColumns+`
		FROM user_style_profile_versions WHERE id = $1
	`, versionID).Scan(&v.ID, &v.ProfileID, &v.Version, &v.Config, &v.Changelog, &v.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get version: %w", err)
	}
	return &v, nil
}

// GetVersionByNumber returns the immutable snapshot used by a frozen
// WABench rule-profile reference. Unlike GetLatestVersion, this never follows
// later edits to the user's style.
func (s *UserStyleStore) GetVersionByNumber(ctx context.Context, profileID string, version int) (*UserStyleProfileVersion, error) {
	if version < 1 {
		return nil, fmt.Errorf("style version must be positive")
	}
	var v UserStyleProfileVersion
	err := s.db.QueryRowContext(ctx, `
		SELECT `+versionColumns+`
		FROM user_style_profile_versions
		WHERE profile_id = $1 AND version = $2
	`, profileID, version).Scan(&v.ID, &v.ProfileID, &v.Version, &v.Config, &v.Changelog, &v.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get user style profile version %d: %w", version, err)
	}
	return &v, nil
}

// ListVersions returns all versions for a profile.
func (s *UserStyleStore) ListVersions(ctx context.Context, profileID string) ([]UserStyleProfileVersion, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+versionColumns+`
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
		if err := scanVersion(rows, &v); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
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

// WithdrawReview cancels a pending review request by the profile owner.
// The profile status reverts from 'pending_review' to 'draft', and the
// associated review request is marked as 'withdrawn'.
func (s *UserStyleStore) WithdrawReview(ctx context.Context, profileID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Verify the profile is currently pending review
	var status string
	err = tx.QueryRowContext(ctx, `
		SELECT status FROM user_style_profiles WHERE id = $1 FOR UPDATE
	`, profileID).Scan(&status)
	if err != nil {
		return fmt.Errorf("lock profile for withdraw: %w", err)
	}
	if status != "pending_review" {
		return fmt.Errorf("cannot withdraw: profile status is %s, not pending_review", status)
	}

	// Revert profile status to draft
	_, err = tx.ExecContext(ctx, `
		UPDATE user_style_profiles SET status = 'draft', updated_at = NOW() WHERE id = $1
	`, profileID)
	if err != nil {
		return fmt.Errorf("revert profile status: %w", err)
	}

	// Mark the pending review request as withdrawn
	_, err = tx.ExecContext(ctx, `
		UPDATE style_review_requests
		SET status = 'withdrawn', reviewed_at = NOW()
		WHERE profile_id = $1 AND status = 'pending'
	`, profileID)
	if err != nil {
		return fmt.Errorf("mark review as withdrawn: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
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
	return reviews, rows.Err()
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
		profileID          string
		submittedVersionID string
		profileSlug        string
		profileName        string
		profileDesc        string
		ownerUserID        string
		versionConfig      []byte
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

	// Also create a profile_versions record (best-effort, log on failure)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO profile_versions (id, profile_slug, version, config, changelog, status, published_at, created_at, created_by)
		VALUES (uuid_generate_v4(), $1, 1, $2, 'community contribution', 'published', NOW(), NOW(), $3)
		ON CONFLICT (profile_slug, version) DO NOTHING
	`, globalSlug, versionConfig, reviewedBy); err != nil {
		slog.Warn("failed to insert profile_versions record (non-fatal)", "error", err, "slug", globalSlug)
	}

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
