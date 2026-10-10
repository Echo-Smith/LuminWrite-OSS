package server

// 写作路径单入口（runtime-agility M2）：POST /api/v2/writing/launch。
//
// 把前端此前要串的七步（documents → writing-contract-draft → contracts →
// confirm → plans → runs → 视需要 approve）收成服务端一次编排。语义字段
// 全部服务端定，前端只透传用户选择：
//   - 预算信封 writingLaunchBudget（compile 要求 run budget ≥ Σ 节点 bound，
//     普通流四节点 × $5/600s 故取 $20/40min——与前端原 DEFAULT_BUDGET 同值，
//     就此收归服务端单源）；
//   - 初始 artifact 取 writingFlowDisplayTerms[flow].initialArtifactTypes
//     （M1 词表单源的同一张表）；
//   - required_final_artifact 固定 revision_set。
//
// 幂等：单个 Idempotency-Key（拒绝冒号的规则在 header 校验层）派生
// "-document"/"-run"/"-approve" 子键，整个 launch 原样重放安全。
// research_review 在 DraftWritingContract 处被拒（INVALID_WRITING_SPEC
// 指名 research-contract-draft 链路），单入口自然继承该拒绝。
//
// 编排本体 runWritingLaunch 只依赖 writingLaunchCore 窄接口——用脚本化
// fake 即可单测步骤顺序/预算/自动批准，不拖真库。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// writingLaunchBudget 是单入口的运行预算信封（服务端单源，前端不再计算）。
var writingLaunchBudget = writingplan.PlanBudget{
	MaxCostUSD:     20,
	MaxDurationMS:  2_400_000,
	MaxConcurrency: 1,
	MaxNodes:       10,
	MaxItems:       4,
}

const writingLaunchFinalArtifact writingplan.ArtifactType = "revision_set"

type writingLaunchCommand struct {
	// IdempotencyKey comes from the header, never from the body.
	IdempotencyKey string `json:"-"`
	// Message is the composer prompt（必填，其余全部可选）。
	Message        string `json:"message"`
	Title          string `json:"title,omitempty"`
	MaterialRefs   []any  `json:"material_refs,omitempty"`
	Flow           string `json:"flow,omitempty"`
	Style          string `json:"style,omitempty"`
	Mode           string `json:"mode,omitempty"`
	AssuranceLevel string `json:"assurance_level,omitempty"`
	ApprovalMode   string `json:"approval_mode,omitempty"`
	Language       string `json:"language,omitempty"`
}

type writingLaunchView struct {
	DocumentID      string `json:"document_id"`
	BaseVersionID   string `json:"base_version_id,omitempty"`
	ContractID      string `json:"contract_id"`
	ContractVersion int    `json:"contract_version"`
	ContractHash    string `json:"contract_hash"`
	PlanID          string `json:"plan_id"`
	PlanHash        string `json:"plan_hash"`
	RunID           string `json:"run_id"`
	RunStatus       string `json:"run_status"`
	Approved        bool   `json:"approved"`
}

// writingLaunchCore 是编排消费的最小服务面（persistentWritingAPI 天然实现）。
type writingLaunchCore interface {
	CreateDocument(ctx context.Context, access writingAccess, command createWritingDocumentCommand) (writingstore.DocumentRecord, error)
	DraftWritingContract(ctx context.Context, access writingAccess, command writingContractDraftCommand) (writingContractDraftView, error)
	PutContract(ctx context.Context, access writingAccess, command putWritingContractCommand) (writingstore.ContractRecord, error)
	ConfirmContract(ctx context.Context, access writingAccess, command confirmWritingContractCommand) (writingstore.ContractRecord, error)
	CompilePlan(ctx context.Context, access writingAccess, command compileWritingPlanCommand) (writingPlanPreview, error)
	CreateRun(ctx context.Context, access writingAccess, command createWritingRunCommand) (writingstore.RuntimeRun, error)
	ApproveRun(ctx context.Context, access writingAccess, command approveWritingRunCommand) (writingstore.RuntimeRun, error)
}

