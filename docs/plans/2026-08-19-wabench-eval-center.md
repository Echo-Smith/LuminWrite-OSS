# Luminbuddy Eval Center Implementation Plan

> Implementation note: execute this plan task-by-task and keep the production release gate unchanged.

**Goal:** Replace the legacy single evaluation panel with an auditable WABench control center covering overview, datasets, candidates, runs, reviews, Badcases, and release decisions.

**Architecture:** Extend the Task 8–9 WABench repository with read-only center projections plus one bounded human-review Excel import command. Keep the legacy evaluation API read-only and render the new center from `/api/v2/admin/evaluation/wabench/*`; private inputs and outputs never enter list responses. The React page becomes a thin workspace shell over typed API responses and pure presentation helpers.

**Tech Stack:** Go 1.25, PostgreSQL, chi, Excelize v2, React 19, TypeScript 5.8, Vite 7, Tailwind, Radix UI, Lucide, Node native test runner.

---

### Task 1: Center read model and privacy contract

**Files:**
- Create: `backend/internal/database/wabench_center.go`
- Create: `backend/internal/database/wabench_center_test.go`
- Modify: `backend/internal/database/wabench_eval.go`

**Steps:**

1. Write PostgreSQL integration tests for empty-center responses, suite/candidate/run projections, gate summaries, Badcase root causes, review provenance, arbitration states, and private-text masking.
2. Run `go test -vet=off ./internal/database -run WABenchCenter` and verify the tests fail because the center repository methods do not exist.
3. Add typed projections for overview, suites, candidates, runs, reviews, Badcases, and releases. Return hashes, storage modes, controlled refs, counts, and metadata; never return private input/output text.
4. Derive arbitration status as `not_required`, `pending`, or `resolved` from independent human reviews and an explicit arbitration review marker in review evidence.
5. Run the focused database tests and verify they pass.

### Task 2: Bounded Chinese Excel review importer

**Files:**
- Create: `backend/internal/services/wabench_excel_import.go`
- Create: `backend/internal/services/wabench_excel_import_test.go`
- Modify: `backend/go.mod`
- Modify: `backend/go.sum`

**Steps:**

1. Add Excelize v2.11.x and write failing tests for Chinese headers, five 1–5 scores, acceptance, modification burden, root causes, reviewer provenance, arbitration role, duplicate review IDs, unknown output IDs, malformed workbooks, row limits, and invalid enums.
2. Use `excelize.OpenReader` with unzip/XML limits and `Rows` iteration. Accept only `.xlsx`, cap the HTTP body and row count, close both workbook and row iterator, and collect row-numbered validation errors before writing.
3. Validate the complete workbook before a single transaction inserts immutable human-review records. A duplicate review ID must fail rather than overwrite audit history.
4. Run `go test -vet=off ./internal/services -run WABenchExcel` and verify all importer tests pass.

### Task 3: Admin WABench Center API

**Files:**
- Modify: `backend/internal/server/server.go`
- Modify: `backend/internal/server/handlers_wabench.go`
- Create: `backend/internal/server/handlers_wabench_center.go`

**Steps:**

1. Add admin-protected GET endpoints for `overview`, `suites`, `candidates`, `runs`, `reviews`, `badcases`, and `releases`.
2. Add `POST /api/v2/admin/evaluation/wabench/reviews/import` with a bounded multipart body and explicit schema/validation error responses.
3. Preserve the existing candidate, run, report, and red-team endpoints; do not switch the production release gate.
4. Add minimal handler verification for authorization, empty state, malformed query/body, oversized upload, and repository-unavailable responses.
5. Run `go test -vet=off ./internal/server ./internal/database ./internal/services`.

### Task 4: Typed frontend contract and pure state helpers

**Files:**
- Modify: `frontend/src/lib/admin-api.ts`
- Modify: `frontend/src/lib/admin-types.ts`
- Create: `frontend/src/lib/wabench-eval.ts`
- Create: `frontend/tests/wabench-eval.test.ts`
- Modify: `frontend/package.json`

**Steps:**

1. Write Node-native TypeScript tests for workspace labels, score formatting, release-state priority, privacy labels, review disagreement, root-cause labels, and API error normalization.
2. Run `npm run test:wabench` and verify failure before the helper module exists.
3. Define exact WABench response types without `any`; boundary JSON remains `unknown` until narrowed.
4. Add GET/upload helpers using the existing admin token and error envelope. Multipart upload must not manually set the content-type boundary.
5. Run `npm run test:wabench` and `npx tsc -b`.

### Task 5: Industrial Eval Center shell and seven workspaces

**Files:**
- Replace: `frontend/src/pages/admin/evaluation-panel.tsx`
- Create: `frontend/src/components/evaluation/eval-center-shell.tsx`
- Create: `frontend/src/components/evaluation/eval-overview.tsx`
- Create: `frontend/src/components/evaluation/eval-datasets.tsx`
- Create: `frontend/src/components/evaluation/eval-candidates.tsx`
- Create: `frontend/src/components/evaluation/eval-runs.tsx`
- Create: `frontend/src/components/evaluation/eval-reviews.tsx`
- Create: `frontend/src/components/evaluation/eval-badcases.tsx`
- Create: `frontend/src/components/evaluation/eval-release.tsx`
- Create: `frontend/src/components/evaluation/eval-states.tsx`
- Create: `frontend/src/components/evaluation/index.ts`
- Modify: `frontend/src/index.css`

**Steps:**

1. Build an industrial/utilitarian shell: asymmetric workspace rail, fixed candidate/environment/gate decision strip, dense tables, side inspection areas, and semantic status color tokens.
2. Implement explicit loading, empty, API failure, permission denied, and privacy-masked states for every workspace.
3. Implement datasets, immutable candidate manifests, run progress/failure stages, provenance-aware reviews, Badcase ownership/regression fields, and release evidence/rollback views.
4. Keep legacy results visually identified as legacy diagnostic data and do not combine them with WABench scores.
5. Add responsive record layouts, keyboard focus states, 44px touch targets, and reduced-motion behavior.

### Task 6: Excel import, disagreement, and arbitration workflow

**Files:**
- Create: `frontend/src/components/evaluation/review-import-panel.tsx`
- Create: `frontend/src/components/evaluation/review-provenance.tsx`
- Modify: `frontend/src/components/evaluation/eval-reviews.tsx`

**Steps:**

1. Add an `.xlsx` file chooser with visible label, upload size/type validation, disabled/loading/success/error states, and row-numbered server validation output.
2. Display reviewer, role, human/model/rule method, timestamp, label source, blind-review flag, and arbitration status for every review.
3. Show disagreements without averaging them away; display the arbitration review as an independent decision.
4. Refresh overview/reviews/Badcases after a successful import.

### Task 7: Documentation and verification

**Files:**
- Create: `docs/16-wabench-eval-center.md`
- Modify: `README.md`

**Steps:**

1. Document roles, endpoints, Excel headers, privacy behavior, arbitration semantics, release limitations, and 1Panel-facing configuration impact (none beyond the existing admin auth/database setup).
2. Run `gofmt` only on newly added/edited Go files, then `go test -vet=off ./...`; separately record the pre-existing `internal/agent/tools.go:1352` vet failure from default `go test ./...`.
3. Run `npm run test:wabench`, `npm run lint`, and `npm run build` in `frontend`.
4. Start the local backend/frontend with test data, open the admin evaluation route in a browser, and verify navigation, empty/error/privacy states, Excel upload, run inspection, and an adjacent admin route with no new console errors.
5. Run `git diff --check`, scan changed files for secrets/private text, update the Task 10 checkbox, and commit the implementation without pushing or deploying.
