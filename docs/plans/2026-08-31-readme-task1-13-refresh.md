# Task1–13 README Refresh Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Refresh both editions' Chinese and English README files so they truthfully present the governed writing platform delivered in Task1–13.

**Architecture:** Keep one shared product and runtime narrative, then apply a narrow edition-specific capability section. Treat existing design, specification, release-readiness, code, and CI evidence as the source of truth.

**Tech Stack:** Markdown, Git, ripgrep, repository Makefile validation, GitHub Actions.

---

### Task 1: Establish the documentation baseline

**Files:**
- Create: `docs/plans/2026-08-31-readme-task1-13-refresh-design.md`
- Create: `docs/plans/2026-08-31-readme-task1-13-refresh.md`
- Modify: `FILE_INDEX.md`

1. Record audience, information architecture, edition boundaries, and truthfulness constraints.
2. Register the new documentation responsibilities in the file index.
3. Verify both repositories contain the same design and plan.

### Task 2: Refresh the Chinese README

**Files:**
- Modify: `README.md`

1. Replace the migration-target language with the implemented governed-runtime status.
2. Replace the legacy Harness-first architecture and flow with Contract → Plan → Artifact → Quality → writingstore → Document.
3. Add execution-control, quality-state, material-governance, recovery, and observability descriptions.
4. Correct Quick Start, validation, search/MCP boundary, design-document links, and production status.
5. Add a v0.8.0 Task1–13 grouped changelog.

### Task 3: Refresh the English README

**Files:**
- Modify: `README.en.md`

1. Mirror the Chinese information architecture and claims.
2. Preserve natural English rather than literal translation.
3. Keep product states, error boundaries, commands, and release status semantically identical.

### Task 4: Apply edition-specific boundaries

**Files:**
- Modify: OSS `README.md`, `README.en.md`
- Modify: Commercial `README.md`, `README.en.md`

1. State that interfaces, MCP, local retrieval, and bounded web fetching are shared.
2. State that OSS excludes paid Provider implementations, credentials, and commercial CLIs.
3. State that Commercial may register governed paid search adapters without exposing secrets.

### Task 5: Validate and deliver

1. Run Markdown structure and relative-link checks.
2. Search for stale target-architecture and legacy-primary wording.
3. Compare shared sections across editions and review intentional differences.
4. Run `git diff --check`.
5. Commit and push one documentation branch per repository, then open PRs.
