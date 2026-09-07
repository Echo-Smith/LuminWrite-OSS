package writingkernel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Golden tests pin the byte-for-byte stability of v1.0 contract hashes.
// WritingContract later gains a trailing, omitempty Research field; these
// assertions are the guardrail that v1.0 serialization and ComputeHash
// output never drift (research-review T01). If any change here breaks,
// that change is a protocol migration, not a refactor.

type goldenContractCase struct {
	name        string
	mode        OrchestrationMode
	taskMode    TaskMode
	assurance   AssuranceLevel
	approval    ApprovalMode
	priority    UserMaterialPriority
	external    bool
	conflict    ConflictHandling
	externalRef bool
}

var goldenCases = []goldenContractCase{
	{
		name: "auto", mode: OrchestrationModeAuto, taskMode: TaskModeAuto, assurance: AssuranceLevelStandard, approval: ApprovalModeAuto,
		priority: UserMaterialPriorityBalanced, external: true, conflict: ConflictHandlingRecordAndContinue, externalRef: true,
	},
	{
		name: "fast", mode: OrchestrationModeFast, taskMode: TaskModeWriting, assurance: AssuranceLevelFlexible, approval: ApprovalModeConditional,
		priority: UserMaterialPriorityPreferred, external: false, conflict: ConflictHandlingPreferUserMaterial, externalRef: false,
	},
	{
		name: "outline_first", mode: OrchestrationModeOutlineFirst, taskMode: TaskModeGuided, assurance: AssuranceLevelStandard, approval: ApprovalModeConditional,
		priority: UserMaterialPriorityHighest, external: false, conflict: ConflictHandlingAskUser, externalRef: false,
	},
	{
		name: "sourced", mode: OrchestrationModeSourced, taskMode: TaskModeWriting, assurance: AssuranceLevelSourced, approval: ApprovalModeAlways,
		priority: UserMaterialPriorityPreferred, external: true, conflict: ConflictHandlingRecordAndContinue, externalRef: true,
	},
	{
		name: "strict_research", mode: OrchestrationModeStrictResearch, taskMode: TaskModeGuided, assurance: AssuranceLevelStrict, approval: ApprovalModeAlways,
		priority: UserMaterialPriorityHighest, external: true, conflict: ConflictHandlingPreferUserMaterial, externalRef: true,
	},
}

// goldenHashes maps case name to the sealed contract hash. To regenerate,
// run the test with LUMIN_REGENERATE_GOLDEN=1, verify the diff is empty for
// untouched inputs, then hardcode the printed values. The in-repo values
// below are the pinned golden samples for v1.0.
var goldenHashes = map[string]string{
	"auto":            "sha256:2dc3f37a4763d9b8b6a45ad9a17a6081d7f0c84083f1ad237a1646ae1e70b33a",
	"fast":            "sha256:38cf0cbace71df97248d347398ab5f86c8ff9436a629fe0062e278476650a4ce",
	"outline_first":   "sha256:3a60737bb64c40e46fd063eaa2aa12ae67895bb5cd15d38b9af46f70f42347ce",
	"sourced":         "sha256:ea513c713888ebfef08b3a940498cde59e70c330800efae20ed6ed9865bc6914",
	"strict_research": "sha256:377da978591b19b42c4526f264fb6b92b01a33d97365536b9373969b09f493b5",
}

func TestGoldenContractHashesV1(t *testing.T) {
	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			contract := goldenContract(tc)
			hash, err := contract.ComputeHash()
			if err != nil {
				t.Fatal(err)
			}
			if os.Getenv("LUMIN_REGENERATE_GOLDEN") == "1" {
				fmt.Printf("%s\t%s\n", tc.name, hash)
				return
			}
			expected, ok := goldenHashes[tc.name]
			if !ok || expected == "" || len(expected) < len(hashPrefix)+64 {
				t.Fatalf("golden hash for %q is not pinned: %q", tc.name, expected)
			}
			if hash != expected {
				t.Fatalf("golden hash drifted for %s:\n  want %s\n  got  %s", tc.name, expected, hash)
			}
			sealed, err := contract.WithComputedHash()
			if err != nil {
				t.Fatal(err)
			}
			if err := sealed.Validate(); err != nil {
				t.Fatalf("golden contract must validate: %v", err)
			}
		})
	}
}

// goldenFixtureHash pins the repo's committed v1 fixture
// (specs/lcp/v1/fixtures/writing-contract.valid.json).
const goldenFixtureHash = "sha256:12062d8a3181118dcb570bae0d97f1a5167b59c5f1ce55f3078fc1ab38cae203"

