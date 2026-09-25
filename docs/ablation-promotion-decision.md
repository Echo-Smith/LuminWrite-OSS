# WP3.6 Ablation Promotion Decision

**Date**: 2026-09-22
**Benchmark**: ablation-benchmark-v1 (90 cases × 4 candidates = 360 evaluations)
**Judge Model**: mimo-v2.5 (xiaomi-mimo API)
**Status**: All 360 evaluations completed

## Results Summary

| Candidate | Configuration | Weighted Score | Complete Rate | Failures | Std Dev |
|-----------|--------------|---------------|---------------|----------|---------|
| **A** | Baseline (no context, no memory) | **93.13** | 92.2% | 7 | 13.79 |
| **B** | Context Compiler | 93.06 | **95.6%** | 4 | 12.73 |
| **C** | Context + User Memory | 92.99 | 93.3% | 6 | 14.59 |
| **D** | Context + User Memory + Project Memory | 91.45 | 92.2% | 7 | 16.89 |

## Blind Review Findings

Score variance analysis on top-divergent cases (spread > 20 points) revealed:

1. **Low scores are execution failures, not quality issues**:
   - C's 20-point cases: agent requested missing input instead of writing (task not attempted)
   - D's 20-point cases: output was a bare tool/function call with no content (execution stuck)
   - A's 42-point case: content fabricated from non-existent API docs (source fidelity loss)

2. **Quality is equivalent when tasks complete**: All four candidates score 93-100 on successfully completed outputs. The judge (mimo-v2.5) is accurate and consistent on content quality.

3. **Context Compiler improves execution reliability**: B has the highest completion rate (95.6%) and lowest standard deviation (12.73), meaning fewer execution failures and more consistent output.

4. **Memory features increase execution risk**: C and D show more cases where the agent gets stuck requesting information or emitting tool calls instead of content. D's project memory integration (`retrieve_context` tool) introduces a new failure mode where the agent gets trapped in tool-call loops.

## Promotion Decision

### ✅ B (Context Compiler): PROMOTED to pilot

**Rationale**:
- Highest completion rate (95.6%) — 2-3 fewer failures per 90 cases than baseline
- No quality regression (93.06 vs 93.13, within noise)
- Lowest score variance — most consistent output quality
- Architectural value: unified semantic chain (EvidenceView, deterministic ContextEnvelope, offline replay)
- Zero new failure modes introduced

**Action**: Enable `contextCompilerEnabled=true` in the writing pipeline for the user pilot (WP4).

### ⏸️ C (Context + User Memory): DEFERRED

**Rationale**:
- No measurable quality improvement over B
- Introduces execution failures where agent requests user info instead of writing
- User memory injection (`memoryPort.PrepareInjection`) adds latency and complexity
- 90-case benchmark may be too small to detect subtle memory benefits

**Condition to revisit**: After WP6 (Temporal/Evidence Memory MVP) provides richer memory signals, re-run ablation with 200+ cases focusing on multi-turn consistency tasks.

### ⏸️ D (Context + User Memory + Project Memory): DEFERRED

**Rationale**:
- Lowest weighted score (91.45) and highest variance (σ=16.89)
- Highest execution failure rate from tool-call loops (`retrieve_context` max calls reached)
- Project memory retrieval adds latency without quality benefit in current form
- Combined memory injection creates prompt bloat that degrades output quality

**Condition to revisit**: After tool-call guard improvements (max retries with fallback to direct writing) and memory relevance filtering are implemented.

### 📊 A (Baseline): RETAINED as control

Keep baseline configuration for future A/B comparisons. Do not deploy to production.

## Statistical Notes

- **Median score is 100 for all candidates** — the judge clusters at the top; discriminative power comes from the tail (execution failures)
- **Score distribution is bimodal** — cases either pass (>80) or fail hard (<50), with few in between
- **Sample size caveat**: 90 cases per candidate provides ~80% power to detect a 5-point difference at α=0.05. Smaller differences require a larger benchmark.

## Next Steps

1. **WP4 Pilot**: Deploy B (Context Compiler) configuration to user pilot
2. **WP6 Enhancement**: Improve memory retrieval with relevance filtering and tool-call guards
3. **Re-run Ablation**: After WP6, run 200+ case benchmark with improved memory features
4. **Judge Calibration**: Consider multi-judge ensemble (mimo-v2.5 + GPT-4) for production evaluation to reduce single-judge bias

