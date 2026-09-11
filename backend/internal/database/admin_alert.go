package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ─── Admin Alerts (ops patrol findings) ─────────────────
//
// Persisted alerts raised by the background ops patrol worker.
// Dedup: an unresolved alert with the same fingerprint inside the cooldown
// window is refreshed in place instead of inserting a duplicate row.

// Alert severities and statuses.
const (
	AlertSeverityInfo     = "info"
	AlertSeverityWarning  = "warning"
	AlertSeverityCritical = "critical"

	AlertStatusPending = "pending"
	AlertStatusAcked   = "acked"
	AlertStatusResolved = "resolved"
)

// AdminAlert is a single ops alert shown in the admin panel.
type AdminAlert struct {
	ID          string                 `json:"id"`
	Severity    string                 `json:"severity"`
	CheckType   string                 `json:"check_type"`
	Fingerprint string                 `json:"fingerprint"`
	Title       string                 `json:"title"`
	Detail      string                 `json:"detail"`
	Evidence    map[string]interface{} `json:"evidence"`
	Status      string                 `json:"status"`
	AcknowledgedBy string             `json:"acknowledged_by,omitempty"`
	AcknowledgedAt *time.Time         `json:"acknowledged_at,omitempty"`
	ResolvedAt  *time.Time             `json:"resolved_at,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// AdminAlertRepo reads and writes admin_alerts rows.
type AdminAlertRepo struct {
	db *DB
}

// NewAdminAlertRepo creates a new repo. All methods are nil-safe no-ops
// when the database is unavailable.
func NewAdminAlertRepo(db *DB) *AdminAlertRepo {
	return &AdminAlertRepo{db: db}
}

// InsertAlertWithDedupe inserts a new alert, unless an unresolved alert with
// the same fingerprint already exists within the cooldown window — in that
// case the existing row is refreshed (severity/title/detail/evidence) instead.
// Returns (alertID, created, error).
func (r *AdminAlertRepo) InsertAlertWithDedupe(ctx context.Context, a *AdminAlert, cooldown time.Duration) (string, bool, error) {
	if r == nil || r.db == nil {
		return "", false, nil
	}
	evidenceJSON, _ := json.Marshal(a.Evidence)
	since := time.Now().Add(-cooldown)

	var existingID string
	err := r.db.QueryRowContext(ctx, `
		SELECT id::text FROM admin_alerts
		WHERE fingerprint = $1 AND status IN ('pending', 'acked') AND created_at >= $2
		ORDER BY created_at DESC
		LIMIT 1
	`, a.Fingerprint, since).Scan(&existingID)

	if err == nil {
		_, uerr := r.db.ExecContext(ctx, `
			UPDATE admin_alerts
			SET severity = $2, title = $3, detail = $4, evidence = $5::jsonb, updated_at = NOW()
			WHERE id = $1::uuid
		`, existingID, a.Severity, a.Title, a.Detail, string(evidenceJSON))
		if uerr != nil {
			return "", false, fmt.Errorf("refresh alert: %w", uerr)
		}
		return existingID, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, fmt.Errorf("query alert dedupe: %w", err)
	}

	var id string
	err = r.db.QueryRowContext(ctx, `
		INSERT INTO admin_alerts (severity, check_type, fingerprint, title, detail, evidence)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
		RETURNING id::text
	`, a.Severity, a.CheckType, a.Fingerprint, a.Title, a.Detail, string(evidenceJSON)).Scan(&id)
	if err != nil {
		return "", false, fmt.Errorf("insert alert: %w", err)
	}
	return id, true, nil
}

// ListAlerts returns alerts ordered by created_at DESC with total count.
// status filters by exact status; "all" (or empty) returns every status.
func (r *AdminAlertRepo) ListAlerts(ctx context.Context, status string, limit, offset int) ([]*AdminAlert, int, error) {
	if r == nil || r.db == nil {
		return nil, 0, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	where := "TRUE"
	if status != "" && status != "all" {
		where = "status = $1"
	}

	var total int
	countQuery := "SELECT COUNT(*) FROM admin_alerts WHERE " + where
	var args []interface{}
	if status != "" && status != "all" {
		args = append(args, status)
	}
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count alerts: %w", err)
	}

	listQuery := "SELECT id::text, severity, check_type, fingerprint, title, detail, evidence, status, acknowledged_by, acknowledged_at, resolved_at, created_at, updated_at FROM admin_alerts WHERE " + where + " ORDER BY created_at DESC LIMIT $" + fmt.Sprint(len(args)+1) + " OFFSET $" + fmt.Sprint(len(args)+2)
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, listQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list alerts: %w", err)
	}
	defer rows.Close()

	alerts := make([]*AdminAlert, 0)
	for rows.Next() {
		a, scanErr := scanAlert(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		alerts = append(alerts, a)
	}
	return alerts, total, rows.Err()
}

// CountsByStatus returns (pending, acked) alert counts.
func (r *AdminAlertRepo) CountsByStatus(ctx context.Context) (pending int, acked int, err error) {
	if r == nil || r.db == nil {
		return 0, 0, nil
	}
	err = r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'pending'),
			COUNT(*) FILTER (WHERE status = 'acked')
		FROM admin_alerts
	`).Scan(&pending, &acked)
	if err != nil {
		return 0, 0, fmt.Errorf("count alerts by status: %w", err)
	}
	return pending, acked, nil
}

