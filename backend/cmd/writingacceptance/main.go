// Task13 acceptance driver: exercises the deployed governed writing stack
// over real HTTP using the production kernel/plan code to build payloads.
//
// Usage (inside the backend module):
//
//	go run ./cmd/writingacceptance -base-url http://localhost:8080 \
//	  -database-url postgres://postgres:postgres@localhost:5432/writing_agent_v2?sslmode=disable \
//	  -jwt-secret change-this-in-production -fixture ../specs/lcp/v1/fixtures/writing-contract.valid.json \
//	  -chain long_form
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

const testUserID = "00000000-0000-0000-0000-000000000001"

type apiClient struct {
	baseURL string
	token   string
}

func (c *apiClient) call(method, path string, body any, out any) error {
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	if body != nil {
		request.Header.Set("Idempotency-Key", path+"-"+fmt.Sprint(time.Now().UnixNano()))
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var payload struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return fmt.Errorf("%s %s: status %d: %w", method, path, response.StatusCode, err)
	}
	if response.StatusCode >= 300 || !payload.Success {
		return fmt.Errorf("%s %s: status %d code %s message %s", method, path, response.StatusCode,
			payload.Error.Code, payload.Error.Message)
	}
	if out != nil {
		return json.Unmarshal(payload.Data, out)
	}
	return nil
}

