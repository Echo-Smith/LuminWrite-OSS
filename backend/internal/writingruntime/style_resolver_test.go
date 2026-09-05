package writingruntime

import (
	"context"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

// profileAwareStep records whether its factory saw a profile.
type profileAwareStep struct {
	slug    string
	profile *profile.StyleProfile
}

func (step *profileAwareStep) Name() engine.StepName { return "profile_aware" }
func (step *profileAwareStep) CanPause() bool        { return false }
func (step *profileAwareStep) Execute(_ context.Context, execCtx *engine.ExecutionContext, _ engine.EventEmitter) error {
	if step.profile != nil {
		execCtx.Article = "styled:" + step.profile.Slug
	} else {
		execCtx.Article = "styled:default"
	}
	return nil
}

// fakeStyleResolver returns the canned profile and records the slugs asked.
type fakeStyleResolver struct {
	slug    string
	profile *profile.StyleProfile
	err     error
	asked   []string
}

func (resolver *fakeStyleResolver) ResolveProfile(slug, userID string) (*profile.StyleProfile, error) {
	resolver.asked = append(resolver.asked, slug)
	if resolver.err != nil {
		return nil, resolver.err
	}
	if slug == resolver.slug {
		return resolver.profile, nil
	}
	return nil, nil
}

func styleRunnerInput(slug, userID string) LegacyNodeInput {
	request := ExecutionRequest{RunID: "run_style", NodeID: "node_draft", Attempt: 1,
		Node: writingplan.PlanNode{NodeID: "node_draft", Kind: writingplan.NodeAction,
			Capability: "core.draft.generate", OutputArtifactTypes: []writingplan.ArtifactType{"full_draft"}}}
	request.StyleSlug = slug
	request.UserID = userID
	return LegacyNodeInput{Request: request,
		Payloads: map[writingplan.ArtifactType][][]byte{}}
}

func TestEngineStepRunnerInjectsResolvedProfile(t *testing.T) {
	want := &profile.StyleProfile{Slug: "custom", Name: "定制风格"}
	resolver := &fakeStyleResolver{slug: "custom", profile: want}
	runner := EngineStepRunner{Styles: resolver,
		StepFactory: func(env StepEnv) (engine.Step, error) {
			return &profileAwareStep{slug: env.Request.StyleSlug, profile: env.Profile}, nil
		},
		Usage: func(*engine.ExecutionContext) (LegacyUsage, error) { return LegacyUsage{Measured: true}, nil }}
	outputs, _, err := runner.Run(context.Background(), styleRunnerInput("custom", "user_1"))
	if err != nil {
		t.Fatal(err)
	}
	if string(outputs[0].Body) != "styled:custom" {
		t.Fatalf("profile not injected: %s", outputs[0].Body)
	}
	if len(resolver.asked) != 1 || resolver.asked[0] != "custom" {
		t.Fatalf("resolver asked %#v", resolver.asked)
	}
}

func TestEngineStepRunnerDefaultsOnEmptySlug(t *testing.T) {
	resolver := &fakeStyleResolver{}
	runner := EngineStepRunner{Styles: resolver,
		StepFactory: func(env StepEnv) (engine.Step, error) { return &profileAwareStep{profile: env.Profile}, nil },
		Usage:       func(*engine.ExecutionContext) (LegacyUsage, error) { return LegacyUsage{Measured: true}, nil }}
	outputs, _, err := runner.Run(context.Background(), styleRunnerInput("", ""))
	if err != nil {
		t.Fatal(err)
	}
	if string(outputs[0].Body) != "styled:default" {
		t.Fatalf("empty slug must mean default semantics: %s", outputs[0].Body)
	}
	if len(resolver.asked) != 0 {
		t.Fatalf("resolver must not be asked for empty slug: %#v", resolver.asked)
	}
}

func TestEngineStepRunnerDegradesOnResolverMissAndError(t *testing.T) {
	for name, resolver := range map[string]StyleResolver{
		"miss":  &fakeStyleResolver{slug: "other"},
		"error": &fakeStyleResolver{slug: "custom", err: context.DeadlineExceeded},
		"nil":   nil,
	} {
		runner := EngineStepRunner{Styles: resolver,
			StepFactory: func(env StepEnv) (engine.Step, error) { return &profileAwareStep{profile: env.Profile}, nil },
			Usage:       func(*engine.ExecutionContext) (LegacyUsage, error) { return LegacyUsage{Measured: true}, nil }}
		outputs, _, err := runner.Run(context.Background(), styleRunnerInput("custom", ""))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(outputs[0].Body) != "styled:default" {
			t.Fatalf("%s: resolver failure must degrade to default: %s", name, outputs[0].Body)
		}
	}
}

func TestEngineStepRunnerFactoryErrorFailsNode(t *testing.T) {
	runner := EngineStepRunner{StepFactory: func(StepEnv) (engine.Step, error) {
		return nil, ErrRuntimeNotReady
	}, Usage: func(*engine.ExecutionContext) (LegacyUsage, error) { return LegacyUsage{Measured: true}, nil }}
	if _, _, err := runner.Run(context.Background(), styleRunnerInput("", "")); err == nil {
		t.Fatal("factory error must surface as node failure")
	}
}

func TestLoaderStyleResolverResolvesAndDegrades(t *testing.T) {
	loader := profile.NewLoader()
	resolver := LoaderStyleResolver{Loader: loader}
	slug := ""
	for _, candidate := range loader.ListAll() {
		slug = candidate.Slug
		break
	}
	if slug == "" {
		t.Skip("no builtin profiles available")
	}
	got, err := resolver.ResolveProfile(slug, "")
	if err != nil || got == nil || got.Slug != slug {
		t.Fatalf("resolve builtin %q: got=%v err=%v", slug, got, err)
	}
	// Unknown slugs ride the loader's own fallback (legacy parity): the
	// resolver returns a profile, never an error.
	if got, err := resolver.ResolveProfile("missing_style", ""); err != nil || got == nil {
		t.Fatalf("unknown slug must fall back to the loader default: got=%v err=%v", got, err)
	}
	if got, err := resolver.ResolveProfile("my_custom", "user_1"); err != nil || got != nil {
		t.Fatalf("user slugs degrade until M1.4 wires the user store: got=%v err=%v", got, err)
	}
	if got, err := (LoaderStyleResolver{}).ResolveProfile(slug, ""); err != nil || got != nil {
		t.Fatal("nil loader must degrade")
	}
	if got, err := (LoaderStyleResolver{Loader: loader}).ResolveProfile("", ""); err != nil || got != nil {
		t.Fatal("empty slug must degrade without asking the loader")
	}
}
