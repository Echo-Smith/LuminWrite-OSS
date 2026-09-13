package agent

import "testing"

// 回归锁：各意图工具名必须唯一（DeepSeek API 对重复 tool name 返回
// 400 "Tool names must be unique"——P1 曾因双重 append 在 chat 意图触发）。
// 临时诊断转正。
func TestToolNamesUniquePerIntent(t *testing.T) {
	for _, tc := range []struct {
		intent Intent
		flags  []bool
	}{
		{IntentWriting, []bool{false, false}},
		{IntentWriting, []bool{true, true}},
		{IntentChat, []bool{false, false}},
		{IntentChat, []bool{true, true}},
		{IntentPolish, []bool{false, false}},
	} {
		defs := ToolsForIntent(tc.intent, true, tc.flags...)
		seen := map[string]int{}
		for _, d := range defs {
			seen[d.Function.Name]++
		}
		for name, n := range seen {
			if n > 1 {
				t.Errorf("intent=%s flags=%v: %s x%d", tc.intent, tc.flags, name, n)
			}
		}
	}
}
