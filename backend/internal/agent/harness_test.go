package agent

import (
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

func TestBuildLLMOptionsPreservesConfiguredModel(t *testing.T) {
	intents := []Intent{IntentWriting, IntentPolish, IntentShorten, IntentExpand, IntentChat}

	for _, intent := range intents {
		t.Run(string(intent), func(t *testing.T) {
			req := &tools.LLMRequest{Model: "DeepSeek-V4-Flash"}
			for _, opt := range (&Harness{}).buildLLMOptions(intent, nil) {
				opt(req)
			}

			if req.Model != "DeepSeek-V4-Flash" {
				t.Fatalf("buildLLMOptions(%q) changed configured model to %q", intent, req.Model)
			}
		})
	}
}
