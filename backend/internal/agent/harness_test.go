package agent

import (
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/worldstate"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

func TestBuildLLMOptionsPreservesConfiguredModel(t *testing.T) {
	intents := []Intent{IntentWriting, IntentPolish, IntentShorten, IntentExpand, IntentChat}

	for _, intent := range intents {
		t.Run(string(intent), func(t *testing.T) {
			req := &tools.LLMRequest{Model: "DeepSeek-V4-Flash"}
			for _, opt := range (&Harness{}).buildLLMOptions(intent, nil) {
				opt(req)
			}

			if req.Model != "DeepSeek-V4-Flash" {
				t.Fatalf("buildLLMOptions(%q) changed configured model to %q", intent, req.Model)
			}
		})
	}
}

func TestBuildMessagesKeepsArticleContractNearLongContextEnd(t *testing.T) {
	articleIntents := []Intent{IntentWriting, IntentPolish, IntentShorten, IntentExpand}
	for _, intent := range articleIntents {
		t.Run(string(intent), func(t *testing.T) {
			h := &Harness{worldState: worldstate.NewWorldState()}
			session := NewWritingSession("conversation", "user", "")
			for i := 0; i < 8; i++ {
				session.Messages = append(session.Messages, memory.ConversationMessage{
					Role:    memory.RoleUser,
					Content: strings.Repeat("很长的历史上下文", 200),
				})
			}
			execCtx := &engine.ExecutionContext{UserInput: "请继续处理文章"}

			_ = h.buildMessages(execCtx, session, intent, false)
			messages := h.buildMessages(execCtx, session, intent, false)
			last := messages[len(messages)-1]
			if last.Role != "user" || !strings.HasSuffix(last.Content, profile.MarkdownArticleOutputReminder) {
				t.Fatalf("last message lacks near-end contract: role=%q content=%q", last.Role, last.Content)
			}
		})
	}
}

func TestBuildMessagesDoesNotAddArticleContractToChat(t *testing.T) {
	h := &Harness{worldState: worldstate.NewWorldState()}
	messages := h.buildMessages(&engine.ExecutionContext{UserInput: "你好"}, NewWritingSession("conversation", "user", ""), IntentChat, false)
	if strings.Contains(messages[len(messages)-1].Content, profile.MarkdownArticleOutputReminder) {
		t.Fatal("chat message should not receive article output contract")
	}
}
