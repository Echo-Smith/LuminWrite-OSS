// Context runtime (docs/18 §18.5.6, V2.9 M5): the components that consume an
// envelope's lifecycle on top of the pure compiler — pressure monitoring with
// 0.70 warn / 0.85 compress thresholds, pre-compression under cooldown and
// in-flight guards, and named recovery paths per failure category. These are
// runtime services with wall-clock and concurrency state; they never live in
// the kernel invariants, and the compiler itself stays pure.
package writingruntime

import (
	"fmt"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/contextcompiler"
)

// Precompression decisions for one envelope observation.
type PrecompressionAction string

const (
	// PrecompressionNone: the envelope sits below the compress threshold.
	PrecompressionNone PrecompressionAction = "none"
	// PrecompressionWarn: pressure crossed 0.70 — an out-of-band alert the
	// telemetry carries, but the attempt proceeds unchanged.
	PrecompressionWarn PrecompressionAction = "warn"
	// PrecompressionRecompile: pressure crossed 0.85 and the cooldown +
	// in-flight guards allow a recompression attempt.
	PrecompressionRecompile PrecompressionAction = "recompile"
	// PrecompressionCooling: pressure crossed 0.85 but the capability is
	// still inside the cooldown window since the last recompression.
	PrecompressionCooling PrecompressionAction = "cooling"
	// PrecompressionInFlight: pressure crossed 0.85 but another
	// recompression for the same capability is already running.
	PrecompressionInFlight PrecompressionAction = "in_flight"
)

// RecoveryPath names the recovery route for one envelope failure category
// (docs/18 §18.5.6: recovery paths are named per failure category, not a
// single generic retry).
type RecoveryPath string

const (
	// RecoveryRecompile retries the compilation as-is: the source degraded
	// transiently (compile_failed / source_failed with retryable cause).
	RecoveryRecompile RecoveryPath = "recompile"
	// RecoveryRecompileCompressed retries with a reduced budget: the
	// envelope itself overflowed (resident fail-closed, budget exhausted),
	// so the same inputs cannot succeed unchanged.
	RecoveryRecompileCompressed RecoveryPath = "recompile_compressed"
	// RecoveryReloadSource refreshes the compiler inputs from the store
	// before recompiling: the underlying canon data moved (e.g. the
	// document advanced between planning and execution).
	RecoveryReloadSource RecoveryPath = "reload_source"
	// RecoveryHumanEscalation leaves the loop: canon is bloated beyond any
	// compression the runtime may apply itself (resident overflow with the
	// reduced budget, repeated recompile failures), an operational problem.
	RecoveryHumanEscalation RecoveryPath = "human_escalation"
)

// ContextRuntime consumes envelope lifecycles: it observes pressure,
// performs guarded pre-compression, and names the recovery path for a
// failure. The zero value is usable (thresholds default to the compiler's,
// cooldown to ContextCompressionCooldown).
type ContextRuntime struct {
	// CompressBudgetFactor scales the total budget on a recompression
	// (0 < factor < 1). Zero means 0.8.
	CompressBudgetFactor float64
	// Cooldown is the minimum spacing between recompressions per
	// capability. Zero means ContextCompressionCooldown.
	Cooldown time.Duration

	// Now overrides the wall clock in tests. Zero means time.Now.
	Now func() time.Time

	mu          sync.Mutex
	lastCompile map[string]time.Time
	inFlight    map[string]bool
}

// ContextCompressionCooldown is the default spacing between recompressions
// of one capability: compression is a mitigation, not a mode of operation.
const ContextCompressionCooldown = 5 * time.Minute

// observeClassify maps a compiled envelope to its precompression action
// under the current cooldown and in-flight guards. The caller has already
// established that pressure crossed the compress threshold.
func (runtime *ContextRuntime) observeClassify(capability string, now time.Time) PrecompressionAction {
	cooldown := runtime.Cooldown
	if cooldown <= 0 {
		cooldown = ContextCompressionCooldown
	}
	if runtime.lastCompile == nil {
		runtime.lastCompile = map[string]time.Time{}
	}
	if runtime.inFlight == nil {
		runtime.inFlight = map[string]bool{}
	}
	if runtime.inFlight[capability] {
		return PrecompressionInFlight
	}
	if last, ok := runtime.lastCompile[capability]; ok && now.Sub(last) < cooldown {
		return PrecompressionCooling
	}
	runtime.lastCompile[capability] = now
	runtime.inFlight[capability] = true
	return PrecompressionRecompile
}

