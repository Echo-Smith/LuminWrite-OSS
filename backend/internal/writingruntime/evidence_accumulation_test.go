package writingruntime

// The allowlist evidence accumulation harness reruns the governed vertical
// scenarios against a durable allowlist policy so comparison evidence keeps
// accruing under that policy's hash. Everything here stays inside the local
// shadow contract: the allowlist subject never appears in these runs, the
// canonical store never receives shadow content, and no approval or
// activation is performed — only evidence health is assessed.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

const evidenceHarnessActivationKey = "change-local-evidence-accumulation"

// allowlistPolicyForHarness derives the governed allowlist policy for one
// vertical node: identity fields stay aligned with the shadow candidate so
// bindRolloutPolicy accepts it, and the hash is stable across invocations
// because every identity field is deterministic.
func allowlistPolicyForHarness(capability, candidateID string) AdapterRolloutPolicy {
	policy := DefaultShadowPolicy(candidateID, AdapterFamilyEngine, capability, "1.0.0")
	policy.PolicyVersion = 2
	policy.Mode = RolloutAllowlist
	policy.ActivationKey = evidenceHarnessActivationKey
	policy.AllowSubjects = []string{"user_operator_first"}
	policy.Reason = "local allowlist evidence accumulation; no production authorization"
	policy, _ = policy.WithComputedHash()
	return policy
}

func TestAllowlistEvidenceAccumulationHarness(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("TASK13_LLM_API_KEY"))
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("TASK13_LLM_BASE_URL")), "/")
	model := strings.TrimSpace(os.Getenv("TASK13_LLM_MODEL"))
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if apiKey == "" || baseURL == "" || model == "" || databaseURL == "" {
		t.Skip("evidence accumulation requires TASK13_LLM_API_KEY, TASK13_LLM_BASE_URL, TASK13_LLM_MODEL, and TEST_DATABASE_URL")
	}
	if model != task13LiveModel {
		t.Fatalf("TASK13_LLM_MODEL=%q; evidence accumulation is pinned to %s", model, task13LiveModel)
	}
	db, err := database.NewPostgres(databaseURL, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(db); err != nil {
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
		governedIndex int
		nodes         func(*tools.LLMClient) []verticalNode
	}{
		{name: "long_form", governedIndex: 1, nodes: liveLongFormNodes},
		{name: "multi_material", governedIndex: 1, nodes: liveMultiMaterialNodes},
		{name: "faithful_rewrite", governedIndex: 0, nodes: liveFaithfulRewriteNodes},
	} {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			invocation := time.Now().UTC().Format("20060102150405")
			scenarioName := "evidence_" + scenario.name
			runID := "run_" + scenarioName + "_" + invocation
			documentID := "doc_" + scenarioName + "_" + invocation
			var governedPolicy AdapterRolloutPolicy
			backend := verticalRolloutBackend{
				evidence: durableEvidenceMirror{persistent: WritingStoreEvidenceStore{Recorder: persistent},
					memory: &MemoryRolloutEvidenceStore{}},
				sink: &durableShadowTracker{persistent: WritingStoreShadowContentSink{Store: persistent, TTL: DefaultShadowContentTTL}},
				ids: func(string) (string, string) { return runID, documentID },
				nodePolicy: func(index int, capability, candidateID string) *AdapterRolloutPolicy {
					if index != scenario.governedIndex {
						return nil
					}
					governedPolicy = allowlistPolicyForHarness(capability, candidateID)
					policy := governedPolicy
					return &policy
				},
				prepare: livePreparePersistentLineage(persistent, db),
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
					return nil
				},
			}
			// The governed node routes by run id (no explicit subject): the run
			// id is deliberately absent from AllowSubjects, so the lane stays
			// baseline while the shadow comparison accrues fresh evidence.
			result := runVerticalScenarioWithBackend(t, scenarioName, scenario.nodes(client), true, 150*time.Second, backend)
			if result.outcome.State != StateCompleted {
				t.Fatalf("evidence scenario did not complete: %#v", result.outcome)
			}
			if len(result.policyHashes) == 0 {
				t.Fatal("governed policy hash was not surfaced")
			}
			if !strings.HasPrefix(result.policyHashes[0], "sha256:") {
				t.Fatalf("governed policy hash malformed: %q", result.policyHashes[0])
			}
			assessHarnessGate(t, db, governedPolicy, "after "+scenario.name)
		})
	}
}

func assessHarnessGate(t *testing.T, db *database.DB, policy AdapterRolloutPolicy, stage string) {
	t.Helper()
	store, err := writingstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	gate := AllowlistPromotionGate{Store: store, Criteria: DefaultPromotionCriteria()}
	assessment, err := gate.EvidenceAssessment(context.Background(), policy)
	if err != nil {
		t.Fatalf("%s gate assessment failed: %v", stage, err)
	}
	payload, err := json.Marshal(assessment)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s evidence gate: allowed=%t reasons=%v comparisons=%d failures=%d last=%s",
		stage, assessment.Allowed, assessment.Reasons, assessment.Health.ComparisonRecords,
		assessment.Health.FailedRecords, assessment.Health.LastRecordedAt.Format(time.RFC3339))
	_ = payload
	// The harness only observes the gate; it never approves or activates.
}