// LaunchWritingRun 把编排委托给 runWritingLaunch（persistentWritingAPI 实现
// writingLaunchCore 的全部方法）。
func (service *persistentWritingAPI) LaunchWritingRun(ctx context.Context, access writingAccess, command writingLaunchCommand) (writingLaunchView, error) {
	return runWritingLaunch(ctx, service, access, command)
}

// runWritingLaunch 一次编排七步启动链。每一步的错误都带步骤前缀，
// writeWritingError 的错误码映射（INVALID_WRITING_SPEC 等）不受影响。
func runWritingLaunch(ctx context.Context, core writingLaunchCore, access writingAccess, command writingLaunchCommand) (writingLaunchView, error) {
	key := strings.TrimSpace(command.IdempotencyKey)
	if key == "" {
		return writingLaunchView{}, errWritingIdempotencyKeyRequired
	}
	message := strings.TrimSpace(command.Message)
	if message == "" {
		return writingLaunchView{}, fmt.Errorf("%w: message must not be blank", errInvalidWritingSpec)
	}
	// flow 缺省 long_form（与旧前端 payload.flow ?? "long_form" 等价；
	// 非法值仍由封存层拒绝）
	if strings.TrimSpace(command.Flow) == "" {
		command.Flow = "long_form"
	}

	// 1. 文档（标题缺省取 prompt 前 60 rune，与原前端行为一致）
	title := strings.TrimSpace(command.Title)
	if title == "" {
		runes := []rune(message)
		if len(runes) > 60 {
			runes = runes[:60]
		}
		title = string(runes)
	}
	if title == "" {
		title = "新写作"
	}
	document, err := core.CreateDocument(ctx, access, createWritingDocumentCommand{
		IdempotencyKey: key + "-document",
		Title:          title,
		Metadata:       map[string]any{"material_refs": command.MaterialRefs},
	})
	if err != nil {
		return writingLaunchView{}, fmt.Errorf("launch: document: %w", err)
	}

	// 2. 服务端封存（flow → 合同对 + intent plan；research_review 在此拒绝）
	draft, err := core.DraftWritingContract(ctx, access, writingContractDraftCommand{
		DocumentID:     document.DocumentID,
		Message:        message,
		Style:          command.Style,
		Mode:           command.Mode,
		Flow:           command.Flow,
		AssuranceLevel: command.AssuranceLevel,
		ApprovalMode:   command.ApprovalMode,
		Language:       command.Language,
	})
	if err != nil {
		return writingLaunchView{}, fmt.Errorf("launch: contract draft: %w", err)
	}

	// 3+4. 合同落库 v1 → 确认 v2（封存版本原样转发，与旧六步链逐字节一致）
	draftRecord, err := core.PutContract(ctx, access, putWritingContractCommand{
		DocumentID: document.DocumentID,
		Contract:   draft.Contract,
	})
	if err != nil {
		return writingLaunchView{}, fmt.Errorf("launch: put contract: %w", err)
	}
	confirmedRecord, err := core.ConfirmContract(ctx, access, confirmWritingContractCommand{
		ContractID:      draftRecord.Contract.ContractID,
		PreviousVersion: draftRecord.Contract.Version,
		Contract:        draft.ConfirmedContract,
	})
	if err != nil {
		return writingLaunchView{}, fmt.Errorf("launch: confirm contract: %w", err)
	}
	sealed := confirmedRecord.Contract

	// 5. 编译计划：初始 artifact 按词表单源的流程映射
	terms := writingFlowDisplayTerms[command.Flow]
	initial := make([]writingplan.ArtifactType, 0, len(terms.initialArtifactTypes))
	for _, artifactType := range terms.initialArtifactTypes {
		initial = append(initial, writingplan.ArtifactType(artifactType))
	}
	preview, err := core.CompilePlan(ctx, access, compileWritingPlanCommand{
		DocumentID:            document.DocumentID,
		ContractID:            sealed.ContractID,
		ContractVersion:       sealed.Version,
		BaseVersionID:         document.CurrentVersionID,
		IntentPlan:            draft.IntentPlan,
		Budget:                writingLaunchBudget,
		InitialArtifactTypes:  initial,
		RequiredFinalArtifact: writingLaunchFinalArtifact,
	})
	if err != nil {
		return writingLaunchView{}, fmt.Errorf("launch: compile plan: %w", err)
	}

	// 6. 创建运行
	styleSlug := strings.TrimSpace(command.Style)
	if styleSlug == "" {
		styleSlug = "default"
	}
	run, err := core.CreateRun(ctx, access, createWritingRunCommand{
		IdempotencyKey:  key + "-run",
		DocumentID:      document.DocumentID,
		ContractID:      sealed.ContractID,
		ContractVersion: sealed.Version,
		ContractHash:    sealed.ContractHash,
		BaseVersionID:   document.CurrentVersionID,
		StyleSlug:       styleSlug,
		Plan:            preview.Envelope,
		Budget:          writingLaunchBudget,
		Permissions:     preview.Permissions,
	})
	if err != nil {
		return writingLaunchView{}, fmt.Errorf("launch: run: %w", err)
	}

	// 7. 需要审批时自动批准（与原前端行为一致：awaiting_approval 即 approve；
	//    plan_version 与前端原值同为 1，ExecutablePlan 无版本字段）
	approved := false
	if run.Status == "awaiting_approval" {
		run, err = core.ApproveRun(ctx, access, approveWritingRunCommand{
			IdempotencyKey: key + "-approve",
			RunID:          run.RunID,
			PlanID:         preview.Envelope.ExecutablePlan.PlanID,
			PlanVersion:    1,
			PlanHash:       preview.Envelope.ExecutablePlan.PlanHash,
			Permissions:    preview.Permissions,
		})
		if err != nil {
			return writingLaunchView{}, fmt.Errorf("launch: approve: %w", err)
		}
		approved = true
	}

	return writingLaunchView{
		DocumentID:      document.DocumentID,
		BaseVersionID:   document.CurrentVersionID,
		ContractID:      sealed.ContractID,
		ContractVersion: sealed.Version,
		ContractHash:    sealed.ContractHash,
		PlanID:          preview.Envelope.ExecutablePlan.PlanID,
		PlanHash:        preview.Envelope.ExecutablePlan.PlanHash,
		RunID:           run.RunID,
		RunStatus:       run.Status,
		Approved:        approved,
	}, nil
}

