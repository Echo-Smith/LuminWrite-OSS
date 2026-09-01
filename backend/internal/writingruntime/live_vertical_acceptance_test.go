package writingruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

const task13LiveModel = "deepseek-v4-flash"

type liveVerticalStep struct {
	client   *tools.LLMClient
	scenario string
}

func (step *liveVerticalStep) Name() engine.StepName {
	return engine.StepName("task13_live_" + step.scenario)
}
func (*liveVerticalStep) CanPause() bool { return false }

func (step *liveVerticalStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, _ engine.EventEmitter) error {
	contract, err := writingkernel.DecodeWritingContractStrict([]byte(execCtx.UserInput))
	if err != nil {
		return err
	}
	request := fmt.Sprintf("主题：%s\n核心问题：%s\n必须覆盖：%s\n请只输出中文 Markdown 正文。",
		contract.Content.Topic, contract.Content.CentralQuestion, strings.Join(contract.Content.RequiredPoints, "、"))
	system := "你是严谨的中文写作助手。必须尊重用户材料，不虚构来源，并清晰区分事实与判断。"
	switch step.scenario {
	case "long_form":
		request += "\n请写一篇结构完整、至少 1200 个中文字符的长文，包含背景、分析、治理边界和结论。"
	case "multi_material":
		materialBytes, _ := json.Marshal(execCtx.SearchResults)
		request += "\n请综合以下冻结材料，说明材料之间的一致点、差异与可追溯结论：\n" + string(materialBytes)
	case "faithful_rewrite":
		request += "\n请忠实改写以下冻结材料；不得删掉标题、事实、实体或限定条件：\n" + strings.Join(execCtx.UserMaterials, "\n")
	default:
		return fmt.Errorf("unsupported live vertical scenario %q", step.scenario)
	}
	text, _, err := step.client.Chat(ctx, []tools.LLMMessage{{Role: "system", Content: system}, {Role: "user", Content: request}})
	if err != nil {
		return fmt.Errorf("live model call failed: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("live model returned an empty response")
	}

	var builder strings.Builder
	builder.WriteString("# Task13 真实模型纵向验收\n")
	for _, point := range contract.Content.RequiredPoints {
		builder.WriteString("\n## " + point + "\n\n")
	}
	if step.scenario == "faithful_rewrite" {
		for _, materialPayload := range execCtx.UserMaterials {
			var manifest MaterialManifest
			if err := json.Unmarshal([]byte(materialPayload), &manifest); err != nil {
				return err
			}
			for _, material := range manifest.Materials {
				builder.WriteString("\n## " + material.Title + "\n\n" + material.Title + "\n")
			}
		}
	}
	builder.WriteString("\n" + strings.TrimSpace(text) + "\n")
	execCtx.Article = builder.String()
	return nil
}

type durableEvidenceMirror struct {
	persistent RolloutEvidenceStore
	memory     *MemoryRolloutEvidenceStore
}

func (store durableEvidenceMirror) Record(ctx context.Context, evidence RuntimeEvidence) error {
	if err := store.persistent.Record(ctx, evidence); err != nil {
		return err
	}
	return store.memory.Record(ctx, evidence)
}

type durableShadowTracker struct {
	persistent WritingStoreShadowContentSink
	mu         sync.Mutex
	keys       []string
}

func (sink *durableShadowTracker) Put(ctx context.Context, key, mediaType string, body []byte) error {
	if err := sink.persistent.Put(ctx, key, mediaType, body); err != nil {
		return err
	}
	sink.mu.Lock()
	sink.keys = append(sink.keys, key)
	sink.mu.Unlock()
	return nil
}

func (sink *durableShadowTracker) Get(ctx context.Context, key string) ([]byte, error) {
	return sink.persistent.Get(ctx, key)
}

func (sink *durableShadowTracker) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	return sink.persistent.DeletePrefix(ctx, prefix)
}

func (sink *durableShadowTracker) DeleteBefore(ctx context.Context, cutoff time.Time) (int, error) {
	return sink.persistent.DeleteBefore(ctx, cutoff)
}

func (sink *durableShadowTracker) Keys() []string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]string(nil), sink.keys...)
}

