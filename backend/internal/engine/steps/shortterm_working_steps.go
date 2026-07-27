package steps

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── 短期记忆 Step：对话历史检索与注入 ──────────────────────

// ShortTermMemoryAdapter 短期记忆服务适配器（避免循环依赖）
type ShortTermMemoryAdapter interface {
	LoadHistory(ctx context.Context, conversationID string, limit int) ([]memory.ConversationMessage, error)
	StoreMessage(ctx context.Context, msg *memory.ConversationMessage) error
	IsEnabledForUser(userID string) bool
	LoadWorkingSummary(ctx context.Context, conversationID string) (*memory.WorkingSummary, error)
	SaveWorkingSummary(ctx context.Context, ws *memory.WorkingSummary) error
}

// ShortTermMemoryStep 在 IntentStep 之前执行：
//   - 加载当前会话的对话历史
//   - 语义裁切 + 动态窗口选择
//   - 将选中的历史注入 ExecutionContext.ConversationHistory
type ShortTermMemoryStep struct {
	svc      ShortTermMemoryAdapter
	embedder EmbedderAdapter
	config   memory.DynamicWindowConfig
}

// EmbedderAdapter embedding 适配器
type EmbedderAdapter interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// NewShortTermMemoryStep 创建短期记忆步骤
func NewShortTermMemoryStep(svc ShortTermMemoryAdapter, embedder EmbedderAdapter, config memory.DynamicWindowConfig) *ShortTermMemoryStep {
	return &ShortTermMemoryStep{svc: svc, embedder: embedder, config: config}
}

func (s *ShortTermMemoryStep) Name() engine.StepName { return engine.StepShortTermMemory }
func (s *ShortTermMemoryStep) CanPause() bool         { return false }
func (s *ShortTermMemoryStep) Timeout() time.Duration { return 15 * time.Second }
func (s *ShortTermMemoryStep) Critical() bool         { return false }

// ShouldSkip 跳过匿名用户和会话 ID 为空的情况
func (s *ShortTermMemoryStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	if execCtx.UserID == "" || execCtx.UserID == "anonymous" {
		return true
	}
	if execCtx.ConversationID == "" {
		return true
	}
	return false
}

func (s *ShortTermMemoryStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	if s.svc == nil {
		return nil
	}

	// 灰度门控：检查该用户是否启用记忆
	if !s.svc.IsEnabledForUser(execCtx.UserID) {
		return nil
	}

	// 1. 加载对话历史
	history, err := s.svc.LoadHistory(ctx, execCtx.ConversationID, 50)
	if err != nil {
		slog.Warn("short-term memory: load history failed", "error", err)
		return nil // Non-fatal
	}

	// 1b. 加载上一轮的工作记忆摘要（跨请求继承）
	if execCtx.WorkingSummary == nil {
		prevWS, err := s.svc.LoadWorkingSummary(ctx, execCtx.ConversationID)
		if err != nil {
			slog.Warn("short-term memory: load working summary failed", "error", err)
		} else if prevWS != nil {
			// 继承上一轮的压缩摘要和步骤摘要，但重置已摘要标记
			// 让 WorkingMemoryStep 可以增量处理本轮的新步骤
			prevWS.TraceID = execCtx.TraceID
			prevWS.ConversationID = execCtx.ConversationID
			execCtx.WorkingSummary = prevWS
			slog.Info("short-term memory: inherited working summary from previous run",
				"conversation_id", execCtx.ConversationID,
				"prev_step_summaries", len(prevWS.StepSummaries),
				"has_compressed", prevWS.CompressedSummary != "",
			)
		}
	}

	if len(history) == 0 {
		return nil
	}

	// 2. 生成当前查询的 embedding
	queryVector, err := s.embedder.Embed(ctx, execCtx.UserInput)
	if err != nil {
		slog.Warn("short-term memory: embed query failed", "error", err)
		// 降级：无 embedding 时使用最近窗口
		queryVector = nil
	}

	// 3. 语义裁切
	chunker := memory.NewSemanticChunker(s.config)
	chunks := chunker.Chunk(history)

	// 4. 动态窗口选择
	selector := memory.NewDynamicWindowSelector(s.config)
	selected := selector.Select(chunks, queryVector)

	// 5. 存入 ExecutionContext
	execCtx.ConversationHistory = selected

	// 6. 初始化随机态（在管道最早期，供后续 RelevanceStep/WriteStep 使用）
	if execCtx.StochasticState == nil {
		seed := memory.GenerateSeedFromInput(execCtx.UserInput)
		execCtx.StochasticState = memory.NewStochasticState(seed)
	}

	slog.Info("short-term memory: history loaded",
		"trace_id", execCtx.TraceID,
		"conversation_id", execCtx.ConversationID,
		"total_history", len(history),
		"chunks", len(chunks),
		"selected", len(selected),
	)

	return nil
}

