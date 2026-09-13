package memoryport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// import 边界检查（计划 §4.2 纪律 1，P0 验收）：
// 消费侧 internal/engine 与 internal/agent 不得引用 pkg/memory 的
// 长期记忆契约类型与旧格式化函数——它们必须经 memoryport。
// 短期记忆类型（ConversationMessage/WorkingSummary/StochasticState 等）
// 属 ShortTermMemory Port 范围，本期白名单不在禁止之列。
var bannedIdentifiers = []string{
	"memory.MemoryContext",
	"memory.RetrieveRequest",
	"memory.MemoryEntry",
	"memory.ExtractSession",
	"memory.FeedbackInfo",
	"memory.EntityGraphResult",
	"memory.EntityGraphQuery",
	"memory.QualitySignal",
	"FormatMemoryForPrompt",
	"FormatReviewGuardForPrompt",
}

func TestConsumerImportBoundary(t *testing.T) {
	roots := []string{"../engine", "../agent"}
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			src := string(data)
			for _, banned := range bannedIdentifiers {
				if strings.Contains(src, banned) {
					t.Errorf("boundary violation in %s: found %q — 长期记忆类型必须经 memoryport 契约", path, banned)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
}
