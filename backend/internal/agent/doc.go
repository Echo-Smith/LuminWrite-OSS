// Package agent is FROZEN — a transitional compatibility package.
//
// The governed writing runtime (internal/writingruntime, driven by the
// capability registry and core executors) is the production execution
// path. This package survives only because two legacy consumers still
// depend on it:
//
//   - internal/writingruntime/executor_adapters.go — the governed
//     HarnessCoreNodeRunner adapts Harness.RunCore as one executor
//     implementation behind the HarnessCoreInvoker contract.
//   - internal/services/wabench_v2_adapter.go — WABench V2 still runs
//     the full Harness.Run lifecycle; its migration to RunCore is
//     tracked separately.
//   - internal/editorial/experiment_runner.go — editorial experiments
//     still use Harness.Run; editorial is converging on the capability
//     executor contract, not on Harness.
//
// Freeze rules (enforced by TestAgentPackageFrozenBoundary in this
// package's directory):
//
//   ALLOWED
//     - bug fixes
//     - compatibility shims
//     - WABench/Editorial migration support
//     - RunCore adapter changes
//
//   FORBIDDEN
//     - new features
//     - new business state
//     - new persistence
//     - new tool capabilities
//     - new memory semantics
//     - new orchestration logic
//     - new importers of this package
//
// The dependency set on this package may only shrink. The end state is
// deletion: RunCore-shaped execution moves to core executors, tool
// definitions move to the capability/tool registries, and session and
// trace persistence retire with the legacy writing path.
//
// Migration status (⑥):
//   - WABench runs Harness.RunCore (⑥B) — no lifecycle ownership.
//   - Editorial experiments run Harness.RunCore (⑥C-1) — no lifecycle
//     ownership. Harness.Run (the full persistent lifecycle) now has ZERO
//     production consumers and is deletion-ready (⑥D).
//   - The governed runtime adapts RunCore through HarnessCoreInvoker
//     (executor_adapters.go) — the surviving production seam.
package agent
