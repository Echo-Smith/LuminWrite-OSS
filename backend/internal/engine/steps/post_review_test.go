package steps

import (
	"context"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

func TestPostReviewStrictModeNeverAutoPassesMissingValidatorDependency(t *testing.T) {
	execCtx := engine.NewExecutionContext("trace_strict_review", "user_test", "article")
	execCtx.Article = "正文"
	if err := NewPostReviewStep(nil).RequireSuccess().Execute(context.Background(), execCtx, nil); err == nil {
		t.Fatal("strict governed validator auto-passed without a model client")
	}

	legacy := engine.NewExecutionContext("trace_legacy_review", "user_test", "article")
	legacy.Article = "正文"
	if err := NewPostReviewStep(nil).Execute(context.Background(), legacy, nil); err != nil || legacy.ReviewResult == nil || !legacy.ReviewResult.Passed {
		t.Fatalf("legacy degraded behavior changed: result=%#v err=%v", legacy.ReviewResult, err)
	}
}
