# Task13 Production Wiring Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make both editions report truthful capability readiness and execute governed writing runs through a production-wired, shadow-only runtime, while keeping paid search source implementations Commercial-only.

**Architecture:** Shared packages define search, crawler, MCP, readiness, writingruntime, evidence, and shadow-storage contracts. OSS provides local search, crawler, MCP extension points, and explicit unavailable paid-provider shims; Commercial provides concrete paid providers behind the same contracts. The server composition root injects persistent adapters and exposes cached readiness without performing paid calls in health handlers.

**Tech Stack:** Go 1.25, PostgreSQL 17/ParadeDB, chi, Docker Compose, MCP JSON-RPC, writingplan/writingruntime/writingstore.

---

### Task 1: Make edition search boundaries explicit

**Files:**
- Modify: `backend/internal/tools/search.go`
- Modify: `backend/internal/tools/search_stubs.go` (OSS only)
- Create: `backend/internal/tools/search_capability_test.go`
- Modify: `backend/internal/tools/search_test.go` (Commercial only)

**Step 1: Write failing OSS tests**

Add tests asserting that an OSS client with paid-provider-looking values has no paid sources and that direct shim calls return a stable unavailable error rather than `(nil, nil)`:

```go
func TestOSSDoesNotAdvertisePaidSearchProviders(t *testing.T) {
    client := newSearchClientForCapabilityTest("paid-key", "paid-key")
    if client.HasExternalSources() {
        t.Fatal("OSS advertised paid search providers")
    }
}

func TestOSSPaidProviderFailsExplicitly(t *testing.T) {
    _, err := NewTavilyClient("key", "https://example.test", time.Second).
        Search(context.Background(), "query", 1)
    if !errors.Is(err, ErrProviderNotInstalled) {
        t.Fatalf("error=%v", err)
    }
}
```

**Step 2: Run the tests and verify they fail**

Run:

```bash
docker run --rm -e GOFLAGS=-mod=readonly \
  -v /tmp/luminbuddy-go-mod-cache:/go/pkg/mod \
  -v /tmp/luminbuddy-go-build-cache:/root/.cache/go-build \
  -v "$PWD":/src -w /src/backend golang:1.25 \
  go test ./internal/tools -run 'OSS|SearchCapability' -count=1
```

Expected: FAIL because paid shims currently return successful empty results and `HasSources` treats AnySearch as present.

**Step 3: Add stable capability errors and availability checks**

In `search.go` add:

```go
var (
    ErrProviderNotInstalled  = errors.New("search provider not installed")
    ErrProviderNotConfigured = errors.New("search provider not configured")
)

type SearchCapability struct {
    ProviderID string `json:"provider_id"`
    Kind       string `json:"kind"`
    Installed  bool   `json:"installed"`
    Configured bool   `json:"configured"`
}
```

Make `HasExternalSources` depend on installed and configured concrete providers. Keep local knowledge and crawler readiness separate. In OSS, every paid shim returns `ErrProviderNotInstalled` and reports `Installed=false`; it must never be appended to active sources.

Commercial tests must prove concrete adapters report installed but only configured when required settings are present.

**Step 4: Run both editions' tool tests**

Expected: PASS, and OSS tests must not import or assert details of a paid provider protocol.

**Step 5: Commit**

```bash
git add backend/internal/tools
git commit -m "fix: enforce search edition capability boundaries"
```

### Task 2: Replace optimistic health flags with cached readiness

**Files:**
- Create: `backend/internal/server/readiness.go`
- Create: `backend/internal/server/readiness_test.go`
- Modify: `backend/internal/server/server.go`
- Modify: `backend/internal/tools/deepseek.go`
- Modify: `backend/internal/tools/search.go`
- Modify: `backend/internal/config/config.go`

**Step 1: Write failing readiness tests**

Cover placeholder or missing keys, configured-but-unprobed providers, successful probes, authentication failure, timeout, and stale checks. Pin that `/health` never reports ready merely because a client pointer is non-nil.

```go
func TestReadinessDoesNotTreatConstructedClientsAsReady(t *testing.T) {
    registry := NewReadinessRegistry()
    registry.Set("llm", CapabilityReadiness{Installed: true, Configured: true})
    snapshot := registry.Snapshot(time.Now())
    if snapshot.Components["llm"].Ready {
        t.Fatal("configured but unprobed LLM reported ready")
    }
}
```

**Step 2: Run the server tests and verify failure**

Run `go test ./internal/server -run Readiness -count=1`.

**Step 3: Implement the readiness registry**