// Observe inspects a compiled envelope: below 0.70 nothing happens, between
// 0.70 and 0.85 the envelope is only reported for warn telemetry, at or
// above 0.85 the runtime schedules a recompression under the cooldown and
// in-flight guards. ReleaseRecompression must be called for every
// PrecompressionRecompile result.
func (runtime *ContextRuntime) Observe(capability string, envelope *contextcompiler.Envelope) PrecompressionAction {
	if envelope == nil {
		return PrecompressionNone
	}
	if runtime.Now == nil {
		runtime.Now = func() time.Time { return time.Now().UTC() }
	}
	pressure := envelope.Pressure()
	if pressure < contextcompiler.PressureWarnThreshold {
		return PrecompressionNone
	}
	if pressure < contextcompiler.PressureCompressThreshold {
		return PrecompressionWarn
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.observeClassify(capability, runtime.Now())
}

// ReleaseRecompression closes one in-flight recompression opened by
// Observe. Capability-keyed: the next observation for the same capability
// decides afresh under the cooldown.
func (runtime *ContextRuntime) ReleaseRecompression(capability string) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.inFlight != nil {
		delete(runtime.inFlight, capability)
	}
}

// CompressedBudget returns the budget a recompression of the given envelope
// should target: the original budget scaled by CompressBudgetFactor, never
// dipping below the resident protection (which would fail closed by
// construction).
func (runtime *ContextRuntime) CompressedBudget(envelope *contextcompiler.Envelope) (int, error) {
	if envelope == nil || envelope.TokenBudget() <= 0 {
		return 0, fmt.Errorf("%w: no envelope budget to compress", ErrContextRuntimeInactive)
	}
	factor := runtime.CompressBudgetFactor
	if factor <= 0 || factor >= 1 {
		factor = 0.8
	}
	budget := int(float64(envelope.TokenBudget()) * factor)
	if budget <= contextcompiler.ResidentBudget {
		return 0, fmt.Errorf("%w: compressed budget %d leaves no room beside the resident budget %d", ErrContextRuntimeInactive, budget, contextcompiler.ResidentBudget)
	}
	return budget, nil
}

// RecoveryPathFor names the recovery path for one envelope failure category.
// The mapping is a decision table, not a heuristic: the caller supplies the
// observed category, the runtime answers with the path its operator should
// expect in evidence.
func RecoveryPathFor(category ContextFailureCategory, recompiles int) RecoveryPath {
	switch category {
	case ContextFailureSource:
		if recompiles > 0 {
			return RecoveryHumanEscalation
		}
		return RecoveryReloadSource
	case ContextFailureCompile:
		if recompiles > 0 {
			return RecoveryHumanEscalation
		}
		return RecoveryRecompile
	case ContextFailureResidentOverflow:
		if recompiles > 0 {
			return RecoveryHumanEscalation
		}
		return RecoveryRecompileCompressed
	case ContextFailureBudget:
		return RecoveryRecompileCompressed
	case ContextFailurePersist:
		// Persistence failures never block execution (shadow discipline):
		// there is nothing to recover for the attempt itself.
		return RecoveryRecompile
	default:
		return RecoveryHumanEscalation
	}
}

// ContextFailureCategory enumerates the envelope failure categories the
// recovery decision table maps from. They correspond 1:1 to the telemetry
// statuses compileNodeContext already reports plus the compiler's two
// failure shapes.
type ContextFailureCategory string

const (
	ContextFailureSource           ContextFailureCategory = "source_failed"
	ContextFailureCompile          ContextFailureCategory = "compile_failed"
	ContextFailureResidentOverflow ContextFailureCategory = "resident_overflow"
	ContextFailureBudget           ContextFailureCategory = "budget_exhausted"
	ContextFailurePersist          ContextFailureCategory = "persist_failed"
)

// ErrContextRuntimeInactive marks runtime-level refusals (no budget to
// compress, compressed budget below the resident floor). It is distinct from
// context-gap errors: infrastructure refusals never fail a node.
var ErrContextRuntimeInactive = errContextRuntimeInactive{}

type errContextRuntimeInactive struct{}

func (errContextRuntimeInactive) Error() string { return "writingruntime: context runtime inactive" }
