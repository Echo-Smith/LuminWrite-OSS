package server

import (
	"net/http"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── A2A Agent Card ──────────────────────────────────────
//
// Implements the A2A (Agent-to-Agent) protocol's Agent Card concept.
// Each Agent role gets a self-describing JSON document that enables
// capability discovery by other Agents or orchestrators.
//
// Reference: https://google.github.io/A2A/
//
// The Agent Card includes:
//   - Identity: name, description, role
//   - Capabilities: what artifact types it can produce/consume
//   - Skills: tool list and decision types
//   - Constraints: isolation requirement, persona
//   - Metadata: version, status

// AgentCard is the A2A protocol Agent Card for a single agent role.
type AgentCard struct {
	// Identity
	Name        string `json:"name"`
	Role        string `json:"role"`
	Description string `json:"description"`
	Version     string `json:"version"`

	// Capabilities
	Capabilities AgentCapabilities `json:"capabilities"`

	// Skills (tools the agent can use)
	Skills []AgentSkill `json:"skills"`

	// Constraints
	RequiresIsolation bool   `json:"requires_isolation"`
	Persona           string `json:"persona,omitempty"`

	// Metadata
	Status string `json:"status"` // active | inactive
}

// AgentCapabilities describes what the agent can produce and consume.
type AgentCapabilities struct {
	Produces  []string `json:"produces"`   // artifact types
	Consumes  []string `json:"consumes"`   // artifact types
	Decisions []string `json:"decisions"` // decision types
}

// AgentSkill describes a tool/skill the agent can use.
type AgentSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// artifactTypeLabels maps artifact types to human-readable labels.
var artifactTypeLabels = map[string]string{
	"topic_card":     "选题卡",
	"research_brief": "研究简报",
	"source_pack":    "信源包",
	"fact_claims":    "事实声明表",
	"outline":        "提纲",
	"draft":          "初稿",
	"review_report":  "审查报告",
	"revised_draft":  "修改稿",
}

// decisionTypeLabels maps decision types to human-readable labels.
var decisionTypeLabels = map[string]string{
	"approve_topic":  "立项审批",
	"select_angle":   "角度选择",
	"trust_source":    "信源信任",
	"accept_review":  "审校接受",
	"allow_rewrite":  "重写许可",
	"publish":         "发布审批",
}

// buildAgentCards generates Agent Cards for all registered agent roles
// plus the built-in role configs.
func (s *Server) buildAgentCards() []AgentCard {
	cards := make([]AgentCard, 0, len(editorial.AgentRegistry)+1)

	// ── Card 0: The Orchestrator (Harness) ──
	cards = append(cards, AgentCard{
		Name:        "LuminBuddy Orchestrator",
		Role:        "orchestrator",
		Description: "单会话 Harness 架构的核心编排器。接收用户写作意图，动态调度工具链（搜索→提纲→写作→审校→事实核查），通过流式 SSE 输出文章。",
		Version:     "2.0",
		Capabilities: AgentCapabilities{
			Produces:  []string{"article", "outline"},
			Consumes:  []string{"user_intent", "search_results", "memory", "style_profile"},
			Decisions: []string{"tool_selection", "memory_gate"},
		},
		Skills: []AgentSkill{
			{Name: "search_web", Description: "多源网络搜索（Tavily/知乎/腾讯新闻/微博/Bing）"},
			{Name: "search_knowledge", Description: "知识库检索（BM25 + Dense + GraphRAG）"},
			{Name: "read_source", Description: "读取搜索结果全文"},
			{Name: "generate_outline", Description: "生成文章提纲供用户确认"},
			{Name: "write_article", Description: "流式写作（按风格 Profile 生成正文）"},
			{Name: "review_article", Description: "质量评审（6 维度打分 + 问题列表）"},
			{Name: "revise_section", Description: "按审校意见修订"},
			{Name: "word_count_check", Description: "字数检查"},
			{Name: "rewrite_title", Description: "标题优化"},
			{Name: "fact_check", Description: "事实核查（声明提取 + 搜索验证 + 较真查证）"},
		},
		RequiresIsolation: false,
		Status:            "active",
	})

	// ── Cards 1-3: Editorial Board Agents ──
	for _, role := range []editorial.AgentRole{editorial.RoleResearch, editorial.RoleWriting, editorial.RoleReview} {
		def, ok := editorial.AgentRegistry[role]
		if !ok {
			continue
		}

		// Get builtin role config for persona and tools
		var persona string
		var tools []string
		builtinKey := strings.TrimSuffix(string(role), "_agent")
		if rc, ok := editorial.BuiltinRoles[builtinKey]; ok {
			persona = rc.Persona
			tools = rc.AllowedTools
		}

		skills := make([]AgentSkill, 0, len(tools))
		for _, t := range tools {
			skills = append(skills, AgentSkill{
				Name:        t,
				Description: toolDescription(t),
			})
		}

		produces := make([]string, 0, len(def.CanProduce))
		for _, a := range def.CanProduce {
			produces = append(produces, string(a))
		}
		consumes := make([]string, 0, len(def.CanConsume))
		for _, a := range def.CanConsume {
			consumes = append(consumes, string(a))
		}
		decisions := make([]string, 0, len(def.CanDecide))
		for _, d := range def.CanDecide {
			decisions = append(decisions, string(d))
		}

		cards = append(cards, AgentCard{
			Name:        def.Name,
			Role:        string(def.Role),
			Description: def.Description,
			Version:     "2.0",
			Capabilities: AgentCapabilities{
				Produces:  produces,
				Consumes:  consumes,
				Decisions: decisions,
			},
			Skills:            skills,
			RequiresIsolation: def.RequiresIsolation,
			Persona:           persona,
			Status:            "active",
		})
	}

	return cards
}

func toolDescription(tool string) string {
	switch tool {
	case "search":
		return "网络搜索 + 知识库检索"
	case "write":
		return "文章撰写（提纲 + 正文）"
	case "factcheck":
		return "事实核查（声明提取 + 搜索验证 + 较真查证）"
	case "style_review":
		return "风格审查（Profile 符合度评分）"
	default:
		return tool
	}
}

// handleAgentCards returns all agent cards for A2A capability discovery.
//
// GET /api/v2/agent-cards
//
// This endpoint is public (no auth) to allow inter-agent discovery.
func (s *Server) handleAgentCards(w http.ResponseWriter, r *http.Request) {
	cards := s.buildAgentCards()

	// Build response with labels for frontend
	artifactLabels := make(map[string]string)
	for k, v := range artifactTypeLabels {
		artifactLabels[k] = v
	}
	decisionLabels := make(map[string]string)
	for k, v := range decisionTypeLabels {
		decisionLabels[k] = v
	}

	response.OK(w, map[string]any{
		"cards":              cards,
		"artifact_labels":    artifactLabels,
		"decision_labels":    decisionLabels,
		"protocol":           "A2A/v1",
		"total_agents":       len(cards),
	})
}

// handleAgentCard returns a single agent card by role.
//
// GET /api/v2/agent-cards/{role}
func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	if role == "" {
		// If no role, return all
		s.handleAgentCards(w, r)
		return
	}

	cards := s.buildAgentCards()
	for _, card := range cards {
		if card.Role == role {
			response.OK(w, card)
			return
		}
	}
	response.Err(w, http.StatusNotFound, "not_found", "agent role not found")
}
