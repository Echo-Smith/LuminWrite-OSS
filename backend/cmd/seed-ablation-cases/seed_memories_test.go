package main

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// findSeed returns the seed entry for a category/key pair.
func findSeed(t *testing.T, category, key string) seedMemory {
	t.Helper()
	for _, s := range ablationSeedMemories() {
		if s.Category == category && s.Key == key {
			return s
		}
	}
	t.Fatalf("seed %s/%s not found in ablationSeedMemories()", category, key)
	return seedMemory{}
}

// findCase returns the ablation case with the given ID.
func findCase(t *testing.T, caseID string) ablationCase {
	t.Helper()
	for _, c := range buildExplicitOverrideCases() {
		if c.caseID == caseID {
			return c
		}
	}
	t.Fatalf("case %s not found in buildExplicitOverrideCases()", caseID)
	return ablationCase{}
}

// TestSeedMemoriesEchoAITermReversal 断言「AI 术语反转」种子存在且与
// override-011 呼应：种子记录的既有偏好是「全文用『人工智能』而非『AI』」，
// 而该用例的显式指令正是把它反转为「全文统一使用『AI』」。
func TestSeedMemoriesEchoAITermReversal(t *testing.T) {
	seed := findSeed(t, "terminology", "term_ai_naming")
	if seed.CaseRef != "ablation-override-011" {
		t.Fatalf("term_ai_naming CaseRef = %q, want ablation-override-011", seed.CaseRef)
	}
	if !strings.Contains(seed.Value, "人工智能") || !strings.Contains(seed.Value, "AI") {
		t.Fatalf("term_ai_naming value %q must mention both 人工智能 and AI", seed.Value)
	}
	if strings.Contains(seed.Value, "统一使用『AI』") {
		t.Fatalf("seed must record the OLD preference, not the override instruction: %q", seed.Value)
	}
	cse := findCase(t, "ablation-override-011")
	if !strings.Contains(cse.inputText, "人工智能") || !strings.Contains(cse.inputText, "AI") {
		t.Fatalf("override-011 input must reference both spellings, got: %s", cse.inputText)
	}
	// 用例的显式指令方向：统一用 AI（新），与种子的旧偏好相反。
	if !strings.Contains(cse.mustHave[0], "AI") {
		t.Fatalf("override-011 mustHave[0] = %q, want the new 'AI' instruction", cse.mustHave[0])
	}
	for _, m := range cse.mustNotHave {
		if strings.Contains(m, "人工智能") {
			return // 旧写法在 mustNotHave 中 —— 反转关系成立
		}
	}
	t.Fatalf("override-011 mustNotHave must reject the old 人工智能 spelling")
}

// TestSeedMemoriesEchoWuxiaWorldview 断言「武侠世界观」种子存在且与
// override-017（memory_isolation）呼应：种子把武侠设定写进记忆库，而该用例
// 要求本任务将其完全隔离。
func TestSeedMemoriesEchoWuxiaWorldview(t *testing.T) {
	seed := findSeed(t, "topic", "wuxia_worldview")
	if seed.CaseRef != "ablation-override-017" {
		t.Fatalf("wuxia_worldview CaseRef = %q, want ablation-override-017", seed.CaseRef)
	}
	for _, kw := range []string{"武侠", "内力", "门派", "轻功"} {
		if !strings.Contains(seed.Value, kw) {
			t.Fatalf("wuxia_worldview value %q must mention %q", seed.Value, kw)
		}
	}
	cse := findCase(t, "ablation-override-017")
	for _, kw := range []string{"武侠", "内力", "门派", "轻功"} {
		if !strings.Contains(cse.inputText, kw) {
			t.Fatalf("override-017 input must mention %q, got: %s", kw, cse.inputText)
		}
	}
}

// TestSeedMemoriesShape 校验播种清单的整体约束：8-12 条、字段非空、CaseRef
// 均指向真实存在的用例、值不含 PII 形态的内容（邮箱/长数字串/URL——种子
// 必须能通过 PII 语义检查）。
func TestSeedMemoriesShape(t *testing.T) {
	seeds := ablationSeedMemories()
	if len(seeds) < 8 || len(seeds) > 12 {
		t.Fatalf("seed count = %d, want 8-12", len(seeds))
	}
	caseIDs := map[string]bool{}
	for _, c := range buildExplicitOverrideCases() {
		caseIDs[c.caseID] = true
	}
	emailRe := regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+`)
	longDigitsRe := regexp.MustCompile(`[0-9]{7,}`)
	urlRe := regexp.MustCompile(`https?://`)
	for _, s := range seeds {
		if s.Category == "" || s.Key == "" || s.Value == "" {
			t.Fatalf("seed %s/%s has empty field(s): %+v", s.Category, s.Key, s)
		}
		if s.CaseRef != "" && !caseIDs[s.CaseRef] {
			t.Fatalf("seed %s/%s CaseRef %q does not match any explicit-override case", s.Category, s.Key, s.CaseRef)
		}
		if emailRe.MatchString(s.Value) || longDigitsRe.MatchString(s.Value) || urlRe.MatchString(s.Value) {
			t.Fatalf("seed %s/%s value looks like PII/secret content: %q", s.Category, s.Key, s.Value)
		}
	}
}

