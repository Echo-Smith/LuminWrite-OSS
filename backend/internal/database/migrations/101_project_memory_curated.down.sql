-- Remove ProjectMemory M2.5 curated tables. Immutable-columns triggers are
-- dropped before their tables.
DROP TRIGGER IF EXISTS trg_project_threads_immutable ON project_threads;
DROP TABLE IF EXISTS project_threads;
DROP TABLE IF EXISTS project_open_questions;
DROP TRIGGER IF EXISTS trg_project_decisions_immutable ON project_decisions;
DROP TABLE IF EXISTS project_decisions;
DROP TRIGGER IF EXISTS trg_project_terminology_immutable ON project_terminology;
DROP TABLE IF EXISTS project_terminology;
