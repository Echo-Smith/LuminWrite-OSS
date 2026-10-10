package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// 单入口编排单测：脚本化 fake 实现writingLaunchCore，验证七步顺序、
// 预算/初始 artifact 服务端单源、自动批准与错误中断。持久层的正确性由
// 各 service/store 自己的测试负责——这里只测编排。

type launchCall struct {
	step string
	key  string
}

type fakeLaunchCore struct {
	calls    []launchCall
	runStatus string // CreateRun 返回的状态
	approveErr error
}

func (f *fakeLaunchCore) CreateDocument(ctx context.Context, access writingAccess, command createWritingDocumentCommand) (writingstore.DocumentRecord, error) {
	f.calls = append(f.calls, launchCall{step: "document", key: command.IdempotencyKey})
	return writingstore.DocumentRecord{DocumentID: "doc_l1", CurrentVersionID: "ver_l1"}, nil
}

func (f *fakeLaunchCore) DraftWritingContract(ctx context.Context, access writingAccess, command writingContractDraftCommand) (writingContractDraftView, error) {
	f.calls = append(f.calls, launchCall{step: "draft:" + command.Flow})
	// 封存核心是纯函数：真实校验 + 真实密封（时钟冻结即可）
	return buildWritingContractDrafts(command, time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC))
}

func (f *fakeLaunchCore) PutContract(ctx context.Context, access writingAccess, command putWritingContractCommand) (writingstore.ContractRecord, error) {
	f.calls = append(f.calls, launchCall{step: "put"})
	return writingstore.ContractRecord{DocumentID: command.DocumentID, Contract: command.Contract}, nil
}

func (f *fakeLaunchCore) ConfirmContract(ctx context.Context, access writingAccess, command confirmWritingContractCommand) (writingstore.ContractRecord, error) {
	f.calls = append(f.calls, launchCall{step: "confirm", key: command.ContractID})
	confirmed := command.Contract
	confirmed.Status = writingkernel.ContractStatusConfirmed
	return writingstore.ContractRecord{DocumentID: "doc_l1", Contract: confirmed}, nil
}

func (f *fakeLaunchCore) CompilePlan(ctx context.Context, access writingAccess, command compileWritingPlanCommand) (writingPlanPreview, error) {
	f.calls = append(f.calls, launchCall{step: "compile"})
	envelope := writingplan.WritingPlanEnvelope{SchemaVersion: writingplan.SchemaVersion}
	envelope.ExecutablePlan.PlanID = "plan_l1"
	envelope.ExecutablePlan.PlanHash = "sha256:" + strings.Repeat("11", 32)
	return writingPlanPreview{Envelope: envelope, Budget: command.Budget, Permissions: []writingplan.Permission{"model.invoke"}}, nil
}

func (f *fakeLaunchCore) CreateRun(ctx context.Context, access writingAccess, command createWritingRunCommand) (writingstore.RuntimeRun, error) {
	f.calls = append(f.calls, launchCall{step: "run", key: command.IdempotencyKey})
	return writingstore.RuntimeRun{RunID: "run_l1", Status: f.runStatus, DocumentID: command.DocumentID}, nil
}

func (f *fakeLaunchCore) ApproveRun(ctx context.Context, access writingAccess, command approveWritingRunCommand) (writingstore.RuntimeRun, error) {
	f.calls = append(f.calls, launchCall{step: "approve", key: command.IdempotencyKey})
	if f.approveErr != nil {
		return writingstore.RuntimeRun{}, f.approveErr
	}
	return writingstore.RuntimeRun{RunID: command.RunID, Status: "planned"}, nil
}

