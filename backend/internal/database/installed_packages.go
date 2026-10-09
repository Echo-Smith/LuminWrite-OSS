package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrInstalledPackageNotFound marks a missing or non-owned package row.
var ErrInstalledPackageNotFound = errors.New("installed package not found")

// InstalledPackage is one registry row: a package the user installed via the
// download-install model. The row is the uninstall anchor — removing it must
// also remove the kind-specific registration (style profile / skill
// directory / MCP server).
type InstalledPackage struct {
	ID          string                 `json:"id"`
	UserID      string                 `json:"user_id"`
	Kind        string                 `json:"kind"` // style | skill | service
	Slug        string                 `json:"slug"`
	Version     string                 `json:"version"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Source      string                 `json:"source"` // builtin | url
	InstallPath string                 `json:"install_path"`
	Manifest    map[string]interface{} `json:"manifest"`
	InstalledAt time.Time              `json:"installed_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// InstalledPackageRepo owns the installed_packages table.
type InstalledPackageRepo struct {
	db *DB
}

// NewInstalledPackageRepo creates a new repo.
func NewInstalledPackageRepo(db *DB) *InstalledPackageRepo {
	return &InstalledPackageRepo{db: db}
}

const installedPackageColumns = `
	id::text, user_id::text, kind, slug, version, title, description,
	source, install_path, manifest, installed_at, updated_at
`

func scanInstalledPackage(scan func(dest ...interface{}) error) (*InstalledPackage, error) {
	var p InstalledPackage
	var manifestJSON []byte
	if err := scan(&p.ID, &p.UserID, &p.Kind, &p.Slug, &p.Version, &p.Title, &p.Description,
		&p.Source, &p.InstallPath, &manifestJSON, &p.InstalledAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	if len(manifestJSON) > 0 {
		json.Unmarshal(manifestJSON, &p.Manifest)
	}
	return &p, nil
}

// ListForUser returns the caller's installed packages, optionally filtered by kind.
func (r *InstalledPackageRepo) ListForUser(ctx context.Context, userID, kind string) ([]*InstalledPackage, error) {
	if r.db == nil || userID == "" {
		return []*InstalledPackage{}, nil
	}
	query := `SELECT ` + installedPackageColumns + ` FROM installed_packages WHERE user_id = $1::uuid`
	args := []interface{}{userID}
	if kind != "" {
		query += ` AND kind = $2`
		args = append(args, kind)
	}
	query += ` ORDER BY kind, slug`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var packages []*InstalledPackage
	for rows.Next() {
		p, err := scanInstalledPackage(rows.Scan)
		if err != nil {
			continue
		}
		packages = append(packages, p)
	}
	return packages, nil
}

// GetForUser retrieves one package by ID, enforcing ownership.
func (r *InstalledPackageRepo) GetForUser(ctx context.Context, userID, id string) (*InstalledPackage, error) {
	if r.db == nil || userID == "" || id == "" {
		return nil, ErrInstalledPackageNotFound
	}
	p, err := scanInstalledPackage(r.db.QueryRowContext(ctx,
		`SELECT `+installedPackageColumns+` FROM installed_packages WHERE id = $2::uuid AND user_id = $1::uuid`,
		userID, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInstalledPackageNotFound
	}
	return p, err
}

// GetBySlug retrieves one package by (user, kind, slug), enforcing ownership.
func (r *InstalledPackageRepo) GetBySlug(ctx context.Context, userID, kind, slug string) (*InstalledPackage, error) {
	if r.db == nil || userID == "" || slug == "" {
		return nil, ErrInstalledPackageNotFound
	}
	p, err := scanInstalledPackage(r.db.QueryRowContext(ctx,
		`SELECT `+installedPackageColumns+` FROM installed_packages WHERE user_id = $1::uuid AND kind = $2 AND slug = $3`,
		userID, kind, slug).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInstalledPackageNotFound
	}
	return p, err
}

// Create inserts a registry row.
func (r *InstalledPackageRepo) Create(ctx context.Context, p *InstalledPackage) (*InstalledPackage, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	if p.Source == "" {
		p.Source = "builtin"
	}
	manifestJSON, _ := json.Marshal(p.Manifest)
	if p.Manifest == nil {
		manifestJSON = []byte("{}")
	}
	created, err := scanInstalledPackage(r.db.QueryRowContext(ctx, `
		INSERT INTO installed_packages (user_id, kind, slug, version, title, description, source, install_path, manifest)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
		RETURNING `+installedPackageColumns,
		p.UserID, p.Kind, p.Slug, p.Version, p.Title, p.Description, p.Source, p.InstallPath, string(manifestJSON)).Scan)
	if err != nil {
		return nil, err
	}
	return created, nil
}

// UpdateVersion bumps an existing row (re-install over the same slug).
func (r *InstalledPackageRepo) UpdateVersion(ctx context.Context, userID, kind, slug, version, installPath, manifestJSON string) error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}
	tag, err := r.db.ExecContext(ctx, `
		UPDATE installed_packages SET version = $4, install_path = $5, manifest = $6::jsonb, updated_at = NOW()
		WHERE user_id = $1::uuid AND kind = $2 AND slug = $3
	`, userID, kind, slug, version, installPath, manifestJSON)
	if err != nil {
		return err
	}
	if n, _ := tag.RowsAffected(); n == 0 {
		return ErrInstalledPackageNotFound
	}
	return nil
}

// Delete removes a registry row, enforcing ownership.
func (r *InstalledPackageRepo) Delete(ctx context.Context, userID, id string) error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}
	tag, err := r.db.ExecContext(ctx,
		`DELETE FROM installed_packages WHERE id = $2::uuid AND user_id = $1::uuid`, userID, id)
	if err != nil {
		return err
	}
	if n, _ := tag.RowsAffected(); n == 0 {
		return ErrInstalledPackageNotFound
	}
	return nil
}
