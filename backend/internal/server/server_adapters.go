package server

import (
	"context"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	memsvc "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memory"
)

// ─── 适配器：将 Service/LLMClient 适配为 engine steps 所需接口 ──

// embedderAdapter 将 memsvc.Service 适配为 steps.EmbedderAdapter
type embedderAdapter struct {
	svc *memsvc.Service
}

func (a *embedderAdapter) Embed(ctx context.Context, text string) ([]float32, error) {
	return a.svc.Embed(ctx, text)
}

// workingMemoryLLMAdapter 将 tools.LLMClient 适配为 memory.LLMSummarizer
type workingMemoryLLMAdapter struct {
	llm *tools.LLMClient
}

func (a *workingMemoryLLMAdapter) Summarize(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	resp, _, err := a.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	})
	if err != nil {
		return "", err
	}
	return resp, nil
}
