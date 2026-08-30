DO $downgrade_guard$
DECLARE shadow_rows bigint;
BEGIN
    SELECT count(*) INTO shadow_rows FROM writing_shadow_content;
    IF shadow_rows > 0 THEN
        RAISE EXCEPTION 'cannot downgrade migration 096: % shadow content rows are still present; purge or archive them first', shadow_rows;
    END IF;
END
$downgrade_guard$;

DROP TABLE writing_shadow_content;
