package worldstate

import (
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memoryport"
)

// P1-1 验收：MemorySection 渲染与 diff 语义。

func testBundle() *memoryport.Bundle {
	return &memoryport.Bundle{
		Version: memoryport.ContractVersion,
		WriteDirectives: []memoryport.Directive{
			{ID: "m1", Category: "style", Value: "标题不超过12个字", Kind: memoryport.KindPreference},
			{ID: "m2", Category: "tone", Value: "偏好口语化表达", Kind: memoryport.KindPreference},
		},
	}
}

func TestMemorySectionRendersOnce(t *testing.T) {
	ws := NewWorldState()
	ws.Register(NewMemorySection(testBundle()))

	fragments := ws.UpdateWorldState()
	if len(fragments) != 1 {
		t.Fatalf("first update should push 1 fragment, got %d", len(fragments))
	}
	if !strings.Contains(fragments[0].Body, "用户写作偏好") || !strings.Contains(fragments[0].Body, "标题不超过12个字") {
		t.Errorf("fragment body unexpected: %q", fragments[0].Body)
	}

	// 记忆在请求内静态：第二轮不重推
	if again := ws.UpdateWorldState(); len(again) != 0 {
		t.Errorf("static memory must not be re-pushed, got %d fragments", len(again))
	}
}

func TestMemorySectionEmptyBundleSilent(t *testing.T) {
	sec := NewMemorySection(&memoryport.Bundle{Version: memoryport.ContractVersion})
	if frag := sec.RenderDiff(nil); frag != nil {
		t.Errorf("empty bundle must render nothing, got %q", frag.Body)
	}
	if sec.Snapshot() != "" {
		t.Errorf("empty bundle snapshot should be empty string")
	}
}

func TestMemorySectionRepushesOnChange(t *testing.T) {
	ws := NewWorldState()
	b := testBundle()
	ws.Register(NewMemorySection(b))
	ws.UpdateWorldState()

	// bundle 增加一条 → 指纹变化 → 重推
	b.WriteDirectives = append(b.WriteDirectives, memoryport.Directive{ID: "m3", Category: "structure", Value: "喜欢三段式", Kind: memoryport.KindPreference})
	fragments := ws.UpdateWorldState()
	if len(fragments) != 1 {
		t.Fatalf("changed bundle should push 1 fragment, got %d", len(fragments))
	}
	if !strings.Contains(fragments[0].Body, "喜欢三段式") {
		t.Errorf("re-pushed fragment missing new directive: %q", fragments[0].Body)
	}
}
