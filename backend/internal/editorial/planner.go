package editorial

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ─── Planner Agent（Beta: 编辑部模式 Phase 3.2）────────────
//
// Planner 分析用户意图，调用 LLM 输出 AgentConfig[] + WorkflowSpec。
// 借鉴 OpenMAIC 的 Director 和 CrewAI 的 Agent 配置格式。
//
// 流程：
// 1. 调用 LLM，输入用户意图 + 可用工具列表 + Artifact 类型清单
// 2. LLM 输出 JSON（agents + workflow）
// 3. 验证输出合法性（DAG 无环、工具集合法、artifact 类型匹配）
// 4. 如果输出不合法，回退到预设角色（Fallback）

// Planner 策划 Agent
type Planner struct {
	llm *tools.LLMClient
}

// NewPlanner 创建 Planner
func NewPlanner(llm *tools.LLMClient) *Planner {
	return &Planner{llm: llm}
}

// PlanResult Planner 输出
type PlanResult struct {
	Agents    []AgentConfig  `json:"agents"`
	Workflow  WorkflowSpec   `json:"workflow"`
	Rationale string         `json:"rationale"`
}

// plannerLLMResponse LLM 的原始响应格式
type plannerLLMResponse struct {
	Agents    []struct {
		ID           string   `json:"id"`
		Name         string   `json:"name"`
		Role         string   `json:"role"`
		Persona      string   `json:"persona"`
		AllowedTools []string `json:"allowed_tools"`
		Priority     int      `json:"priority"`
	} `json:"agents"`
	Workflow struct {
		Nodes []struct {
			ID             string        `json:"id"`
			AgentID        string        `json:"agent_id"`
			Label          string        `json:"label"`
			Dependencies   []string      `json:"dependencies"`
			InputArtifacts []ArtifactType `json:"input_artifacts"`
			OutputArtifact ArtifactType  `json:"output_artifact"`
			ContextFork    int           `json:"context_fork"`
		} `json:"nodes"`
		Edges []Edge `json:"edges"`
	} `json:"workflow"`
	Rationale string `json:"rationale"`
}

// Plan 分析用户意图，生成 Agent 集群 + DAG 工作流
func (p *Planner) Plan(ctx context.Context, userInput string, taskID, userID string) (*PlanResult, error) {
	// 1. 调用 LLM
	systemPrompt := PlannerSystemPrompt
	userPrompt := fmt.Sprintf("用户写作意图：%s\n\n请设计 SubAgent 集群。", userInput)

	messages := []tools.LLMMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	content, _, err := p.llm.Chat(ctx, messages,
		tools.WithTemperature(0.7),
		tools.WithJSONResponse(),
		func(r *tools.LLMRequest) { r.MaxTokens = 2000 },
	)
	if err != nil {
		slog.Error("planner: LLM call failed", "error", err)
		return p.fallbackPlan(userInput, taskID, userID)
	}

	// 2. 解析 JSON
	var llmResp plannerLLMResponse
	if err := json.Unmarshal([]byte(content), &llmResp); err != nil {
		slog.Error("planner: failed to parse LLM JSON", "error", err, "content", content)
		return p.fallbackPlan(userInput, taskID, userID)
	}

	// 3. 转换为 AgentConfig
	// LLM 输出的 agent_id 是临时 ID（a1, a2...），我们保持这些 ID 不变，
	// 直接用 LLM 的 ID 作为 AgentConfig.ID，避免映射问题。
	agents := make([]AgentConfig, 0, len(llmResp.Agents))
	for _, a := range llmResp.Agents {
		// 验证角色合法性
		if _, ok := BuiltinRoles[a.Role]; !ok {
			slog.Warn("planner: invalid role from LLM, using fallback", "role", a.Role)
			a.Role = "writer" // fallback
		}
		base := BuiltinRoles[a.Role]
		cfg := &AgentConfig{
			ID:           a.ID, // 保持 LLM 分配的 ID，确保 node.agent_id 引用一致
			Name:         a.Name,
			Role:         a.Role,
			Persona:      a.Persona,
			AllowedTools: base.AllowedTools,
			CanProduce:   base.CanProduce,
			CanConsume:   base.CanConsume,
			BaseRole:     a.Role,
			IsGenerated:  true,
			CreatedAt:     time.Now(),
		}
		if a.Priority > 0 {
			cfg.Priority = a.Priority
		}
		agents = append(agents, *cfg)
	}

	// 4. 转换为 WorkflowSpec
	nodes := make([]NodeSpec, 0, len(llmResp.Workflow.Nodes))
	for _, n := range llmResp.Workflow.Nodes {
		nodes = append(nodes, NodeSpec{
			ID:             n.ID,
			AgentID:        n.AgentID, // 引用 LLM 分配的 agent ID
			Label:          n.Label,
			Dependencies:   n.Dependencies,
			InputArtifacts: n.InputArtifacts,
			OutputArtifact: n.OutputArtifact,
			ContextFork:    ContextForkMode(n.ContextFork),
		})
	}

	spec := WorkflowSpec{
		TaskID:    taskID,
		Nodes:     nodes,
		Edges:     llmResp.Workflow.Edges,
		CreatedBy: userID,
		Source:    "llm_generated",
		CreatedAt: time.Now(),
	}

	// 5. 校验 DAG
	// 注意：这里需要临时注册生成的 agents 来做校验
	tmpRegistry := NewDynamicAgentRegistry()
	tmpConfigs := make([]*AgentConfig, len(agents))
	for i := range agents {
		tmpConfigs[i] = &agents[i]
	}
	tmpRegistry.ApplyGeneratedAgents(taskID, tmpConfigs)

	if err := ValidateDAG(&spec, tmpRegistry); err != nil {
		slog.Error("planner: DAG validation failed, using fallback", "error", err)
		return p.fallbackPlan(userInput, taskID, userID)
	}

	slog.Info("planner: plan generated",
		"task_id", taskID, "agents", len(agents), "nodes", len(nodes),
		"rationale", llmResp.Rationale)

	return &PlanResult{
		Agents:    agents,
		Workflow:  spec,
		Rationale: llmResp.Rationale,
	}, nil
}

