// Per-request style resolution (V3.0 M1.3, docs/23). The governed run
// carries the style slug a request asked for (writing_runs.style_slug, 105);
// the composition stays style-agnostic and resolves it lazily per node
// attempt, mirroring the legacy server's slug → profile lookup. The resolved
// profile is advisory engine-step configuration: a resolver miss or failure
// degrades to a nil profile (built-in step prompts) and is recorded in the
// step environment — it never fails a governed node, because a style gap is
// not an execution-contract gap.
package writingruntime

import (
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
)

// StepEnv is what an engine step factory needs to build one attempt's step.
// The profile is the per-request resolution (nil = default semantics); the
// request carries the run's StyleSlug for logging and diagnostics.
type StepEnv struct {
	Request ExecutionRequest
	Profile *profile.StyleProfile
}

// StyleResolver maps a run's style slug to the engine-step profile. Slugs
// follow the legacy StyleSlug vocabulary ("my_" prefixes scope to the
// requesting user's custom styles). Implementations must be safe for
// concurrent use; resolution happens per node attempt.
type StyleResolver interface {
	ResolveProfile(slug, userID string) (*profile.StyleProfile, error)
}

// LoaderStyleResolver adapts profile.Loader. Unknown slugs ride the loader's
// own fallback profile (legacy parity with the server's slug lookup); the
// resolver only yields nil — meaning default step semantics — for empty
// slugs, user-owned "my_" slugs (whose resolution needs the requesting user,
// wired by the composition in M1.4), and a nil loader.
type LoaderStyleResolver struct{ Loader *profile.Loader }

func (resolver LoaderStyleResolver) ResolveProfile(slug, userID string) (*profile.StyleProfile, error) {
	if resolver.Loader == nil || slug == "" {
		return nil, nil
	}
	if strings.HasPrefix(slug, "my_") {
		return nil, nil
	}
	if loaded, ok := resolver.Loader.Get(slug); ok {
		return loaded, nil
	}
	return nil, nil
}

// resolveStepProfile is the runtime's single degrade path: any resolver error
// or miss yields a nil profile — engine steps then run their built-in
// prompts, and the gap stays observable through StepEnv for diagnostics.
func resolveStepProfile(resolver StyleResolver, request ExecutionRequest) *profile.StyleProfile {
	if resolver == nil || request.StyleSlug == "" {
		return nil
	}
	profile, err := resolver.ResolveProfile(request.StyleSlug, request.UserID)
	if err != nil {
		return nil
	}
	return profile
}
