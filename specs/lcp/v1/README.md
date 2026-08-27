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
| `writing-plan` | IntentPlan, ExecutablePlan, StrategyDecision, bounded nodes, static validation result | Selected-plan equality, DAG acyclicity, capability/permission/budget lookup, typed artifact flow |
| `artifact` | Producer attempt, idempotency key, content lifecycle and provenance | Content checksum, immutable transition, parent/reference existence |
| `document-ast` | Typed containers, required text/code content, citation source | Unique block IDs, exact child grammar, AST checksum, revision conflict checks |
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
