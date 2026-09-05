package writingruntime

import "testing"

func TestNormalizeRuntimeMode(t *testing.T) {
	// Recognized values (case/space-insensitive) map to their mode and report
	// known=true.
	for _, tc := range []struct {
		raw  string
		want RuntimeMode
	}{
		{"off", RuntimeModeOff},
		{"OFF", RuntimeModeOff},
		{" shadow ", RuntimeModeShadow},
		{"Shadow", RuntimeModeShadow},
		{"allowlist", RuntimeModeAllowlist},
		{"ALLOWLIST ", RuntimeModeAllowlist},
	} {
		mode, known := NormalizeRuntimeMode(tc.raw)
		if !known || mode != tc.want {
			t.Fatalf("NormalizeRuntimeMode(%q) = %s/%v, want %s/true", tc.raw, mode, known, tc.want)
		}
	}
	// Empty, blank, and unknown values all collapse to off AND report
	// known=false, so a deployment typo can never accidentally mount the
	// runtime and composition can log the surprise.
	for _, raw := range []string{"", "   ", "production", "on", "governed"} {
		mode, known := NormalizeRuntimeMode(raw)
		if mode != RuntimeModeOff || known {
			t.Fatalf("NormalizeRuntimeMode(%q) = %s/%v, want off/false", raw, mode, known)
		}
	}
}
