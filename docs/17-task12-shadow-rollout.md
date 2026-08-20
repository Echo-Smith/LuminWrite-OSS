# Task 12 Shadow Rollout Decision

**Date:** 2026-08-20
**Status:** in progress — shadow only

## Decision

WritingAgentBench keeps one common evaluation contract for Luminbuddy V1 and V2: five Rubric dimensions, hard failures, Badcase taxonomy, privacy/audit records, candidate freeze, latency/cost status, and outcome events. Provider and interaction checks are product-specific. They are shown alongside the common result, not converted into a cross-product ranking.

Task 12 may now begin its shadow work. This decision does **not** switch a production release gate, merge the release branch, or deploy a production build.

## Product boundaries

| Product | Shadow gate | Provider boundary |
| --- | --- | --- |
| V1 | Real process-stream evidence, Lexiang-only routing, source-evidence gate, no web-search false trigger | `lexiang` is a V1-only provider check |
| V2 | Authenticated WebSocket rejection, registered-user first write, and same-conversation follow-up | Current knowledge base is `local-pg-kb`; V2 must not claim `lexiang` |

V2 supports three built-in styles and explicitly selected custom styles. Style and Memory settings remain part of its frozen candidate manifest; they are not inferred from V1 configuration.

## Entry evidence

The isolated V2 outer-path E2E run rejected an unauthenticated WebSocket, then completed both a first write and a same-conversation follow-up for an ephemeral registered account. Its private report stores only redacted metadata, trace identifiers, timing, and outcome states. Directional AMD/SenseNova Harness results remain useful diagnostic evidence, but are not treated as production-token or full quality/cost equivalence.

The release candidate is the audited replay branch in draft PR #1. It deliberately excludes the locally-ahead migration history identified in the release integration audit.

## Shadow actions and exit criteria

1. Run WABench in shadow/double-write or read-only observation mode with rollback controls preserved.
2. Continue V1 provider-specific monitoring without applying V1 Lexiang-only assertions to V2.
3. For V2, capture a real outer-path `local-pg-kb` trace with truthful provider labeling.
4. For V2, inject a controlled tool/provider fault and require an explicit degraded or rejected terminal outcome.
5. Complete applicable quality, cost, latency, hard-failure, and human-review evidence before requesting a production gate switch.

Until all exit criteria are met, the WABench result remains a shadow decision aid and the existing production release path stays authoritative.

## 2026-08-20 fault finding

The first controlled database outage exposed a gap: the hybrid local knowledge search converted an unavailable PostgreSQL dependency into an ordinary zero-result response. This is a failed shadow exit check, not a pass. The search path now probes the database only after every fallback returns no result; an unavailable database returns an explicit error, while a healthy empty knowledge base remains a valid zero-result response. The controlled outer-path case must be rerun after this correction.

### Rerun evidence

The rerun used a fresh local PostgreSQL database, a loopback-only V2 backend, and a deterministic OpenAI-compatible stub that only selected `search_knowledge`. The real V2 WebSocket/JWT/Harness/local PostgreSQL path remained under test.

- Healthy path: redacted run `v2_auth_e2e_20260820140520279_ed25f2` rejected unauthenticated access, emitted `search_knowledge=complete` with a healthy zero-result response, and completed a same-conversation follow-up.
- Fault path: redacted run `v2_auth_e2e_20260820140413880_6edd42` rejected unauthenticated access, emitted `search_knowledge=error` when its explicitly named disposable PostgreSQL dependency was stopped, completed with a degraded usable terminal response, and restored the database in `finally`.

Neither case is a quality, cost, production-provider, or production-token equivalence claim. They close the Task 12 shadow routing/abnormal outer-path evidence only.
