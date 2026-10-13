package memory

import (
	"context"
	"testing"
	"time"
)

// ─── WP7 对齐感知抽取：ResolveAndSaveAligned ─────────────────────
//
// 复用 conflict_evidence_test.go 的 fakeMemStore（同包内存 Store）。

func seedBelief(t *testing.T, store *fakeMemStore, m *Memory) *Memory {
	t.Helper()
	if m.FirstSeen.IsZero() {
		m.FirstSeen = time.Now().Add(-time.Hour)
	}
	if m.LastSeen.IsZero() {
		m.LastSeen = m.FirstSeen
	}
	if err := store.Save(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return m
}

func alignedBeliefs(m *Memory) []BeliefContext {
	return []BeliefContext{{Index: 1, ID: m.ID, Category: m.Category, Key: m.Key, Value: m.Value, Tier: m.Tier}}
}

// retract 修 key 漂移：新提取换了 key 也必须把旧 belief 撤下，
// 且撤回链指向新建的记忆。
func TestRetractSupersedesDriftedKey(t *testing.T) {
	store := newFakeMemStore()
	r := NewConflictResolver(store)
	old := seedBelief(t, store, &Memory{
		UserID: "u1", Tier: TierPattern, Category: "title", Key: "title_style",
		Value: "偏好长标题，喜用设问", Confidence: 0.6, Status: StatusActive,
	})

	mem, err := r.ResolveAndSaveAligned(context.Background(), "u1",
		ExtractedMemory{Category: "title", Key: "prefers_short_title", Value: "偏好短标题", Action: ActionRetract, RetractTarget: 1},
		TierPattern, "trace-1", GradeNeutral, QualityNone, alignedBeliefs(old))
	if err != nil {
		t.Fatal(err)
	}
	if mem == nil || mem.Key != "prefers_short_title" {
		t.Fatalf("new memory = %+v, want key prefers_short_title", mem)
	}
	superseded := store.items[old.ID]
	if superseded.Status != StatusSuperseded || superseded.SupersededBy != mem.ID {
		t.Errorf("old = status %q superseded_by %q, want superseded by %q",
			superseded.Status, superseded.SupersededBy, mem.ID)
	}
}

// reinforce 修措辞降级：含义一致的措辞更新走强化路径，不得把
// active 偏好打回候选期，也不得另立平行记忆。
func TestReinforceParaphraseKeepsBelief(t *testing.T) {
	store := newFakeMemStore()
	r := NewConflictResolver(store)
	old := seedBelief(t, store, &Memory{
		UserID: "u1", Tier: TierPattern, Category: "tone", Key: "tone_preference",
		Value: "理性克制", Confidence: 0.7, Occurrences: 3, Status: StatusActive,
	})

	mem, err := r.ResolveAndSaveAligned(context.Background(), "u1",
		ExtractedMemory{Category: "tone", Key: "tone_preference", Value: "理性、克制", Action: ActionReinforce, RetractTarget: 1},
		TierPattern, "trace-2", GradeNeutral, QualityNone, alignedBeliefs(old))
	if err != nil {
		t.Fatal(err)
	}
	if mem.ID != old.ID {
		t.Fatalf("reinforce returned %q, want existing %q", mem.ID, old.ID)
	}
	if mem.Status != StatusActive {
		t.Errorf("status = %q, want active (no candidate demotion)", mem.Status)
	}
	if mem.Occurrences != 4 {
		t.Errorf("occurrences = %d, want 4", mem.Occurrences)
	}
	if mem.Value != "理性、克制" {
		t.Errorf("value = %q, want refreshed phrasing", mem.Value)
	}
	if len(store.items) != 1 {
		t.Errorf("store has %d memories, want 1 (no parallel memory)", len(store.items))
	}
}

// reinforce 对 candidate 的 two-strike 晋升照常生效。
func TestReinforcePromotesCandidate(t *testing.T) {
	store := newFakeMemStore()
	r := NewConflictResolver(store)
	old := seedBelief(t, store, &Memory{
		UserID: "u1", Tier: TierPattern, Category: "sentence", Key: "sentence_style",
		Value: "长句为主", Confidence: 0.3, Status: StatusCandidate,
	})

	mem, err := r.ResolveAndSaveAligned(context.Background(), "u1",
		ExtractedMemory{Category: "sentence", Key: "sentence_style", Value: "偏好长句", Action: ActionReinforce, RetractTarget: 1},
		TierPattern, "trace-3", GradeNeutral, QualityNone, alignedBeliefs(old))
	if err != nil {
		t.Fatal(err)
	}
	if mem.Status != StatusActive {
		t.Errorf("status = %q, want active after two-strike", mem.Status)
	}
	if mem.Confidence < 0.5 {
		t.Errorf("confidence = %v, want >= 0.5 after promotion", mem.Confidence)
	}
}

// 序号越界：标签剥掉，降级为无标签路径（旧 belief 不受影响）。
func TestInvalidTargetDegradesToUnlabeled(t *testing.T) {
	store := newFakeMemStore()
	r := NewConflictResolver(store)
	old := seedBelief(t, store, &Memory{
		UserID: "u1", Tier: TierPattern, Category: "title", Key: "title_style",
		Value: "偏好长标题", Confidence: 0.6, Status: StatusActive,
	})

	mem, err := r.ResolveAndSaveAligned(context.Background(), "u1",
		ExtractedMemory{Category: "title", Key: "prefers_short_title", Value: "偏好短标题", Action: ActionRetract, RetractTarget: 99},
		TierPattern, "trace-4", GradeNeutral, QualityNone, alignedBeliefs(old))
	if err != nil {
		t.Fatal(err)
	}
	if store.items[old.ID].Status != StatusActive {
		t.Errorf("old status = %q, want active (out-of-range label must not act)", store.items[old.ID].Status)
	}
	if mem == nil || mem.Key != "prefers_short_title" {
		t.Fatalf("expected fresh creation via Case 1, got %+v", mem)
	}
}

// Tier1 双保险：即使 belief 列表混入硬偏好（正常情况下
// beliefContext 已过滤），对齐标签也不得撤回它。
func TestRetractTierHardRejected(t *testing.T) {
	store := newFakeMemStore()
	r := NewConflictResolver(store)
	hard := seedBelief(t, store, &Memory{
		UserID: "u1", Tier: TierHard, Category: "style", Key: "must_style",
		Value: "必须书面语", Confidence: 1.0, Status: StatusActive,
	})

	mem, err := r.ResolveAndSaveAligned(context.Background(), "u1",
		ExtractedMemory{Category: "style", Key: "prefers_zen", Value: "偏好留白", Action: ActionRetract, RetractTarget: 1},
		TierPattern, "trace-5", GradeNeutral, QualityNone, alignedBeliefs(hard))
	if err != nil {
		t.Fatal(err)
	}
	if store.items[hard.ID].Status != StatusActive {
		t.Errorf("hard preference status = %q, want active (Tier1 is not retractable)", store.items[hard.ID].Status)
	}
	if mem == nil || mem.Key != "prefers_zen" {
		t.Fatalf("expected fresh creation via Case 1, got %+v", mem)
	}
}

// reinforce 指向已撤回的目标：降级为无标签路径，不得复活死记忆。
func TestReinforceSupersededTargetDegrades(t *testing.T) {
	store := newFakeMemStore()
	r := NewConflictResolver(store)
	dead := seedBelief(t, store, &Memory{
		UserID: "u1", Tier: TierPattern, Category: "tone", Key: "tone_preference",
		Value: "感性", Confidence: 0.6, Status: StatusSuperseded, SupersededBy: "mem-other",
	})

	mem, err := r.ResolveAndSaveAligned(context.Background(), "u1",
		ExtractedMemory{Category: "tone", Key: "tone_preference", Value: "理性", Action: ActionReinforce, RetractTarget: 1},
		TierPattern, "trace-6", GradeNeutral, QualityNone, alignedBeliefs(dead))
	if err != nil {
		t.Fatal(err)
	}
	if mem.ID == dead.ID {
		t.Fatal("reinforce must not revive a superseded memory")
	}
	if dead.SupersededBy != "mem-other" {
		t.Errorf("dead.SupersededBy = %q, want unchanged", dead.SupersededBy)
	}
}

// 无标签路径保持原语义：同 key 不同 value → supersede + 新建（Case 3）。
func TestUnlabeledStillSupersedesExactKey(t *testing.T) {
	store := newFakeMemStore()
	r := NewConflictResolver(store)
	old := seedBelief(t, store, &Memory{
		UserID: "u1", Tier: TierPattern, Category: "title", Key: "title_style",
		Value: "偏好长标题", Confidence: 0.6, Status: StatusActive,
	})

	mem, err := r.ResolveAndSaveAligned(context.Background(), "u1",
		ExtractedMemory{Category: "title", Key: "title_style", Value: "偏好短标题"},
		TierPattern, "trace-7", GradeNeutral, QualityNone, alignedBeliefs(old))
	if err != nil {
		t.Fatal(err)
	}
	if store.items[old.ID].Status != StatusSuperseded {
		t.Errorf("old status = %q, want superseded by exact-key mismatch", store.items[old.ID].Status)
	}
	if mem.ID == old.ID {
		t.Fatal("expected a new memory for the changed value")
	}
}

// beliefContext：排除 Tier1 与非活跃记忆，最近优先，截断到上限。
func TestBeliefContextFiltersOrdersAndCaps(t *testing.T) {
	store := newFakeMemStore()
	s := &SDK{store: store}
	base := time.Now().Add(-2 * time.Hour)

	seedBelief(t, store, &Memory{ID: "hard", UserID: "u1", Tier: TierHard,
		Category: "style", Key: "must_style", Value: "必须书面语",
		Status: StatusActive, LastSeen: base.Add(3 * time.Hour)})
	seedBelief(t, store, &Memory{ID: "superseded", UserID: "u1", Tier: TierPattern,
		Category: "tone", Key: "tone_preference", Value: "感性",
		Status: StatusSuperseded, LastSeen: base.Add(2 * time.Hour)})
	seedBelief(t, store, &Memory{ID: "older", UserID: "u1", Tier: TierPattern,
		Category: "title", Key: "title_style", Value: "偏长标题",
		Status: StatusActive, LastSeen: base})
	seedBelief(t, store, &Memory{ID: "newer", UserID: "u1", Tier: TierFeedback,
		Category: "title", Key: "title_feedback", Value: "标题要短",
		Status: StatusCandidate, LastSeen: base.Add(time.Hour)})

	beliefs := s.beliefContext(context.Background(), "u1")
	if len(beliefs) != 2 {
		t.Fatalf("len(beliefs) = %d, want 2 (Tier1 and superseded excluded)", len(beliefs))
	}
	if beliefs[0].ID != "newer" || beliefs[0].Index != 1 {
		t.Errorf("beliefs[0] = {%s, idx %d}, want newest first with index 1", beliefs[0].ID, beliefs[0].Index)
	}
	if beliefs[1].ID != "older" || beliefs[1].Index != 2 {
		t.Errorf("beliefs[1] = {%s, idx %d}, want older with index 2", beliefs[1].ID, beliefs[1].Index)
	}
	for _, b := range beliefs {
		if b.Tier == TierHard {
			t.Errorf("TierHard %s leaked into belief context", b.ID)
		}
	}

	// 截断：12 条只取最近 10 条（ID 留空由 fakeMemStore 自增分配）
	for i := 0; i < 12; i++ {
		seedBelief(t, store, &Memory{UserID: "u2", Tier: TierPattern,
			Category: "tone", Key: "tone_preference", Value: "v",
			Status: StatusActive, LastSeen: base})
	}
	if got := s.beliefContext(context.Background(), "u2"); len(got) != maxBeliefContext {
		t.Errorf("capped len = %d, want %d", len(got), maxBeliefContext)
	}
}
