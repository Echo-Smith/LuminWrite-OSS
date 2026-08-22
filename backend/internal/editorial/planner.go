package editorial

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
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

	// 元数据（从 PlanInput 传递，供 handleWorkflowExecute 构建 Task 使用）
	StyleSlug string   `json:"-"`
	Tags      []string `json:"-"`
	UserInput string   `json:"-"`
	KBEnabled *bool    `json:"-"` // 知识库搜索开关（nil=默认开启）
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

// PlanInput Planner 的输入，包含用户意图和可选的任务元数据
type PlanInput struct {
	UserInput   string  // 用户写作意图
	Title       string  // 任务标题（可选，由前端提供）
	Description string  // 任务详细描述（可选）
	StyleSlug   string  // 写作风格标识（可选）
	Tags        []string // 栏目标签（可选）
	KBEnabled   *bool   // 知识库搜索开关（nil=默认开启）
}

// Plan 分析用户意图，生成 Agent 集群 + DAG 工作流
func (p *Planner) Plan(ctx context.Context, input PlanInput, taskID, userID string) (*PlanResult, error) {
	// 1. 构建 LLM 输入
	systemPrompt := PlannerSystemPrompt

	var userPromptBuilder strings.Builder
	userPromptBuilder.WriteString("用户写作意图：")
	userPromptBuilder.WriteString(input.UserInput)

	if input.Title != "" {
		userPromptBuilder.WriteString("\n\n任务标题：")
		userPromptBuilder.WriteString(input.Title)
	}
	if input.Description != "" {
		userPromptBuilder.WriteString("\n任务描述：")
		userPromptBuilder.WriteString(input.Description)
	}
	if input.StyleSlug != "" {
		userPromptBuilder.WriteString("\n写作风格：")
		userPromptBuilder.WriteString(input.StyleSlug)
	}
	if len(input.Tags) > 0 {
		userPromptBuilder.WriteString("\n栏目标签：")
		userPromptBuilder.WriteString(strings.Join(input.Tags, ", "))
	}
	userPromptBuilder.WriteString("\n\n请设计 SubAgent 集群。")

	userPrompt := userPromptBuilder.String()

	messages := []tools.LLMMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	// Planner 是结构化输出任务，禁用 thinking 模式以确保 JSON 输出格式稳定。
	// thinking 模式下模型可能输出 reasoning_content 前缀或改变 JSON 结构。
	// 同时使用较低 temperature + JSON mode + 足够大的 max_tokens。
	content, _, err := p.llm.Chat(ctx, messages,
		tools.WithThinking(false),
		tools.WithTemperature(0.3),
		tools.WithJSONResponse(),
		func(r *tools.LLMRequest) { r.MaxTokens = 8192 },
	)
	if err != nil {
		// 触发点 1: LLM 调用失败 — 不再 fallback，直接返回错误
		// 原因：LLM 不可用时 fallback 只是把失败延后到执行阶段，没有意义
		slog.Error("planner: LLM call failed, cannot generate plan", "error", err)
		return nil, fmt.Errorf("AI 服务暂时不可用，请稍后重试：%w", err)
	}

	// 2. 解析 JSON — 多层容错
	// 某些 LLM 即使设了 response_format: json_object，仍可能：
	//   a) 在输出前后残留 ```json ... ``` 包裹
	//   b) 在 JSON 前输出说明文字（thinking 模式残留）
	//   c) 输出被 max_tokens 截断导致 JSON 不完整
	var llmResp plannerLLMResponse
	parseErr := parsePlannerJSON(content, &llmResp)

	if parseErr != nil {
		// 第一次解析失败：记录错误，尝试重试一次（更低 temperature，确保格式稳定）
		slog.Warn("planner: first JSON parse failed, retrying with lower temperature",
			"error", parseErr,
			"raw_content_len", len(content),
			"raw_content_preview", truncateForLog(content, 500))

		retryContent, _, retryErr := p.llm.Chat(ctx, messages,
			tools.WithThinking(false),
			tools.WithTemperature(0.1),
			tools.WithJSONResponse(),
			func(r *tools.LLMRequest) { r.MaxTokens = 8192 },
		)
		if retryErr != nil {
			slog.Error("planner: retry LLM call also failed", "error", retryErr)
			// 回退到预设计划，而非直接报错
			slog.Info("planner: falling back to preset plan after retry failure")
			return p.fallbackPlan(input, taskID, userID)
		}

		if err := parsePlannerJSON(retryContent, &llmResp); err != nil {
			slog.Error("planner: retry JSON parse also failed, falling back to preset plan",
				"error", err,
				"raw_content_len", len(retryContent),
				"raw_content_preview", truncateForLog(retryContent, 500))
			// 回退到预设计划，而非直接报错
			return p.fallbackPlan(input, taskID, userID)
		}
	}

	// 3. 转换为 AgentConfig
	// LLM 输出的 agent_id 是临时 ID（a1, a2...），我们保持这些 ID 不变，
	// 直接用 LLM 的 ID 作为 AgentConfig.ID，避免映射问题。
	agents := make([]AgentConfig, 0, len(llmResp.Agents))
	for _, a := range llmResp.Agents {
		// 验证角色合法性
		if _, ok := BuiltinRoles[a.Role]; !ok {
			slog.Warn("planner: invalid role from LLM, forcing to writer",
				"original_role", a.Role, "agent_name", a.Name)
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
		// 触发点 3: DAG 校验失败 — 记录完整的 LLM 输出和校验错误，便于诊断
		slog.Error("planner: DAG validation failed, cannot proceed",
			"error", err,
			"agent_count", len(agents),
			"node_count", len(nodes),
"agents_json", mustJSONStr(agents),
		"nodes_json", mustJSONStr(nodes),
		"edges_json", mustJSONStr(spec.Edges),
			"llm_rationale", llmResp.Rationale)
		return nil, fmt.Errorf("AI 生成的 DAG 结构不合法（%v），请重试", err)
	}

	slog.Info("planner: plan generated",
		"task_id", taskID, "agents", len(agents), "nodes", len(nodes),
		"rationale", llmResp.Rationale)

	return &PlanResult{
		Agents:    agents,
		Workflow:  spec,
		Rationale: llmResp.Rationale,
		StyleSlug: input.StyleSlug,
		Tags:      input.Tags,
		UserInput: input.UserInput,
		KBEnabled: input.KBEnabled,
	}, nil
}

// stripMarkdownCodeBlock 剥离 LLM 输出中可能残留的 Markdown 代码块包裹。
// 处理以下情况：
//   - ```json\n{...}\n```  →  {...}
//   - ```\n{...}\n```      →  {...}
//   - 前后有多余的空白或说明文字
//   - thinking 模式残留的 <think>...</think> 标签
//
// 策略：优先用正则提取 JSON 片段（第一个 { 到最后一个 }），
// 如果没有找到 { 则原样返回。
func stripMarkdownCodeBlock(content string) string {
	s := strings.TrimSpace(content)

	// 去除 thinking 模式残留的 <think>...</think> 标签
	if idx := strings.Index(s, "</think>"); idx >= 0 {
		s = strings.TrimSpace(s[idx+len("</think>"):])
	}
	// 也去除可能的 <think> 开头（无闭合标签的情况）
	if strings.HasPrefix(s, "<think>") {
		s = strings.TrimSpace(s[len("<think>"):])
	}

	// 去除常见的 Markdown 代码块前缀/后缀
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```JSON")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	// 如果剥离后仍然不是以 { 开头，尝试提取第一个 { 到最后一个 }
	if !strings.HasPrefix(s, "{") {
		first := strings.Index(s, "{")
		last := strings.LastIndex(s, "}")
		if first >= 0 && last > first {
			s = s[first : last+1]
		}
	}

	return s
}

// parsePlannerJSON 解析 Planner LLM 响应为 plannerLLMResponse。
// 多层容错：
// 1. 先用 stripMarkdownCodeBlock 清理
// 2. 直接 json.Unmarshal
// 3. 如果失败，用 ExtractJSONObject 提取（处理残留包裹）
// 4. 如果仍然失败，尝试修复常见 JSON 截断问题
func parsePlannerJSON(content string, resp *plannerLLMResponse) error {
	// 层 1: 清理 Markdown 包裹 + thinking 残留
	cleaned := stripMarkdownCodeBlock(content)
	if cleaned == "" {
		return fmt.Errorf("empty content after cleaning")
	}

	// 层 2: 直接解析
	if err := json.Unmarshal([]byte(cleaned), resp); err == nil {
		return nil
	}

	// 层 3: 用 ExtractJSONObject 提取（处理更复杂的包裹情况）
	jsonStr := tools.ExtractJSONObject(content)
	if jsonStr != "" {
		if err := json.Unmarshal([]byte(jsonStr), resp); err == nil {
			return nil
		}
	}

	// 层 4: 尝试修复 JSON 截断 — 如果输出被 max_tokens 截断，
	// JSON 可能不完整。尝试补全缺失的闭合括号。
	if jsonStr == "" {
		jsonStr = cleaned
	}
	// 只在有 { 的情况下尝试修复
	if strings.Contains(jsonStr, "{") {
		repaired := repairTruncatedJSON(jsonStr)
		if repaired != jsonStr {
			if err := json.Unmarshal([]byte(repaired), resp); err == nil {
				slog.Info("planner: JSON repaired via bracket completion",
					"original_len", len(jsonStr),
					"repaired_len", len(repaired))
				return nil
			}
		}
	}

	return fmt.Errorf("JSON parse failed after all fallbacks")
}

// repairTruncatedJSON 尝试修复被截断的 JSON。
// 当 LLM 输出被 max_tokens 截断时，JSON 可能缺少闭合的 } 和 ]。
// 此函数统计未闭合的 { 和 [，并在末尾补全。
func repairTruncatedJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}

	// 统计未闭合的括号
	openBraces := 0   // { 计数
	openBrackets := 0 // [ 计数
	inString := false
	escape := false

	for _, ch := range s {
		if escape {
			escape = false
			continue
		}
		if ch == '\\' {
			escape = true
			continue
		}
		if ch == '"' && !escape {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch ch {
		case '{':
			openBraces++
		case '}':
			openBraces--
		case '[':
			openBrackets++
		case ']':
			openBrackets--
		}
	}

	// 补全缺失的闭合括号
	// 先尝试截断到最后一个完整的值
	// 然后补全 ] 和 }
	if openBraces > 0 || openBrackets > 0 {
		// 尝试在最后一个完整的 key-value 后截断
		// 找最后一个 } 或 ] 或 " 的位置
		lastValid := strings.LastIndexAny(s, "}\"]")
		if lastValid > 0 {
			// 检查是否在字符串中间
			tail := s[lastValid+1:]
			if strings.TrimSpace(tail) == "" || strings.HasPrefix(strings.TrimSpace(tail), ",") {
				// 如果尾部只有空白或逗号，截断到 lastValid+1
				s = s[:lastValid+1]
			}
		}
		// 重新计算需要补全的括号
		openBraces = 0
		openBrackets = 0
		inString = false
		escape = false
		for _, ch := range s {
			if escape {
				escape = false
				continue
			}
			if ch == '\\' {
				escape = true
				continue
			}
			if ch == '"' && !escape {
				inString = !inString
				continue
			}
			if inString {
				continue
			}
			switch ch {
			case '{':
				openBraces++
			case '}':
				openBraces--
			case '[':
				openBrackets++
			case ']':
				openBrackets--
			}
		}
		// 补全
		for i := 0; i < openBrackets; i++ {
			s += "]"
		}
		for i := 0; i < openBraces; i++ {
			s += "}"
		}
	}

	return s
}

// truncateForLog 截断字符串用于日志输出，避免日志过长
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
}

// mustJSONStr 将对象序列化为 JSON 字符串，失败时返回 "<error>"
func mustJSONStr(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<marshal error>"
	}
	return string(b)
}

// fallbackPlan 生成预设的线性三 Agent 计划（研究→写作→审校）
func (p *Planner) fallbackPlan(input PlanInput, taskID, userID string) (*PlanResult, error) {
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
		StyleSlug: input.StyleSlug,
		Tags:      input.Tags,
		UserInput: input.UserInput,
		KBEnabled: input.KBEnabled,
	}, nil
}
