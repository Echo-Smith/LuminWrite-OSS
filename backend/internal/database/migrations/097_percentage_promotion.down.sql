-- Restore the allowlist-only approval target mode. The append-only trigger is
-- lifted just long enough to remove percentage approvals that would violate
-- the restored constraint, then reinstalled.
DROP TRIGGER trg_writing_rollout_approvals_append_only ON writing_rollout_approvals;
DELETE FROM writing_rollout_approvals WHERE target_mode = 'percentage';
ALTER TABLE writing_rollout_approvals
    DROP CONSTRAINT chk_writing_rollout_approval_mode;
ALTER TABLE writing_rollout_approvals
    ADD CONSTRAINT chk_writing_rollout_approval_mode
    CHECK (target_mode = 'allowlist');
CREATE TRIGGER trg_writing_rollout_approvals_append_only
BEFORE UPDATE OR DELETE ON writing_rollout_approvals
FOR EACH ROW EXECUTE FUNCTION writing_reject_rollout_approval_mutation();