// TestAblationMemoryUserIDIsUUID 锁定记忆用户常量的格式： wipe/seed 的 SQL
// 都按 $1::uuid 绑定，非法 UUID 会在运行期炸掉。
func TestAblationMemoryUserIDIsUUID(t *testing.T) {
	uuidRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	if !uuidRe.MatchString(ablationMemoryUserID) {
		t.Fatalf("ablationMemoryUserID %q is not a lowercase UUID", ablationMemoryUserID)
	}
}

// fakeSeedStore 是 seedStore 的内存实现，用于验证幂等与用户归属。
type fakeSeedStore struct {
	memories map[string]bool    // "userID|category|key" -> exists
	values   map[string]string  // key -> value
	created  []string           // insertion order
	failOn   string             // category|key that always fails on create ("" = none)
}

func newFakeSeedStore() *fakeSeedStore {
	return &fakeSeedStore{
		memories: map[string]bool{},
		values:   map[string]string{},
	}
}

func (f *fakeSeedStore) findActive(_ context.Context, userID, category, key string) (bool, error) {
	return f.memories[userID+"|"+category+"|"+key], nil
}

func (f *fakeSeedStore) createHardPreference(_ context.Context, userID, category, key, value string) error {
	if category+"|"+key == f.failOn {
		return context.DeadlineExceeded
	}
	k := userID + "|" + category + "|" + key
	if f.memories[k] {
		return nil // no-op（真实 PgStore 为 upsert；此处不该发生）
	}
	f.memories[k] = true
	f.values[k] = value
	f.created = append(f.created, k)
	return nil
}

func silentLogf(string, ...interface{}) {}

// TestSeedMemoriesIdempotent 断言幂等：第一次全量创建，第二次执行 0 新增、
// 全部按 category+key 命中跳过。
func TestSeedMemoriesIdempotent(t *testing.T) {
	seeds := ablationSeedMemories()
	st := newFakeSeedStore()

	created, skipped, err := seedMemoriesInto(context.Background(), st, ablationMemoryUserID, seeds, silentLogf)
	if err != nil {
		t.Fatalf("first seed run: %v", err)
	}
	if created != len(seeds) || skipped != 0 {
		t.Fatalf("first run: created=%d skipped=%d, want %d/0", created, skipped, len(seeds))
	}

	created2, skipped2, err := seedMemoriesInto(context.Background(), st, ablationMemoryUserID, seeds, silentLogf)
	if err != nil {
		t.Fatalf("second seed run: %v", err)
	}
	if created2 != 0 {
		t.Fatalf("second run created %d memories, want 0 (idempotency broken)", created2)
	}
	if skipped2 != len(seeds) {
		t.Fatalf("second run skipped=%d, want %d", skipped2, len(seeds))
	}
	if len(st.created) != len(seeds) {
		t.Fatalf("store holds %d memories, want %d (duplicates inserted)", len(st.created), len(seeds))
	}
}

// TestSeedMemoriesBelongToAblationUser 断言所有写入都以 ablationMemoryUserID
// 归属（C/D 候选 feature_flags.memoryUserId 指向的用户）。
func TestSeedMemoriesBelongToAblationUser(t *testing.T) {
	st := newFakeSeedStore()
	if _, _, err := seedMemoriesInto(context.Background(), st, ablationMemoryUserID, ablationSeedMemories(), silentLogf); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	prefix := ablationMemoryUserID + "|"
	for _, k := range st.created {
		if !strings.HasPrefix(k, prefix) {
			t.Fatalf("memory %q does not belong to ablation user %s", k, ablationMemoryUserID)
		}
	}
	if len(st.created) == 0 {
		t.Fatal("no memories were created")
	}
}

// TestSeedMemoriesCreateErrorPropagates 断言单条创建失败会中止并带出
// category/key 上下文，而不是静默吞掉。
func TestSeedMemoriesCreateErrorPropagates(t *testing.T) {
	st := newFakeSeedStore()
	st.failOn = "terminology|term_ai_naming"
	_, _, err := seedMemoriesInto(context.Background(), st, ablationMemoryUserID, ablationSeedMemories(), silentLogf)
	if err == nil {
		t.Fatal("expected error from failing create, got nil")
	}
	if !strings.Contains(err.Error(), "terminology/term_ai_naming") {
		t.Fatalf("error %v must identify the failing category/key", err)
	}
}
