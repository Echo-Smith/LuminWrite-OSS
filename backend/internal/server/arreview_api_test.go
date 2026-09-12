package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

func newArReviewJobFixture() writingstore.ArReviewJob {
	return writingstore.ArReviewJob{
		ID: "arjob_test", OwnerUserID: "user_1", RunID: "run_1", Status: writingstore.ArReviewJobRunning,
		SurrogateProjectID: "lb-" + "0123456789abcdef0123456789abcdef",
		IdempotencyKey:     "ar012-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		CreatedAt:          time.Now(), UpdatedAt: time.Now(),
	}
}

// Unit tests for the AR-012 evaluation service helpers (T10). The store- and
// sidecar-facing behavior is covered by writingstore's TestArReview* suite
// and the arreview package tests.

func TestUsageFromReceiptSumsStages(t *testing.T) {
	receipt := []byte(`{
		"llm": {
			"provider": "sensenova",
			"model": "deepseek-v4-flash",
			"stages": [
				{"stage": "plan", "calls": [{"usage": {"prompt_tokens": 100, "completion_tokens": 20}}]},
				{"stage": "draft", "calls": [{"usage": {"prompt_tokens": 200, "completion_tokens": 400}}]},
				{"stage": "revision", "calls": [{"usage": {}}]}
			]
		}
	}`)
	usage := usageFromReceipt(receipt)
	if usage["measured"] != true {
		t.Fatalf("usage must be marked measured: %v", usage)
	}
	if usage["input_tokens"] != int64(300) || usage["output_tokens"] != int64(420) {
		t.Fatalf("token sums wrong: %v", usage)
	}
	if usage["model"] != "deepseek-v4-flash" {
		t.Fatalf("model attribution wrong: %v", usage)
	}
}

func TestUsageFromReceiptNeverClaimsUnmeasuredCost(t *testing.T) {
	// A receipt without usage numbers must yield an empty map — never a
	// fabricated zero-cost claim.
	if usage := usageFromReceipt([]byte(`{"llm":{"stages":[{"calls":[{}]}]}}`)); len(usage) != 0 {
		t.Fatalf("empty usage must stay empty: %v", usage)
	}
	if usage := usageFromReceipt([]byte(`not json`)); len(usage) != 0 {
		t.Fatalf("undecodable receipt must stay empty: %v", usage)
	}
}

func TestArReviewJobViewShape(t *testing.T) {
	// The API view must JSON-encode stable field names (frontend contract).
	view := arReviewJobViewOf(newArReviewJobFixture())
	payload, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("view must decode as object: %v", err)
	}
	for _, key := range []string{"job_id", "run_id", "status", "artifact_refs", "usage", "corpus_warnings", "created_at", "updated_at"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("view missing stable field %q: %s", key, payload)
		}
	}
}
