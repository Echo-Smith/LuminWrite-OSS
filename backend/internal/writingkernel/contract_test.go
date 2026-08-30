package writingkernel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestContractEnums(t *testing.T) {
	tests := []struct {
		name  string
		valid func(string) bool
		ok    []string
	}{
		{
			name:  "task_mode",
			valid: func(value string) bool { return TaskMode(value).Valid() },
			ok:    []string{"auto", "writing", "guided", "polish"},
		},
		{
			name:  "orchestration_mode",
			valid: func(value string) bool { return OrchestrationMode(value).Valid() },
			ok:    []string{"auto", "fast", "outline_first", "sourced", "strict_research"},
		},
		{
			name:  "assurance_level",
			valid: func(value string) bool { return AssuranceLevel(value).Valid() },
			ok:    []string{"flexible", "standard", "sourced", "strict"},
		},
		{
			name:  "approval_mode",
			valid: func(value string) bool { return ApprovalMode(value).Valid() },
			ok:    []string{"conditional", "always", "auto"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, value := range test.ok {
				if !test.valid(value) {
					t.Fatalf("expected %q to be valid", value)
				}
			}
			for _, value := range []string{"", "AUTO", "legacy", "editorial"} {
				if test.valid(value) {
					t.Fatalf("expected %q to be invalid", value)
				}
			}
		})
	}
}

func TestContractValidateRequiresVersionHashAndKeyFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*WritingContract)
	}{
		{name: "missing schema version", mutate: func(c *WritingContract) { c.SchemaVersion = "" }},
		{name: "missing version", mutate: func(c *WritingContract) { c.Version = 0 }},
		{name: "missing hash", mutate: func(c *WritingContract) { c.ContractHash = "" }},
		{name: "blank topic", mutate: func(c *WritingContract) { c.Content.Topic = "  " }},
		{name: "missing assurance", mutate: func(c *WritingContract) { c.Collaboration.AssuranceLevel = "" }},
		{name: "invalid length", mutate: func(c *WritingContract) { c.Delivery.Length = LengthRange{Min: 7000, Max: 5000} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contract := validContract(t)
			test.mutate(&contract)
			if test.name != "missing hash" {
				refreshControlAttributions(t, &contract)
				contract = sealContract(t, contract)
			}
			if err := contract.Validate(); err == nil {
				t.Fatal("expected validation to fail")
			}
		})
	}
}

func TestContractRejectsHashMismatch(t *testing.T) {
	contract := validContract(t)
	contract.Intent.Purpose = "changed without resealing"
	if err := contract.Validate(); err == nil || !strings.Contains(err.Error(), "contract_hash mismatch") {
		t.Fatalf("expected hash mismatch, got %v", err)
	}
}

