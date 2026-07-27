package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
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
}

// StyleBuilderResponse is returned after each message.
type StyleBuilderResponse struct {
	Message string                `json:"message"`             // AI's reply text
	Ready   bool                  `json:"ready"`               // true when profile is complete
	Profile *profile.StyleProfile `json:"profile,omitempty"`   // present when ready=true
}

// ─── Service ─────────────────────────────────────────────

// StyleBuilderService manages AI-assisted style creation sessions.
type StyleBuilderService struct {
	llm      *tools.LLMClient
	sessions map[string]*StyleBuilderSession
	mu       sync.Mutex
}

// NewStyleBuilderService creates a new StyleBuilderService.
func NewStyleBuilderService(llm *tools.LLMClient) *StyleBuilderService {
	return &StyleBuilderService{
		llm:      llm,
		sessions: make(map[string]*StyleBuilderSession),
	}
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
// The AI decides whether to ask more questions or generate the final config.
func (s *StyleBuilderService) SendMessage(ctx context.Context, sessionID, userMessage string) (*StyleBuilderResponse, error) {
	s.mu.Lock()
	session, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("session not found")
	}

	// Append user message
	session.Messages = append(session.Messages, StyleBuilderMessage{
		Role:    "user",
		Content: userMessage,
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

	// Call LLM
	reply, _, err := s.llm.Chat(ctx, llmMessages, tools.WithTemperature(0.7))
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
			reply = stripTrailingJSON(reply)
		}
	}

	// Append assistant message
	session.Messages = append(session.Messages, StyleBuilderMessage{
		Role:    "assistant",
		Content: reply,
	})

	if ready {
		session.Profile = styleProfile
		session.Ready = true
	}

	return &StyleBuilderResponse{
		Message: reply,
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

// ─── Helpers ─────────────────────────────────────────────

// stripTrailingJSON removes the last balanced JSON object from text,
// keeping only the conversational prefix. Returns the original text
// if no balanced JSON block is found.
func stripTrailingJSON(text string) string {
	depth := 0
	for i := len(text) - 1; i >= 0; i-- {
		switch text[i] {
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
const styleBuilderSystemPrompt = `你是一个专业的写作风格配置助手。你的任务是通过多轮对话帮助用户创建自定义写作风格。

## 工作流程
1. 用户描述他们想要的写作风格
2. 你根据描述提出 1-2 个追问，了解关键细节：
   - 写作类型（评论/种草/学术/申论/其他）
   - 目标字数范围
   - 结构偏好（三段式/自由式/其他）
   - 修辞要求（是否需要排比/比喻/设问）
   - 语言风格（正式/口语化/文学性）
   - 标题风格偏好
3. 当你认为信息充分后，直接输出完整的风格配置 JSON

## 输出规则
- 在对话阶段，正常回复文字（不包含 JSON）
- 当信息充分时，先简短说明"风格已配置完成"，然后输出一个 JSON 对象
- JSON 必须包含以下所有字段：

{
  "slug": "英文slug（仅小写字母数字下划线）",
  "name": "风格名称",
  "description": "一句话描述",
  "version": 1,
  "tags": ["标签1", "标签2"],
  "word_range": {"min": 800, "max": 1500, "hard_limit": true},
  "structure": {
    "type": "three_part 或 free_form",
    "opening": "开头部分描述",
    "body": "主体部分描述",
    "conclusion": "结尾部分描述",
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
- 不要在 JSON 外输出多余内容`