```go
type CapabilityReadiness struct {
    Installed     bool      `json:"installed"`
    Configured    bool      `json:"configured"`
    Reachable     bool      `json:"reachable"`
    Ready         bool      `json:"ready"`
    Degraded      bool      `json:"degraded"`
    ErrorCode     string    `json:"error_code,omitempty"`
    LastCheckedAt time.Time `json:"last_checked_at,omitempty"`
}
```

`/health` remains liveness. Add `/ready` for the detailed snapshot. Neither handler performs network calls. Add `LLMClient.IsConfigured()` using the EmbeddingClient placeholder rules. Search readiness comes from provider capability state, not object presence.

**Step 4: Normalize probe failures**

Map HTTP 401/403, deadline, 429 and 5xx to stable codes without logging bodies or headers. Store only the latest non-sensitive result.

**Step 5: Run tests and commit**

```bash
go test ./internal/server ./internal/tools -count=1
git add backend/internal/server backend/internal/tools backend/internal/config
git commit -m "feat: expose truthful runtime readiness"
```

### Task 3: Make MCP connection state observable and safe

**Files:**
- Modify: `backend/internal/mcp/registry.go`
- Modify: `backend/internal/mcp/registry_test.go`
- Modify: `backend/internal/server/handlers_admin_mcp.go`
- Modify: `backend/internal/server/server.go`
- Modify: `backend/internal/server/readiness_test.go`

**Step 1: Add failing tests**

Test zero-config state, failed connection state, connected tool counts, disconnect, and nil execution context. Pin that absent MCP configuration reports `unconfigured`, not ready, and `MCPAgentTool.Execute` cannot dereference a nil `ExecutionContext`.

**Step 2: Implement connection snapshots**

```go
type ServerStatus struct {
    Name        string    `json:"name"`
    Transport   string    `json:"transport"`
    Connected   bool      `json:"connected"`
    ToolCount   int       `json:"tool_count"`
    ErrorCode   string    `json:"error_code,omitempty"`
    LastChecked time.Time `json:"last_checked_at,omitempty"`
}
```

Registry retains failed configured servers as status entries while keeping clients and credentials private. Feed the aggregate into readiness. Keep in-process MCP disabled by default.

**Step 3: Fix nil execution context handling**

Resolve trace and user IDs through guarded local variables before logging or sandbox checks.

**Step 4: Verify and commit**

Run `go test ./internal/mcp ./internal/server -run 'MCP|Readiness' -count=1`, then commit `feat: expose governed MCP readiness`.

### Task 4: Persist rollout evidence through writingstore

**Files:**
- Create: `backend/internal/writingruntime/store_evidence.go`
- Create: `backend/internal/writingruntime/store_evidence_test.go`
- Modify: `backend/internal/writingstore/runtime.go`

**Step 1: Write adapter contract tests**

Use a recording `RuntimeEvidenceRecorder` to assert mapping from `RuntimeEvidence` to `writingstore.RuntimeEvidenceRecord`, including run/node/attempt identity, kind, time, and JSON payload. Reject missing identity or unsupported kinds.

**Step 2: Implement the adapter**

```go
type StoreRolloutEvidence struct {
    recorder RuntimeEvidenceRecorder
}

func NewStoreRolloutEvidence(recorder RuntimeEvidenceRecorder) (*StoreRolloutEvidence, error) {
    if recorder == nil {
        return nil, ErrRuntimeNotReady
    }
    return &StoreRolloutEvidence{recorder: recorder}, nil
}
```

`Record` marshals the existing evidence type and delegates to `RecordRuntimeEvidence`; do not add a second persistence model.

**Step 3: Run unit and real PostgreSQL tests**

Expected: append-only `writing_run_events` contains one idempotent rollout event; replay with a conflicting payload fails closed.

**Step 4: Commit**

Commit `feat: persist governed rollout evidence` in both editions.

### Task 5: Add durable isolated shadow content

**Files:**
- Create: `backend/internal/database/migrations/096_shadow_content.up.sql`
- Create: `backend/internal/database/migrations/096_shadow_content.down.sql`
- Create: `backend/internal/writingstore/shadow_content.go`
- Create: `backend/internal/writingstore/shadow_content_test.go`
- Create: `backend/internal/writingruntime/store_shadow_content.go`
- Create: `backend/internal/writingruntime/store_shadow_content_test.go`

**Step 1: Write migration and repository tests first**

Schema: opaque key primary key, policy hash, run ID, media type, body, content hash, created/expiry timestamps; no foreign key to canonical artifacts; indexes on expiry and `(policy_hash, run_id)`; same key may only be replayed with the same body hash.

**Step 2: Implement sink and reader**

The repository implements `Put`, `Get`, `DeletePrefix`, and `DeleteBefore`. Validate path segments before SQL. Prefix deletion preserves run boundaries. Reads verify the stored body hash.

