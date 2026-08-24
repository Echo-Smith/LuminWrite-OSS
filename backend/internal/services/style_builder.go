package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	worldstate "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/worldstate"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ─── Types ───────────────────────────────────────────────

// StyleBuilderMessage represents one turn in the style builder conversation.
type StyleBuilderMessage struct {
	Role    string `json:"role"` // user | assistant
	Content string `json:"content"`
}

// StyleBuilderSession holds the state of an AI style creation conversation.
type StyleBuilderSession struct {
	ID       string                `json:"id"`
	UserID   string                `json:"user_id"`
	Messages []StyleBuilderMessage `json:"messages"`
	Profile  *profile.StyleProfile `json:"profile,omitempty"` // generated config (nil until ready)
	Ready    bool                  `json:"ready"`             // true when AI thinks the profile is complete

	// KBID is set when the AI creates a knowledge base during the session.
	// It will be bound to the style profile's kb_id on commit.
	KBID string `json:"kb_id,omitempty"`

	// UploadedFiles holds the content of files uploaded by the user.
	// These are injected into the LLM context as part of the user message.
	UploadedFiles []UploadedFile `json:"uploaded_files,omitempty"`
}

// UploadedFile represents a file uploaded during style builder session.
type UploadedFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// StyleBuilderResponse is returned after each message.
type StyleBuilderResponse struct {
	Message string                `json:"message"`             // AI's reply text
	Ready   bool                  `json:"ready"`               // true when profile is complete
	Profile *profile.StyleProfile `json:"profile,omitempty"`   // present when ready=true
}

// ─── Service ─────────────────────────────────────────────

// StyleBuilderService manages AI-assisted style creation sessions.
// It supports tool calls: the AI can create knowledge bases, import documents,
// and save the style config — all within the conversation.
type StyleBuilderService struct {
	llm           *tools.LLMClient
	sessions      map[string]*StyleBuilderSession
	mu            sync.Mutex
	worldState    *worldstate.WorldState // v3.0: StyleBuilder prompt 的 section 基线管理
	kbMgr         *KbManager             // for create_kb / import_documents tools
	userStyleStore *database.UserStyleStore // for save_style tool
}

// NewStyleBuilderService creates a new StyleBuilderService.
func NewStyleBuilderService(llm *tools.LLMClient) *StyleBuilderService {
	return &StyleBuilderService{
		llm:        llm,
		sessions:   make(map[string]*StyleBuilderSession),
		worldState: worldstate.NewWorldState(),
	}
}

// WithKbManager injects the KB manager for tool calls (create_kb, import_documents).
func (s *StyleBuilderService) WithKbManager(mgr *KbManager) *StyleBuilderService {
	s.kbMgr = mgr
	return s
}

// WithUserStyleStore injects the user style store for the save_style tool.
func (s *StyleBuilderService) WithUserStyleStore(store *database.UserStyleStore) *StyleBuilderService {
	s.userStyleStore = store
	return s
}

// CreateSession starts a new style builder conversation.
func (s *StyleBuilderService) CreateSession(userID string) *StyleBuilderSession {
	sessionID := fmt.Sprintf("sb_%d_%s", time.Now().UnixNano(), uuid.NewString()[:8])
	session := &StyleBuilderSession{
		ID:       sessionID,
		UserID:   userID,
		Messages: []StyleBuilderMessage{},
	}

	s.mu.Lock()
	s.sessions[sessionID] = session
	s.mu.Unlock()

	return session
}

