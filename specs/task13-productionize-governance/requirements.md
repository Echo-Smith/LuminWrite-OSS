# Task13 — Governance Kernel Productionization Requirements

## Problem

Task11/Task12 have proved the governed writing runtime in local shadow, but its rollout evidence and shadow content are process-local. There is no enforceable, auditable allowlist-promotion gate, and the three governed writing families have not completed end-to-end acceptance with a live model. These gaps prevent an evidence-based promotion beyond local shadow.

## Scope

This task productionizes the governance kernel only. It covers durable shadow evidence and content persistence, an explicit allowlist gate and operational procedure, live-model acceptance for long-form creation, multi-material synthesis, and faithful rewrite, plus full regression evidence. It applies to the shared OSS and Commercial kernel; Commercial-only billing/search behavior remains outside the common implementation unless a shared boundary requires it.

## Non-goals

- Do not enable percentage rollout, production traffic, remote push, or deployment.
- Do not replace Harness, Pipeline, Editorial, Memory, or the tool registry.
- Do not store a live-model credential in tracked files, test fixtures, source code, documentation, logs, or persisted rollout evidence.
- Do not treat a successful live-model call as an automatic promotion decision.

## User stories

1. As a release operator, I can persist and query shadow evidence after a process restart without letting shadow content enter canonical Artifact or Document storage.
2. As a release operator, I can determine whether a named subject is permitted to use the candidate-authoritative path from a versioned policy and durable evidence health, with an auditable deny reason.
3. As an evaluator, I can run each of the three writing families against a live OpenAI-compatible model and retain only redacted operational evidence, output references, quality outcomes, and governed-run lineage.
4. As a maintainer, I can reproduce the acceptance and regression procedure in both OSS and Commercial without committing a provider credential.

## Acceptance criteria

### Durable evidence and shadow content

- When a shadow lane creates execution or comparison evidence, the system shall persist the record transactionally in PostgreSQL with the run, policy hash, lane, status, stable reason/error code, timestamps, and redacted metadata.
- When a shadow lane stores content, the system shall persist it only through a shadow namespace/sink that is physically and logically distinct from canonical Artifact and Document references.
- When the process restarts, the system shall load durable evidence and shadow content metadata without reclassifying a shadow reference as canonical.
- If durable evidence persistence fails during shadow routing, the system shall route the request to baseline and record the failure where possible; it shall not run a candidate-authoritative lane.
- If durable evidence or canonical-commit preconditions fail during a candidate-authoritative request, the system shall fail closed with a stable error code.

### Allowlist gate

- When rollout mode is allowlist, the system shall require an explicit activation subject and a versioned allowlist policy that is not expired, disabled, or hash-mismatched.
- When an operator requests promotion from shadow to allowlist, the system shall require a durable-evidence health report and an explicit approval record; it shall never promote from a test result alone.
- If any gate prerequisite is missing, unhealthy, stale, or unverifiable, the system shall deny promotion and expose an auditable reason without falling through to percentage or enabled modes.
- The implementation shall retain the existing construction-time separation between shadow-isolated and candidate-authoritative executors.

### Live-model vertical acceptance

- While the test-only provider environment is present, the acceptance runner shall exercise long-form creation, multi-material synthesis, and faithful rewrite through the governed runtime using `deepseek-v4-flash` over B.AI's OpenAI-compatible Chat Completions endpoint.
- Each scenario shall verify a completed governed run, material/lineage integrity, quality-gate result, baseline/shadow isolation, durable evidence, and absence of canonical shadow references.
- The runner shall redact credentials, prompt/body contents, and provider response bodies from durable evidence and normal test output; it may record hashed identifiers, token counts, duration, model id, status, and stable error code.
- If the test-only provider environment is absent, the live acceptance suite shall skip with an explicit message; ordinary unit and integration tests shall remain offline.

### Regression and parity

- When shared Task13 code changes, OSS and Commercial shall retain byte-equivalent shared implementation and test coverage, excluding documented Commercial-only boundaries.
- Before an allowlist-readiness report is issued, both repositories shall pass Go tests, required PostgreSQL integration tests, frontend lint/build, and the governed live acceptance suite.
- The runbook and readiness report shall state the exact remaining authorization boundary: allowlist may be assessed after the gates pass; percentage and production remain separately unauthorized.