// writingLaunchService 是单入口的服务切片，按 writingDraftAPIOf 同款
// 惰性解析——部分 fake 实现保持可编译。
type writingLaunchService interface {
	LaunchWritingRun(ctx context.Context, access writingAccess, command writingLaunchCommand) (writingLaunchView, error)
}

func writingLaunchAPIOf(service writingAPIService) (writingLaunchService, error) {
	if launch, ok := service.(writingLaunchService); ok {
		return launch, nil
	}
	return nil, errors.New("writing launch unavailable")
}

// handleCreateWritingLaunch serves POST /api/v2/writing/launch.
func (s *Server) handleCreateWritingLaunch(w http.ResponseWriter, r *http.Request) {
	access, err := writingAccessFromRequest(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	launch, err := writingLaunchAPIOf(s.writingAPI)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	key, err := writingIdempotencyKey(r)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	var body writingLaunchCommand
	if err := decodeWritingJSON(w, r, &body); err != nil {
		s.writeWritingError(w, err)
		return
	}
	body.IdempotencyKey = key
	view, err := launch.LaunchWritingRun(r.Context(), access, body)
	if err != nil {
		s.writeWritingError(w, err)
		return
	}
	response.Created(w, view)
}

// 编译期锚：persistentWritingAPI 必须持续满足编排窄接口。
var _ writingLaunchCore = (*persistentWritingAPI)(nil)
