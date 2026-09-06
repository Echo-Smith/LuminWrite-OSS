# P0 Service Readiness Implementation Plan

**Goal:** Complete A0–A6 from the 2026-09-06 assessment against the user's uploaded 1Panel baseline.

**Architecture:** Keep the governed kernel and legacy gateways. Add missing capability/style/metrics wiring, persist rollout configuration and execution ownership, and validate through isolated databases. Develop shared code in OSS and copy only changed shared files to Commercial; preserve its existing migration/payment fixes. Runtime stays off by default; package creation does not activate production traffic.

**Tech Stack:** Existing Go, PostgreSQL, React and Prometheus infrastructure; no new framework.

## A1 / A2 — runnable templates and personal styles
Files: backend/internal/server/governed_runners.go, governed_styles.go; backend/internal/writingruntime/dispatch.go; backend/internal/writingstore/runtime.go.
1. Add template activation and two-user personal-style tests; run to expose missing wiring.
2. Activate strict_search and map to research runner; supply material input where selected by contract.
3. Load owner from persisted document, pass UserID, resolve personal style by owner and current version, preserve KB binding and observe fallback.
4. Run focused server/runtime/store tests in isolated PostgreSQL.

## A3 — operational metrics
Files: backend/internal/server/metrics.go, governed_runners.go; monitoring dashboard.
1. Test exporter for context/lifecycle events and verify composition receives MetricsRegistry.
2. Extend existing exporter and add dashboard for completion, latency, usage, shadow and context failures.
3. Verify no high-cardinality/user/content labels and no inferred USD costs.

## A4 — gated allowlist composition
Files: writingstore rollout policy persistence + additive migration; writingruntime service rollout wrapper; server composition; operator CLI.
1. Test persisted reload, missing/expired/mismatched approval, kill switch, subject miss and shadow isolation.
2. Reuse exact-scope gates; construct distinct canonical and isolated candidates. Read active policy per dispatch. Subject comes from persisted owner.
3. Missing/denied policy serves baseline; misses still shadow under the governance policy hash. No automatic approval or activation.
4. Validate real database policy round-trip and stage rejection.

## A5 — durable task ownership and restart recovery
Files: writingstore execution ownership; server governed trigger/controller; runtime recovery/concurrency tests.
1. Test competing workers, duplicate approval, restart, cancellation and uncertain attempts.
2. Use database-backed exclusive ownership with connection loss cancellation; scan durable run status on startup/periodically. Reuse existing checkpoint recovery; uncertain model side effects pause for human review.
3. Serialize resume under the same ownership boundary and observe remote pause/cancel state.
4. Run database and race tests; preserve kernel canonical commit/idempotency gates.

## A6 — acceptance and package
Files: focused tests, docs/releases/2026-09-06-p0-service-readiness.md; upgrade package under output/p0-20260906.
1. Fix E2E fixture to use the private database it creates.
2. Run both repositories' full backend tests with DB, targeted race, frontend checks, migration compatibility checks, and available real-model vertical tests.
3. Prepare blind-review artifacts; distinguish machine validation from unperformed human preference scoring.
4. Create source upgrade package including Commercial baseline fixes, excluding environment secrets and persistent user data; record file hashes, upgrade and rollback instructions.

## A0 — current state
Update PROJECT_LEDGER and README with separate implementation/test/service/deployment states only after evidence exists. Do not rewrite historical records. Record external acceptance gaps honestly.