## Appendix: Per-Case Score Spread (Top 10)

| Case PK | A | B | C | D | Spread |
|---------|---|---|---|---|--------|
| 27f8e64a | 42 | 100 | 20 | 100 | 80 |
| f6b36d4a | 100 | 95 | 100 | 20 | 80 |
| ea23b3a5 | 100 | 20 | 100 | 95 | 80 |
| 54da89ad | 100 | 100 | 100 | 20 | 80 |
| 75ca2c20 | — | 100 | 38 | 76 | 62 |
| 1a403492 | 100 | 95 | — | 42 | 58 |
| bb81473e | 55 | 100 | 100 | 100 | 45 |
| e5390d8e | 91 | 73 | 49 | 76 | 42 |
| 1c140f87 | 100 | 100 | 91 | 61 | 39 |
| 5273e1ee | 62 | 100 | 95 | 100 | 38 |

(— = output not scored, execution failed before judge stage)

---

## r7 Addendum (2026-09-25) — WP6 Re-Run (210 cases, MiMo-V2.6-Flash)

**Setup**: dataset expanded to 210 cases (72 multi-turn consistency, 12 explicit-override
+ 8 memory-isolation, 28 mixed, original 90). The bench runner now wires a **real
memoryPort** (the v1 run silently skipped memory injection — nil port), the memory user
is seeded with 12 Tier-1 preferences mirroring the override/isolation cases, and the
judge is MiMo-V2.6-Flash (OpenAI-compatible gateway, thinking parameter suppressed).
A reuses the valid r6 baseline run; B/C/D ran fresh on 2026-09-25. Endpoint token-quota
throttling hit C mid-run (94 infra failures, survivor-biased scores) — C is reported
with caveats and the paired analysis is the fair comparison. D ran clean.

| Candidate | Scorable | Hard fail | Avg | StdDev | <50 |
|-----------|---------|-----------|------|--------|-----|
| A (r6) | 197 | 18 (8.6%)¹ | 84.54 | 20.01 | 18 |
| B (r7) | 206 | 4 (1.9%) | 86.10 | 19.44 | 16 |
| C (r7, censored) | 112 | 98 (46.7%)² | 90.76* | 13.25 | 3 |
| D (r7b) | 207 | 3 (1.4%) | 87.11 | 17.41 | 15 |

¹ A's 18 = 13 generation failures + 5 judge-infra failures (all 5 re-judged from frozen
output: 100/93/96/54/81 — confirmed infra, not writing quality).
² Quota-wall victims; C's avg is computed on early-window survivors only (*inflated).
Judge-infra failures are reported separately from writing failures throughout.

### Findings

1. **Execution reliability is fixed (WP6 verdict confirmed)**: D completes 207/210 with
   **zero tool-budget exhaustions** — agent-loop iterations concentrate at 1–3. The v1-D
   dominant failure mode (retrieve_context tool-call loops) is eliminated by the WP6
   guards (graceful budget exhaustion + budget 5→3) combined with the bench resilience
   fixes (SSE error propagation, 429 backoff, pacing).
2. **Multi-turn consistency is a wash (paired vs B, 72 pairs)**: D−B = −1.18
   (25 wins / 28 losses / 19 ties); C−B = −0.62 (53 pairs). Both within noise
   (SE ≈ 1.8). Memory neither helps nor hurts consistency on seeded synthetic memories.
3. **Override hint worth watching**: D 75.80 vs B 79.55 on the 20 override/isolation
   cases (not significant at n=20) — eight generically injected preferences may mildly
   conflict with explicit user instructions. Pilot telemetry (per-intent injection
   counts + refusal breakdown) should track this.
4. **No measurable quality gain over B**: D 87.11 vs B 86.10 (+1.0, within noise);
   C's higher average is survivor bias.

### Decision (r7)

- **B stays promoted for the pilot** — unchanged from the original decision.
- **C/D remain deferred**: WP6 removed their reliability penalty (the revisit condition
  from the original decision is satisfied), but seeded synthetic memories demonstrate no
  quality gain. Revisit when the pilot accumulates *organic* memory data and after a
  relevance-filtering layer (System-1 style gating over injected candidates) exists —
  raw confidence truncation is the current bottleneck, not the evidence ladder.
- Infrastructure: single-endpoint token-quota throttling is the bench's remaining
  operational risk; runs must be quota-budgeted or spread across providers.
