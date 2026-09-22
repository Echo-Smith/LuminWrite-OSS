package writingruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/contextcompiler"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

func pressureEnvelope(budget int) *contextcompiler.Envelope {
	input := contextcompiler.Input{
		ContractDigest: "长文报告：检索优于重排",
		ThreadLabels:   []string{"论点链：检索优于重排"},
		DocumentState:  "第三章草稿中",
		TotalBudget:    budget,
	}
	for i := 0; i < 200; i++ {
		input.FactLines = append(input.FactLines, "主体 | state | 状态行")
	}
	envelope, err := contextcompiler.Compile(input)
	if err != nil {
		panic(err)
	}
	return &envelope
}

// pressureBudgets compiles one deterministic content-heavy fixture across
// an ascending budget scan and returns budgets landing in each pressure
// band: quiet (< 0.70), warn ([0.70, 0.85)), hot (>= 0.85). The pressure
// curve is monotone in the budget but not linear (the retention walk
// reclaims unused room), so the bands are located by scanning, not by
// arithmetic that would silently drift with the estimator.
func pressureBudgets(t *testing.T) (quiet, warn, hot int, raw contextcompiler.Input) {
	t.Helper()
	raw = contextcompiler.Input{
		ContractDigest: "长文报告：检索优于重排",
		ThreadLabels:   []string{"论点链：检索优于重排"},
		DocumentState:  "第三章草稿中",
	}
	for i := 0; i < 300; i++ {
		raw.FactLines = append(raw.FactLines, "主体 | state | 状态行")
	}
	found := 0
	for budget := 1300; budget <= 40000 && found < 2; budget += 25 {
		input := raw
		input.TotalBudget = budget
		envelope, err := contextcompiler.Compile(input)
		if err != nil {
			t.Fatal(err)
		}
		pressure := envelope.Pressure()
		switch {
		case pressure >= contextcompiler.PressureCompressThreshold:
			hot = budget
		case pressure >= contextcompiler.PressureWarnThreshold:
			if hot == 0 {
				t.Fatalf("budget scan skipped the hot band: %.2f at %d", pressure, budget)
			}
			if warn == 0 {
				warn = budget
				found++
			}
		default:
			if warn == 0 {
				t.Fatalf("budget scan skipped the warn band: %.2f at %d", pressure, budget)
			}
			if quiet == 0 {
				quiet = budget
				found++
			}
		}
	}
	if found < 2 {
		t.Fatal("budget scan did not locate the warn and quiet bands")
	}
	return quiet, warn, hot, raw
}

func TestContextRuntimePressureBands(t *testing.T) {
	runtime := &ContextRuntime{}
	now := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	runtime.Now = func() time.Time { return now }
	quietBudget, warnBudget, hotBudget, _ := pressureBudgets(t)

	compile := func(budget int) *contextcompiler.Envelope {
		input := contextcompiler.Input{ContractDigest: "长文报告：检索优于重排",
			ThreadLabels: []string{"论点链：检索优于重排"}, DocumentState: "第三章草稿中", TotalBudget: budget}
		for i := 0; i < 300; i++ {
			input.FactLines = append(input.FactLines, "主体 | state | 状态行")
		}
		envelope, err := contextcompiler.Compile(input)
		if err != nil {
			t.Fatal(err)
		}
		return &envelope
	}

	// Below 0.70: silent.
	if action := runtime.Observe("core.draft.generate", compile(quietBudget)); action != PrecompressionNone {
		t.Fatalf("quiet pressure classified %s", action)
	}
	// Between 0.70 and 0.85: warn only, no guard engagement.
	if action := runtime.Observe("core.draft.generate", compile(warnBudget)); action != PrecompressionWarn {
		t.Fatalf("warn band misclassified")
	}
	// At or above 0.85: one recompression slot, then cooldown.
	hot := compile(hotBudget)
	if action := runtime.Observe("core.draft.generate", hot); action != PrecompressionRecompile {
		t.Fatalf("hot pressure classified %s", action)
	}
	runtime.ReleaseRecompression("core.draft.generate")
	if action := runtime.Observe("core.draft.generate", hot); action != PrecompressionCooling {
		t.Fatalf("second hot observation within cooldown classified %s", action)
	}
	// Past the cooldown window the slot reopens.
	now = now.Add(ContextCompressionCooldown + time.Second)
	if action := runtime.Observe("core.draft.generate", hot); action != PrecompressionRecompile {
		t.Fatalf("post-cooldown observation classified %s", action)
	}
	// While a recompression is in flight the same capability is guarded.
	if action := runtime.Observe("core.draft.generate", hot); action != PrecompressionInFlight {
		t.Fatalf("in-flight observation classified %s", action)
	}
	runtime.ReleaseRecompression("core.draft.generate")
	// Capabilities are guarded independently.
	if action := runtime.Observe("core.validation.quality", hot); action != PrecompressionRecompile {
		t.Fatalf("unrelated capability classified %s", action)
	}
}

