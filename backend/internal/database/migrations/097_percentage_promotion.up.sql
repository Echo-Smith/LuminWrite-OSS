-- Percentage is the second rung of the rollout ladder: approval records may
-- now bind either authoritative mode. The percentage promotion gate still
-- requires the same activation key to have passed the allowlist stage first.
ALTER TABLE writing_rollout_approvals
    DROP CONSTRAINT chk_writing_rollout_approval_mode;
ALTER TABLE writing_rollout_approvals
    ADD CONSTRAINT chk_writing_rollout_approval_mode
    CHECK (target_mode IN ('allowlist', 'percentage'));