func TestWritingLaunchHappyPathAutoApproves(t *testing.T) {
	core := &fakeLaunchCore{runStatus: "awaiting_approval"}
	view, err := runWritingLaunch(context.Background(), core, writingAccess{UserID: "u1"}, writingLaunchCommand{
		IdempotencyKey: "idem-1",
		Message:        "综合这批材料写一篇分析",
		Flow:           "multi_material",
		Style:          "yinyue",
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}

	// 七步顺序：document → draft → put → confirm → compile → run → approve
	wantSteps := []string{"document", "draft:multi_material", "put", "confirm", "compile", "run", "approve"}
	if len(core.calls) != len(wantSteps) {
		t.Fatalf("steps = %v, want %v", launchSteps(core.calls), wantSteps)
	}
	for i, want := range wantSteps {
		if core.calls[i].step != want {
			t.Fatalf("steps[%d] = %q, want %q", i, core.calls[i].step, want)
		}
	}

	// 幂等子键派生
	if core.calls[0].key != "idem-1-document" || core.calls[5].key != "idem-1-run" || core.calls[6].key != "idem-1-approve" {
		t.Fatalf("幂等子键不正确: %v", []string{core.calls[0].key, core.calls[5].key, core.calls[6].key})
	}

	// 视图：合同 v2（确认后）、计划身份、approved 标记
	if view.ContractVersion != 2 || view.ContractID == "" || view.PlanID != "plan_l1" {
		t.Fatalf("view = %+v", view)
	}
	if view.RunID != "run_l1" || view.RunStatus != "planned" || !view.Approved {
		t.Fatalf("run 视图 = %+v", view)
	}
	if view.DocumentID != "doc_l1" || view.BaseVersionID != "ver_l1" {
		t.Fatalf("document 视图 = %+v", view)
	}
}

func TestWritingLaunchPlannedSkipsApprove(t *testing.T) {
	core := &fakeLaunchCore{runStatus: "planned"}
	view, err := runWritingLaunch(context.Background(), core, writingAccess{UserID: "u1"}, writingLaunchCommand{
		IdempotencyKey: "idem-2",
		Message:        "写一篇行业分析",
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if view.Approved || view.RunStatus != "planned" {
		t.Fatalf("planned 不应批准: %+v", view)
	}
	if launchSteps(core.calls)[len(core.calls)-1] == "approve" {
		t.Fatal("planned 不应有 approve 步")
	}
}

// 语义字段服务端单源：预算信封、初始 artifact（词表单源表）、final artifact
// 必须由编排层填，而不是客户端透传。
func TestWritingLaunchServerOwnedSemantics(t *testing.T) {
	core := &fakeLaunchCore{runStatus: "planned"}
	var compiled compileWritingPlanCommand
	var created createWritingRunCommand
	// 用包装 fake 捕获命令
	wrapped := &capturingLaunchCore{core: core}
	wrapped.onCompile = func(c compileWritingPlanCommand) { compiled = c }
	wrapped.onCreateRun = func(c createWritingRunCommand) { created = c }
	if _, err := runWritingLaunch(context.Background(), wrapped, writingAccess{UserID: "u1"}, writingLaunchCommand{
		IdempotencyKey: "idem-3",
		Message:        "写一篇",
		Flow:           "long_form",
	}); err != nil {
		t.Fatalf("launch: %v", err)
	}

	if compiled.Budget != writingLaunchBudget {
		t.Fatalf("预算必须来自服务端 writingLaunchBudget: %+v", compiled.Budget)
	}
	if len(compiled.InitialArtifactTypes) != 1 || compiled.InitialArtifactTypes[0] != "contract" {
		t.Fatalf("long_form 初始 artifact 应为 [contract]: %v", compiled.InitialArtifactTypes)
	}
	if compiled.RequiredFinalArtifact != writingLaunchFinalArtifact {
		t.Fatalf("final artifact 应为 revision_set: %q", compiled.RequiredFinalArtifact)
	}
	if created.Budget != writingLaunchBudget {
		t.Fatalf("run 预算必须来自服务端: %+v", created.Budget)
	}
	// style 缺省 default
	if created.StyleSlug != "default" {
		t.Fatalf("style_slug 缺省 default: %q", created.StyleSlug)
	}
}

func TestWritingLaunchBlankMessageRejectsBeforeAnyStep(t *testing.T) {
	core := &fakeLaunchCore{runStatus: "planned"}
	_, err := runWritingLaunch(context.Background(), core, writingAccess{UserID: "u1"}, writingLaunchCommand{
		IdempotencyKey: "idem-4",
		Message:        "   ",
	})
	if !errors.Is(err, errInvalidWritingSpec) {
		t.Fatalf("want errInvalidWritingSpec, got %v", err)
	}
	if len(core.calls) != 0 {
		t.Fatalf("拒绝后不得有任何服务调用: %v", launchSteps(core.calls))
	}
}

func TestWritingLaunchResearchReviewRefusedAtDraft(t *testing.T) {
	core := &fakeLaunchCore{runStatus: "planned"}
	_, err := runWritingLaunch(context.Background(), core, writingAccess{UserID: "u1"}, writingLaunchCommand{
		IdempotencyKey: "idem-5",
		Message:        "写一篇综述",
		Flow:           "research_review",
	})
	if err == nil {
		t.Fatal("research_review 必须被拒（走 research-contract-draft 链路）")
	}
	if !strings.Contains(err.Error(), "research-contract-draft") {
		t.Fatalf("拒绝信息应指名研究链路: %v", err)
	}
	steps := launchSteps(core.calls)
	// 拒绝发生在 draft 步骤内部（与旧前端一致：文档已建、封存 400，
	// 后续 put/confirm/compile/run 不得发生）
	if len(steps) != 2 || steps[0] != "document" || steps[1] != "draft:research_review" {
		t.Fatalf("研究流拒绝应停在封存步: %v", steps)
	}
}

func TestWritingLaunchApproveFailureWrapsStep(t *testing.T) {
	core := &fakeLaunchCore{runStatus: "awaiting_approval", approveErr: errors.New("gate closed")}
	_, err := runWritingLaunch(context.Background(), core, writingAccess{UserID: "u1"}, writingLaunchCommand{
		IdempotencyKey: "idem-6",
		Message:        "写一篇",
	})
	if err == nil || !strings.Contains(err.Error(), "launch: approve:") {
		t.Fatalf("approve 失败应带步骤前缀: %v", err)
	}
}

// ─── helpers ────────────────────────────────────────────────────────────────

func launchSteps(calls []launchCall) []string {
	steps := make([]string, 0, len(calls))
	for _, call := range calls {
		steps = append(steps, call.step)
	}
	return steps
}

type capturingLaunchCore struct {
	core        writingLaunchCore
	onCompile   func(compileWritingPlanCommand)
	onCreateRun func(createWritingRunCommand)
}

func (f *capturingLaunchCore) CreateDocument(ctx context.Context, access writingAccess, command createWritingDocumentCommand) (writingstore.DocumentRecord, error) {
	return f.core.CreateDocument(ctx, access, command)
}
func (f *capturingLaunchCore) DraftWritingContract(ctx context.Context, access writingAccess, command writingContractDraftCommand) (writingContractDraftView, error) {
	return f.core.DraftWritingContract(ctx, access, command)
}
func (f *capturingLaunchCore) PutContract(ctx context.Context, access writingAccess, command putWritingContractCommand) (writingstore.ContractRecord, error) {
	return f.core.PutContract(ctx, access, command)
}
func (f *capturingLaunchCore) ConfirmContract(ctx context.Context, access writingAccess, command confirmWritingContractCommand) (writingstore.ContractRecord, error) {
	return f.core.ConfirmContract(ctx, access, command)
}
func (f *capturingLaunchCore) CompilePlan(ctx context.Context, access writingAccess, command compileWritingPlanCommand) (writingPlanPreview, error) {
	f.onCompile(command)
	return f.core.CompilePlan(ctx, access, command)
}
func (f *capturingLaunchCore) CreateRun(ctx context.Context, access writingAccess, command createWritingRunCommand) (writingstore.RuntimeRun, error) {
	f.onCreateRun(command)
	return f.core.CreateRun(ctx, access, command)
}
func (f *capturingLaunchCore) ApproveRun(ctx context.Context, access writingAccess, command approveWritingRunCommand) (writingstore.RuntimeRun, error) {
	return f.core.ApproveRun(ctx, access, command)
}
