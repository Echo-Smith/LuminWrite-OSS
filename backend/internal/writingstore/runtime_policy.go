package writingstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type RuntimePolicyRevision struct {
	Revision     int64           `json:"revision"`
	CapabilityID string          `json:"capability_id"`
	PolicyHash   string          `json:"policy_hash"`
	Policy       json.RawMessage `json:"policy"`
	Active       bool            `json:"active"`
	OperatorID   string          `json:"operator_id"`
}

func (s *Store) AppendRuntimePolicy(ctx context.Context, record RuntimePolicyRevision) error {
	if strings.TrimSpace(record.CapabilityID) == "" || !sha256Pattern.MatchString(record.PolicyHash) || strings.TrimSpace(record.OperatorID) == "" || !json.Valid(record.Policy) {
		return ErrInvalidRecord
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO writing_runtime_policy_revisions(capability_id,policy_hash,policy,active,operator_id) VALUES($1,$2,$3,$4,$5)`, record.CapabilityID, record.PolicyHash, []byte(record.Policy), record.Active, record.OperatorID)
	return err
}
func (s *Store) LatestRuntimePolicy(ctx context.Context, capability string) (RuntimePolicyRevision, error) {
	var v RuntimePolicyRevision
	err := s.db.QueryRowContext(ctx, `SELECT revision,capability_id,policy_hash,policy,active,operator_id FROM writing_runtime_policy_revisions WHERE capability_id=$1 ORDER BY revision DESC LIMIT 1`, capability).Scan(&v.Revision, &v.CapabilityID, &v.PolicyHash, &v.Policy, &v.Active, &v.OperatorID)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, fmt.Errorf("load runtime policy: %w", err)
	}
	return v, nil
}
