package server

import (
	"encoding/json"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
)

// 词表单源契约测试：下发视图必须与密封消费的权威表逐字一致（语义字段不
// 允许在本文件出现第二份拷贝），证据策略必须来自活表。

func TestWritingFlowsPayloadShape(t *testing.T) {
	payload := buildWritingFlowsPayload()
	if payload.DefaultFlow != "long_form" {
		t.Fatalf("default_flow = %q, want long_form", payload.DefaultFlow)
	}
	if len(payload.Flows) != len(writingFlowOrder) {
		t.Fatalf("flows = %d, want %d", len(payload.Flows), len(writingFlowOrder))
	}
	for i, flow := range writingFlowOrder {
		if payload.Flows[i].Type != flow {
			t.Fatalf("flows[%d].type = %q, want %q（下发顺序必须与 writingFlowOrder 一致）", i, payload.Flows[i].Type, flow)
		}
		if payload.Flows[i].Label == "" || payload.Flows[i].Description == "" || payload.Flows[i].TemplateID == "" {
			t.Fatalf("flow %q 展示字段不完整（label/description/templateId 必填）", flow)
		}
	}
}

// 语义字段与 writingFlowContractPlans（密封消费的同一张表）逐字一致；
// evidence policy 与 writingruntime 活表一致——这里故意不手抄任何期望值，
// 权威表改了测试就该跟着改，镜像死不掉就是测试失职。
func TestWritingFlowsSemanticFieldsMatchSealMap(t *testing.T) {
	payload := buildWritingFlowsPayload()
	byType := map[string]writingFlowSpecView{}
	for _, view := range payload.Flows {
		byType[view.Type] = view
	}

	for flow, seal := range writingFlowContractPlans {
		view, ok := byType[flow]
		if !ok {
			t.Fatalf("flow %q 缺失", flow)
		}
		if view.Launch != "writing" {
			t.Fatalf("flow %q launch = %q, want writing", flow, view.Launch)
		}
		if view.IntentOperation != string(seal.operation) {
			t.Fatalf("flow %q intentOperation = %q, want %q", flow, view.IntentOperation, string(seal.operation))
		}
		if view.Orchestration != string(seal.orchestration) {
			t.Fatalf("flow %q orchestration = %q, want %q", flow, view.Orchestration, string(seal.orchestration))
		}
		if view.Summary != seal.summary {
			t.Fatalf("flow %q summary 与密封表不一致", flow)
		}
		if len(view.Steps) != len(seal.steps) {
			t.Fatalf("flow %q steps = %d, want %d", flow, len(view.Steps), len(seal.steps))
		}
		wiring := writingFlowStepIO[flow]
		for i, step := range seal.steps {
			got := view.Steps[i]
			if got.StepID != step.StepID || got.Capability != step.CapabilityHint {
				t.Fatalf("flow %q steps[%d] = %s/%s, want %s/%s", flow, i, got.StepID, got.Capability, step.StepID, step.CapabilityHint)
			}
			if got.Description != step.Objective {
				t.Fatalf("flow %q steps[%d].description 与密封表 objective 不一致", flow, i)
			}
			wantDeps := step.DependsOn
			if wantDeps == nil {
				wantDeps = []string{}
			}
			if len(got.DependsOn) != len(wantDeps) {
				t.Fatalf("flow %q steps[%d] depends_on = %v, want %v", flow, i, got.DependsOn, wantDeps)
			}
			io, ok := wiring[step.StepID]
			if !ok || len(io[0]) == 0 || len(io[1]) == 0 {
				t.Fatalf("flow %q step %q 缺 inputs/outputs wiring（writingFlowStepIO 补行）", flow, step.StepID)
			}
			if len(got.Inputs) != len(io[0]) || len(got.Outputs) != len(io[1]) {
				t.Fatalf("flow %q step %q inputs/outputs 与 wiring 表不一致", flow, step.StepID)
			}
		}
		if policy, ok := writingruntime.EvidenceScenarioPolicyFor(flow); ok {
			if view.EvidencePolicy == nil {
				t.Fatalf("flow %q evidencePolicy 缺失（活表有行）", flow)
			}
			if view.EvidencePolicy.Name != policy.Name || view.EvidencePolicy.GovernedIndex != policy.GovernedIndex || view.EvidencePolicy.NodeName != policy.NodeName {
				t.Fatalf("flow %q evidencePolicy 与活表不一致", flow)
			}
		} else if view.EvidencePolicy != nil {
			t.Fatalf("flow %q evidencePolicy 应为 null（活表无行）", flow)
		}
	}
}

