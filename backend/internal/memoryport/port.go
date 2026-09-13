// Package memoryport 是记忆消费契约（anti-corruption layer）。
//
// 执行引擎（internal/engine、internal/agent、未来的 editorial DAG）只允许
// 通过本包消费长期记忆；pkg/memory SDK 的类型与策略不得泄漏到消费侧，
// 唯一例外是 internal/memoryport/adapter（跨界适配器）。
//
// 演进纪律（docs/plans/2026-09-12-memory-injection-port-harness-pipeline.md §4.2）：
//   - 契约只做加性演进（加字段/加槽位），不删改既有字段；
//   - 破坏性变更必须 bump ContractVersion 并双实现并行，不允许全库同步改；
//   - 记忆内部概念（tier/candidate/decay/gate）不出现在契约语义中，
//     Directive.Strength 仅用于消费端预算截断排序。
package memoryport

import "context"

// ContractVersion 契约版本。消费方按已知版本渲染，未知字段忽略。
const ContractVersion = 1

// DirectiveKind 指令的消费角色（决定渲染进哪个槽位）。
type DirectiveKind string

const (
	// KindPreference 写作偏好，渲染进写作/对话 prompt（原 Tier1+2 通道）。
	KindPreference DirectiveKind = "preference"
	// KindFeedback 反馈记忆，渲染进审校标准（原 Tier3 通道）。
	KindFeedback DirectiveKind = "feedback"
	// KindExplicit 用户显式指令（remember 工具），作为硬偏好落库。
	KindExplicit DirectiveKind = "explicit"
)

// Directive 是一条渲染就绪的记忆指令。
//
// JSON 字段名与 pkg/memory.MemoryEntry 对齐（WS memory.used 事件 payload
// 结构保持兼容）；kind/strength 为加性字段（omitempty，缺省时 payload
// 与旧事件逐字节一致）。消费方不得解析 ID/Strength 之外的语义。
type Directive struct {
	ID             string        `json:"id"`
	Tier           string        `json:"tier"`
	Category       string        `json:"category"`
	Value          string        `json:"value"`
	Confidence     float64       `json:"confidence"`
	Dismissible    bool          `json:"dismissible"`
	EvidenceStatus string        `json:"evidence_status,omitempty"`
	Kind           DirectiveKind `json:"kind,omitempty"`
	Strength       float64       `json:"strength,omitempty"`
}

// Bundle 是门控后、按消费者分槽的注入载体。
type Bundle struct {
	Version int `json:"version"`
	// WriteDirectives 渲染进写作/对话 prompt 的偏好指令。
	WriteDirectives []Directive `json:"write_directives,omitempty"`
	// ReviewGuard 渲染进审校标准的反馈指令。
	ReviewGuard   []Directive `json:"review_guard,omitempty"`
	// EntityProfile 实体画像网络的渲染文本（可选能力，见 Capabilities）。
	EntityProfile string `json:"entity_profile,omitempty"`
	// RefusalReason 非空表示记忆侧主动拒注入（证据不足等），消费方可提示。
	RefusalReason string `json:"refusal_reason,omitempty"`
	// Dismissed 本次被用户关闭的记忆 ID（遥测与事件载荷用）。
	Dismissed []string `json:"dismissed,omitempty"`
}

// IsEmpty 报告 bundle 是否没有任何可注入内容。
func (b *Bundle) IsEmpty() bool {
	return b == nil || (len(b.WriteDirectives) == 0 && len(b.ReviewGuard) == 0 && b.EntityProfile == "")
}

// Request 一次注入预取请求。Explicit 是用户本轮显式指定的维度
// （word_limit/style/mode/message 等），门控会据此让位。
type Request struct {
	UserID         string
	ConversationID string
	Query          string
	Intent         string
	Explicit       map[string]any
	SessionID      string
	// TraceID / Source（P3 遥测，加性字段）：pipeline | harness | tool。
	TraceID string
	Source  string
	// WithEntityGraph 请求同时检索实体画像（仅当 Capabilities.EntityGraph 时生效）。
	WithEntityGraph bool
}

// QueryRequest 按需检索请求（retrieve_context 类工具的后端）。
type QueryRequest struct {
	UserID    string
	Query     string
	Intent    string
	SessionID string
	// TraceID / Source（P3 遥测，加性字段）。
	TraceID string
	Source  string
	// ExcludeIDs 是本轮已注入的 Directive ID，结果会去重避免与 prompt 打架。
	ExcludeIDs []string
}

// OutcomeKind 写回类型。
type OutcomeKind string

const (
	// OutcomeWrite 一次写作/修订完成，携带提取输入。
	OutcomeWrite OutcomeKind = "write"
	// OutcomeExplicit 用户显式要求记住（remember 工具）。
	OutcomeExplicit OutcomeKind = "explicit"
)

// Message 提取输入中的一条对话消息。
type Message struct {
	Role    string
	Content string
}

// Feedback 提取输入中的一条用户反馈。
type Feedback struct {
	SegmentType string // title | paragraph | overall
	Rating      int    // 1-5
	Comment     string
}

// Outcome 是消费方提交的写回输入。浓信号字段（BeforeRevision/Transcript）
// 为契约 v1 预留：适配器暂可忽略，提取增强（P2）启用后生效——
// 这是加性演进的示范位。
type Outcome struct {
	Kind   OutcomeKind
	UserID string
	TraceID string

	// OutcomeWrite 通道
	Article   string
	StyleSlug string
	Mode      string
	WordLimit int

	// 浓信号（P2 启用）
	BeforeRevision string
	Transcript     []Message
	Feedbacks      []Feedback

	// OutcomeExplicit 通道
	ExplicitValue string
}

// Capabilities 能力协商：消费方据此决定渲染哪些槽位/注册哪些工具。
type Capabilities struct {
	EntityGraph bool
	// ExplicitCapture 表示 remember 后端可用（含 PII 检查）。
	ExplicitCapture bool
}

// Port 是记忆消费的三动词契约。
// 实现保证 SubmitOutcome 非阻塞（内部异步），失败只记日志不拖垮写作流程。
type Port interface {
	// EnabledForUser 报告该用户是否启用记忆（灰度门控 + 服务可用性）。
	EnabledForUser(userID string) bool
	// PrepareInjection 会话/请求开始时的批量预取 + 门控。
	// 返回 nil bundle（无记忆）不算错误。
	PrepareInjection(ctx context.Context, req Request) (*Bundle, error)
	// QueryOnDemand 按需检索。必须真检索真门控，不允许查内存副本；
	// 结果按 ExcludeIDs 去重后返回增量。
	QueryOnDemand(ctx context.Context, req QueryRequest) ([]Directive, error)
	// SubmitOutcome 请求/会话结束后的写回入口。
	SubmitOutcome(ctx context.Context, out Outcome) error
	// Capabilities 能力协商。
	Capabilities() Capabilities
}