func TestContextRuntimeCompressedBudget(t *testing.T) {
	envelope := pressureEnvelope(6000)
	runtime := &ContextRuntime{}
	budget, err := runtime.CompressedBudget(envelope)
	if err != nil || budget != 4800 {
		t.Fatalf("budget=%d err=%v, want 0.8x=4800", budget, err)
	}
	runtime.CompressBudgetFactor = 0.5
	budget, err = runtime.CompressedBudget(envelope)
	if err != nil || budget != 3000 {
		t.Fatalf("budget=%d err=%v, want 3000", budget, err)
	}
	// A compressed budget below the resident protection cannot compile, so
	// the runtime refuses instead of deferring a guaranteed failure.
	runtime.CompressBudgetFactor = 0.1
	if _, err := runtime.CompressedBudget(envelope); err == nil {
		t.Fatal("sub-resident compression must be refused")
	}
	if _, err := (&ContextRuntime{}).CompressedBudget(nil); err == nil {
		t.Fatal("nil envelope must be refused")
	}
}

func TestRecoveryPathForDecisionTable(t *testing.T) {
	// docs/18 §18.5.6: recovery paths are named per failure category. The
	// table is the contract between the runtime and its evidence consumers.
	cases := []struct {
		category   ContextFailureCategory
		recompiles int
		want       RecoveryPath
	}{
		{ContextFailureSource, 0, RecoveryReloadSource},
		{ContextFailureSource, 1, RecoveryHumanEscalation},
		{ContextFailureCompile, 0, RecoveryRecompile},
		{ContextFailureCompile, 1, RecoveryHumanEscalation},
		{ContextFailureResidentOverflow, 0, RecoveryRecompileCompressed},
		{ContextFailureResidentOverflow, 1, RecoveryHumanEscalation},
		{ContextFailureBudget, 0, RecoveryRecompileCompressed},
		{ContextFailurePersist, 0, RecoveryRecompile},
		{ContextFailureCategory("unknown"), 0, RecoveryHumanEscalation},
	}
	for _, entry := range cases {
		if got := RecoveryPathFor(entry.category, entry.recompiles); got != entry.want {
			t.Fatalf("RecoveryPathFor(%s, %d) = %s, want %s", entry.category, entry.recompiles, got, entry.want)
		}
	}
}

