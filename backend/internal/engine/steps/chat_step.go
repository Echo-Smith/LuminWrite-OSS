package steps

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── ChatStep ────────────────────────────────────────────

// ChatStep handles "chat" intent — a lightweight conversational response
// without the full writing pipeline (no search, no article formatting,
// no post-review, no auto-fix).
type ChatStep struct {
	llm *tools.LLMClient
}

func NewChatStep(llm *tools.LLMClient) *ChatStep {
	return &ChatStep{llm: llm}
}

func (s *ChatStep) Name() engine.StepName { return engine.StepChat }
func (s *ChatStep) CanPause() bool         { return true }
func (s *ChatStep) Timeout() time.Duration { return 60 * time.Second }
func (s *ChatStep) Critical() bool         { return true }

// ShouldSkip returns true when the intent is NOT chat — the step only
// runs for chat-mode requests.
func (s *ChatStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	if execCtx.TaskIntent == nil {
		return true
	}
	return execCtx.TaskIntent.TaskMode != "chat"
}

func (s *ChatStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	if s.llm == nil {
		return fmt.Errorf("LLM client not available")
	}

	// Build a conversational system prompt
	systemPrompt := "你是一个智能写作助手「笔润智谈」。你可以帮助用户写文章、润色文字、提取观点等。请用自然、友好的语气回答用户的问题。如果用户的意图是写作相关的，可以引导用户使用更具体的指令（如「写一篇关于…」）。"

	// Build user message
	var promptBuilder strings.Builder
	promptBuilder.WriteString(execCtx.NormalizedInput)

	// If user materials are provided, append them
	if len(execCtx.UserMaterials) > 0 {
		promptBuilder.WriteString("\n\n用户补充材料：\n")
		for i, m := range execCtx.UserMaterials {
			promptBuilder.WriteString(fmt.Sprintf("%d. %s\n", i+1, m))
		}
	}

	// Add memory context if available (for personalized chat)
	if execCtx.MemoryContext != nil {
		if memCtx, ok := execCtx.MemoryContext.(*memory.MemoryContext); ok {
			if memStr := FormatMemoryForPrompt(memCtx); memStr != "" {
				promptBuilder.WriteString(memStr)
			}
		}
	}

	// Add entity context if available
	if execCtx.EntityContext != nil {
		if entityCtx, ok := execCtx.EntityContext.(*memory.EntityGraphResult); ok {
			promptBuilder.WriteString(entityCtx.FormattedContext)
		}
	}

	// Build messages with conversation history (short-term memory)
	messages := []tools.LLMMessage{
		{Role: "system", Content: systemPrompt},
	}

	// Inject conversation history as alternating user/assistant messages
	if execCtx.ConversationHistory != nil {
		if history, ok := execCtx.ConversationHistory.([]memory.ConversationMessage); ok {
			for _, msg := range history {
				content := msg.Content
				// 安全截断过长的历史消息，避免 token 溢出
				maxLen := 800
				if msg.Role == memory.RoleAssistant && msg.ContentType == memory.ContentArticle {
					maxLen = 500 // 助手文章类历史更短
				}
				if len(content) > maxLen {
					content = memory.SafeTruncate(content, maxLen) + "...（已截断）"
				}
				messages = append(messages, tools.LLMMessage{
					Role:    string(msg.Role),
					Content: content,
				})
			}
		}
	}

	// Add working memory summary if available
	if execCtx.WorkingSummary != nil {
		if ws, ok := execCtx.WorkingSummary.(*memory.WorkingSummary); ok {
			if wsStr := memory.FormatWorkingSummaryForPrompt(ws); wsStr != "" {
				promptBuilder.WriteString(wsStr)
			}
		}
	}

	messages = append(messages, tools.LLMMessage{
		Role: "user", Content: promptBuilder.String(),
	})

	// Stream the response (non-thinking mode for fast conversational replies)
	chatOpts := []tools.ChatOption{tools.WithThinking(false)}
	// Apply stochastic temperature adjustment
	if execCtx.StochasticState != nil {
		if ss, ok := execCtx.StochasticState.(*memory.StochasticState); ok {
			chatOpts = append(chatOpts, tools.WithTemperature(ss.AdjustedTemperature(0.6)))
		}
	}
	fullText, tokens, err := s.llm.ChatStreamWithReasoning(ctx, messages, func(delta string) {
		emitter.StreamDelta(delta)
		if err := execCtx.CheckPause(ctx, emitter, engine.StepChat); err != nil {
			return
		}
	}, nil, chatOpts...)
	if err != nil {
		return fmt.Errorf("chat response generation failed: %w", err)
	}

	execCtx.Article = fullText
	execCtx.TotalTokens += tokens

	// Emit stream done
	emitter.StreamDone(fullText)

	// Chat mode: no review or feedback needed — leave ReviewResult as nil
	// so the frontend skips rendering the ReviewCard and FeedbackBar.

	return nil
}
