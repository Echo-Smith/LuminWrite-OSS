package memory

import (
	"context"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// DashscopeEmbedder 是 memory.Embedder 的通义 Dashscope 实现
type DashscopeEmbedder struct {
	client *tools.EmbeddingClient
}

// NewDashscopeEmbedder 创建 Dashscope embedding 适配器
func NewDashscopeEmbedder(client *tools.EmbeddingClient) *DashscopeEmbedder {
	return &DashscopeEmbedder{client: client}
}

// Embed 生成文本的向量表示
func (e *DashscopeEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if e.client == nil {
		return nil, nil
	}
	vec, _, err := e.client.EmbedSingle(ctx, text)
	if err != nil {
		return nil, err
	}
	// Convert []float64 to []float32
	result := make([]float32, len(vec))
	for i, v := range vec {
		result[i] = float32(v)
	}
	return result, nil
}

// Dimension 返回向量维度
func (e *DashscopeEmbedder) Dimension() int {
	return 1024 // 通义 text-embedding-v3
}