// SendMessage processes a user message and returns the AI response.
// The AI decides whether to ask more questions, call tools, or generate the final config.
// uploadedFiles is optional — when non-empty, their content is injected into the user message.
func (s *StyleBuilderService) SendMessage(ctx context.Context, sessionID, userMessage string, uploadedFiles []UploadedFile) (*StyleBuilderResponse, error) {
	s.mu.Lock()
	session, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("session not found")
	}

	// Build the effective user message: original text + uploaded file contents
	effectiveMsg := userMessage
	if len(uploadedFiles) > 0 {
		var sb strings.Builder
		sb.WriteString(userMessage)
		sb.WriteString("\n\n--- 用户上传的文件 ---\n")
		for _, f := range uploadedFiles {
			sb.WriteString(fmt.Sprintf("\n## 文件: %s\n```\n%s\n```\n", f.Name, f.Content))
		}
		effectiveMsg = sb.String()

		// Store uploaded files in session for reference
		session.UploadedFiles = append(session.UploadedFiles, uploadedFiles...)
	}

	// Append user message to session history
	session.Messages = append(session.Messages, StyleBuilderMessage{
		Role:    "user",
		Content: effectiveMsg,
	})

	// Build conversation messages for LLM
	llmMessages := []tools.LLMMessage{
		{Role: "system", Content: styleBuilderSystemPrompt},
	}
	for _, msg := range session.Messages {
		llmMessages = append(llmMessages, tools.LLMMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	// Build tool definitions and executor
	toolDefs := s.builderToolDefs()
	executor := s.builderToolExecutor(session)

	// Call LLM with tools
	reply, _, err := s.llm.ChatWithTools(
		ctx, llmMessages,
		func(string) {},  // onDelta — not needed for Style Builder (no streaming)
		func(string) {},  // onReasoning
		func() {},        // onReset
		toolDefs, executor,
		tools.WithTemperature(0.7),
	)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	// Check if the reply contains a complete JSON config
	var styleProfile *profile.StyleProfile
	ready := false

	jsonStr := tools.ExtractJSONObject(reply)
	if jsonStr != "" {
		var p profile.StyleProfile
		if err := json.Unmarshal([]byte(jsonStr), &p); err == nil && p.Slug != "" && p.SystemPrompt != "" {
			styleProfile = &p
			ready = true
		}
	}

	// For session history (LLM context), strip the JSON to keep the conversation clean.
	// For the frontend response, preserve the full original reply so the UI can
	// render the JSON in a nicely formatted, collapsible code block.
	historyContent := reply
	if ready {
		historyContent = stripTrailingJSON(reply)
	}

	// Append assistant message (stripped version for LLM context)
	session.Messages = append(session.Messages, StyleBuilderMessage{
		Role:    "assistant",
		Content: historyContent,
	})

	if ready {
		session.Profile = styleProfile
		session.Ready = true

		// If a KB was created during the session, bind it to the profile
		if session.KBID != "" && styleProfile.KbID == "" {
			styleProfile.KbID = session.KBID
		}
	}

	return &StyleBuilderResponse{
		Message: reply, // full original reply (with JSON) for frontend
		Ready:   ready,
		Profile: styleProfile,
	}, nil
}

// GetSession returns a session by ID.
func (s *StyleBuilderService) GetSession(sessionID string) (*StyleBuilderSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	return session, ok
}

// DeleteSession removes a session.
func (s *StyleBuilderService) DeleteSession(sessionID string) {
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
}

// ─── Tool Definitions ────────────────────────────────────

// builderToolDefs returns the tool definitions available to the Style Builder AI.
// Tools are only included when the required dependencies are available.
func (s *StyleBuilderService) builderToolDefs() []tools.ToolDef {
	defs := []tools.ToolDef{}

	if s.kbMgr != nil && s.kbMgr.IsConfigured() {
		defs = append(defs,
			tools.ToolDef{
				Type: "function",
				Function: tools.ToolDefFunction{
					Name:        "create_knowledge_base",
					Description: "创建一个新的知识库，用于存储参考文章和写作素材。当用户上传了参考文章或需要建立风格素材库时调用。返回知识库 ID。",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{
								"type":        "string",
								"description": "知识库名称，如「科技评论范文库」",
							},
							"description": map[string]any{
								"type":        "string",
								"description": "知识库描述",
							},
						},
						"required": []string{"name"},
					},
				},
			},
			tools.ToolDef{
				Type: "function",
				Function: tools.ToolDefFunction{
					Name:        "import_document",
					Description: "将一篇文档导入到指定知识库。文档内容会被自动分块和建立索引。当用户上传了参考文章时调用此工具将其导入知识库。",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title": map[string]any{
								"type":        "string",
								"description": "文档标题",
							},
							"content": map[string]any{
								"type":        "string",
								"description": "文档全文内容",
							},
						},
						"required": []string{"title", "content"},
					},
				},
			},
		)
	}

	return defs
}

// builderToolExecutor returns a ToolExecutor that handles tool calls
// in the context of a specific Style Builder session.
func (s *StyleBuilderService) builderToolExecutor(session *StyleBuilderSession) tools.ToolExecutor {
	return func(name string, arguments string) (string, error) {
		switch name {
		case "create_knowledge_base":
			return s.executeCreateKB(session, arguments)
		case "import_document":
			return s.executeImportDocument(session, arguments)
		default:
			return fmt.Sprintf("未知工具: %s", name), nil
		}
	}
}