func TestOrchestratorPrecompressesHighPressureEnvelope(t *testing.T) {
	fixture := newOrchestratorFixture(t, writingplan.IdempotencySafe, false)
	metrics := &metricCapture{}
	fixture.orchestrator.Telemetry = metrics
	_, _, hotBudget, raw := pressureBudgets(t)
	fixture.orchestrator.Context = &fixedContextSource{input: raw}
	envelopes := &envelopeCaptureStore{fakeRuntimeStore: fixture.store}
	fixture.orchestrator.Envelopes = envelopes

	capabilities := writingplan.NewCapabilityRegistry("runtime-test")
	if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: "engine.step.write",
		AcceptedInputTypes: []writingplan.ArtifactType{"contract"}, ProducedOutputTypes: []writingplan.ArtifactType{"full_draft"},
		Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
			return writingplan.ExecutionResult{}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	manifest, ok := fixture.orchestrator.Capabilities.Get("core.draft.generate")
	if !ok {
		t.Fatal("draft capability missing")
	}
	manifest.Context.ContextTokenBudget = hotBudget
	if err := capabilities.Register(manifest); err != nil {
		t.Fatal(err)
	}
	fixture.orchestrator.Capabilities = capabilities

	if _, err := fixture.orchestrator.Execute(context.Background(), fixture.store.run.RunID); err != nil {
		t.Fatal(err)
	}
	if len(envelopes.envelopes) != 1 {
		t.Fatalf("envelopes=%d", len(envelopes.envelopes))
	}
	var compiled contextcompiler.Envelope
	if err := json.Unmarshal(envelopes.envelopes[0].Payload, &compiled); err != nil {
		t.Fatal(err)
	}
	full, err := contextcompiler.Compile(contextcompiler.Input{
		ContractDigest: raw.ContractDigest, ThreadLabels: raw.ThreadLabels,
		DocumentState: raw.DocumentState, FactLines: raw.FactLines, TotalBudget: hotBudget,
	})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.TotalTokens >= full.TotalTokens {
		t.Fatalf("pre-compression did not shrink the envelope: %d vs %d", compiled.TotalTokens, full.TotalTokens)
	}
	if !metrics.has(MetricContextPressure, "recompiled") || !metrics.has(MetricContextPressure, "recompile") {
		t.Fatalf("metrics=%#v", metrics.metrics)
	}
}

func TestOrchestratorPressureWarnKeepsEnvelope(t *testing.T) {
	fixture := newOrchestratorFixture(t, writingplan.IdempotencySafe, false)
	metrics := &metricCapture{}
	fixture.orchestrator.Telemetry = metrics
	_, _, _, raw := pressureBudgets(t)
	fixture.orchestrator.Context = &fixedContextSource{input: raw}
	envelopes := &envelopeCaptureStore{fakeRuntimeStore: fixture.store}
	fixture.orchestrator.Envelopes = envelopes

	capabilities := writingplan.NewCapabilityRegistry("runtime-test")
	if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: "engine.step.write",
		AcceptedInputTypes: []writingplan.ArtifactType{"contract"}, ProducedOutputTypes: []writingplan.ArtifactType{"full_draft"},
		Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
			return writingplan.ExecutionResult{}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	manifest, ok := fixture.orchestrator.Capabilities.Get("core.draft.generate")
	if !ok {
		t.Fatal("draft capability missing")
	}
	// Scan for a warn-band budget using the manifest's compilation context
	// (Wanted + RetentionPriority), not the open compile from pressureBudgets().
	// The manifest's context contract (including review_guard) changes the
	// weight distribution, so the open-compile bands may not align.
	wanted := manifest.Context.ContextWanted()
	retentionPriority := manifest.Context.ContextRetentionPriority()
	var warnBudget int
	for budget := 1300; budget <= 40000; budget += 25 {
		input := raw
		input.TotalBudget = budget
		input.Wanted = wanted
		input.RetentionPriority = retentionPriority
		envelope, err := contextcompiler.Compile(input)
		if err != nil {
			t.Fatal(err)
		}
		pressure := envelope.Pressure()
		if pressure >= contextcompiler.PressureWarnThreshold && pressure < contextcompiler.PressureCompressThreshold {
			warnBudget = budget
			break
		}
	}
	if warnBudget == 0 {
		t.Fatal("budget scan did not locate a warn-band budget for the manifest context")
	}
	manifest.Context.ContextTokenBudget = warnBudget
	if err := capabilities.Register(manifest); err != nil {
		t.Fatal(err)
	}
	fixture.orchestrator.Capabilities = capabilities

	if _, err := fixture.orchestrator.Execute(context.Background(), fixture.store.run.RunID); err != nil {
		t.Fatal(err)
	}
	if len(envelopes.envelopes) != 1 {
		t.Fatalf("envelopes=%d", len(envelopes.envelopes))
	}
	var compiled contextcompiler.Envelope
	if err := json.Unmarshal(envelopes.envelopes[0].Payload, &compiled); err != nil {
		t.Fatal(err)
	}
	if !metrics.has(MetricContextPressure, "warn") {
		t.Fatalf("warn not observed: %#v", metrics.metrics)
	}
	if metrics.has(MetricContextPressure, "recompile") {
		t.Fatalf("warn band must not recompile: %#v", metrics.metrics)
	}
	// The persisted payload's tokenBudget is not serialized (metadata stays
	// out of the payload), so the pressure is recomputed from the budget
	// the manifest declared.
	pressure := float64(compiled.TotalTokens) / float64(warnBudget)
	if pressure < contextcompiler.PressureWarnThreshold || pressure >= contextcompiler.PressureCompressThreshold {
		t.Fatalf("fixture left the warn band: %.2f", pressure)
	}
}

