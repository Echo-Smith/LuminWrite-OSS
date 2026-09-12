-- Remove ProjectMemory M1 tables. The immutable-columns trigger on
-- project_facts must be dropped before the table itself.
DROP TRIGGER IF EXISTS trg_project_facts_immutable ON project_facts;
DROP TABLE IF EXISTS project_facts;
DROP TABLE IF EXISTS project_memory_candidates;
ALTER TABLE writing_documents
    DROP COLUMN IF EXISTS project_id;
DROP TABLE IF EXISTS writing_projects;
