# Task13 — Governance Kernel Productionization Design

## Decision

Task13 extends the current Task12 boundaries instead of introducing another rollout service. Runtime evidence remains an append-only projection in `writing_run_events`; shadow bodies move from the in-memory sink to a dedicated PostgreSQL table; allowlist authority is granted only by a versioned approval record plus a fresh durable-evidence health report. Percentage and enabled modes remain denied by the production gate.

## Components

### Persistent storage

Migration 096 creates `writing_shadow_contents` and `writing_rollout_approvals`. Shadow content uses its existing policy-bound key as the primary key and stores media type, bytes, content hash, policy hash, run id, and timestamps. It has no foreign key or writable path into canonical Artifact/Document tables. Approval rows bind the exact policy hash, version, activation key, mode, approver, evidence report, expiry, and reason; update/delete are forbidden by a database trigger.

`writingstore.Store` adds typed put/get/purge/sweep methods for shadow content, evidence-health aggregation over existing RunLedger events, and append/read methods for approvals. `writingruntime` wraps those methods behind the existing `ShadowContentSink`, `ShadowContentReader`, and rollout evidence interfaces.

### Promotion gate

`AllowlistPromotionGate` validates the policy, rejects percentage/enabled modes, loads the exact approval, recalculates evidence health, and returns a stable fail-closed error when the approval is absent, expired, stale, unhealthy, or bound to another policy. `GatedRolloutPolicyProvider` applies this decision before `RolloutExecutor` can select a candidate lane. Construction-time separation between shadow-isolated and canonical candidates remains unchanged.

An operator CLI supports `assess` and `approve`. Approval requires an allowlist policy file, an explicit operator id/reason, and a passing health assessment. It writes the approval record but never changes runtime traffic or deploys code.

### Live acceptance

An environment-gated Go acceptance suite uses the existing OpenAI-compatible `tools.LLMClient` with a test-only Base URL, model id, and key. Three deterministic scenario envelopes ask the live model for long-form creation, multi-material synthesis, and faithful rewrite; returned text is staged through a real shadow gateway and compared through the real rollout executor. PostgreSQL-backed evidence and shadow storage are then queried to prove restart durability, lineage, isolation, quality output, and redaction. Ordinary tests skip this suite when the live-test variables are absent.

## Failure rules

- Shadow evidence persistence failure serves baseline and blocks authority.
- Candidate-authoritative evidence failure returns no provisional result.
- Shadow body persistence failure fails only the shadow lane; baseline remains independent.
- An unhealthy or missing approval prevents candidate selection.
- No credential, prompt body, model response body, or shadow body is copied into rollout evidence.

## Verification

Unit tests cover validation and failure states. PostgreSQL integration tests cover migration, restart reads, purge/sweep, append-only approval, evidence aggregation, and fail-closed gating. The live suite proves the three model-backed verticals. Both repositories then run Go tests/race, frontend lint/build, migration checks, parity comparison, and secret scans.