func TestRenderDocumentStateSubtree(t *testing.T) {
	documentID := "doc_render"
	// Real versions root at the document node whose children are sections;
	// the render walks root children in document order.
	root := &writingkernel.DocumentNode{Type: writingkernel.NodeTypeDocument,
		Children: []*writingkernel.DocumentNode{
			{Type: writingkernel.NodeTypeSection,
				Attrs: map[string]any{"section_id": "sec_1", "title": "第二章 现状", "level": 1},
				Children: []*writingkernel.DocumentNode{
					{Type: writingkernel.NodeTypeParagraph, Children: []*writingkernel.DocumentNode{
						{Type: writingkernel.NodeTypeText, Text: "第三章草稿已开篇。"},
					}},
				}},
		}}
	version := writingstore.StoredDocumentVersion{Sequence: 2, QualityState: "candidate_draft"}
	version.Version = writingkernel.DocumentVersion{DocumentID: documentID, VersionID: "ver_2", Root: root}
	rendered := renderDocumentState(version)
	for _, want := range []string{"文档版本 v2（candidate_draft）", "## 第二章 现状", "- 第三章草稿已开篇。"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered=%q missing %q", rendered, want)
		}
	}
	again := renderDocumentState(version)
	if again != rendered {
		t.Fatal("renderer not deterministic")
	}
	// Long text truncates at the fixed limit with an ellipsis marker.
	long := &writingkernel.DocumentNode{Type: writingkernel.NodeTypeParagraph, Children: []*writingkernel.DocumentNode{
		{Type: writingkernel.NodeTypeText, Text: strings.Repeat("长", documentStateTextLimit+20)},
	}}
	truncated := writingstore.StoredDocumentVersion{Sequence: 1, QualityState: "candidate_draft"}
	truncated.Version = writingkernel.DocumentVersion{DocumentID: documentID, VersionID: "ver_1", Root: long}
	out := renderDocumentState(truncated)
	if !strings.HasSuffix(out, "…") || len([]rune(out)) > documentStateTextLimit+len("文档版本 v1（candidate_draft）\n- ")+2 {
		t.Fatalf("truncation failed: %q", out)
	}
	// A root without children renders the truthful empty body.
	empty := writingstore.StoredDocumentVersion{Sequence: 3, QualityState: "candidate_draft"}
	empty.Version = writingkernel.DocumentVersion{DocumentID: documentID, VersionID: "ver_3"}
	if out := renderDocumentState(empty); out != "文档版本 v3（candidate_draft）：正文为空" {
		t.Fatalf("empty render=%q", out)
	}
}