**Step 3: Protect rollback**

The down migration refuses while shadow rows exist and never touches canonical artifacts or run evidence.

**Step 4: Run PostgreSQL and runtime tests**

Use a fresh ParadeDB instance and cover restart persistence and TTL cleanup.

**Step 5: Commit**

Commit `feat: persist isolated shadow content` in both editions.

### Task 6: Build the governed runtime composition root

**Files:**
- Create: `backend/internal/server/governed_runtime.go`
- Create: `backend/internal/server/governed_runtime_test.go`
- Modify: `backend/internal/server/server.go`
- Modify: `backend/internal/server/writing_api.go`
- Modify: `backend/internal/server/handlers_writing_run.go`
- Modify: `backend/internal/server/writing_routes_test.go`

**Step 1: Pin current failure**

Assert that a ready server injects a run controller, dispatches ordinary confirmed runs, requires approval for manual or high-risk runs, and returns structured `WRITING_RUNTIME_NOT_READY` before persistence when dependencies are missing.

**Step 2: Introduce a composition result**

```go
type governedRuntime struct {
    orchestrator *writingruntime.Orchestrator
    registry     *writingruntime.ExecutorRegistry
    evidence     *writingruntime.StoreRolloutEvidence
    shadow       writingruntime.ShadowContentSink
}
```

The builder receives writingstore, LLM/search/KB/MCP capabilities, metrics, and an edition manifest. Register only adapters whose dependencies are ready. Every adapter starts offline; baseline remains authoritative. Task13 cannot enable allowlist, percentage, or enabled.

**Step 3: Wire controls and dispatch**

Inject Orchestrator as `writingRunController`. Ordinary runs dispatch asynchronously with a bounded server-owned context after their transaction commits. If dispatch cannot be scheduled, append a stable transition rejection and expose the reason; do not leave an unexplained planned run.

**Step 4: Verify recovery and control**

Cover restart recovery, pause/resume/cancel, material snapshot reuse, search/MCP permission denial, evidence failure, and shadow isolation.

**Step 5: Commit**

Commit `feat: wire governed writing runtime` in both editions.

### Task 7: Add controlled provider preflight and deployment configuration

**Files:**
- Create: `backend/internal/server/provider_preflight.go`
- Create: `backend/internal/server/provider_preflight_test.go`
- Modify: `backend/internal/server/server.go`
- Modify: `backend/.env.example`
- Modify: `docker-compose.yml`
- Modify: `docs/runbook.md`

**Step 1: Write fake-server tests**

Use `httptest.Server` and a local MCP server. Cover valid response, 401, 429, timeout, malformed payload, and recovery after credential update. Prove probes redact secrets.

**Step 2: Implement explicit and opt-in probes**

Startup validates configuration only. Network probes run through an admin action or opt-in `PROVIDER_PREFLIGHT_ENABLED` loop with a long interval and strict request budget. OSS never probes commercial providers.

**Step 3: Document edition deployments**

Shared examples contain local search, crawler, MCP, LLM, embedding, and readiness options. Paid provider variables exist only in Commercial deployment files. Add commands for liveness, readiness, MCP status, credential testing, and rollback.

**Step 4: Commit**

Commit `feat: add provider production preflight` separately in OSS and Commercial.

### Task 8: Run the Task13 acceptance matrix

**Files:**
- Create: `docs/releases/2026-08-30-task13-production-wiring-readiness.md` (OSS)
- Modify: `FILE_INDEX.md`

**Step 1: Run static and unit validation**

For both editions:

```bash
go test ./... -count=1
go test -race ./internal/writingruntime ./internal/writingstore ./internal/mcp ./internal/server -count=1
go vet ./...
```

**Step 2: Run fresh-database validation**

Apply migrations through 096, execute writingstore integration tests, restart the backend, verify evidence/shadow persistence, then exercise 096 down protection.

**Step 3: Run deployment smoke tests**

Verify liveness separately from readiness. OSS shows local/crawler/MCP capability without paid source details. Commercial rejects invalid paid credentials with stable codes and becomes ready only after valid credentials pass preflight.

**Step 4: Run real writing acceptance**

Execute long-form writing, multi-material synthesis, and faithful rewrite using real LLM output. Verify Candidate Draft / Accepted Draft / Verified Deliverable transitions, citations, context-drift checks, cost/latency evidence, pause/cancel, and shadow-only isolation.

**Step 5: Record the release conclusion**

Mark every component ready, degraded, blocked, or disabled. Task13 may authorize internal shadow only; allowlist requires a separate decision based on durable evidence.

**Step 6: Commit**

Commit `docs: record task13 production wiring acceptance`.
