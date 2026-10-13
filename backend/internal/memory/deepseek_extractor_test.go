package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── WP7 对齐感知抽取：prompt 渲染与响应解析 ─────────────────────

// 带 action/retract_target 的响应能解析出新字段。
func TestParseExtractedMemoriesWithAlignment(t *testing.T) {
	resp := `[{"category": "title", "key": "prefers_short_title", "value": "偏好短标题", "source": "llm", "action": "retract", "retract_target": 2},
{"category": "tone", "key": "tone_preference", "value": "理性克制", "source": "llm", "action": "reinforce", "retract_target": 1}]`
	got := parseExtractedMemories(resp, "llm")
	if len(got) != 2 {
		t.Fatalf("parsed %d entries, want 2", len(got))
	}
	if got[0].Action != memory.ActionRetract || got[0].RetractTarget != 2 {
		t.Errorf("entry0 = action %q target %d, want retract/2", got[0].Action, got[0].RetractTarget)
	}
	if got[1].Action != memory.ActionReinforce || got[1].RetractTarget != 1 {
		t.Errorf("entry1 = action %q target %d, want reinforce/1", got[1].Action, got[1].RetractTarget)
	}
}

// 旧形状响应（无对齐字段）解析结果不受影响，走既有精确匹配路径。
func TestParseExtractedMemoriesWithoutAlignment(t *testing.T) {
	resp := `[{"category": "tone", "key": "tone_preference", "value": "理性", "source": "llm"}]`
	got := parseExtractedMemories(resp, "llm")
	if len(got) != 1 {
		t.Fatalf("parsed %d entries, want 1", len(got))
	}
	if got[0].Action != "" || got[0].RetractTarget != 0 {
		t.Errorf("action = %q target = %d, want zero values", got[0].Action, got[0].RetractTarget)
	}
}

func TestBuildBeliefBlockEmpty(t *testing.T) {
	if got := buildBeliefBlock(nil); got != "" {
		t.Errorf("empty beliefs block = %q, want empty string", got)
	}
}

// belief 块带序号清单与三值标注规则；retract_target 序号与清单一致。
func TestBuildBeliefBlockRendersIndexAndRules(t *testing.T) {
	block := buildBeliefBlock([]memory.BeliefContext{
		{Index: 1, ID: "m1", Category: "title", Key: "title_style", Value: "偏好长标题", Tier: memory.TierPattern},
		{Index: 2, ID: "m2", Category: "tone", Key: "tone_preference", Value: "感性", Tier: memory.TierFeedback},
	})
	for _, want := range []string{
		"1 | title/title_style | 偏好长标题",
		"2 | tone/tone_preference | 感性",
		`"action"`,
		`"retract_target"`,
		"reinforce",
		"拿不准",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("belief block missing %q", want)
		}
	}
	// ID 不得进入 prompt：模型只看得到序号
	if strings.Contains(block, "m1") || strings.Contains(block, "m2") {
		t.Error("belief block leaks memory IDs into the prompt")
	}
}

// 无 LLM 客户端时对齐通道静默返回，不 panic。
func TestExtractFromSessionWithBeliefsNoLLM(t *testing.T) {
	e := &DeepSeekExtractor{}
	out, err := e.ExtractFromSessionWithBeliefs(context.Background(),
		memory.ExtractSession{Article: strings.Repeat("文", 200)},
		[]memory.BeliefContext{{Index: 1, ID: "m1", Category: "tone", Key: "k", Value: "v", Tier: memory.TierPattern}})
	if out != nil || err != nil {
		t.Errorf("out = %v err = %v, want nil/nil without LLM client", out, err)
	}
}
