-- 121 回滚
-- 121 rollback: drop the pilot draft-replay store first (it references
-- writing_documents), then the entitlement table.
DROP TABLE IF EXISTS research_contract_drafts;
DROP TABLE IF EXISTS research_pilot_entitlements;