func TestTask13LiveModelVerticalAcceptance(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("TASK13_LLM_API_KEY"))
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("TASK13_LLM_BASE_URL")), "/")
	model := strings.TrimSpace(os.Getenv("TASK13_LLM_MODEL"))
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if apiKey == "" || baseURL == "" || model == "" || databaseURL == "" {
		t.Skip("Task13 live acceptance requires TASK13_LLM_API_KEY, TASK13_LLM_BASE_URL, TASK13_LLM_MODEL, and TEST_DATABASE_URL")
	}
	if model != task13LiveModel {
		t.Fatalf("TASK13_LLM_MODEL=%q; acceptance is pinned to %s", model, task13LiveModel)
	}
	db, err := database.NewPostgres(databaseURL, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `TRUNCATE writing_rollout_approvals, writing_documents CASCADE`); err != nil {
		t.Fatal(err)
	}
	persistent, err := writingstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	client := tools.NewLLMClient(baseURL, apiKey, model, 4096, .2, 150*time.Second)
	client.SetReasoningEffort("")

	for _, scenario := range []struct {
		name          string
		withMaterials bool
		nodes         func(*tools.LLMClient) []verticalNode
	}{
		{name: "long_form", nodes: liveLongFormNodes},
		{name: "multi_material", withMaterials: true, nodes: liveMultiMaterialNodes},
		{name: "faithful_rewrite", withMaterials: true, nodes: liveFaithfulRewriteNodes},
	} {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			testName := "live_" + scenario.name
			memory := &MemoryRolloutEvidenceStore{}
			tracker := &durableShadowTracker{persistent: WritingStoreShadowContentSink{Store: persistent, TTL: DefaultShadowContentTTL}}
			backend := verticalRolloutBackend{
				evidence: durableEvidenceMirror{persistent: WritingStoreEvidenceStore{Recorder: persistent}, memory: memory},
				sink:     tracker,
				prepare:  livePreparePersistentLineage(persistent, db),
				records: func(t *testing.T, runID string) []RuntimeEvidence {
					t.Helper()
					reopened, err := writingstore.New(db)
					if err != nil {
						t.Fatal(err)
					}
					return liveReadDurableEvidence(t, reopened, runID, apiKey)
				},
				keys: func(t *testing.T, _ string) []string {
					t.Helper()
					reopened, err := writingstore.New(db)
					if err != nil {
						t.Fatal(err)
					}
					keys := tracker.Keys()
					for _, key := range keys {
						body, err := reopened.GetShadowContent(context.Background(), key)
						if err != nil || len(body.Body) == 0 {
							t.Fatalf("durable shadow reload key=%s err=%v", key, err)
						}
					}
					return keys
				},
			}
			result := runVerticalScenarioWithBackend(t, testName, scenario.nodes(client), scenario.withMaterials, 150*time.Second, backend)
			if result.outcome.State != StateCompleted || len(result.shadowKeys(t)) == 0 {
				t.Fatalf("live scenario did not complete with durable shadow output: %#v", result.outcome)
			}
		})
	}
}

