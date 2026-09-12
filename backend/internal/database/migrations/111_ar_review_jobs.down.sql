-- 111 down: drop the AR-012 candidate evaluation job ledger. Content blobs
-- referenced only from artifact_refs are content-addressed and unreferenced
-- by any other table; a storage sweep may reclaim them separately.
DROP INDEX IF EXISTS idx_ar_review_jobs_unknown;
DROP INDEX IF EXISTS idx_ar_review_jobs_pending;
DROP INDEX IF EXISTS idx_ar_review_jobs_replay;
DROP TABLE IF EXISTS writing_ar_review_jobs;
