package engine

import "testing"

func TestRecordArticleOutputProtocolCreatesTraceMarker(t *testing.T) {
	ctx := NewExecutionContext("trace-1", "user-1", "write")

	RecordArticleOutputProtocol(ctx, "legacy_json")

	if ctx.ArticleOutputProtocol != "legacy_json" {
		t.Fatalf("ArticleOutputProtocol = %q, want legacy_json", ctx.ArticleOutputProtocol)
	}
	if len(ctx.StepHistory) != 1 {
		t.Fatalf("StepHistory length = %d, want 1", len(ctx.StepHistory))
	}
	record := ctx.StepHistory[0]
	if record.Step != StepArticleOutput || record.Status != "complete" {
		t.Fatalf("unexpected record: %#v", record)
	}
	result, ok := record.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("Result type = %T, want map[string]interface{}", record.Result)
	}
	if result["protocol"] != "legacy_json" || result["deviated"] != true {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestRecordArticleOutputProtocolUpdatesExistingMarker(t *testing.T) {
	ctx := NewExecutionContext("trace-2", "user-2", "write")
	RecordArticleOutputProtocol(ctx, "missing_title")
	RecordArticleOutputProtocol(ctx, "markdown")

	if len(ctx.StepHistory) != 1 {
		t.Fatalf("StepHistory length = %d, want 1", len(ctx.StepHistory))
	}
	result := ctx.StepHistory[0].Result.(map[string]interface{})
	if result["protocol"] != "markdown" || result["deviated"] != false {
		t.Fatalf("unexpected updated result: %#v", result)
	}
}

func TestRecordArticleOutputProtocolIgnoresEmptyInput(t *testing.T) {
	ctx := NewExecutionContext("trace-3", "user-3", "write")
	RecordArticleOutputProtocol(nil, "markdown")
	RecordArticleOutputProtocol(ctx, "")

	if ctx.ArticleOutputProtocol != "" || len(ctx.StepHistory) != 0 {
		t.Fatalf("empty protocol should not mutate context: %#v", ctx)
	}
}
