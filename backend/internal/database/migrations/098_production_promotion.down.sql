-- Restore the allowlist|percentage approval target modes. The append-only
-- trigger is lifted just long enough to remove enabled approvals that would
-- violate the restored constraint, then reinstalled.
DROP TRIGGER trg_writing_rollout_approvals_append_only ON writing_rollout_approvals;
DELETE FROM writing_rollout_approvals WHERE target_mode = 'enabled';
ALTER TABLE writing_rollout_approvals
    DROP CONSTRAINT chk_writing_rollout_approval_mode;
ALTER TABLE writing_rollout_approvals
    ADD CONSTRAINT chk_writing_rollout_approval_mode
    CHECK (target_mode IN ('allowlist', 'percentage'));
CREATE TRIGGER trg_writing_rollout_approvals_append_only
BEFORE UPDATE OR DELETE ON writing_rollout_approvals
FOR EACH ROW EXECUTE FUNCTION writing_reject_rollout_approval_mutation();
