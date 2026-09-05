-- Remove per-run style resolution.
ALTER TABLE writing_runs DROP COLUMN IF EXISTS style_slug;
