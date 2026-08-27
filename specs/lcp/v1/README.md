# LCP v1 Protocol Schemas

LCP v1 separates machine-readable structure from semantic governance:

- JSON Schema Draft 2020-12 rejects unknown fields, invalid types, invalid enums, missing references, and structurally impossible quality states.
- Domain validators recompute hashes, compare assurance ranks and document versions, validate graph topology, resolve cross-object references, and prove persistence.
- A Schema-valid payload is an input candidate. It is not automatically executable, committed, Accepted, or Verified.

Every top-level payload carries `schema_version: "lcp/1.0"`. Hashes use `sha256:<64 lowercase hex>`.

## Schema responsibilities

| Schema | Structurally enforced | Domain validation still required |
|---|---|---|
| `writing-contract` | Four user controls, field provenance, inference state, policy enums | Canonical hash, length ordering, value-hash binding, immutable version transition |
| `writing-plan` | IntentPlan, versioned capabilities, bounded nodes/fallback targets, StrategyDecision selection source | Selected-plan equality, DAG reachability/acyclicity, capability/permission/budget lookup, typed artifact and validator flow |
| `artifact` | Producer attempt, idempotency key, content lifecycle and provenance | Content checksum, immutable transition, parent/reference existence |
| `document-ast` | Canonical node names, provenance/content hashes, required text/link/citation fields | Hash recomputation, safe link schemes, exact child grammar, unique block IDs, revision conflict checks |
| `quality-report` | Accepted/Verified structural gates, BLOCKER non-waiver, validator and snapshot requirements | Assurance rank comparison, version ID equality, decision-reference existence, recomputation of state |
| `run-event` | Ordered event identity and node attempt/idempotency fields | Sequence monotonicity, deduplication, causation and ledger checksum |
| `snapshot-manifest` | Partial versus complete shape and full provenance categories | Referenced-object hashes, durable persistence proof, logical replay |

## Fixture policy

Each schema has a `*.valid.json` and `*.invalid.json` pair. Quality additionally covers Candidate, Accepted, and Verified; Snapshot additionally covers partial and complete checkpoints. Invalid fixtures must fail for a governance-relevant reason rather than malformed JSON.

Syntax check:

```bash
jq empty specs/lcp/v1/*.json specs/lcp/v1/fixtures/*.json
```

The Go WritingContract fixture is also loaded by `backend/internal/writingkernel/contract_test.go`, strictly decoded, hash-validated, and round-tripped.

`document-ast` is the canonical DocumentVersion wire envelope. Its node vocabulary is shared by the Lumin Markdown compiler and `writingkernel`; legacy `list`/`quote`/`code` spellings are not emitted. Structural JSON Schema checks are followed by Go semantic validation, which recomputes node, document, and version hashes before a version can be committed.

Node `attrs` numbers are semantically restricted to canonical JSON-safe integers (absolute value at most `2^53-1`) so hashes survive JSON persistence without lexical or precision drift. Decimal measurements and larger identifiers must use strings. `content_hash` represents content; `version_hash` additionally binds the complete AST block identity and provenance.

The common capability catalog declares stable outline, draft, quality, finalization, and search identities, but it does not treat legacy step names as executable bindings. A host must register a concrete typed function whose accepted inputs and produced outputs cover the manifest before the capability becomes available. The registry deliberately exposes no direct execution method: Task 7's governed orchestrator is the sole future consumer and must enforce DAG readiness, persisted Artifact provenance, NodeAttempt idempotency, runtime bounds, permissions, cancellation, and validator gates. Until those adapters and the orchestrator are composed, plans preserve their intended topology but remain non-runnable `T4`. Independent fact/evidence validators are also intentionally absent because the current legacy review steps cannot guarantee them; sourced and strict plans therefore fail closed instead of silently claiming lower assurance.
