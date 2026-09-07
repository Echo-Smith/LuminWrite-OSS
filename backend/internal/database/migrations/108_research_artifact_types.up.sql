-- 108: Research-review formal artifact types (T06) — extend the artifact_type
-- CHECK with the frozen contract type names (specs/research-review/contracts.md
-- §2) so the research template's plan outputs can be persisted as their own
-- types. Incremental only: no existing rows are rewritten.
--
-- T05 carried discover/read outputs in the CHECK-permitted research_note /
-- claim_map types (schema_version self-describes the content); T06 converges
-- the plan template and executors onto the formal names below. research_note
-- and claim_map stay in the CHECK — older rows keep loading.

ALTER TABLE writing_artifacts
    DROP CONSTRAINT chk_writing_artifact_type;

ALTER TABLE writing_artifacts
    ADD CONSTRAINT chk_writing_artifact_type CHECK (artifact_type IN (
        'contract', 'materials', 'brief', 'source_pack', 'research_note',
        'claim_map', 'outline', 'section_draft', 'full_draft',
        'review_report', 'revision_set', 'quality_report',
        'evidence_report', 'fact_report', 'evidence_approval',
        'research_candidates', 'research_evidence_pack',
        'research_outline', 'approved_research_outline',
        'research_citation_index', 'research_validation_details'
    ));
