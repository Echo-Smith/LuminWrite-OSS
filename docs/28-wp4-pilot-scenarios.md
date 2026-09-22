# WP4: Three Core Writing Flows — Pilot Scenarios & Acceptance Criteria

**Date**: 2026-09-22
**Status**: Ready for pilot
**Context Compiler**: Enabled (B candidate promoted per ablation decision)

## Three Core Writing Flows

### 1. Long-Form Creation (长文创作)

**Flow**: Brief → Materials → Structure → Segmented Writing → Full Consolidation

**Orchestration Mode**: `outline_first` (tpl_outline_first_v1) or `strict_research` (tpl_strict_research_v1)

**Evidence Policy**: `long_form` — governed node at index 1 (`draft`)

**Node Graph**:
```
contract → outline → draft → quality → finalize → revision_set
```

**Pilot Scenario**: Write a 3000-word industry analysis report from a brief and 3-5 source materials.

**Acceptance Criteria**:
- [ ] Contract specifies document type, audience, length target, style constraints
- [ ] Outline artifact produced with ≥3 hierarchical levels
- [ ] Draft artifact covers all outline sections with consistent terminology
- [ ] Quality gate scores ≥80 on all five rubric dimensions
- [ ] Final revision_set preserves factual claims from source materials
- [ ] ContextEnvelope compiled for each node attempt (deterministic hash)
- [ ] RunFingerprint captures contract_hash, plan, capability, envelope, model, output

### 2. Multi-Material Synthesis (多材料综合)

**Flow**: Parallel Understanding → Conflict Identification → Synthesized Expression → Source Tracing

**Orchestration Mode**: `sourced` (tpl_sourced_v1)

**Evidence Policy**: `multi_material` — governed node at index 1 (`synthesis`)

**Node Graph**:
```
contract + materials → synthesis → quality → finalize → revision_set
```

**Pilot Scenario**: Synthesize 3 market research reports with conflicting data into a unified analysis with discrepancy explanations.

**Acceptance Criteria**:
- [ ] All input materials represented in EvidenceView with source_ref lineage
- [ ] Conflicting claims identified and flagged in synthesis artifact
- [ ] Each factual claim traces to at least one source via EvidenceViewItem
- [ ] Contradiction status recorded per claim (`supports`/`contradicts`/`neutral`)
- [ ] Quality gate scores ≥80 on sourceFidelity dimension
- [ ] EvidenceView projection deterministic across replay runs

### 3. Faithful Rewrite (忠实改写)

**Flow**: Polish/Restructure while preserving facts, viewpoints, and author voice

**Orchestration Mode**: `fast` (tpl_fast_v1)

**Evidence Policy**: `faithful_rewrite` — governed node at index 0 (`rewrite`)

**Node Graph**:
```
contract + materials + article → rewrite → quality → finalize → revision_set
```

**Pilot Scenario**: Polish a 2000-word corporate announcement while excluding internal HR information and preserving public messaging.

**Acceptance Criteria**:
- [ ] Input article captured as `article` context field
- [ ] Rewrite preserves all factual claims (edit distance ratio ≤0.3)
- [ ] Excluded content (PII, internal info) absent from output
- [ ] Quality gate scores ≥80 on styleConsistency dimension
- [ ] Source fidelity maintained: no fabricated claims added
- [ ] Deterministic checks verify no leakage of marked private content

## Pilot Deployment

### Configuration

```yaml
contextCompiler:
  enabled: true          # B candidate promoted
  compilerVersion: 2
  persistEnvelopes: true # offline replay support

userMemory:
  enabled: false         # C deferred per ablation decision

projectMemory:
  enabled: false         # D deferred per ablation decision
```

### Entry Points

1. **REST API**: `POST /api/v2/documents` → `POST /api/v2/contracts` → `POST /api/v2/plans` → `POST /api/v2/runs`
2. **Frontend**: Writing workspace → "New Document" → select flow type
3. **CLI**: `go run ./cmd/seed-ablation-cases/ --seed-cases` for test data

### Monitoring

- **RunFingerprint**: Every run produces complete identity binding
- **ContextEnvelope**: Compiled per node attempt, persisted for replay
- **EvidenceView**: Deterministic projection from 5 source types
- **Quality Gates**: Five-dimension rubric scoring per artifact
- **Terminal State Guards**: DB-level prevention of writes to terminal runs

### Success Metrics (4-week pilot)

| Metric | Target | Measurement |
|--------|--------|-------------|
| Task completion rate | ≥95% | completed_runs / total_runs |
| Quality gate pass rate | ≥80% | runs with all gates passing |
| ContextEnvelope compile success | ≥99% | envelopes / node_attempts |
| Offline replay success | 100% | replay_runs / sampled_runs |
| User satisfaction (1-5) | ≥4.0 | pilot feedback form |

## Verification Checklist

- [x] Three flows E2E tests pass (`TestTask12WritingScenariosTraverseGovernedArtifactGraph`)
- [x] Shadow rollout tests pass (`TestTask12ScenariosRouteRealB2AdaptersThroughShadowRollout`)
- [x] Context Compiler wired in orchestrator (`governed_runtime.go:180`)
- [x] EvidenceView renders from 5 source types (`evidence_view.go`)
- [x] Offline replay reconstitutes envelopes (`offline_replay.go`)
- [x] Terminal state guards enforced (`runtime.go`)
- [x] Full test suite passes (`go test ./...`)
- [x] Frontend flow selection UI complete
- [x] Pilot user onboarding documented

## References

- [19-governed-writing-runtime.md](19-governed-writing-runtime.md) — Core runtime architecture
- [18-project-memory-context-compiler.md](18-project-memory-context-compiler.md) — Context Compiler design
- ablation-promotion-decision.md（商业版仓库文档）— B candidate promotion rationale
- [20-v3-capability-runtime.md](20-v3-capability-runtime.md) — Capability contracts
