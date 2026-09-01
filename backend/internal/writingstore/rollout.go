package writingstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ShadowContentRecord struct {
	ContentKey, PolicyHash, RunID, MediaType, ContentHash string
	Body                                                  []byte
	StoredAt, ExpiresAt                                   time.Time
}

func (s *Store) PutShadowContent(ctx context.Context, record ShadowContentRecord) error {
	if s == nil || strings.TrimSpace(record.ContentKey) == "" || strings.TrimSpace(record.MediaType) == "" || len(record.Body) == 0 {
		return fmt.Errorf("%w: incomplete shadow content", ErrInvalidRecord)
	}
	if err := validateHash(record.PolicyHash, "policy_hash"); err != nil {
		return err
	}
	if err := validateHash(record.ContentHash, "content_hash"); err != nil {
		return err
	}
	if err := validateID(record.RunID, "run_", "run_id"); err != nil {
		return err
	}
	sum := sha256.Sum256(record.Body)
	if "sha256:"+hex.EncodeToString(sum[:]) != record.ContentHash {
		return fmt.Errorf("%w: shadow body hash mismatch", ErrInvalidRecord)
	}
	if record.StoredAt.IsZero() {
		record.StoredAt = time.Now().UTC()
	}
	if !record.ExpiresAt.After(record.StoredAt) {
		return fmt.Errorf("%w: shadow expiry must follow storage time", ErrInvalidRecord)
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO writing_shadow_contents
		(content_key, policy_hash, run_id, media_type, content_hash, body, stored_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (content_key) DO NOTHING
	`, record.ContentKey, record.PolicyHash, record.RunID, record.MediaType, record.ContentHash, record.Body, record.StoredAt, record.ExpiresAt)
	if err != nil {
		return fmt.Errorf("put shadow content: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 1 {
		return nil
	}
	existing, err := s.GetShadowContent(ctx, record.ContentKey)
	if err != nil {
		return err
	}
	if existing.PolicyHash != record.PolicyHash || existing.RunID != record.RunID || existing.MediaType != record.MediaType || existing.ContentHash != record.ContentHash || string(existing.Body) != string(record.Body) {
		return fmt.Errorf("%w: shadow content key reused with different value", ErrIdempotencyConflict)
	}
	return nil
}

func (s *Store) GetShadowContent(ctx context.Context, key string) (ShadowContentRecord, error) {
	var record ShadowContentRecord
	err := s.db.QueryRowContext(ctx, `
		SELECT content_key, policy_hash, run_id, media_type, content_hash, body, stored_at, expires_at
		FROM writing_shadow_contents WHERE content_key=$1
	`, key).Scan(&record.ContentKey, &record.PolicyHash, &record.RunID, &record.MediaType, &record.ContentHash, &record.Body, &record.StoredAt, &record.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ShadowContentRecord{}, ErrNotFound
	}
	if err != nil {
		return ShadowContentRecord{}, fmt.Errorf("get shadow content: %w", err)
	}
	return record, nil
}

func (s *Store) DeleteShadowContentPrefix(ctx context.Context, prefix string) (int, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM writing_shadow_contents WHERE content_key LIKE $1 ESCAPE E'\\'`, escapeLike(prefix)+"%")
	if err != nil {
		return 0, fmt.Errorf("delete shadow content prefix: %w", err)
	}
	rows, err := result.RowsAffected()
	return int(rows), err
}

func (s *Store) DeleteShadowContentBefore(ctx context.Context, cutoff time.Time) (int, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM writing_shadow_contents WHERE stored_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete expired shadow content: %w", err)
	}
	rows, err := result.RowsAffected()
	return int(rows), err
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

type RolloutEvidenceHealth struct {
	PolicyHash        string    `json:"policy_hash"`
	Cutoff            time.Time `json:"cutoff"`
	TotalRecords      int64     `json:"total_records"`
	ComparisonRecords int64     `json:"comparison_records"`
	FailedRecords     int64     `json:"failed_records"`
	LastRecordedAt    time.Time `json:"last_recorded_at"`
}

func (s *Store) RolloutEvidenceHealth(ctx context.Context, policyHash string, cutoff time.Time) (RolloutEvidenceHealth, error) {
	if err := validateHash(policyHash, "policy_hash"); err != nil {
		return RolloutEvidenceHealth{}, err
	}
	var health RolloutEvidenceHealth
	health.PolicyHash, health.Cutoff = policyHash, cutoff
	var latest sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE event_type='runtime.shadow_compared'),
		       COUNT(*) FILTER (WHERE COALESCE(payload->>'status','') IN
		         ('failed','evidence_failed','shadow_timeout','shadow_circuit_open','candidate_lane_blocked','shadow_mode_unavailable')
		         OR COALESCE(payload->>'error_code','') <> ''),
		       MAX(occurred_at)
		FROM writing_run_events
		WHERE entity_kind='rollout_evidence' AND payload->>'policy_hash'=$1 AND occurred_at >= $2
	`, policyHash, cutoff).Scan(&health.TotalRecords, &health.ComparisonRecords, &health.FailedRecords, &latest)
	if err != nil {
		return RolloutEvidenceHealth{}, fmt.Errorf("rollout evidence health: %w", err)
	}
	if latest.Valid {
		health.LastRecordedAt = latest.Time
	}
	return health, nil
}

type RolloutApprovalRecord struct {
	ApprovalID, PolicyHash, ActivationKey, TargetMode string
	PolicyVersion                                     int
	ApprovedBy, Reason                                string
	EvidenceHealth                                    RolloutEvidenceHealth
	EvidenceCutoff, EvidenceLastRecordedAt            time.Time
	ExpiresAt, CreatedAt                              time.Time
}

func (s *Store) RecordRolloutApproval(ctx context.Context, record RolloutApprovalRecord) error {
	if err := validateID(record.ApprovalID, "approval_", "approval_id"); err != nil {
		return err
	}
	if err := validateHash(record.PolicyHash, "policy_hash"); err != nil {
		return err
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	if record.PolicyVersion < 1 || record.TargetMode != "allowlist" || strings.TrimSpace(record.ActivationKey) == "" || strings.TrimSpace(record.ApprovedBy) == "" || strings.TrimSpace(record.Reason) == "" ||
		record.EvidenceCutoff.IsZero() || record.EvidenceLastRecordedAt.IsZero() || record.EvidenceHealth.PolicyHash != record.PolicyHash ||
		!record.EvidenceHealth.Cutoff.Equal(record.EvidenceCutoff) || !record.EvidenceHealth.LastRecordedAt.Equal(record.EvidenceLastRecordedAt) || !record.ExpiresAt.After(record.CreatedAt) {
		return fmt.Errorf("%w: incomplete rollout approval", ErrInvalidRecord)
	}
	health, err := json.Marshal(record.EvidenceHealth)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO writing_rollout_approvals
		(approval_id, policy_hash, policy_version, activation_key, target_mode, approved_by, reason,
		 evidence_health, evidence_cutoff, evidence_last_recorded_at, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, record.ApprovalID, record.PolicyHash, record.PolicyVersion, record.ActivationKey, record.TargetMode,
		record.ApprovedBy, record.Reason, health, record.EvidenceCutoff, record.EvidenceLastRecordedAt, record.ExpiresAt, record.CreatedAt)
	if err != nil {
		return fmt.Errorf("record rollout approval: %w", err)
	}
	return nil
}

func (s *Store) LatestRolloutApproval(ctx context.Context, policyHash string, policyVersion int, activationKey string) (RolloutApprovalRecord, error) {
	var record RolloutApprovalRecord
	var health []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT approval_id, policy_hash, policy_version, activation_key, target_mode, approved_by, reason,
		 evidence_health, evidence_cutoff, evidence_last_recorded_at, expires_at, created_at
		FROM writing_rollout_approvals
		WHERE policy_hash=$1 AND policy_version=$2 AND activation_key=$3
		ORDER BY created_at DESC LIMIT 1
	`, policyHash, policyVersion, activationKey).Scan(&record.ApprovalID, &record.PolicyHash, &record.PolicyVersion,
		&record.ActivationKey, &record.TargetMode, &record.ApprovedBy, &record.Reason, &health,
		&record.EvidenceCutoff, &record.EvidenceLastRecordedAt, &record.ExpiresAt, &record.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RolloutApprovalRecord{}, ErrNotFound
	}
	if err != nil {
		return RolloutApprovalRecord{}, fmt.Errorf("load rollout approval: %w", err)
	}
	if err := json.Unmarshal(health, &record.EvidenceHealth); err != nil {
		return RolloutApprovalRecord{}, err
	}
	return record, nil
}
