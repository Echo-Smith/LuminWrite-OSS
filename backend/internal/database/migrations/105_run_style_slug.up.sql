-- 105: per-run style resolution (V3.0 M1.3, docs/23). The governed run is the
-- request unit: the style a run wants must travel with the run, not with the
-- process composition. The slug follows the legacy StyleSlug vocabulary
-- (004_agent_traces.style_slug); an empty/NULL slug means "no per-request
-- style" and resolves to the default profile semantics (nil profile → engine
-- steps fall back to their built-in prompts). Resolution itself (slug →
-- profile, KB binding travels inside the profile) stays a runtime-side
-- resolver so the composition stays style-agnostic.

ALTER TABLE writing_runs ADD COLUMN style_slug VARCHAR(64);