func mintToken(secret, userID string) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]any{
		"sub": userID, "role": "user", "jti": "acceptance-session",
		"iat": time.Now().Unix(), "exp": time.Now().Add(2 * time.Hour).Unix(),
	})
	if err != nil {
		return "", err
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := header + "." + payloadB64
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func reseal(contract writingkernel.WritingContract) (writingkernel.WritingContract, error) {
	for index := range contract.SourceAttributions {
		hash, err := contract.FieldValueHash(contract.SourceAttributions[index].FieldPath)
		if err != nil {
			return contract, err
		}
		contract.SourceAttributions[index].ValueHash = hash
	}
	return contract.WithComputedHash()
}

type runSnapshot struct {
	RunID   string `json:"run_id"`
	Status  string `json:"status"`
	Nodes   int
	Events  int64
	Outcome string
}

func waitForRun(client *apiClient, runID string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	lastStatus := ""
	for time.Now().Before(deadline) {
		var run struct {
			RunID  string `json:"run_id"`
			Status string `json:"status"`
		}
		if err := client.call(http.MethodGet, "/api/v2/runs/"+runID, nil, &run); err != nil {
			return "", err
		}
		if run.Status != lastStatus {
			fmt.Printf("  [%s] status → %s\n", runID[:16], run.Status)
			lastStatus = run.Status
		}
		switch run.Status {
		case "completed", "failed", "cancelled":
			return run.Status, nil
		}
		time.Sleep(4 * time.Second)
	}
	return "timeout", nil
}

func ensureUser(db *sql.DB, userID string) error {
	uid := "acceptance-" + userID[:8]
	_, err := db.Exec(`
		INSERT INTO users (id, uid, name) VALUES ($1, $2, 'task13 acceptance')
		ON CONFLICT (id) DO NOTHING
	`, userID, uid)
	return err
}

func insertMaterial(db *sql.DB, userID, materialID, title, body string) error {
	_, err := db.Exec(`
		INSERT INTO user_materials (id, user_id, title, content_preview, source_type, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'text', now(), now())
		ON CONFLICT (id) DO NOTHING
	`, materialID, userID, title, body)
	return err
}

func runChain(client *apiClient, db *sql.DB, fixture []byte, suffix, title, operation, orchestrationMode string, materials bool, budget writingplan.PlanBudget) (string, error) {
	var contract writingkernel.WritingContract
	if err := json.Unmarshal(fixture, &contract); err != nil {
		return "", err
	}
	// Chain variants re-seal the contract fields they legitimately change:
	// the orchestration mode picks the governing template, and faithful
	// rewrite runs with the polish operation. A unique contract id keeps
	// repeated acceptance runs independent.
	contract.ContractID = fmt.Sprintf("ctr_acc_%s_%d", suffix, time.Now().UnixNano())
	if orchestrationMode != "" {
		contract.Collaboration.OrchestrationMode = writingkernel.OrchestrationMode(orchestrationMode)
	}
	// Acceptance runs at standard assurance: the sourced evidence validator is
	// not part of the registered capability set in this edition.
	contract.Collaboration.AssuranceLevel = writingkernel.AssuranceLevelStandard
	contract.EvidencePolicy.Level = writingkernel.EvidenceLevelStandard
	if operation != "" && operation != string(contract.Intent.Operation) {
		contract.Intent.Operation = writingkernel.Operation(operation)
	}
	draft := contract
	draft.Status = writingkernel.ContractStatusDraft
	draftContract, err := reseal(draft)
	if err != nil {
		return "", err
	}
	documentMetadata := map[string]any{}
	if materials {
		materialID := "9a1b2c3d-0000-4000-8000-0000000000" + suffix
		if err := insertMaterial(db, testUserID, materialID, "森林生态观察笔记", "森林深处的狐狸在晨雾中活动，狐狸是森林生态的重要成员，晨雾为观测提供了独特的光照条件。"); err != nil {
			return "", err
		}
		documentMetadata["material_refs"] = []map[string]any{{
			"material_id": materialID, "title": "森林生态观察笔记", "source_kind": "text",
			"source_ref": "mem://forest-fox",
		}}
	}
	var document writingstoreDocument
	if err := client.call(http.MethodPost, "/api/v2/documents", map[string]any{
		"title": title, "metadata": documentMetadata}, &document); err != nil {
		return "", fmt.Errorf("create document: %w", err)
	}
	var putContract writingstoreContract
	if err := client.call(http.MethodPost, "/api/v2/documents/"+document.DocumentID+"/contracts",
		map[string]any{"contract": draftContract}, &putContract); err != nil {
		return "", fmt.Errorf("put contract: %w", err)
	}
	confirmed := contract
	confirmed.Version = draftContract.Version + 1
	confirmedContract, err := reseal(confirmed)
	if err != nil {
		return "", err
	}
	var confirmedRecord writingstoreContract
	if err := client.call(http.MethodPost, "/api/v2/contracts/"+confirmedContract.ContractID+"/confirm",
		map[string]any{"previous_version": draftContract.Version, "contract": confirmedContract}, &confirmedRecord); err != nil {
		return "", fmt.Errorf("confirm contract: %w", err)
	}
	intent := writingplan.IntentPlan{IntentPlanID: "iplan_acceptance_" + suffix,
		ContractRef: writingplan.ObjectRef{ID: confirmedContract.ContractID, Version: confirmedContract.Version, Hash: confirmedContract.ContractHash},
		Summary:     "task13 acceptance: " + title, CreatedBy: "user",
		CreatedAt:     time.Now().UTC(),
		ProposedSteps: []writingplan.ProposedStep{{StepID: "write", Objective: "produce the governed draft", CapabilityHint: "writing.draft"}}}
	intent, err = intent.WithComputedHash()
	if err != nil {
		return "", err
	}
	var preview struct {
		Envelope    writingplan.WritingPlanEnvelope `json:"plan"`
		Permissions []writingplan.Permission        `json:"permissions"`
	}
	if err := client.call(http.MethodPost, "/api/v2/documents/"+document.DocumentID+"/plans", map[string]any{
		"contract_id": confirmedContract.ContractID, "contract_version": confirmedContract.Version,
		"base_version_id": "", "intent_plan": intent, "budget": budget,
		"required_final_artifact": "revision_set",
	}, &preview); err != nil {
		return "", fmt.Errorf("compile plan: %w", err)
	}
	fmt.Printf("  plan %s trust=%s approval_required=%v nodes=%d\n", preview.Envelope.ExecutablePlan.PlanID[:20],
		preview.Envelope.ExecutablePlan.TrustLevel, preview.Envelope.StrategyDecision.ApprovalRequired, len(preview.Envelope.ExecutablePlan.Nodes))
	var run struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	if err := client.call(http.MethodPost, "/api/v2/runs", map[string]any{
		"document_id": document.DocumentID, "contract_id": confirmedContract.ContractID,
		"contract_version": confirmedContract.Version, "contract_hash": confirmedContract.ContractHash,
		"base_version_id": "", "plan": preview.Envelope, "budget": budget,
		"permissions": preview.Permissions,
	}, &run); err != nil {
		return "", fmt.Errorf("create run: %w", err)
	}
	if run.Status == "awaiting_approval" {
		fmt.Println("  run awaits approval; approving")
		if err := client.call(http.MethodPost, "/api/v2/runs/"+run.RunID+"/approve", map[string]any{
			"plan_id": preview.Envelope.ExecutablePlan.PlanID, "plan_version": 1,
			"plan_hash": preview.Envelope.ExecutablePlan.PlanHash, "permissions": preview.Permissions,
		}, nil); err != nil {
			return "", fmt.Errorf("approve run: %w", err)
		}
	}
	status, err := waitForRun(client, run.RunID, 20*time.Minute)
	if err != nil {
		return "", err
	}
	fmt.Printf("  chain %s finished: %s\n", suffix, status)
	return status, nil
}

type writingstoreDocument struct {
	DocumentID string         `json:"document_id"`
	Metadata   map[string]any `json:"metadata"`
}

type writingstoreContract struct {
	Contract writingkernel.WritingContract `json:"contract"`
	Version  int                           `json:"version,omitempty"`
}

func main() {
	baseURL := flag.String("base-url", "http://localhost:8080", "governed API base URL")
	databaseURL := flag.String("database-url", "postgres://postgres:postgres@localhost:5432/writing_agent_v2?sslmode=disable", "host database URL")
	jwtSecret := flag.String("jwt-secret", "change-this-in-production", "JWT secret")
	fixturePath := flag.String("fixture", "specs/lcp/v1/fixtures/writing-contract.valid.json", "valid contract fixture")
	chain := flag.String("chain", "all", "long_form | multi_material | faithful_rewrite | all")
	flag.Parse()

	db, err := sql.Open("postgres", *databaseURL)
	if err != nil {
		fmt.Println("open database:", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := ensureUser(db, testUserID); err != nil {
		fmt.Println("ensure user:", err)
		os.Exit(1)
	}
	token, err := mintToken(*jwtSecret, testUserID)
	if err != nil {
		fmt.Println("mint token:", err)
		os.Exit(1)
	}
	client := &apiClient{baseURL: *baseURL, token: token}
	fixture, err := os.ReadFile(*fixturePath)
	if err != nil {
		fmt.Println("read fixture:", err)
		os.Exit(1)
	}
	// The budget covers every node's worst-case bounds (the compiler sums
	// node.Bounds.MaxCostUSD), not the expected cost.
	budget := writingplan.PlanBudget{MaxCostUSD: 30, MaxDurationMS: 3600000, MaxConcurrency: 1, MaxNodes: 10, MaxItems: 4}

	failures := 0
	run := func(name string, fn func() (string, error)) {
		fmt.Printf("── chain %s ──\n", name)
		status, err := fn()
		if err != nil || status != "completed" {
			fmt.Printf("  chain %s FAILED: status=%s err=%v\n", name, status, err)
			failures++
			return
		}
		fmt.Printf("  chain %s PASSED\n", name)
	}
	if *chain == "all" || *chain == "long_form" {
		run("long_form", func() (string, error) {
			return runChain(client, db, fixture, "10", "Task13 验收：长文创作", "", "outline_first", false, budget)
		})
	}
	if *chain == "all" || *chain == "multi_material" {
		run("multi_material", func() (string, error) {
			return runChain(client, db, fixture, "20", "Task13 验收：多材料综合", "", "outline_first", true, budget)
		})
	}
	if *chain == "all" || *chain == "faithful_rewrite" {
		run("faithful_rewrite", func() (string, error) {
			return runChain(client, db, fixture, "30", "Task13 验收：忠实改写", "polish", "auto", true, budget)
		})
	}
	if failures > 0 {
		fmt.Printf("acceptance finished with %d failed chains\n", failures)
		os.Exit(1)
	}
	fmt.Println("acceptance finished: all chains completed")
}
