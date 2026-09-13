package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memoryport"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// P1 验收：retrieve_context 与 remember 必须在全意图、无搜索/无知识库
// 的退化配置下仍然注册——这是旧实现消费断链的主因（旧 ChatToolDefs 在
// 无搜索时返回 nil）。

func hasToolNamed(defs []tools.ToolDef, name string) bool {
	for _, d := range defs {
		if d.Function.Name == name {
			return true
		}
	}
	return false
}

func TestMemoryToolsRegisteredForAllIntents(t *testing.T) {
	cases := []struct {
		intent Intent
		flags  []bool // guided, hasKnowledge
	}{
		{IntentWriting, []bool{false, false}},
		{IntentWriting, []bool{true, true}},
		{IntentPolish, []bool{false, false}},
		{IntentShorten, []bool{false, true}},
		{IntentChat, []bool{false, false}}, // 旧行为：nil
		{IntentChat, []bool{false, true}},
	}
	for _, tc := range cases {
		defs := ToolsForIntent(tc.intent, false, tc.flags...)
		if !hasToolNamed(defs, "retrieve_context") {
			t.Errorf("intent=%s flags=%v: retrieve_context missing", tc.intent, tc.flags)
		}
		if !hasToolNamed(defs, "remember") {
			t.Errorf("intent=%s flags=%v: remember missing", tc.intent, tc.flags)
		}
	}
}

func TestExecuteRememberDispatches(t *testing.T) {
	captured := make(chan memoryport.Outcome, 1)
	port := &stubMemoryPort{onSubmit: func(o memoryport.Outcome) error {
		captured <- o
		return nil
	}}
	execCtx := &engine.ExecutionContext{}
	execCtx.UserID = "user-1"
	execCtx.TraceID = "trace-1"
	cfg := ToolExecutorConfig{ExecCtx: execCtx, MemoryPort: port}

	out, err := executeRemember(cfg, `{"content":"标题不超过12个字"}`)
	if err != nil {
		t.Fatalf("executeRemember: %v", err)
	}
	if !strings.Contains(out, "已记住") {
		t.Errorf("unexpected response: %q", out)
	}
	got := <-captured
	if got.Kind != memoryport.OutcomeExplicit || got.UserID != "user-1" || got.ExplicitValue != "标题不超过12个字" {
		t.Errorf("outcome mismatch: %+v", got)
	}
}

// stubMemoryPort 实现 memoryport.Port，捕获 SubmitOutcome。
type stubMemoryPort struct {
	onSubmit func(memoryport.Outcome) error
}

func (s *stubMemoryPort) EnabledForUser(string) bool { return true }
func (s *stubMemoryPort) PrepareInjection(ctx context.Context, req memoryport.Request) (*memoryport.Bundle, error) {
	return nil, nil
}
func (s *stubMemoryPort) QueryOnDemand(ctx context.Context, req memoryport.QueryRequest) ([]memoryport.Directive, error) {
	return nil, nil
}
func (s *stubMemoryPort) SubmitOutcome(ctx context.Context, out memoryport.Outcome) error {
	if s.onSubmit != nil {
		return s.onSubmit(out)
	}
	return nil
}
func (s *stubMemoryPort) Capabilities() memoryport.Capabilities {
	return memoryport.Capabilities{ExplicitCapture: true}
}
