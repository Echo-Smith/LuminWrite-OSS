package server

import (
	"context"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
	"testing"
	"time"
)

type overlapBudgetLedger struct{ brokenBudgetLedger }

func (overlapBudgetLedger) ListRunAttempts(context.Context, string) ([]writingstore.NodeAttempt, error) {
	return []writingstore.NodeAttempt{{NodeID: "discover", ActualDurationMS: 100}, {NodeID: "read", ActualDurationMS: 500}}, nil
}
func (overlapBudgetLedger) ListResearchTasks(context.Context, string, string) ([]writingstore.ResearchTask, error) {
	return []writingstore.ResearchTask{{NodeID: "read", Usage: map[string]any{"duration_ms": 400}}, {NodeID: "recovered", Usage: map[string]any{"duration_ms": 200}}}, nil
}
func TestResearchBudgetDoesNotDoubleCountSubTasks(t *testing.T) {
	guard := NewWallClockResearchBudgetBoundary(overlapBudgetLedger{})
	spent, err := guard.spentMS(context.Background(), writingruntime.ExecutionRequest{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if spent != 800 {
		t.Fatalf("spent=%d, want 100 discover + max(500,400) read + 200 recovered", spent)
	}
}
