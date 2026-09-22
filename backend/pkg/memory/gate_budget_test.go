package memory

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// ─── WP6 最小披露预算：MaxInjectedPerIntent 截断测试 ─────────────
//
// injected 与 reviewGuard 同用 MaxInjectedPerIntent 上限，
// 按 Confidence 降序稳定排序后取前 n（gate.go topByConfidence）。
// fakeMemStore 复用 conflict_evidence_test.go 的内存版 Store。

// fakeEmbedder 返回空向量：跳过语义检索，走 unionRecall 的
// 缺 embedding 活跃记忆并入路径。
type fakeEmbedder struct{}

func (fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, nil
}

func (fakeEmbedder) Dimension() int {
	return 0
}

func seedGateMemories(t *testing.T, store *fakeMemStore, tier Tier, prefix string, confidences []float64) {
	t.Helper()
	now := time.Now()
	for i, conf := range confidences {
		mem := &Memory{
			ID:             fmt.Sprintf("%s-%d", prefix, i),
			UserID:         "u1",
			Tier:           tier,
			Category:       "style",
			Key:            fmt.Sprintf("%s-key-%d", prefix, i),
			Value:          fmt.Sprintf("%s 偏好 %d", prefix, i),
			Confidence:     conf,
			Status:         StatusActive,
			EvidenceStatus: EvidenceSupported,
			FirstSeen:      now,
			LastSeen:       now,
		}
		if err := store.Save(context.Background(), mem); err != nil {
			t.Fatal(err)
		}
	}
}

func retrieveForBudget(t *testing.T, store *fakeMemStore) *MemoryContext {
	t.Helper()
	g := NewGate(store, fakeEmbedder{}, DefaultConfig())
	got, err := g.RetrieveAndGate(context.Background(), RetrieveRequest{
		UserID:    "u1",
		UserInput: "写一篇关于环保的短文",
		Intent:    "writing",
		SessionID: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDefaultBudgetConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Safety.MaxInjectedPerIntent != 8 {
		t.Errorf("default MaxInjectedPerIntent = %d, want 8（WP6 最小披露默认开启）", cfg.Safety.MaxInjectedPerIntent)
	}
}

func TestGateBudgetCapsInjected(t *testing.T) {
	store := newFakeMemStore()
	// 9 条同置信度活跃记忆 → 注入 8 条
	equal := make([]float64, 9)
	for i := range equal {
		equal[i] = 0.9
	}
	seedGateMemories(t, store, TierPattern, "p", equal)

	got := retrieveForBudget(t, store)
	if len(got.Injected) != 8 {
		t.Fatalf("injected = %d, want 8（最小披露预算截断）", len(got.Injected))
	}
}

func TestGateBudgetKeepsHighestConfidence(t *testing.T) {
	store := newFakeMemStore()
	// 8 条 0.9 + 1 条 0.7 → 截断后应保留置信度最高的 8 条
	confs := make([]float64, 0, 9)
	for i := 0; i < 8; i++ {
		confs = append(confs, 0.9)
	}
	confs = append(confs, 0.7)
	seedGateMemories(t, store, TierPattern, "p", confs)

	got := retrieveForBudget(t, store)
	if len(got.Injected) != 8 {
		t.Fatalf("injected = %d, want 8", len(got.Injected))
	}
	for _, e := range got.Injected {
		if e.ID == "p-8" {
			t.Fatalf("lowest confidence entry kept: %+v", e)
		}
		// 有效置信度含微量时间衰减，用近似比较
		if diff := e.Confidence - 0.9; diff > 1e-6 || diff < -1e-6 {
			t.Errorf("unexpected confidence %v for %s", e.Confidence, e.ID)
		}
	}
}

func TestGateBudgetCapsReviewGuard(t *testing.T) {
	store := newFakeMemStore()
	equal := make([]float64, 9)
	for i := range equal {
		equal[i] = 0.9
	}
	seedGateMemories(t, store, TierFeedback, "f", equal)

	got := retrieveForBudget(t, store)
	if len(got.ReviewGuard) != 8 {
		t.Fatalf("review_guard = %d, want 8（reviewGuard 同用预算上限）", len(got.ReviewGuard))
	}
}

func TestTopByConfidence(t *testing.T) {
	entries := []MemoryEntry{
		{ID: "a", Confidence: 0.5},
		{ID: "b", Confidence: 0.9},
		{ID: "c", Confidence: 0.7},
	}

	// n<=0 原样返回
	if got := topByConfidence(entries, 0); len(got) != 3 || got[0].ID != "a" {
		t.Errorf("n=0 must return unchanged, got %+v", got)
	}

	// 按 Confidence 降序稳定排序后取前 n
	got := topByConfidence(entries, 2)
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "c" {
		t.Errorf("top-2 = %+v, want [b c]", got)
	}

	// 不足 n 条不截断
	if got := topByConfidence(entries[:1], 8); len(got) != 1 {
		t.Errorf("under-budget must not truncate, got %+v", got)
	}
}
