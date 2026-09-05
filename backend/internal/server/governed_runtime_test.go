package server

import (
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
)

// M0b-1 gating: the factory must refuse to mount anything unless the operator
// explicitly selects a wired mode, and must not half-build on missing deps.
func TestNewGovernedWritingRuntimeModeGating(t *testing.T) {
	// off → no runtime, no error: the serving path stays exactly as pre-V3.0.
	runtime, err := newGovernedWritingRuntime(nil, writingruntime.RuntimeModeOff, governedRuntimeDependencies{}, nil)
	if err != nil || runtime != nil {
		t.Fatalf("off must yield (nil, nil), got (%v, %v)", runtime, err)
	}

	// allowlist is not wired in M0b-1 — it errors rather than silently
	// serving shadow under an allowlist label.
	if _, err := newGovernedWritingRuntime(nil, writingruntime.RuntimeModeAllowlist, governedRuntimeDependencies{}, nil); err == nil ||
		!strings.Contains(err.Error(), "not wired") {
		t.Fatalf("allowlist should error as not-wired, got %v", err)
	}

	// shadow with missing dependencies is refused before any construction.
	if _, err := newGovernedWritingRuntime(nil, writingruntime.RuntimeModeShadow, governedRuntimeDependencies{}, nil); err == nil ||
		!strings.Contains(err.Error(), "dependencies are required") {
		t.Fatalf("shadow with nil deps should error, got %v", err)
	}
}
