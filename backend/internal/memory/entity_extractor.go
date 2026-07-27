package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// LLMEntityExtractor 是 memory.EntityExtractor 的 LLM 实现。
// 从文章和用户查询中提取实体（话题、风格、偏好、概念、语气、结构）
// 以及实体间的关系，构建实体记忆网络的数据来源。
type LLMEntityExtractor struct {
	llm *tools.LLMClient
}

// NewLLMEntityExtractor 创建 LLM 实体提取器
func NewLLMEntityExtractor(llm *tools.LLMClient) *LLMEntityExtractor {
	return &LLMEntityExtractor{llm: llm}
}

// entityExtractionResult LLM 返回的 JSON 结构
type entityExtractionResult struct {
	Entities  []entityExtractionItem `json:"entities"`
	Relations []relationExtractionItem `json:"relations"`
}

type entityExtractionItem struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type relationExtractionItem struct {
	Source       string  `json:"source"`
	Target       string  `json:"target"`
	RelationType string  `json:"relation_type"`
	Weight       float64 `json:"weight"`
}

// ExtractFromArticle 从文章中提取实体和关系
func (e *LLMEntityExtractor) ExtractFromArticle(ctx context.Context, article, styleSlug string) ([]memory.ExtractedEntity, error) {
	if e.llm == nil || len(article) < 100 {
		return nil, nil
	}

	prompt := fmt.Sprintf(`分析以下文章，提取写作实体和它们之间的关系。

文章（风格：%s）：
%s

请提取以下类型的实体：
- topic: 文章讨论的核心话题（如"人工智能"、"乡村振兴"）
- style: 写作风格（如"科技评论"、"时评"）
- preference: 可推断的作者/用户偏好（如"短文"、"数据驱动"）
- concept: 文章使用的写作概念或技巧（如"结构化论证"、"对比分析"）
- tone: 文章语气（如"严肃"、"轻松"）
- structure: 文章结构（如"三段论"、"递进式"）

同时提取实体之间的关系：
- prefers: 用户偏好 A 胜过 B
- dislikes: 用户不喜欢
- related_to: A 与 B 相关
- evolved_from: A 由 B 演化而来
- co_occurs_with: A 与 B 共现
- contrasts_with: A 与 B 对比

输出 JSON（只输出 JSON，不要其他文字）：
{
  "entities": [
    {"type": "topic", "name": "实体名称", "description": "简要描述"},
    {"type": "style", "name": "实体名称", "description": "简要描述"}
  ],
  "relations": [
    {"source": "源实体名称", "target": "目标实体名称", "relation_type": "related_to", "weight": 0.8}
  ]
}

注意：
- 实体名称用中文，简洁明确（2-10字）
- 每篇文章提取 3-8 个实体
- 关系提取 0-5 条，weight 范围 0.0-1.0
- 只提取文章中明确体现的实体和关系`, styleSlug, truncateForEntity(article, 2000))

	resp, _, err := e.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: "你是写作分析专家，擅长提取文章中的关键实体和关系。只输出 JSON 格式结果。"},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, fmt.Errorf("entity extraction LLM call failed: %w", err)
	}

	return parseExtractedEntities(resp)
}

// ExtractFromQuery 从用户查询中提取实体
func (e *LLMEntityExtractor) ExtractFromQuery(ctx context.Context, query string) ([]memory.ExtractedEntity, error) {
	if e.llm == nil || len(query) < 5 {
		return nil, nil
	}

	prompt := fmt.Sprintf(`分析以下用户输入，提取其中的关键写作实体。

用户输入：
%s

提取实体类型：
- topic: 用户想写的话题
- style: 期望的写作风格
- preference: 表达的偏好（如"短文"、"通俗易懂"）
- tone: 期望的语气
- structure: 期望的结构

输出 JSON（只输出 JSON）：
{
  "entities": [
    {"type": "topic", "name": "实体名称", "description": "简要描述"}
  ],
  "relations": []
}

注意：
- 只提取用户输入中明确提到的实体
- 实体名称用中文，简洁明确
- 最多提取 5 个实体`, query)

	resp, _, err := e.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: "你是写作需求分析专家。只输出 JSON 格式结果。"},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, fmt.Errorf("entity extraction from query failed: %w", err)
	}

	return parseExtractedEntities(resp)
}

// parseExtractedEntities 从 LLM 响应中解析实体提取结果
func parseExtractedEntities(resp string) ([]memory.ExtractedEntity, error) {
	// 清理 markdown 代码块
	resp = strings.TrimSpace(resp)
	resp = strings.TrimPrefix(resp, "```json")
	resp = strings.TrimPrefix(resp, "```")
	resp = strings.TrimSuffix(resp, "```")
	resp = strings.TrimSpace(resp)

	// 提取 JSON 对象
	objStart := strings.Index(resp, "{")
	objEnd := strings.LastIndex(resp, "}")
	if objStart < 0 || objEnd < 0 || objEnd <= objStart {
		slog.Debug("entity extraction: no JSON object found", "response", resp[:minForEntity(len(resp), 200)])
		return nil, nil
	}

	jsonStr := resp[objStart : objEnd+1]

	var result entityExtractionResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		slog.Warn("entity extraction: failed to parse JSON", "error", err)
		return nil, nil
	}

	// 转换为 memory.ExtractedEntity
	entities := make([]memory.ExtractedEntity, 0, len(result.Entities))
	entityNames := make(map[string]bool)

	for _, e := range result.Entities {
		et := memory.EntityType(e.Type)
		// 验证实体类型
		if !isValidEntityType(et) {
			continue
		}
		name := strings.TrimSpace(e.Name)
		if name == "" {
			continue
		}
		entityNames[name] = true
		entities = append(entities, memory.ExtractedEntity{
			EntityType:  et,
			Name:        name,
			Description: e.Description,
		})
	}

	// 解析关系，只保留源和目标实体都存在的关系
	for _, r := range result.Relations {
		source := strings.TrimSpace(r.Source)
		target := strings.TrimSpace(r.Target)
		if !entityNames[source] || !entityNames[target] {
			continue
		}
		rt := memory.RelationType(r.RelationType)
		if !isValidRelationType(rt) {
			continue
		}

		// 找到源实体并附加关系
		for i := range entities {
			if entities[i].Name == source {
				weight := r.Weight
				if weight <= 0 {
					weight = 0.5
				}
				if weight > 1 {
					weight = 1
				}
				entities[i].Relations = append(entities[i].Relations, memory.ExtractedRelation{
					TargetName:   target,
					RelationType: rt,
					Weight:       weight,
				})
				break
			}
		}
	}

	slog.Debug("entity extraction: parsed",
		"entities", len(entities),
		"relations", countRelations(entities),
	)

	return entities, nil
}

func isValidEntityType(t memory.EntityType) bool {
	switch t {
	case memory.EntityTopic, memory.EntityStyle, memory.EntityPreference,
		memory.EntityConcept, memory.EntityTone, memory.EntityStructure:
		return true
	}
	return false
}

func isValidRelationType(t memory.RelationType) bool {
	switch t {
	case memory.RelPrefers, memory.RelDislikes, memory.RelRelatedTo,
		memory.RelEvolvedFrom, memory.RelCoOccursWith, memory.RelContrastsWith:
		return true
	}
	return false
}

func countRelations(entities []memory.ExtractedEntity) int {
	total := 0
	for _, e := range entities {
		total += len(e.Relations)
	}
	return total
}

func truncateForEntity(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func minForEntity(a, b int) int {
	if a < b {
		return a
	}
	return b
}