func TestContractRoundTripPreservesControlSourceAndVersion(t *testing.T) {
	contract := validContract(t)
	payload, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWritingContractStrict(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(contract, decoded) {
		t.Fatalf("round trip changed contract\nwant: %#v\ngot:  %#v", contract, decoded)
	}
	if decoded.Version != 1 || decoded.Collaboration.TaskMode != TaskModeGuided {
		t.Fatal("version or task mode was not preserved")
	}
	if decoded.SourceAttributions[0].Source != AttributionSourceUser {
		t.Fatal("source attribution was not preserved")
	}
}

func TestContractFixtureRoundTrip(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "..", "specs", "lcp", "v1", "fixtures", "writing-contract.valid.json")
	payload, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := DecodeWritingContractStrict(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.Validate(); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWritingContractStrict(roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(contract, decoded) {
		t.Fatal("fixture changed during JSON round trip")
	}
}

func TestContractStrictDecodeRejectsUnknownAndDuplicateFields(t *testing.T) {
	contract := validContract(t)
	payload, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	unknown := strings.Replace(string(payload), "{", `{"agent_mode":"harness",`, 1)
	if _, err := DecodeWritingContractStrict([]byte(unknown)); err == nil {
		t.Fatal("expected unknown legacy field to fail")
	}
	duplicate := `{"contract_id":"ctr_a","contract_id":"ctr_b"}`
	if _, err := DecodeWritingContractStrict([]byte(duplicate)); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("expected duplicate-key error, got %v", err)
	}
}

func TestContractUserSelectionCannotBeOverwritten(t *testing.T) {
	contract := validContract(t)
	recommendation := ExecutionRecommendation{
		TaskMode:          TaskModeWriting,
		OrchestrationMode: OrchestrationModeStrictResearch,
	}
	resolved, err := ResolveExecutionControl(contract, recommendation)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Effective.TaskMode != TaskModeGuided {
		t.Fatalf("user task_mode was overwritten: %s", resolved.Effective.TaskMode)
	}
	if resolved.Effective.AssuranceLevel != AssuranceLevelSourced {
		t.Fatalf("user assurance_level was overwritten: %s", resolved.Effective.AssuranceLevel)
	}
	if resolved.Effective.ApprovalMode != ApprovalModeConditional {
		t.Fatalf("user approval_mode was overwritten: %s", resolved.Effective.ApprovalMode)
	}
	if contract.Collaboration.TaskMode != TaskModeGuided {
		t.Fatal("resolver mutated the contract")
	}
}

func TestContractAutoAllowsSeparateEffectiveRecommendation(t *testing.T) {
	contract := validContract(t)
	contract.Collaboration.TaskMode = TaskModeAuto
	contract.Collaboration.OrchestrationMode = OrchestrationModeAuto
	refreshControlAttributions(t, &contract)
	contract = sealContract(t, contract)

	recommendation := ExecutionRecommendation{
		OrchestrationMode: OrchestrationModeOutlineFirst,
	}
	resolved, err := ResolveExecutionControl(contract, recommendation)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Requested.TaskMode != TaskModeAuto || contract.Collaboration.TaskMode != TaskModeAuto {
		t.Fatal("requested contract control must remain auto")
	}
	if resolved.Effective.TaskMode != TaskModeAuto || resolved.Effective.OrchestrationMode != OrchestrationModeOutlineFirst {
		t.Fatalf("recommendation was not applied to auto fields: %#v", resolved.Effective)
	}
	if resolved.Effective.AssuranceLevel != AssuranceLevelSourced {
		t.Fatal("non-auto assurance level must not be overwritten")
	}
}

func TestContractPartialRecommendationValidatesOnlyProvidedFields(t *testing.T) {
	contract := validContract(t)
	resolved, err := ResolveExecutionControl(contract, ExecutionRecommendation{
		OrchestrationMode: OrchestrationModeFast,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Effective.OrchestrationMode != OrchestrationModeFast {
		t.Fatalf("partial recommendation was not applied: %#v", resolved)
	}
	if _, err := ResolveExecutionControl(contract, ExecutionRecommendation{TaskMode: "invalid"}); err == nil {
		t.Fatal("expected invalid provided recommendation to fail")
	}
}

func TestContractPendingDirectionalInferenceBlocksConfirmation(t *testing.T) {
	contract := validContract(t)
	contract.Inferences = []Inference{{
		FieldPath:     "/content/topic",
		ProposedValue: "一个未经用户确认的新主题",
		Confidence:    0.4,
		Status:        InferenceStatusPendingClarification,
		ReasonCode:    "ambiguous_topic",
		Summary:       "用户输入可能指向两个不同主题",
	}}
	contract = sealContract(t, contract)
	if err := contract.Validate(); err == nil || !strings.Contains(err.Error(), "requires clarification") {
		t.Fatalf("expected pending inference to block confirmation, got %v", err)
	}
}

func TestContractTransitionRequiresMonotonicVersion(t *testing.T) {
	previous := validContract(t)
	next := previous
	next.Version = 2
	next.Intent.Purpose = "第二版目的"
	next = sealContract(t, next)
	if err := ValidateTransition(previous, next); err != nil {
		t.Fatalf("expected valid transition: %v", err)
	}
	regressed := next
	regressed.Version = 1
	regressed = sealContract(t, regressed)
	if err := ValidateTransition(previous, regressed); err == nil {
		t.Fatal("expected non-increasing version to fail")
	}
}

func TestContractAttributionValueHashMustMatch(t *testing.T) {
	contract := validContract(t)
	contract.SourceAttributions[0].ValueHash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	contract = sealContract(t, contract)
	if err := contract.Validate(); err == nil || !strings.Contains(err.Error(), "value_hash mismatch") {
		t.Fatalf("expected attribution mismatch, got %v", err)
	}
}

func validContract(t *testing.T) WritingContract {
	t.Helper()
	contract := WritingContract{
		SchemaVersion: SchemaVersionV1,
		ContractVersion: ContractVersion{
			ContractID: "ctr_01HZZ000000000000000000001",
			Version:    1,
		},
		Status: ContractStatusConfirmed,
		Intent: IntentSpec{
			Operation: OperationCreate,
			Genre:     "industry_analysis",
			Purpose:   "支持 AI 产品架构决策",
		},
		Audience: AudienceSpec{
			Role:           "AI 产品负责人",
			KnowledgeLevel: "professional",
		},
		Content: ContentSpec{
			Topic:            "多 Agent 写作治理",
			CentralQuestion:  "如何兼顾自动选择与用户控制",
			RequiredPoints:   []string{},
			ProhibitedPoints: []string{},
		},
		Voice: VoiceSpec{
			Tone:              "professional",
			PreserveUserVoice: true,
		},
		MaterialPolicy: MaterialPolicy{
			UserMaterialPriority:  UserMaterialPriorityHighest,
			AllowExternalResearch: true,
			ConflictHandling:      ConflictHandlingAskUser,
		},
		EvidencePolicy: EvidencePolicy{
			Level:             EvidenceLevelSourced,
			UnsupportedClaims: UnsupportedClaimsProhibit,
		},
		Delivery: DeliverySpec{
			Format:   DeliveryFormatMarkdown,
			Language: "zh-CN",
			Length:   LengthRange{Min: 5000, Max: 7000},
		},
		Collaboration: ExecutionControl{
			TaskMode:          TaskModeGuided,
			OrchestrationMode: OrchestrationModeAuto,
			AssuranceLevel:    AssuranceLevelSourced,
			ApprovalMode:      ApprovalModeConditional,
		},
		SourceAttributions: []SourceAttribution{},
		Inferences:         []Inference{},
	}
	refreshControlAttributions(t, &contract)
	return sealContract(t, contract)
}

func refreshControlAttributions(t *testing.T, contract *WritingContract) {
	t.Helper()
	paths := []string{
		"/collaboration/task_mode",
		"/collaboration/orchestration_mode",
		"/collaboration/assurance_level",
		"/collaboration/approval_mode",
	}
	contract.SourceAttributions = make([]SourceAttribution, 0, len(paths))
	for _, fieldPath := range paths {
		valueHash, err := contract.FieldValueHash(fieldPath)
		if err != nil {
			t.Fatal(err)
		}
		contract.SourceAttributions = append(contract.SourceAttributions, SourceAttribution{
			FieldPath:  fieldPath,
			Source:     AttributionSourceUser,
			ValueHash:  valueHash,
			RecordedAt: "2026-08-27T00:00:00Z",
		})
	}
}

func sealContract(t *testing.T, contract WritingContract) WritingContract {
	t.Helper()
	sealed, err := contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}
