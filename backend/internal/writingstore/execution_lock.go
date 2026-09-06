package writingstore

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/binary"
	"time"
)

// WithRunExecutionLock owns a dedicated PostgreSQL session for the entire run.
// Durable run/attempt/snapshot rows are the queue; session death releases the
// lock. No TTL may expire while a healthy worker is still executing.
func (s *Store) WithRunExecutionLock(ctx context.Context, runID string, work func(context.Context) error) (bool, error) {
	if err := validateID(runID, "run_", "run_id"); err != nil {
		return false, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	sum := sha256.Sum256([]byte(runID))
	key := int32(binary.BigEndian.Uint32(sum[:4]))
	const namespace int32 = 1280660039
	var acquired bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1,$2)`, namespace, key).Scan(&acquired); err != nil || !acquired {
		return false, err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(release, `SELECT pg_advisory_unlock($1,$2)`, namespace, key); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	owned, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-owned.Done():
				return
			case <-ticker.C:
				probe, stop := context.WithTimeout(owned, 2*time.Second)
				err := conn.PingContext(probe)
				stop()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { close(done); <-stopped }()
	err = work(owned)
	if err == nil {
		err = owned.Err()
	}
	return true, err
}
func (s *Store) DispatchableRunIDs(ctx context.Context, limit int) ([]string, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.run_id FROM writing_runs r JOIN writing_run_plans p ON p.run_id=r.run_id AND p.plan_id=r.active_plan_id AND p.plan_version=r.active_plan_version WHERE r.status IN ('planned','running','pausing','cancelling') AND (p.approval_status='approved' OR p.strategy_decision->>'approval_required'='false') ORDER BY r.updated_at,r.run_id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