func livePreparePersistentLineage(store *writingstore.Store, db *database.DB) func(*testing.T, writingkernel.WritingContract, writingplan.WritingPlanEnvelope, []verticalNode, string, string) {
	return func(t *testing.T, contract writingkernel.WritingContract, envelope writingplan.WritingPlanEnvelope, _ []verticalNode, runID, documentID string) {
		t.Helper()
		ctx := context.Background()
		var ownerID string
		uid := "task13_" + strings.TrimPrefix(runID, "run_vertical_")
		if err := db.QueryRowContext(ctx, `
			INSERT INTO users (uid, name) VALUES ($1, 'Task13 live acceptance')
			ON CONFLICT (uid) DO UPDATE SET name=EXCLUDED.name RETURNING id::text
		`, uid).Scan(&ownerID); err != nil {
			t.Fatal(err)
		}
		trace := writingstore.TraceContext{Provenance: map[string]any{"suite": "task13_live_acceptance", "model": task13LiveModel},
			SourceRefs: []string{}, Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "task13-live-acceptance"}}
		if err := store.CreateDocument(ctx, writingstore.DocumentRecord{DocumentID: documentID, OwnerUserID: ownerID,
			Title: "Task13 live acceptance", Actor: trace.Actor}); err != nil {
			t.Fatal(err)
		}
		if err := store.PutContract(ctx, writingstore.ContractRecord{DocumentID: documentID, Contract: contract, Trace: trace}); err != nil {
			t.Fatal(err)
		}
		budget := writingplan.PlanBudget{MaxCostUSD: 40, MaxDurationMS: 600000, MaxConcurrency: 1,
			MaxNodes: len(envelope.ExecutablePlan.Nodes) + 2, MaxItems: 4}
		permissions := []writingplan.Permission{"model.invoke", "materials.read"}
		if err := store.CreateRunWithPlan(ctx, writingstore.RunRecord{RunID: runID, DocumentID: documentID,
			ContractID: contract.ContractID, ContractVersion: contract.Version, ContractHash: contract.ContractHash,
			ApprovalMode: writingkernel.ApprovalModeAuto, RequestedAssurance: writingkernel.AssuranceLevelStandard,
			Budget: budget, Permissions: permissions, Trace: trace}, writingstore.PlanRecord{RunID: runID, PlanVersion: 1,
			Envelope: envelope, Budget: budget, Permissions: permissions, ApprovalStatus: "not_required", Trace: trace}, "running"); err != nil {
			t.Fatal(err)
		}
		if err := store.InTransaction(ctx, func(tx *writingstore.Tx) error {
			for _, node := range envelope.ExecutablePlan.Nodes {
				if _, _, err := tx.EnsureNodeAttempt(ctx, writingstore.NodeAttempt{RunID: runID,
					PlanID: envelope.ExecutablePlan.PlanID, PlanVersion: 1, NodeID: node.NodeID, Attempt: 1,
					NodeKind: node.Kind, CapabilityID: node.Capability, CapabilityVersion: node.CapabilityVersion,
					ExecutorID:  "vertical." + strings.TrimPrefix(runID, "run_vertical_") + ".baseline.0",
					FailurePath: node.FailurePath, Bounds: node.Bounds, InputHash: hashForTest(runID + node.NodeID), InputArtifactIDs: []string{}}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func liveReadDurableEvidence(t *testing.T, store *writingstore.Store, runID, apiKey string) []RuntimeEvidence {
	t.Helper()
	events, err := store.ListRunEvents(context.Background(), runID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	records := make([]RuntimeEvidence, 0, len(events))
	for _, event := range events {
		if event.EntityKind != "rollout_evidence" {
			continue
		}
		payload, err := json.Marshal(event.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payload), apiKey) || strings.Contains(string(payload), "Authorization") {
			t.Fatal("credential material leaked into durable rollout evidence")
		}
		var record RuntimeEvidence
		if err := json.Unmarshal(payload, &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func liveLongFormNodes(client *tools.LLMClient) []verticalNode {
	return []verticalNode{
		{name: "outline", kind: writingplan.NodeAction, inputs: []writingplan.ArtifactType{"contract"}, output: "outline", runner: verticalEngineRunner(&verticalOutlineStep{})},
		{name: "draft", kind: writingplan.NodeAction, inputs: []writingplan.ArtifactType{"contract", "outline"}, output: "full_draft", runner: verticalEngineRunner(&liveVerticalStep{client: client, scenario: "long_form"})},
		{name: "quality", kind: writingplan.NodeValidate, inputs: []writingplan.ArtifactType{"contract", "outline", "full_draft"}, output: "quality_report", runner: verticalQualityNodeRunner(nil)},
	}
}

func liveMultiMaterialNodes(client *tools.LLMClient) []verticalNode {
	return []verticalNode{
		{name: "research", kind: writingplan.NodeAction, inputs: []writingplan.ArtifactType{"contract", "materials"}, output: "source_pack", runner: verticalEngineRunner(&verticalSourcePackStep{})},
		{name: "synthesis", kind: writingplan.NodeAction, inputs: []writingplan.ArtifactType{"contract", "source_pack"}, output: "full_draft", runner: verticalEngineRunner(&liveVerticalStep{client: client, scenario: "multi_material"})},
		{name: "quality", kind: writingplan.NodeValidate, inputs: []writingplan.ArtifactType{"contract", "full_draft"}, output: "quality_report", runner: verticalQualityNodeRunner(nil)},
	}
}

func liveFaithfulRewriteNodes(client *tools.LLMClient) []verticalNode {
	return []verticalNode{
		{name: "rewrite", kind: writingplan.NodeAction, inputs: []writingplan.ArtifactType{"contract", "materials"}, output: "full_draft", runner: verticalEngineRunner(&liveVerticalStep{client: client, scenario: "faithful_rewrite"})},
		{name: "semantic_quality", kind: writingplan.NodeValidate, inputs: []writingplan.ArtifactType{"contract", "materials", "full_draft"}, output: "quality_report", runner: verticalQualityNodeRunner(func(input LegacyNodeInput) *writingkernel.DocumentVersion {
			var manifest MaterialManifest
			if err := json.Unmarshal(input.Payloads["materials"][0], &manifest); err != nil {
				return nil
			}
			origin := writingkernel.Origin{Kind: writingkernel.OriginSystem, Ref: "writingruntime/task13-live"}
			root := &writingkernel.DocumentNode{BlockID: "blk_live_base_root", Type: writingkernel.NodeTypeDocument,
				Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{}, Origin: origin}
			for index, material := range manifest.Materials {
				text := &writingkernel.DocumentNode{BlockID: fmt.Sprintf("blk_live_base_text_%d", index), Type: writingkernel.NodeTypeText,
					Text: material.Title, Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{}, Origin: origin}
				paragraph := &writingkernel.DocumentNode{BlockID: fmt.Sprintf("blk_live_base_paragraph_%d", index), Type: writingkernel.NodeTypeParagraph,
					Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{text}, Origin: origin}
				root.Children = append(root.Children, &writingkernel.DocumentNode{BlockID: fmt.Sprintf("blk_live_base_section_%d", index),
					Type: writingkernel.NodeTypeSection, Attrs: map[string]any{"title": material.Title, "level": 1},
					Children: []*writingkernel.DocumentNode{paragraph}, Origin: origin})
			}
			return &writingkernel.DocumentVersion{SchemaVersion: writingkernel.SchemaVersionV1,
				DocumentID: "doc_vertical_live_faithful_rewrite", VersionID: "ver_live_base", Root: root}
		})},
	}
}
