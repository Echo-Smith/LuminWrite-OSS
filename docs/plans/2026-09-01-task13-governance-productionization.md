# Task13 Governance Productionization Implementation Plan

**Goal:** Make Task11/Task12 durable and allowlist-assessable, then prove the three governed writing families against a live model without authorizing percentage or production traffic.

**Architecture:** Extend the existing PostgreSQL RunLedger and shadow isolation. Add separate persistent shadow content and append-only approvals, enforce a fail-closed promotion provider before route selection, and run a credential-free-by-default live acceptance suite.

**Tech Stack:** Go 1.25, PostgreSQL 17/ParadeDB, OpenAI-compatible Chat Completions, Docker Compose, React/Vite regression gates.

---

### Task 1: Durable schema and store

**Files:**
- Create: `backend/internal/database/migrations/096_governance_productionization.up.sql`
- Create: `backend/internal/database/migrations/096_governance_productionization.down.sql`
- Create: `backend/internal/writingstore/rollout.go`
- Modify: `backend/internal/writingstore/store_test.go`

Write failing PostgreSQL tests for shadow put/get/restart, prefix purge, TTL sweep, evidence health, and immutable approval. Implement migration and store methods, then run the focused writingstore integration tests.

### Task 2: Runtime adapters and gate

**Files:**
- Create: `backend/internal/writingruntime/persistent_rollout.go`
- Create: `backend/internal/writingruntime/persistent_rollout_test.go`
- Create: `backend/internal/writingruntime/promotion_gate.go`
- Create: `backend/internal/writingruntime/promotion_gate_test.go`

Write failing interface and gate tests. Implement adapters for the existing sink/evidence contracts and a gated provider that rejects absent, expired, stale, unhealthy, percentage, and enabled promotion states. Run focused runtime tests and race checks.

### Task 3: Production composition and operator command

**Files:**
- Modify: `backend/internal/server/server.go`
- Create: `backend/internal/server/governed_rollout.go`
- Create: `backend/internal/server/governed_rollout_test.go`
- Create: `backend/cmd/governance-gate/main.go`
- Modify: `docs/runbook.md`

Write a composition test proving production dependencies are PostgreSQL-backed. Wire the durable evidence/sink/gate bundle when the governed store exists. Add an assess/approve command that only records explicit approval after a passing report. Document exact commands and non-authorization boundaries.

### Task 4: Live model vertical acceptance

**Files:**
- Create: `backend/internal/writingruntime/live_vertical_acceptance_test.go`
- Create: `scripts/run-task13-live-acceptance.sh`

Write an environment-gated test that skips without `TASK13_LLM_API_KEY` and `TEST_DATABASE_URL`. Exercise long-form, multi-material, and faithful rewrite using the configured model through real shadow rollout, then assert persistent redacted evidence and isolated shadow bodies. Run it with the temporary local credential.

### Task 5: Mirror, regress, and close evidence

**Files:**
- Mirror shared Task13 files to Commercial.
- Modify: `docs/releases/2026-08-29-governed-runtime-readiness.md`
- Modify: `FILE_INDEX.md`
- Modify: `PROJECT_LEDGER.md`

Run migration, Go, race, frontend lint/build, live acceptance, parity, and secret scans in both repositories. Record actual results, mark completed tasks, and remove temporary credential files. Do not push, deploy, or enable traffic.
