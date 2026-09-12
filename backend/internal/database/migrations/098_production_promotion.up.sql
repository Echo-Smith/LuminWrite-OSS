-- Enabled is the final rung of the rollout ladder: approval records may now
-- bind any authoritative mode. The production promotion gate still requires
-- the same activation key to have passed the percentage stage first, with
-- fresh evidence under the percentage policy hash.
ALTER TABLE writing_rollout_approvals
    DROP CONSTRAINT chk_writing_rollout_approval_mode;
ALTER TABLE writing_rollout_approvals
    ADD CONSTRAINT chk_writing_rollout_approval_mode
    CHECK (target_mode IN ('allowlist', 'percentage', 'enabled'));
