-- 065 down: Reverse evolution gate configs migration

DROP TABLE IF EXISTS canary_health_snapshots;
DROP TABLE IF EXISTS evolution_gate_events;
DROP TABLE IF EXISTS evolution_gate_configs;

ALTER TABLE style_profile_candidates
    DROP COLUMN IF EXISTS eval_run_id,
    DROP COLUMN IF EXISTS eval_score,
    DROP COLUMN IF EXISTS eval_passed,
    DROP COLUMN IF EXISTS eval_completed_at,
    DROP COLUMN IF EXISTS eval_summary,
    DROP COLUMN IF EXISTS rejected_reason,
    DROP COLUMN IF EXISTS approved_by,
    DROP COLUMN IF EXISTS approved_at;