// executeCreateKB handles the create_knowledge_base tool call.
// Creates a KB scoped to the session's user ID and stores the KB ID in the session.
func (s *StyleBuilderService) executeCreateKB(session *StyleBuilderSession, arguments string) (string, error) {
	var args struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Name == "" {
		return "错误：知识库名称不能为空", nil
	}

	kbID := uuid.NewString()
	kb, err := s.kbMgr.CreateKB(context.Background(), kbID, args.Name, args.Description, session.UserID)
	if err != nil {
		slog.Warn("style builder: create_knowledge_base failed", "error", err)
		return fmt.Sprintf("创建知识库失败: %v", err), nil
	}

	// Store KB ID in session for later binding
	session.KBID = kb.ID

	slog.Info("style builder: knowledge base created",
		"session_id", session.ID,
		"user_id", session.UserID,
		"kb_id", kb.ID,
		"kb_name", kb.Name,
	)

	return fmt.Sprintf("知识库已创建成功。\n- 知识库 ID: %s\n- 名称: %s\n- 描述: %s\n\n后续导入的文档将存入此知识库。最终生成的风格将自动绑定到此知识库。", kb.ID, kb.Name, kb.Description), nil
}

// executeImportDocument handles the import_document tool call.
// Imports a document into the KB created during this session.
func (s *StyleBuilderService) executeImportDocument(session *StyleBuilderSession, arguments string) (string, error) {
	var args struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Title == "" || args.Content == "" {
		return "错误：标题和内容不能为空", nil
	}

	kbID := session.KBID
	if kbID == "" {
		return "错误：尚未创建知识库，请先调用 create_knowledge_base 创建知识库。", nil
	}

	// Add document to KB
	doc, err := s.kbMgr.AddDocumentToKB(context.Background(), session.UserID, kbID, args.Title, args.Content, "text", map[string]interface{}{
		"source": "style_builder",
	})
	if err != nil {
		slog.Warn("style builder: import_document failed", "error", err)
		return fmt.Sprintf("导入文档失败: %v", err), nil
	}

	// Chunk and store
	chunkConfig := DefaultChunkConfig()
	chunks := ChunkText(args.Content, chunkConfig)
	for _, chunk := range chunks {
		s.kbMgr.AddChunk(context.Background(), doc.ID, session.UserID, chunk.Index, chunk.Title, chunk.Content, nil)
	}
	s.kbMgr.UpdateChunkCount(context.Background(), doc.ID, len(chunks))

	slog.Info("style builder: document imported",
		"session_id", session.ID,
		"kb_id", kbID,
		"doc_id", doc.ID,
		"title", args.Title,
		"chunks", len(chunks),
	)

	return fmt.Sprintf("文档已导入成功。\n- 标题: %s\n- 分块数: %d\n- 知识库: %s", args.Title, len(chunks), kbID), nil
}

// ─── Helpers ─────────────────────────────────────────────