// TestGoldenContractFixtureHash pins the repo's committed v1 fixture: its
// contract_hash must equal the golden value and survive strict decode,
// re-validation, and a JSON round trip unchanged.
func TestGoldenContractFixtureHash(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "specs", "lcp", "v1", "fixtures", "writing-contract.valid.json"))
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
	hash, err := contract.ComputeHash()
	if err != nil {
		t.Fatal(err)
	}
	if hash != goldenFixtureHash || hash != contract.ContractHash {
		t.Fatalf("fixture hash drifted:\n  golden %s\n  file   %s\n  recomputed %s", goldenFixtureHash, contract.ContractHash, hash)
	}
	roundTrip, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	redecoded, err := DecodeWritingContractStrict(roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(contract, redecoded) {
		t.Fatal("fixture changed during JSON round trip")
	}
}

func goldenContract(tc goldenContractCase) WritingContract {
	contract := WritingContract{
		SchemaVersion: SchemaVersionV1,
		ContractVersion: ContractVersion{
			ContractID: "ctr_01HZZGOLDEN00000000000000001",
			Version:    1,
		},
		Status: ContractStatusConfirmed,
		Intent: IntentSpec{
			Operation: OperationCreate,
			Genre:     "industry_analysis",
			Purpose:   "为研究综述路径固化历史合同基线",
		},
		Audience: AudienceSpec{
			Role:           "研究分析负责人",
			KnowledgeLevel: "professional",
		},
		Content: ContentSpec{
			Topic:            "合同哈希金样本",
			CentralQuestion:  "历史 v1.0 合同哈希是否逐字节稳定",
			RequiredPoints:   []string{"覆盖全部既有模式"},
			ProhibitedPoints: []string{"允许未记录的哈希漂移"},
		},
		Voice: VoiceSpec{
			Tone:              "professional",
			PreserveUserVoice: true,
		},
		MaterialPolicy: MaterialPolicy{
			UserMaterialPriority:  tc.priority,
			AllowExternalResearch: tc.external,
			ConflictHandling:      tc.conflict,
		},
		EvidencePolicy: EvidencePolicy{
			Level:             evidenceLevelForAssurance(tc.assurance),
			UnsupportedClaims: UnsupportedClaimsProhibit,
		},
		Delivery: DeliverySpec{
			Format:   DeliveryFormatMarkdown,
			Language: "zh-CN",
			Length:   LengthRange{Min: 5000, Max: 7000},
		},
		Collaboration: ExecutionControl{
			TaskMode:          tc.taskMode,
			OrchestrationMode: tc.mode,
			AssuranceLevel:    tc.assurance,
			ApprovalMode:      tc.approval,
		},
		SourceAttributions: []SourceAttribution{},
		Inferences: []Inference{
			{
				FieldPath:     "/content/topic",
				ProposedValue: "研究综述路径金样本主题",
				Confidence:    0.9,
				Status:        InferenceStatusAccepted,
				ReasonCode:    "golden_sample",
				Summary:       "由金样本测试固化的已接受推断",
			},
		},
	}
	recordedAt := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	controlPaths := []string{
		"/collaboration/task_mode",
		"/collaboration/orchestration_mode",
		"/collaboration/assurance_level",
		"/collaboration/approval_mode",
	}
	for _, fieldPath := range controlPaths {
		valueHash, err := contract.FieldValueHash(fieldPath)
		if err != nil {
			panic(err)
		}
		contract.SourceAttributions = append(contract.SourceAttributions, SourceAttribution{
			FieldPath:  fieldPath,
			Source:     AttributionSourceUser,
			ValueHash:  valueHash,
			RecordedAt: recordedAt,
		})
	}
	materialPath := "/material_policy/allow_external_research"
	materialSource := AttributionSourceUser
	if tc.externalRef {
		materialSource = AttributionSourcePlatformDefault
	}
	materialHash, err := contract.FieldValueHash(materialPath)
	if err != nil {
		panic(err)
	}
	contract.SourceAttributions = append(contract.SourceAttributions, SourceAttribution{
		FieldPath:  materialPath,
		Source:     materialSource,
		ValueHash:  materialHash,
		RecordedAt: recordedAt,
	})
	sealed, err := contract.WithComputedHash()
	if err != nil {
		panic(err)
	}
	return sealed
}

func evidenceLevelForAssurance(level AssuranceLevel) EvidenceLevel {
	switch level {
	case AssuranceLevelStrict:
		return EvidenceLevelStrict
	case AssuranceLevelSourced:
		return EvidenceLevelSourced
	case AssuranceLevelFlexible:
		return EvidenceLevelFlexible
	default:
		return EvidenceLevelStandard
	}
}
