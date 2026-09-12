// V3.0 M0 (docs/20 §20.2): the governed writing runtime is wired into the
// serving path behind an explicit mode, defaulting to off so that enabling
// V3.0 is a deliberate, reversible operator choice — never a silent behavior
// change to the live write flow. The mode is parsed once at composition and
// normalized defensively: an unknown or empty value collapses to off, so a
// typo in deployment config can never accidentally mount the runtime.
package writingruntime

import "strings"

// RuntimeMode selects how (or whether) the governed Orchestrator serves runs.
type RuntimeMode string

const (
	// RuntimeModeOff: the governed runtime is not constructed; the writing
	// API behaves exactly as before V3.0 (controller nil, no execution).
	RuntimeModeOff RuntimeMode = "off"
	// RuntimeModeShadow: approved governed runs execute through the
	// Orchestrator via a shadow RolloutExecutor — baseline lane is
	// authoritative and user-visible, the candidate lane runs isolated and
	// only produces evidence (context envelopes, shadow comparison). No
	// user-visible behavior changes.
	RuntimeModeShadow RuntimeMode = "shadow"
	// RuntimeModeAllowlist: subjects on the allowlist serve the candidate
	// lane under the promotion gate; misses keep running shadow. Activation
	// itself remains an independent §9 authorized change — this mode only
	// provides the mechanism, never the authorization.
	RuntimeModeAllowlist RuntimeMode = "allowlist"
)

// NormalizeRuntimeMode maps a raw config/env value to a RuntimeMode. Unknown
// or empty values collapse to RuntimeModeOff and report known=false so the
// composition can log the surprise instead of silently mis-enabling.
func NormalizeRuntimeMode(raw string) (mode RuntimeMode, known bool) {
	switch RuntimeMode(strings.ToLower(strings.TrimSpace(raw))) {
	case RuntimeModeOff:
		return RuntimeModeOff, true
	case RuntimeModeShadow:
		return RuntimeModeShadow, true
	case RuntimeModeAllowlist:
		return RuntimeModeAllowlist, true
	default:
		return RuntimeModeOff, false
	}
}