// ─── 短期记忆存储 Step：在 agent 完成后存储消息 ──────────────

// ShortTermStoreStep 在管道结束后执行：
//   - 存储用户消息
//   - 存储助手响应（文章/聊天回复）
type ShortTermStoreStep struct {
	svc      ShortTermMemoryAdapter
	embedder EmbedderAdapter
}

// NewShortTermStoreStep 创建短期记忆存储步骤
func NewShortTermStoreStep(svc ShortTermMemoryAdapter, embedder EmbedderAdapter) *ShortTermStoreStep {
	return &ShortTermStoreStep{svc: svc, embedder: embedder}
}

func (s *ShortTermStoreStep) Name() engine.StepName { return engine.StepShortTermStore }
func (s *ShortTermStoreStep) CanPause() bool         { return false }
func (s *ShortTermStoreStep) Timeout() time.Duration { return 15 * time.Second }
func (s *ShortTermStoreStep) Critical() bool         { return false }

func (s *ShortTermStoreStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	if execCtx.UserID == "" || execCtx.UserID == "anonymous" {
		return true
	}
	if execCtx.ConversationID == "" {
		return true
	}
	return false
}

func (s *ShortTermStoreStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	if s.svc == nil {
		return nil
	}

	// 灰度门控
	if !s.svc.IsEnabledForUser(execCtx.UserID) {
		return nil
	}

	// 1. 存储用户消息
	userMsg := &memory.ConversationMessage{
		ConversationID: execCtx.ConversationID,
		UserID:         execCtx.UserID,
		TraceID:        execCtx.TraceID,
		Role:           memory.RoleUser,
		Content:        execCtx.UserInput,
		ContentType:    memory.ContentText,
		Intent:         "",
		TokenCount:     memory.EstimateTokens(execCtx.UserInput),
		CreatedAt:      execCtx.StartedAt,
	}
	if execCtx.TaskIntent != nil {
		userMsg.Intent = execCtx.TaskIntent.TaskMode
	}

	if err := s.svc.StoreMessage(ctx, userMsg); err != nil {
		slog.Warn("short-term memory: store user message failed", "error", err)
	}

	// 异步生成 embedding（带超时，防止 goroutine 泄露）
	if s.embedder != nil && userMsg.ID != "" {
		go func() {
			asyncCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			vec, err := s.embedder.Embed(asyncCtx, execCtx.UserInput)
			if err != nil {
				slog.Warn("short-term memory: async embed user message failed", "error", err)
				return
			}
			// 通过 store 更新 embedding
			if updater, ok := s.svc.(interface {
				UpdateEmbedding(ctx context.Context, messageID string, embedding []float32) error
			}); ok {
				if err := updater.UpdateEmbedding(asyncCtx, userMsg.ID, vec); err != nil {
					slog.Warn("short-term memory: update user embedding failed", "error", err)
				}
			}
		}()
	}

	// 2. 存储助手响应
	if execCtx.Article != "" {
		contentType := memory.ContentArticle
		if execCtx.TaskIntent != nil && execCtx.TaskIntent.TaskMode == "chat" {
			contentType = memory.ContentText
		}

		assistantMsg := &memory.ConversationMessage{
			ConversationID: execCtx.ConversationID,
			UserID:         execCtx.UserID,
			TraceID:        execCtx.TraceID,
			Role:           memory.RoleAssistant,
			Content:        execCtx.Article,
			ContentType:    contentType,
			Intent:         "",
			TokenCount:     memory.EstimateTokens(execCtx.Article),
			CreatedAt:      time.Now(),
		}

		if err := s.svc.StoreMessage(ctx, assistantMsg); err != nil {
			slog.Warn("short-term memory: store assistant message failed", "error", err)
		}

		// 异步生成 embedding（带超时，防止 goroutine 泄露）
		if s.embedder != nil {
			go func() {
				asyncCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				// 对长文章安全截断后再 embedding
				embedText := execCtx.Article
				if len(embedText) > 500 {
					embedText = memory.SafeTruncate(embedText, 500)
				}
				vec, err := s.embedder.Embed(asyncCtx, embedText)
				if err != nil {
					slog.Warn("short-term memory: async embed assistant message failed", "error", err)
					return
				}
				if updater, ok := s.svc.(interface {
					UpdateEmbedding(ctx context.Context, messageID string, embedding []float32) error
				}); ok {
					if err := updater.UpdateEmbedding(asyncCtx, assistantMsg.ID, vec); err != nil {
						slog.Warn("short-term memory: update assistant embedding failed", "error", err)
					}
				}
			}()
		}
	}

	slog.Info("short-term memory: messages stored",
		"trace_id", execCtx.TraceID,
		"conversation_id", execCtx.ConversationID,
	)

	// 3. 持久化工作记忆摘要（跨请求继承）
	if execCtx.WorkingSummary != nil {
		if ws, ok := execCtx.WorkingSummary.(*memory.WorkingSummary); ok && ws != nil {
			ws.ConversationID = execCtx.ConversationID
			ws.TraceID = execCtx.TraceID
			ws.LastUpdatedAt = time.Now()
			if err := s.svc.SaveWorkingSummary(ctx, ws); err != nil {
				slog.Warn("short-term memory: save working summary failed", "error", err)
			}
		}
	}

	return nil
}