func TestWritingFlowsResearchReviewIsResearchLaunch(t *testing.T) {
	payload := buildWritingFlowsPayload()
	var research *writingFlowSpecView
	for i := range payload.Flows {
		if payload.Flows[i].Type == "research_review" {
			research = &payload.Flows[i]
		}
	}
	if research == nil {
		t.Fatal("research_review 缺失")
	}
	if research.Launch != "research" {
		t.Fatalf("research_review launch = %q, want research（其执行链是研究运行时的权威）", research.Launch)
	}
	if research.EvidencePolicy != nil {
		t.Fatal("research_review evidencePolicy 应为 null（legacy 证据情景表刻意无此行）")
	}
	if len(research.Steps) == 0 {
		t.Fatal("research_review steps 不应为空（选择器需要节点简图）")
	}
	// 语义字段不在本文件出现第二份：research_review 不允许混进密封表
	if _, ok := writingFlowContractPlans["research_review"]; ok {
		t.Fatal("research_review 不得进入 writingFlowContractPlans（其启动链走 research-contract-draft）")
	}
}

// 下发 JSON 的形状必须与前端 WritingFlowSpec 契约一致（camelCase spec 字段
// + snake_case step 字段）——这是前后端唯一共享的形状声明。
func TestWritingFlowsJSONShapeMatchesFrontendContract(t *testing.T) {
	raw, err := json.Marshal(buildWritingFlowsPayload())
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Flows []struct {
			Type          string `json:"type"`
			IntentOperation string `json:"intentOperation"`
			Orchestration   string `json:"orchestration"`
			TemplateID      string `json:"templateId"`
			EvidencePolicy  *struct {
				Name          string `json:"name"`
				GovernedIndex int    `json:"governedIndex"`
				NodeName      string `json:"governedNode"`
			} `json:"evidencePolicy"`
			InitialArtifactTypes []string `json:"initialArtifactTypes"`
			Steps []struct {
				StepID     string   `json:"step_id"`
				Capability string   `json:"capability"`
				Inputs     []string `json:"inputs"`
				Outputs    []string `json:"outputs"`
				DependsOn  []string `json:"depends_on"`
			} `json:"steps"`
		} `json:"flows"`
		DefaultFlow string `json:"default_flow"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("payload 不是合法 JSON: %v", err)
	}
	if len(probe.Flows) == 0 || probe.Flows[0].IntentOperation == "" || probe.Flows[0].TemplateID == "" || len(probe.Flows[0].InitialArtifactTypes) == 0 {
		t.Fatal("camelCase 契约字段缺失（intentOperation/templateId/initialArtifactTypes）")
	}
	if probe.Flows[0].EvidencePolicy == nil || probe.Flows[0].EvidencePolicy.GovernedIndex != 1 {
		t.Fatalf("long_form evidencePolicy.governedIndex 应为 1，got %+v", probe.Flows[0].EvidencePolicy)
	}
	if len(probe.Flows[0].Steps) == 0 || probe.Flows[0].Steps[0].StepID == "" || probe.Flows[0].Steps[0].Capability == "" {
		t.Fatal("snake_case 步骤契约字段缺失（step_id/capability）")
	}
}