// stripTrailingJSON removes the last balanced JSON object from text,
// keeping only the conversational prefix. Returns the original text
// if no balanced JSON block is found.
//
// This function handles both raw JSON and markdown-fenced JSON (```json ... ```).
// It also cleans up residual markdown fence markers left behind after removal.
func stripTrailingJSON(text string) string {
	// First, try to remove a markdown code block (```json ... ``` or ``` ... ```)
	// from the end of the text.
	if fenceEnd := strings.LastIndex(text, "```"); fenceEnd >= 0 {
		// Find the matching opening fence
		beforeFence := text[:fenceEnd]
		fenceStart := strings.LastIndex(beforeFence, "```")
		if fenceStart >= 0 {
			// Extract content between fences
			inner := beforeFence[fenceStart+3:]
			// Strip optional "json" language tag
			inner = strings.TrimPrefix(inner, "json")
			inner = strings.TrimSpace(inner)
			// Verify it looks like JSON
			if strings.HasPrefix(inner, "{") && strings.HasSuffix(inner, "}") {
				// Remove the entire code block + preceding whitespace
				return strings.TrimRight(text[:fenceStart], " \n\r\t")
			}
		}
	}

	// Fallback: remove a raw (unfenced) JSON object from the end
	depth := 0
	inString := false
	for i := len(text) - 1; i >= 0; i-- {
		ch := text[i]
		// Track string boundaries to avoid counting braces inside strings
		if ch == '"' && (i == 0 || text[i-1] != '\\') {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch ch {
		case '}':
			depth++
		case '{':
			depth--
			if depth == 0 {
				return strings.TrimRight(text[:i], " \n\r\t")
			}
		}
	}
	return text
}

// styleBuilderSystemPrompt is the system prompt for the AI style builder.
const styleBuilderSystemPrompt = `你是一个专业的写作风格配置助手。你的任务是通过多轮对话帮助用户创建自定义写作风格（Skill）。

## 核心能力
1. **对话式风格创建**：通过对话了解用户需求，生成完整的风格配置
2. **知识库管理**：当用户上传参考文章时，自动创建知识库并导入文档
3. **风格绑定**：将生成的风格自动绑定到知识库，实现定向检索

## 工作流程

### 场景一：用户描述需求（无文件上传）
1. 用户描述他们想要的写作风格
2. 你根据描述提出 1-2 个追问，了解关键细节：
   - 写作类型（评论/种草/学术/申论/其他）
   - 目标字数范围
   - 结构偏好（三段式/自由式/其他）
   - 修辞要求（是否需要排比/比喻/设问）
   - 语言风格（正式/口语化/文学性）
   - 标题风格偏好
3. 当信息充分后，输出完整的风格配置 JSON

### 场景二：用户上传参考文章 / skill 文件
当用户上传文件时，文件内容会附加在用户消息末尾。你应该：
1. **分析文件内容**：阅读用户上传的文章/文档，提取风格特征
   - 文章结构（开头-正文-结尾的模式）
   - 语言风格（正式/口语化/文学性/技术性）
   - 修辞手法（比喻/排比/设问/数据引用等）
   - 字数范围和标题风格
   - 价值观取向和情感基调
2. **创建知识库**：调用 create_knowledge_base 工具创建一个知识库
3. **导入文档**：调用 import_document 工具将上传的每篇文章导入知识库
4. **生成风格配置**：基于分析结果生成 StyleProfile JSON，kb_id 字段会自动绑定
5. 简要说明你的分析和配置思路，然后输出 JSON

### 场景三：用户上传 skill.md（已有的风格描述文件）
1. 阅读 skill.md 内容，理解其中描述的风格规范
2. 如果包含参考范文，创建知识库并导入
3. 将描述转化为系统兼容的 StyleProfile JSON 格式

## 输出规则
- 在对话阶段，正常回复文字（不包含 JSON）
- 调用工具时，工具会返回执行结果，你继续对话
- 当信息充分时，先简短说明"风格已配置完成"及关键决策，然后直接输出一个 JSON 对象
- **禁止使用 markdown 代码块（三反引号）包裹 JSON**，直接输出裸 JSON 即可
- JSON 必须包含以下所有字段：

{
  "slug": "英文slug（仅小写字母数字下划线）",
  "name": "风格名称",
  "description": "一句话描述",
  "version": 1,
  "tags": ["标签1", "标签2"],
  "word_range": {"min": 800, "max": 1500, "hard_limit": true},
  "structure": {
    "type": "three_part / free_form / custom（自定义结构类型）",
    "opening": "开头部分描述（可为任意段落角色，不限于三段式）",
    "body": "主体部分描述",
    "conclusion": "结尾部分描述（可为任意段落角色，不限于三段式）",
    "argument_pattern": "论证模式（可选）",
    "argument_count": {"min": 2, "max": 3}
  },
  "rhetoric": {
    "required_metaphor": false,
    "required_parallelism": false,
    "required_rhetorical_question": false,
    "metaphor_description": ""
  },
  "value_orientation": {
    "type": "custom",
    "emotional_gradient": "情感走向描述",
    "keywords": ["关键词1", "关键词2"]
  },
  "title_guidelines": {
    "length": {"min": 5, "max": 25},
    "style": "标题风格描述",
    "forbidden_patterns": [],
    "examples": ["示例标题"]
  },
  "system_prompt": "完整的系统提示词，定义写作角色和规则",
  "writing_standard": "写作标准简述",
  "fact_guard": {
    "future_tense_required": [],
    "forbidden_results": [],
    "user_material_priority": false
  },
  "output_format": {
    "use_markdown": true,
    "title_prefix": "## ",
    "separator": "",
    "include_modification_notes": false,
    "note_label": ""
  },
  "length_profiles": {
    "writing": {"min": 800, "max": 1500, "hard_limit": true}
  }
}

## 重要
- slug 必须是英文，仅小写字母和下划线
- system_prompt 是最关键字段，需详细定义写作角色、语言风格、结构要求和输出格式
- 不要在 JSON 外输出多余内容
- **绝对不要用 markdown 代码块（三反引号）包裹 JSON 输出**
- 当用户上传文件时，优先使用工具创建知识库和导入文档，再输出最终配置`
