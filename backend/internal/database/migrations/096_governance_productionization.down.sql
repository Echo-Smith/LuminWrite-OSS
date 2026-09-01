DO $downgrade_guard$
DECLARE
    approval_rows bigint;
    shadow_rows bigint;
BEGIN
    SELECT count(*) INTO approval_rows FROM writing_rollout_approvals;
    SELECT count(*) INTO shadow_rows FROM writing_shadow_contents;
    IF approval_rows > 0 OR shadow_rows > 0 THEN
        RAISE EXCEPTION 'cannot downgrade migration 096: % approval rows and % shadow rows are still present; archive and purge them first', approval_rows, shadow_rows;
    END IF;
END
$downgrade_guard$;

DROP TRIGGER IF EXISTS trg_writing_rollout_approvals_append_only ON writing_rollout_approvals;
DROP FUNCTION IF EXISTS writing_reject_rollout_approval_mutation();
DROP TABLE writing_rollout_approvals;
DROP TABLE writing_shadow_contents;
