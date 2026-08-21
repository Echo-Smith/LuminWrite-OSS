package editorial

import (
	"time"

	"github.com/google/uuid"
)

// ─── 预设角色库（Beta: 编辑部模式 Phase 1.6）──────────────
//
// 借鉴 Codex 的 Role 系统：内置角色经过验证的高质量角色模板，
// LLM 生成角色时基于预设角色做定制化而非从零生成。

// AgentRoleConfig 预设角色配置
type AgentRoleConfig struct {
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	Persona           string         `json:"persona"`
	AllowedTools      []string       `json:"allowed_tools"`
	CanProduce        []ArtifactType `json:"can_produce"`
	CanConsume        []ArtifactType `json:"can_consume"`
	DisabledFeatures  []string       `json:"disabled_features,omitempty"`
}

// ToAgentConfig 将预设角色转换为 AgentConfig（非生成态）
func (r *AgentRoleConfig) ToAgentConfig() *AgentConfig {
	return &AgentConfig{
		ID:           "builtin_" + r.Name,
		Name:         r.Name,
		Role:         r.Name,
		Persona:      r.Persona,
		AllowedTools: r.AllowedTools,
		CanProduce:   r.CanProduce,
		CanConsume:   r.CanConsume,
		BaseRole:     r.Name,
		IsGenerated:  false,
		CreatedAt:    time.Now(),
	}
}

// BuiltinRoles 预设角色库
var BuiltinRoles = map[string]*AgentRoleConfig{
	"researcher": {
		Name:         "researcher",
		Description:  "负责信息搜集和事实核查",
		Persona:      "你是一位严谨的研究员，擅长多源检索、信源分级、事实声明与证据绑定。你的产出是结构化的研究简报和信源包。",
		AllowedTools: []string{"search", "factcheck"},
		CanProduce:   []ArtifactType{ArtifactResearchBrief, ArtifactSourcePack, ArtifactFactClaims},
		CanConsume:   []ArtifactType{ArtifactTopicCard},
		DisabledFeatures: []string{"write"},
	},
	"writer": {
		Name:         "writer",
		Description:  "负责文章撰写",
		Persona:      "你是一位资深撰稿人，基于已批准研究包写作，按风格 Profile 生成提纲和初稿，接受审校意见修改。",
		AllowedTools: []string{"write"},
		CanProduce:   []ArtifactType{ArtifactOutline, ArtifactDraft, ArtifactRevisedDraft},
		CanConsume:   []ArtifactType{ArtifactResearchBrief, ArtifactFactClaims, ArtifactReviewReport, ArtifactTopicCard},
	},
	"reviewer": {
		Name:             "reviewer",
		Description:      "负责审校和质量控制",
		Persona:          "你是一位严格的编辑，使用独立上下文审查事实、风格、风险。你拥有驳回权但不能直接发布。",
		AllowedTools:     []string{"factcheck", "style_review"},
		CanProduce:       []ArtifactType{ArtifactReviewReport},
		CanConsume:       []ArtifactType{ArtifactSourcePack, ArtifactFactClaims, ArtifactDraft, ArtifactRevisedDraft, ArtifactResearchBrief},
	},
}

// GenerateAgentConfig 基于 BuiltinRoles 生成定制化的 AgentConfig。
// LLM 定制的 persona 覆盖预设角色，工具集从预设继承。
func GenerateAgentConfig(baseRole, customName, customPersona string) *AgentConfig {
	base, ok := BuiltinRoles[baseRole]
	if !ok {
		// Fallback: 默认使用 writer
		base = BuiltinRoles["writer"]
		baseRole = "writer"
	}
	cfg := &AgentConfig{
		ID:           uuid.New().String(),
		Name:         customName,
		Role:         baseRole,
		Persona:      customPersona,
		AllowedTools: base.AllowedTools,
		CanProduce:   base.CanProduce,
		CanConsume:   base.CanConsume,
		BaseRole:     baseRole,
		IsGenerated:  true,
		CreatedAt:    time.Now(),
	}
	return cfg
}
