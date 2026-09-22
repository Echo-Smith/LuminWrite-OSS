package memory

import (
	"context"
	"sort"
	"strconv"
	"testing"
	"time"
)

// ─── WP6 证据阶梯补全 / 多源计数 / DeriveSource 测试 ─────────────
//
// fakeMemStore 是内存版 Store：ResolveAndSave 需要 Save 持久化、
// FindByCategoryKey 按 userID/category/key 过滤。file_format_test.go 的
// mockFileSyncStore 是不落盘 stub（Save 空实现），不可复用。

type fakeMemStore struct {
	items map[string]*Memory
	seq   int
}

func newFakeMemStore() *fakeMemStore {
	return &fakeMemStore{items: map[string]*Memory{}}
}

func (s *fakeMemStore) Save(_ context.Context, m *Memory) error {
	if m.ID == "" {
		s.seq++
		m.ID = "mem-" + strconv.Itoa(s.seq)
	}
	s.items[m.ID] = m
	return nil
}

func (s *fakeMemStore) Get(_ context.Context, id string) (*Memory, error) {
	return s.items[id], nil
}

func (s *fakeMemStore) List(_ context.Context, userID string, opts ListOptions) ([]*Memory, error) {
	var out []*Memory
	for _, m := range s.items {
		if m.UserID != userID {
			continue
		}
		if opts.Status != nil && m.Status != *opts.Status {
			continue
		}
		if opts.Tier != nil && m.Tier != *opts.Tier {
			continue
		}
		out = append(out, m)
	}
	// map 遍历无序，按 ID 稳定排序保证测试可复现
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

func (s *fakeMemStore) Search(_ context.Context, _ string, _ []float32, _ int) ([]*Memory, error) {
	return nil, nil
}

func (s *fakeMemStore) FindByCategoryKey(_ context.Context, userID, category, key string) ([]*Memory, error) {
	var out []*Memory
	for _, m := range s.items {
		if m.UserID == userID && m.Category == category && m.Key == key {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *fakeMemStore) UpdateStatus(_ context.Context, id string, status MemoryStatus) error {
	if m, ok := s.items[id]; ok {
		m.Status = status
	}
	return nil
}

func (s *fakeMemStore) IncrementOccurrence(_ context.Context, id string) error {
	if m, ok := s.items[id]; ok {
		m.Occurrences++
	}
	return nil
}

func (s *fakeMemStore) Supersede(_ context.Context, oldID, newID string) error {
	if m, ok := s.items[oldID]; ok {
		m.Status = StatusSuperseded
		m.SupersededBy = newID
	}
	return nil
}

func (s *fakeMemStore) Delete(_ context.Context, id string) error {
	delete(s.items, id)
	return nil
}

func (s *fakeMemStore) DismissForSession(_ context.Context, memoryID, sessionID string) error {
	return nil
}

func (s *fakeMemStore) GetDismissals(_ context.Context, sessionID string) ([]string, error) {
	return nil, nil
}

// ─── Case 1 新建路径：证据阶梯 ──────────────────────────────

func TestResolveAndSaveNewMemoryEvidence(t *testing.T) {
	extract := ExtractedMemory{Category: "style", Key: "tone", Value: "偏好口语化表达"}

	t.Run("好评 → supported", func(t *testing.T) {
		r := NewConflictResolver(newFakeMemStore())
		mem, err := r.ResolveAndSave(context.Background(), "u1", extract, TierPattern, "trace-1", GradePositive, QualityHighRating)
		if err != nil {
			t.Fatal(err)
		}
		if mem.EvidenceStatus != EvidenceSupported {
			t.Errorf("evidence = %q, want supported", mem.EvidenceStatus)
		}
		if mem.QualitySource != QualityHighRating {
			t.Errorf("quality source = %q, want high_rating", mem.QualitySource)
		}
		if mem.SourceCount != 1 {
			t.Errorf("source count = %d, want 1", mem.SourceCount)
		}
	})

	t.Run("workbuddy 强信号 → verified", func(t *testing.T) {
		r := NewConflictResolver(newFakeMemStore())
		mem, err := r.ResolveAndSave(context.Background(), "u1", extract, TierPattern, "trace-1", GradeNeutral, QualityWorkbuddy)
		if err != nil {
			t.Fatal(err)
		}
		if mem.EvidenceStatus != EvidenceVerified {
			t.Errorf("evidence = %q, want verified", mem.EvidenceStatus)
		}
		if mem.QualitySource != QualityWorkbuddy {
			t.Errorf("quality source = %q, want workbuddy_adopt", mem.QualitySource)
		}
	})

	t.Run("中性 → none", func(t *testing.T) {
		r := NewConflictResolver(newFakeMemStore())
		mem, err := r.ResolveAndSave(context.Background(), "u1", extract, TierPattern, "trace-1", GradeNeutral, QualityNone)
		if err != nil {
			t.Fatal(err)
		}
		if mem.EvidenceStatus != EvidenceNone {
			t.Errorf("evidence = %q, want none（首次观测不构成复现证据）", mem.EvidenceStatus)
		}
		if mem.SourceCount != 1 {
			t.Errorf("source count = %d, want 1", mem.SourceCount)
		}
	})
}

// ─── Case 2 强化路径：two-strike 升级 + 多源计数 ─────────────

func TestResolveAndSaveReinforceEvidence(t *testing.T) {
	extract := ExtractedMemory{Category: "style", Key: "tone", Value: "偏好口语化表达"}
	ctx := context.Background()

	t.Run("二次观测 none→supported，不同 traceID 自增 SourceCount", func(t *testing.T) {
		r := NewConflictResolver(newFakeMemStore())
		first, err := r.ResolveAndSave(ctx, "u1", extract, TierPattern, "trace-1", GradeNeutral, QualityNone)
		if err != nil {
			t.Fatal(err)
		}
		if first.EvidenceStatus != EvidenceNone {
			t.Fatalf("first evidence = %q, want none", first.EvidenceStatus)
		}
		second, err := r.ResolveAndSave(ctx, "u1", extract, TierPattern, "trace-2", GradeNeutral, QualityNone)
		if err != nil {
			t.Fatal(err)
		}
		if second.EvidenceStatus != EvidenceSupported {
			t.Errorf("reinforced evidence = %q, want supported (two-strike)", second.EvidenceStatus)
		}
		if second.SourceCount != 2 {
			t.Errorf("source count = %d, want 2（不同 traceID 自增）", second.SourceCount)
		}
		if second.SourceTraceID != "trace-1" {
			t.Errorf("source trace = %q, want trace-1（保留首个出处）", second.SourceTraceID)
		}
	})

	t.Run("同 traceID 不自增", func(t *testing.T) {
		r := NewConflictResolver(newFakeMemStore())
		if _, err := r.ResolveAndSave(ctx, "u1", extract, TierPattern, "trace-1", GradeNeutral, QualityNone); err != nil {
			t.Fatal(err)
		}
		second, err := r.ResolveAndSave(ctx, "u1", extract, TierPattern, "trace-1", GradeNeutral, QualityNone)
		if err != nil {
			t.Fatal(err)
		}
		if second.SourceCount != 1 {
			t.Errorf("source count = %d, want 1（同 traceID 不自增）", second.SourceCount)
		}
	})

	t.Run("旧记录 SourceCount=0 先补到 1 再自增", func(t *testing.T) {
		r := NewConflictResolver(newFakeMemStore())
		first, err := r.ResolveAndSave(ctx, "u1", extract, TierPattern, "trace-1", GradeNeutral, QualityNone)
		if err != nil {
			t.Fatal(err)
		}
		first.SourceCount = 0 // 模拟 WP6 之前的存量记录
		second, err := r.ResolveAndSave(ctx, "u1", extract, TierPattern, "trace-2", GradeNeutral, QualityNone)
		if err != nil {
			t.Fatal(err)
		}
		if second.SourceCount != 2 {
			t.Errorf("source count = %d, want 2（0 先补到 1 再自增）", second.SourceCount)
		}
	})

	t.Run("强信号覆盖质量出处并升 verified", func(t *testing.T) {
		r := NewConflictResolver(newFakeMemStore())
		first, err := r.ResolveAndSave(ctx, "u1", extract, TierPattern, "trace-1", GradePositive, QualityHighRating)
		if err != nil {
			t.Fatal(err)
		}
		if first.EvidenceStatus != EvidenceSupported {
			t.Fatalf("first evidence = %q, want supported", first.EvidenceStatus)
		}
		second, err := r.ResolveAndSave(ctx, "u1", extract, TierPattern, "trace-2", GradeNeutral, QualityWorkbuddy)
		if err != nil {
			t.Fatal(err)
		}
		if second.QualitySource != QualityWorkbuddy {
			t.Errorf("quality source = %q, want workbuddy_adopt（强信号覆盖）", second.QualitySource)
		}
		if second.EvidenceStatus != EvidenceVerified {
			t.Errorf("evidence = %q, want verified", second.EvidenceStatus)
		}
	})
}

// ─── DeriveSource：质量信号 → 质量出处 ──────────────────────

func TestDeriveSource(t *testing.T) {
	qc := NewQualityCalculator()
	now := time.Now()

	t.Run("workbuddy 优先于 high_rating", func(t *testing.T) {
		got := qc.DeriveSource([]QualitySignal{
			{Source: QualityHighRating, Weight: 0.7, EvidencedAt: now},
			{Source: QualityWorkbuddy, Weight: 0.9, EvidencedAt: now},
		})
		if got != QualityWorkbuddy {
			t.Errorf("derive = %q, want workbuddy_adopt", got)
		}
	})

	t.Run("强信号优先于更高权重的 high_rating", func(t *testing.T) {
		got := qc.DeriveSource([]QualitySignal{
			{Source: QualityHighRating, Weight: 0.95, EvidencedAt: now},
			{Source: QualityWorkbuddy, Weight: 0.8, EvidencedAt: now},
		})
		if got != QualityWorkbuddy {
			t.Errorf("derive = %q, want workbuddy_adopt（权重≥0.8 强信号优先）", got)
		}
	})

	t.Run("超过 7 天的信号忽略", func(t *testing.T) {
		got := qc.DeriveSource([]QualitySignal{
			{Source: QualityWorkbuddy, Weight: 0.9, EvidencedAt: now.Add(-8 * 24 * time.Hour)},
		})
		if got != QualityNone {
			t.Errorf("derive = %q, want none（过期信号忽略）", got)
		}
	})

	t.Run("仅 high_rating 取 high_rating", func(t *testing.T) {
		got := qc.DeriveSource([]QualitySignal{
			{Source: QualityHighRating, Weight: 0.7, EvidencedAt: now},
		})
		if got != QualityHighRating {
			t.Errorf("derive = %q, want high_rating", got)
		}
	})

	t.Run("无信号返回 QualityNone", func(t *testing.T) {
		if got := qc.DeriveSource(nil); got != QualityNone {
			t.Errorf("derive = %q, want none", got)
		}
	})
}
