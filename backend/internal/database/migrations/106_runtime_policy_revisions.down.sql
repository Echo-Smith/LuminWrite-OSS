DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM writing_runtime_policy_revisions) THEN
  RAISE EXCEPTION 'cannot downgrade: runtime policy audit records exist';
 END IF;
END $$;
DROP TABLE writing_runtime_policy_revisions;
DROP FUNCTION writing_runtime_policy_immutable();