// AckAlert marks a pending alert as acknowledged by the given operator.
func (r *AdminAlertRepo) AckAlert(ctx context.Context, id, by string) error {
	if r == nil || r.db == nil {
		return nil
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE admin_alerts
		SET status = 'acked', acknowledged_by = $2, acknowledged_at = NOW(), updated_at = NOW()
		WHERE id = $1::uuid AND status = 'pending'
	`, id, by)
	if err != nil {
		return fmt.Errorf("ack alert: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("alert %s not in pending state", id)
	}
	return nil
}

// ResolveAlert marks an alert (pending or acked) as resolved.
func (r *AdminAlertRepo) ResolveAlert(ctx context.Context, id string) error {
	if r == nil || r.db == nil {
		return nil
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE admin_alerts
		SET status = 'resolved', resolved_at = NOW(), updated_at = NOW()
		WHERE id = $1::uuid AND status IN ('pending', 'acked')
	`, id)
	if err != nil {
		return fmt.Errorf("resolve alert: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("alert %s already resolved", id)
	}
	return nil
}

// ListAdminUserIDs returns the bootstrap admin account plus every user
// assigned the system 'admin' role — the SSE push targets for ops alerts.
func (r *AdminAlertRepo) ListAdminUserIDs(ctx context.Context, bootstrapAdminID string) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id::text FROM users WHERE id::text = $1
		UNION
		SELECT ur.user_id::text FROM user_roles ur
		JOIN roles ro ON ro.id = ur.role_id
		WHERE ro.name = 'admin'
	`, bootstrapAdminID)
	if err != nil {
		return nil, fmt.Errorf("list admin user ids: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// alertScanner abstracts *sql.Row / *sql.Rows for shared scanning.
type alertScanner interface {
	Scan(dest ...interface{}) error
}

// PatrolConfig is the runtime toggle for the ops patrol worker.
type PatrolConfig struct {
	Enabled   bool      `json:"enabled"`
	UpdatedBy string    `json:"updated_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GetPatrolConfig returns the singleton patrol config. Missing row or
// unavailable DB yields Enabled=true (fail-open: keep patrolling).
func (r *AdminAlertRepo) GetPatrolConfig(ctx context.Context) (PatrolConfig, error) {
	cfg := PatrolConfig{Enabled: true}
	if r == nil || r.db == nil {
		return cfg, nil
	}
	err := r.db.QueryRowContext(ctx, `
		SELECT enabled, updated_by, updated_at FROM admin_patrol_config WHERE id = 1
	`).Scan(&cfg.Enabled, &cfg.UpdatedBy, &cfg.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PatrolConfig{Enabled: true}, nil
	}
	if err != nil {
		return PatrolConfig{Enabled: true}, fmt.Errorf("get patrol config: %w", err)
	}
	return cfg, nil
}

// SetPatrolEnabled upserts the singleton patrol config.
func (r *AdminAlertRepo) SetPatrolEnabled(ctx context.Context, enabled bool, by string) error {
	if r == nil || r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO admin_patrol_config (id, enabled, updated_by, updated_at)
		VALUES (1, $1, $2, NOW())
		ON CONFLICT (id) DO UPDATE SET enabled = $1, updated_by = $2, updated_at = NOW()
	`, enabled, by)
	if err != nil {
		return fmt.Errorf("set patrol enabled: %w", err)
	}
	return nil
}

func scanAlert(sc alertScanner) (*AdminAlert, error) {
	var a AdminAlert
	var evidenceJSON []byte
	if err := sc.Scan(
		&a.ID, &a.Severity, &a.CheckType, &a.Fingerprint, &a.Title, &a.Detail,
		&evidenceJSON, &a.Status, &a.AcknowledgedBy, &a.AcknowledgedAt,
		&a.ResolvedAt, &a.CreatedAt, &a.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan alert: %w", err)
	}
	if len(evidenceJSON) > 0 {
		_ = json.Unmarshal(evidenceJSON, &a.Evidence)
	}
	return &a, nil
}