// fallbackPlan 生成预设的线性三 Agent 计划（研究→写作→审校）
func (p *Planner) fallbackPlan(userInput, taskID, userID string) (*PlanResult, error) {
	slog.Info("planner: using fallback plan (linear research→writing→review)")

	researcher := GenerateAgentConfig("researcher", "研究员", "你是一位严谨的研究员，负责搜集与主题相关的信息。")
	writer := GenerateAgentConfig("writer", "撰稿人", "你是一位资深撰稿人，基于研究简报撰写文章。")
	reviewer := GenerateAgentConfig("reviewer", "审校编辑", "你是一位严格的编辑，负责审校稿件质量。")

	agents := []AgentConfig{*researcher, *writer, *reviewer}

	// 构建线性 DAG: research → writing → review
	spec := WorkflowSpec{
		TaskID: taskID,
		Nodes: []NodeSpec{
			{
				ID:             "n1",
				AgentID:        researcher.ID,
				Label:          "研究",
				Dependencies:   []string{},
				InputArtifacts: []ArtifactType{ArtifactTopicCard},
				OutputArtifact: ArtifactResearchBrief,
				ContextFork:    ContextForkFull,
			},
			{
				ID:             "n2",
				AgentID:        writer.ID,
				Label:          "写作",
				Dependencies:   []string{"n1"},
				InputArtifacts: []ArtifactType{ArtifactResearchBrief, ArtifactFactClaims, ArtifactTopicCard},
				OutputArtifact: ArtifactDraft,
				ContextFork:    ContextForkFull,
			},
			{
				ID:             "n3",
				AgentID:        reviewer.ID,
				Label:          "审校",
				Dependencies:   []string{"n2"},
				InputArtifacts: []ArtifactType{ArtifactDraft, ArtifactSourcePack, ArtifactFactClaims, ArtifactResearchBrief},
				OutputArtifact: ArtifactReviewReport,
				ContextFork:    ContextForkSummary,
			},
		},
		Edges: []Edge{
			{From: "n1", To: "n2", Label: "research_brief"},
			{From: "n2", To: "n3", Label: "draft"},
		},
		CreatedBy: userID,
		Source:    "fallback",
		CreatedAt: time.Now(),
	}

	return &PlanResult{
		Agents:    agents,
		Workflow:  spec,
		Rationale: "LLM 生成角色或 DAG 不合法，回退到预设的线性三 Agent 模式（研究→写作→审校）",
	}, nil
}