// ─── 工作记忆 Step：增量摘要 ────────────────────────────────

// WorkingMemoryStep 在关键步骤后执行增量摘要
type WorkingMemoryStep struct {
	summarizer memory.LLMSummarizer
	config     memory.SummarizerConfig
}

// NewWorkingMemoryStep 创建工作记忆步骤
func NewWorkingMemoryStep(summarizer memory.LLMSummarizer, config memory.SummarizerConfig) *WorkingMemoryStep {
	return &WorkingMemoryStep{summarizer: summarizer, config: config}
}

func (s *WorkingMemoryStep) Name() engine.StepName { return engine.StepWorkingMemory }
func (s *WorkingMemoryStep) CanPause() bool         { return false }
func (s *WorkingMemoryStep) Timeout() time.Duration { return 30 * time.Second }
func (s *WorkingMemoryStep) Critical() bool         { return false }

func (s *WorkingMemoryStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	return false // 不跳过任何场景
}

func (s *WorkingMemoryStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	// 初始化工作记忆（如果尚未初始化）
	if execCtx.WorkingSummary == nil {
		execCtx.WorkingSummary = &memory.WorkingSummary{
			TraceID:         execCtx.TraceID,
			LastUpdatedAt:   time.Now(),
			SummarizedSteps: make(map[string]bool),
		}
	}

	// 初始化随机态（如果 ShortTermMemoryStep 被 skip 导致尚未初始化）
	if execCtx.StochasticState == nil {
		seed := memory.GenerateSeedFromInput(execCtx.UserInput)
		execCtx.StochasticState = memory.NewStochasticState(seed)
	}

	// 类型断言获取 WorkingSummary
	ws, ok := execCtx.WorkingSummary.(*memory.WorkingSummary)
	if !ok {
		return nil
	}
	if ws.SummarizedSteps == nil {
		ws.SummarizedSteps = make(map[string]bool)
	}

	// 遍历 StepHistory，摘要所有已完成但尚未摘要的步骤
	is := memory.NewIncrementalSummarizer(s.config, s.summarizer)
	summarizedAny := false

	for _, rec := range execCtx.StepHistory {
		if rec.Status != "completed" {
			continue
		}
		stepName := string(rec.Step)
		if ws.SummarizedSteps[stepName] {
			continue
		}

		// 跳过自身和内部辅助步骤（不产生有意义的摘要）
		if stepName == string(engine.StepWorkingMemory) ||
			stepName == string(engine.StepShortTermMemory) ||
			stepName == string(engine.StepShortTermStore) ||
			stepName == string(engine.StepMemoryExtract) {
			ws.SummarizedSteps[stepName] = true
			continue
		}

		summary := s.summarizeStep(execCtx, stepName)
		// 无论是否生成摘要都标记为已处理，避免重复尝试
		ws.SummarizedSteps[stepName] = true
		if summary == "" {
			continue
		}

		is.AddStepSummary(ws, stepName, summary, execCtx.TotalTokens)
		summarizedAny = true

		slog.Debug("working memory: step summarized",
			"trace_id", execCtx.TraceID,
			"step", stepName,
			"total_steps", len(ws.StepSummaries),
		)
	}

	if summarizedAny {
		slog.Info("working memory: batch summarization completed",
			"trace_id", execCtx.TraceID,
			"summarized_steps", len(ws.SummarizedSteps),
			"total_step_summaries", len(ws.StepSummaries),
		)
	}

	return nil
}

