-- The canonical content table stores artifact bodies referenced by governed
-- artifacts. Downgrade removes only this cache; artifact rows that still
-- reference canonical content would become unreadable, so the guard refuses
-- while references exist.
DO $downgrade_guard$
DECLARE referenced_rows bigint;
BEGIN
    SELECT count(*) INTO referenced_rows
      FROM writing_artifacts a
      JOIN writing_canonical_content c
        ON a.content_ref = 'db://canonical/' || c.content_key;
    IF referenced_rows > 0 THEN
        RAISE EXCEPTION 'cannot downgrade migration 097: % canonical artifact bodies are still referenced by governed artifacts', referenced_rows;
    END IF;
END
$downgrade_guard$;

DROP TABLE IF EXISTS writing_canonical_content;
