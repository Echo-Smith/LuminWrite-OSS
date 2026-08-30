package editorial

import (
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

func TestCollectLegacyPayloadsIsPureAndKeepsCandidateStatusOutsideEditorial(t *testing.T) {
	ctx := engine.NewExecutionContext("trace_test", "user_test", "topic")
	ctx.Article = "# governed draft"
	ctx.ArticleTitle = "governed draft"
	ctx.Outline = &engine.OutlineData{Title: "governed draft", Outline: []engine.OutlineItem{{Point: "one", Type: "section"}}}
	payloads := CollectLegacyPayloads(ctx)
	if len(payloads) != 2 || payloads[0].Type != ArtifactOutline || payloads[1].Type != ArtifactDraft {
		t.Fatalf("payloads=%#v", payloads)
	}
	if payloads[1].MediaType != "text/markdown" || payloads[1].Content != ctx.Article {
		t.Fatalf("draft=%#v", payloads[1])
	}
}