// summarizeStep 为指定步骤生成摘要（基于当前 execCtx 中的数据状态）
func (s *WorkingMemoryStep) summarizeStep(execCtx *engine.ExecutionContext, step string) string {
	switch step {
	case "intent":
		if execCtx.TaskIntent != nil {
			return fmt.Sprintf("意图分类: %s (置信度 %.2f)", execCtx.TaskIntent.TaskMode, execCtx.TaskIntent.Confidence)
		}
	case "memory_gate":
		if execCtx.MemoryContext != nil {
			return "记忆门控: 已注入用户偏好记忆"
		}
		return "记忆门控: 无可用记忆"
	case "query_plan":
		if execCtx.WritingTask != nil && len(execCtx.WritingTask.SearchQueries) > 0 {
			return fmt.Sprintf("检索规划: 话题「%s」, %d 个查询", execCtx.WritingTask.Topic, len(execCtx.WritingTask.SearchQueries))
		}
		return "检索规划: 已生成搜索计划"
	case "search":
		return fmt.Sprintf("搜索完成: %d 条结果", len(execCtx.SearchResults))
	case "relevance":
		return fmt.Sprintf("相关性过滤: 保留 %d 条", len(execCtx.SearchResults))
	case "compress":
		if execCtx.CompressedContext != "" {
			return fmt.Sprintf("素材压缩: 已生成研究简报 (%d 字)", len(execCtx.CompressedContext))
		}
		return "素材压缩: 已完成"
	case "outline":
		if execCtx.Outline != nil {
			return fmt.Sprintf("大纲生成: 标题「%s」, %d 个要点", execCtx.Outline.Title, len(execCtx.Outline.Outline))
		}
	case "write":
		return fmt.Sprintf("文章生成: %d 字", len(execCtx.Article))
	case "post_review":
		if execCtx.ReviewResult != nil {
			return fmt.Sprintf("审查完成: 通过=%v, %d 个问题", execCtx.ReviewResult.Passed, len(execCtx.ReviewResult.Issues))
		}
	case "auto_fix":
		if execCtx.ReviewResult != nil {
			return fmt.Sprintf("自动修正: 通过=%v", execCtx.ReviewResult.Passed)
		}
	case "chat":
		return fmt.Sprintf("对话回复: %d 字", len(execCtx.Article))
	}
	return ""
}
