-- 108 down: restore the 107 artifact_type CHECK (which already carried
-- evidence_approval). Durable research artifacts written with the formal
-- types would violate the narrower CHECK after this down migration; per
-- design.md §9 a production rollback closes the feature entry first and
-- keeps artifacts readable — run this down only on throwaway databases.

ALTER TABLE writing_artifacts
    DROP CONSTRAINT chk_writing_artifact_type;

ALTER TABLE writing_artifacts
    ADD CONSTRAINT chk_writing_artifact_type CHECK (artifact_type IN (
        'contract', 'materials', 'brief', 'source_pack', 'research_note',
        'claim_map', 'outline', 'section_draft', 'full_draft',
        'review_report', 'revision_set', 'quality_report',
        'evidence_report', 'fact_report', 'evidence_approval'
    ));
