# LuminBuddy V2

[中文](README.md) · [Live workspace](https://luminbuddy2.ericdocmic.top/v2/) · [Changelog](#changelog)

**Edition: OSS** — includes the governed writing kernel, MCP, baseline retrieval, and extension contracts; excludes commercial paid-search providers, credential settings, and proprietary connectors.

> **An AI writing workspace for Chinese content creators**: from intent understanding, source retrieval, and outline confirmation, to style-aware drafting, post-review, feedback, and memory sedimentation—transforming one-shot generation into an observable, interruptible, and iterative writing process.

![LuminBuddy writing workspace](docs/assets/luminbuddy-workspace.png)

---

## About LuminBuddy

**LuminBuddy** is an AI writing assistant designed for Chinese content production. It doesn't pursue "one-click generation" magic, but instead breaks down the writing process into observable, interruptible, and iterative engineering workflows—keeping creators in control at key decision points while AI assists in source gathering, structure planning, style adaptation, and quality checking.

**Current maturity: Engineering Beta; the governed kernel is connected to the primary flow.** Task1–13 delivered WritingContract, plan compilation, LCP/Document AST, typed Artifacts, transactional storage, runtime orchestration, three quality gates, V2 APIs, a document-first workspace, governed materials, shadow rollout, and production wiring. Code, builds, CI, and three real writing paths in an isolated environment pass; production traffic still requires staging evidence, recovery/cancel/rollback drills, and credential rotation.

### Governed Writing Runtime

The [Governed Writing Runtime](docs/19-governed-writing-runtime.md) is the single authoritative writing kernel for V2. Every new task binds a versioned WritingContract and a statically validated ExecutablePlan. Models and legacy operators may submit only typed Artifacts or Revisions; they cannot bypass quality gates to overwrite the official document.

- **The document is the primary object:** chat changes the contract, explains decisions, and controls execution; body text, versions, citations, and quality states belong to the document.
- **writingstore is the sole source of truth:** Contract, Plan, Run, Artifact, Decision, Ledger, Snapshot, and canonical/shadow content are persisted together.
- **Quality states cannot be fabricated:** Candidate Draft, Accepted Draft, and Verified Deliverable advance monotonically. A BLOCKER, a missing required validator, or a missing complete snapshot prevents Verified.
- **Legacy capabilities are governed adapters:** Harness, Pipeline, and Editorial Roles run through ExecutorAdapter and do not form a parallel source of truth. Contract, permission, budget, or lineage violations fail closed.
- **Release claims stay evidence-based:** process health, run completion, or successful Markdown parsing does not mean a deliverable was accepted, verified, or approved for production traffic.

---

## Problems Solved

General chat models can draft quickly, but real writing work usually fails at these points:

| Pain Point | Manifestation | LuminBuddy's Solution |
|------------|--------------|----------------------|
| **Intent drifts in long context** | Requirements, length, style, and evidence constraints are gradually lost | WritingContract records explicit choices, inference sources, and delivery criteria |
| **Generation is a black box** | Materials, planning, execution, and drafting collapse into one opaque response | ExecutablePlan, RunLedger, and durable events make work observable, cancellable, and recoverable |
| **Materials lose provenance** | Multi-source synthesis drops conflicts, snapshots, or citation relationships | MaterialAdapter, SourcePack, content snapshots, and lineage govern every input |
| **Users lose control at costly decisions** | A system can execute expensive or risky work before review | Automatic and manual strategies coexist; high-cost, high-risk, and manual tasks require confirmation |
| **“Generated” is mistaken for deliverable** | Correct formatting does not prove facts, style, or contract compliance | Candidate / Accepted / Verified gates with non-waivable BLOCKER findings |
| **Failures create conflicting versions** | Retries, restarts, and duplicate events can corrupt state | Idempotent commits, complete checkpoints, snapshots, pause/cancel/rollback, and shadow isolation |

---

## How It Works

### Core Architecture: Contract-Driven Document Runtime

```text
User request / materials / explicit strategy
  → WritingContract (goal, style, evidence, budget, permissions, approval)
  → Strategy Compiler → ExecutablePlan (bounded DAG + capability binding)
  → Orchestrator → ExecutorAdapter (Harness / Engine Step / Editorial Role)
  → typed Artifact + lineage + usage
  → LCP / Document AST / RevisionSet
  → Quality Gate (Candidate → Accepted → Verified)
  → writingstore atomically commits DocumentVersion + Snapshot + RunLedger
  → frontend projects the document, run summary, detail report, and chat controls
```

The compiler selects among templates, the capability registry, and constrained extension strategies. Unknown capabilities, unbounded retries, missing inputs, permission violations, budget overflow, and missing final artifacts are rejected before execution.

### Automatic Execution and User Control

- Ordinary tasks: execute immediately after the system compiles the plan; the plan remains visible and cancellable.
- High-cost or high-risk tasks: require confirmation of plan and budget.
- Manual mode: explicit user choices win; system recommendations cannot silently replace them.
- Mid-run changes: chat updates the contract or sends control commands; provisional stream content cannot overwrite a committed document version.

### Smart Context Management

**Compaction (dialog compression)**: When dialog history exceeds threshold (10 messages / 6000 tokens), automatically compress old messages into summary, frontend displays saved token count.

**On-demand retrieval (retrieve_context)**: LLM can actively query based on current task needs:
- `article` — specific paragraphs of current article
- `memory` — user's writing preferences and historical memory
- `history` — current dialog history
- `search` — collected search sources
- `profile` — current style configuration details

System Prompt reduced from full injection (3000+ tokens) to resident layer (500-800 tokens), more precise information, more ample context window.

### Writing Flow

```text
Create or revise WritingContract
  → normalize user materials, web sources, and knowledge results
  → compile and validate ExecutablePlan
  → execute automatically or await confirmation by cost/risk/user policy
  → produce typed Outline, SectionDraft, ResearchNote, and ClaimMap Artifacts
  → compile Document AST and create Candidate Draft
  → validate contract, style, facts, citations, and safety
  → advance to Accepted Draft only with no BLOCKER
  → produce Verified Deliverable only after required validators and complete Snapshot
  → continue user edits, RevisionSets, feedback, evaluation, and memory in one document lifecycle
```

---

## Features

### Writing Core

| Feature | Description |
|---------|-------------|
| **WritingContract** | Captures task mode, style, materials, evidence, budget, permissions, approval, and delivery requirements |
| **Adaptive Planning** | T1–T4 trust levels, capability binding, static validation, and explicit user strategy precedence |
| **Three Writing Scenarios** | Long-form creation, multi-material synthesis, faithful rewrite, and their fail-closed paths |
| **Material Governance** | Material/SourcePack/ClaimMap Artifacts, conflict Findings, citations, and content snapshots |
| **Style Configuration** | Style Profile independently managed, supports versioning, grayscale release and rollback |
| **Document-First Workspace** | Document stage, collapsible global panel, run summary, detail tabs, and resizable bottom chat |
| **Durable Run Control** | WebSocket durable events, pause/resume/cancel, idempotent commit, and restart recovery |
| **Rich Text Editor** | Tiptap/ProseMirror editor with bold, lists, blockquotes, code blocks |
| **Three Quality States** | Candidate / Accepted / Verified, validator degradation, BLOCKER, and full audit reports |
| **Isolated Rollout** | off/shadow/allowlist/percentage policy, shadow isolation, TTL, and rollback protection |

### Writing Toolset

The governed runtime schedules built-in and adapted tools through the Capability Registry. Tool output must become a typed Artifact and cannot directly commit the official document:

| Tool | Purpose |
|------|---------|
| `search_web` | Search internet for latest information (pluggable search sources) |
| `search_knowledge` | Retrieve internal knowledge base examples and style guidelines |
| `read_source` | Read detailed content of search results |
| `generate_outline` | Generate article outline for user confirmation |
| `write_article` | Start streaming output of complete article |
| `review_article` | Quality review of article |
| `revise_section` | Directional modification of article section |
| `word_count_check` | Check if word count meets style requirements |
| `rewrite_title` | Generate 3 alternative titles with recommendations |
| `fact_check` | Extract factual claims and verify through search |
| `retrieve_context` | On-demand retrieval of session context |

> **Search boundary:** OSS includes the common retrieval contracts, local knowledge retrieval, bounded shared web fetching, and extension interfaces. It does not register commercial paid providers or include their credential variables or CLIs.

### Online Editing & Export

- **Rich Text Editing**: Tiptap/ProseMirror WYSIWYG editor supporting bold, italic, lists, blockquotes, code blocks and other Markdown formats
- **Multi-format export**: Markdown (.md) / Word (.doc) / PDF (print mode)
- **Pure frontend implementation**: No backend API needed, browser directly generates files

### Memory System

Four-tier memory architecture:

| Tier | Type | Purpose |
|------|------|---------|
| Tier 1 | Hard preferences | User explicitly set writing preferences |
| Tier 2 | Behavioral patterns | Automatically extracted writing habits |
| Tier 3 | Feedback signals | User feedback-driven improvement signals |
| Tier 4 | Entity network | Topic, person, concept relationship graph |

Supports file-layer bidirectional sync (Markdown file ↔ database), human-readable and editable.

### Editorial Multi-Agent Collaboration

Research, writing, and review roles are governed Executor capabilities behind the shared Orchestrator:

```
WritingContract → constrained role plan → research/writing/review Artifacts
  → shared quality gates → document version or human decision
```

- **Role-based Agent Executor** (RoleAgentRunner): Each Agent has independent Persona, toolset, and signal tools
- **Tool Registry Management** (EditorialToolRegistry): Add new tools with `Register`, no switch-case modification needed
- **Single commit path**: Roles return ExecutionResult; only the Orchestrator writes to writingstore
- **Missing dependencies fail closed**: An incomplete registry, permission, input, or usage record cannot become success
- **Three-layer Model**: Event (objective facts) + Decision (human/system choices) + Transition (state changes)
- **Quality Routing**: Source count, information gaps, verified claims auto-scored, auto-advance when passing
- **Agent Reputation**: Records success rate, Token cost, quality scores
- **Controlled Experiments**: Pipeline / Harness / Editorial three-group blind evaluation (six-dimension LLM scoring)

### A2A Agent Card

Implements the Agent Card concept from the A2A (Agent-to-Agent) protocol, where each Agent role has a self-describing JSON document supporting capability discovery:
- **Identity**: Name, role, description, version
- **Capabilities**: Producibles/consumables Artifact types, decision types
- **Skills**: Tool list
- **Constraints**: Isolation requirements, Persona

### Authentication & Security

- **Passkey/WebAuthn**: Passwordless login, device-level security
- **Guest Mode**: Experience without registration, supports subsequent upgrade
- **Prompt Injection Defense**: Input sanitization (SanitizeExternalContent) + System Prompt 7 defense directives
- **Security Audit Persistence**: All security events recorded to database, supports historical query and compliance audit
- **RBAC Fine-grained Permissions**: Role + permission management, supports custom roles and permission assignment
- **MCP Security Sandbox**: Tool call policy control, domain restrictions, resource limits, violation audit

### MCP Bidirectional Integration

- **MCP Client**: Supports stdio and SSE transports, connects to external MCP servers
- **MCP Server**: In-process MCP Server, exposes local tools via JSON-RPC 2.0
- **Tool Registry**: Unified management of built-in tools, MCP tools, and Pipeline steps, naming `mcp__server__tool`
- **Admin Dashboard**: Visual management of MCP server connection status and tool discovery

### Admin Dashboard

- **Style Management**: Profile creation, editing, version control, grayscale release
- **Model Configuration**: Multi-model access (DeepSeek/OpenAI compatible), key management, custom HTTP headers, Reasoning Effort
- **A/B Evaluation**: Control/experiment group automated evaluation and metric comparison
- **Luminbuddy Eval Center**: WABench dataset management, frozen candidates, Shadow Run, manual review, badcase and release evidence
- **Feedback Analysis**: Section feedback statistics, quality trends
- **Audit Logs**: Operation tracking, security audit
- **Token Monitoring**: Usage statistics, cost analysis
- **Security Audit**: Prompt Injection event statistics, interception trends, attack pattern analysis
- **RBAC Management**: Role creation, permission assignment, user-role binding

---

## Technical Architecture

```text
┌──────────────────────────────────────────────────────────────────┐
│ React writing workspace                                          │
│ global panel │ document stage │ run summary │ details │ chat     │
└───────────────────────────┬──────────────────────────────────────┘
                            │ REST + WebSocket durable events
┌───────────────────────────▼──────────────────────────────────────┐
│ Go API / Governed Composition Root                               │
│ Contract API │ Plan/Run API │ Document API │ Quality/Audit API   │
├──────────────────────────────────────────────────────────────────┤
│ writingkernel → writingplan → writingruntime → writingquality    │
│ Contract       Compiler       Orchestrator      Validators        │
│ LCP/AST        Registry       Recovery/Rollout  Quality Gates     │
├──────────────────────────────────────────────────────────────────┤
│ ExecutorAdapter: Harness Core │ Engine Step │ Editorial Role      │
│ Shared capabilities: local retrieval │ web fetch │ MCP │ memory  │
├──────────────────────────────────────────────────────────────────┤
│ writingstore: Contract / Plan / Run / Artifact / Decision /       │
│ Ledger / DocumentVersion / Snapshot / canonical / shadow          │
└───────────────────────────┬──────────────────────────────────────┘
                            │
┌───────────────────────────▼──────────────────────────────────────┐
│ PostgreSQL 17 + pgvector + ParadeDB │ Redis wake-up │ object data │
└──────────────────────────────────────────────────────────────────┘
```

### Tech Stack

| Layer | Technology |
|-------|------------|
| Frontend | React 19, Vite, TypeScript, Tailwind CSS, shadcn/ui, Tiptap/ProseMirror |
| Backend | Go 1.25+, chi router, coder/websocket |
| Database | PostgreSQL 17 + pgvector + paradedb (BM25) |
| LLM | DeepSeek API (default), supports OpenAI compatible interface |
| Embedding | DashScope text-embedding-v3 (1024-dim) |
| Deployment | Docker Compose, 1Panel |
| Runtime Protocol | LCP v1, WritingContract, ExecutablePlan, typed Artifact, durable RunEvent |
| Monitoring | Prometheus metrics + slog structured logging + RunLedger + Trace tracking |

---

## Quick Start

### Docker Compose (recommended)

```bash
cp .env.docker.example .env.docker
# Edit .env.docker with only the model, embedding, and database settings you need locally
docker compose up -d
```

Frontend is at `http://localhost:3002`. `/api/v2/health` only means the process is alive; check `http://localhost:8080/api/v2/ready` for production dependencies. Never commit real credentials.

### Local Development

```bash
# Backend
cd backend && cp .env.example .env && go run ./cmd/server/

# Frontend
cd frontend && npm ci && npm run dev
```

### Validation

```bash
make verify
docker compose config --quiet
```

`make verify` runs the Go build and full test suite plus frontend lint, production build, and WaBench tests. PostgreSQL integration tests should use the CI-equivalent ParadeDB image with both `vector` and `pg_search`, and set `TEST_DATABASE_URL`.

### Deployment Packaging

```bash
# Source only
./scripts/pack-for-1panel.sh

# Source + Docker images (recommended for China servers)
./scripts/pack-for-1panel.sh --images
```

### Search Source Extension

OSS shares:

- common `SearchClient` / `KnowledgeSearcher` contracts and the Capability Registry;
- local knowledge retrieval plus bounded shared web fetching and extraction;
- MCP Client/Server, discovery, readiness, and sandboxing;
- fail-closed stubs and tests for custom search adapters.

Paid provider implementations, credential variables, and commercial CLIs are not part of OSS. A custom source must return governed Source/SourcePack Artifacts and declare permissions, timeout, cost, and stable errors:

```go
// Example: implement a custom search source
type MySearchClient struct { /* ... */ }

func NewMySearchClient(/* params */) *MySearchClient { /* ... */ }

func (c *MySearchClient) Search(ctx context.Context, query string, limit int) ([]engine.SearchResult, error) {
    // Return results with source, timestamp, and provenance metadata
}
```

Do not turn an uninstalled provider into fake success or an unexplained empty result. `/ready` distinguishes installed, configured, reachable, and ready.

---

## Design Documents

| Document | Contents |
|----------|----------|
| [Architecture Blueprint](docs/01-architecture.md) | Agent engine, data flow, system boundaries |
| [Harness Design](docs/architecture-c-design.md) | Single-layer LLM orchestrator design and tool granularity |
| [Database Schema](docs/02-database-schema.md) | PostgreSQL, pgvector, migrations |
| [API Spec](docs/03-api-specification.md) | REST and WebSocket protocol |
| [Style Profile](docs/04-style-profile.md) | Style versioning, rollout, rollback |
| [Grayscale Routing](docs/05-grayscale-routing.md) | Profile flags and UID hash routing |
| [Evaluation](docs/06-evaluation.md) | Evaluation sets and triggers |
| [Feedback](docs/07-feedback.md) | Section feedback and reputation weights |
| [Admin Dashboard](docs/08-admin-dashboard.md) | Config, evaluation, observability |
| [Memory System](docs/11-memory-system.md) | Hard preferences, behavioral patterns, feedback signals |
| [Editorial System](docs/12-editorial-system.md) | Editorial task management and workflow |
| [WritingAgentBench Data Layer](docs/13-wabench-data-layer.md) | WABench v1 tables, legacy importer, partitioning, privacy |
| [Luminbuddy Eval Center](docs/16-wabench-eval-center.md) | Seven evaluation workspaces, Chinese Excel, review traceability |
| [WritingAgentBench V2](docs/14-wabench-v2-evaluation.md) | Real Harness Adapter, five Rubric, failure-first, independent red team |
| [Runbook](docs/runbook.md) | Deployment, monitoring, troubleshooting |
| [Governed Writing Runtime](docs/19-governed-writing-runtime.md) | Contract, Plan, LCP, document, quality, snapshot, workspace, and edition boundaries |
| [Task1–12 Implementation](docs/plans/2026-08-27-governed-writing-runtime-implementation.md) | Domain protocol through workspace, materials, and vertical release gates |
| [LCP v1](specs/lcp/v1/README.md) | Schemas, enums, fixtures, and three writing scenarios |
| [Task11 Governed Materials](specs/task11-governed-materials/design.md) | MaterialAdapter, typed Artifacts, conflict handling, and the sole source of truth |
| [Task12 Governed Rollout](specs/task12-governed-rollout/design.md) | ExecutorAdapters, shadow isolation, telemetry, and rollback |
| [Task13 Production Wiring](docs/releases/2026-08-30-task13-production-wiring-readiness.md) | Passed evidence, remaining production gates, and credential boundaries |

---

## Changelog

### v0.8.0 (2026-08-31) — Task1–13 Governed Writing Platform

- **Task1–4 · Protocol and planning:** unified architecture boundary; LCP v1, WritingContract, Document AST, RevisionSet, typed Writing Plan IR, Capability Registry, and T1–T4 strategy compilation.
- **Task5–7 · Persistence and execution:** governed schema, transactional Repository, immutable RunLedger, atomic Snapshot commits, state machine, ExecutorRegistry, budget/permission/approval gates, and bounded recovery.
- **Task8–10 · Quality, API, and workspace:** Candidate/Accepted/Verified, non-waivable BLOCKER, validator degradation, V2 Contract/Document/Run/Quality/Audit APIs, and the document-first four-region workspace.
- **Task11 · Governed materials:** MaterialAdapter, typed Material/Source Artifacts, conflict Findings, single Orchestrator commit path, and B2 contracts for legacy operators.
- **Task12 · Governed rollout:** three ExecutorAdapters, shadow-content isolation, rollout evidence, telemetry, defense matrix, and three vertical scenarios.
- **Task13 · Production wiring:** unified composition root, readiness/provider preflight, MCP concurrency hardening, canonical/shadow persistence, and real-model path acceptance. Production traffic remains gated by staging evidence.

### v0.7.0 (2026-08-24)

- **Rich Text Editor**: Upgraded writing input to Tiptap/ProseMirror with bold, lists, blockquotes, code blocks
- **Unified Material Library**: Merged "knowledge base" and "material library" concepts into a unified UI
- **Model Config Enhancement**: Custom HTTP headers and Reasoning Effort parameter support
- **Style-Knowledge Binding**: Style profiles can bind to specific material libraries
- **Deployment Optimization**: GOAMD64 v3 compatibility, 1Panel offline image packaging, Docker mirror acceleration
- **Frontend Port**: Default port changed from 3000 to 3002

### v0.6.0 (2026-08-23)

- **Editorial Agent Tooling**: RoleAgentRunner, EditorialToolRegistry, signal tool mechanism
- **A2A Agent Card**: Agent capability self-description, A2A protocol discovery support
- **Security Audit Persistence**: Security events recorded to database, historical query and compliance audit
- **Brand UI Upgrade**: Unified brand identity, favicon/apple-touch-icon update
- **Personal Center Refactor**: Split into 8 independent section components

### v0.5.0 (2026-08-21)

- **Editorial Multi-Agent**: Three-Agent orchestration system (research → writing → review + quality routing + reputation + controlled experiments)
- **Security System**: Red team 20-case evaluation, Prompt Injection defense details, MCP bidirectional integration
- **Documentation Unification**: UnifiedAgent → Harness naming + architecture history documentation

### v0.4.0 (2026-08-18)

- **Smart Context Management**: `retrieve_context` tool lets LLM fetch information on demand, System Prompt tokens reduced 60%+
- **Dialog History Compaction**: Inspired by dsh pattern, auto-compresses history, frontend displays saved tokens
- **Writing Toolset Extension**: Added `word_count_check`, `rewrite_title`, `fact_check`
- **Online Editing & Export**: Support Markdown/Word/PDF export
- **Admin Dashboard Refactor**: Unified permission/poll/resource management hooks, added audit logs
- **Slim docreader Image**: ~150MB replacing old 5.53GB

### v0.3.0 (2026-08-16)

- **Harness Architecture**: Single-layer LLM continuous session orchestration
- **A/B Testing Framework**: Control/experiment group automated evaluation
- **Passkey Auth**: WebAuthn passwordless login
- **Session Event Log**: Append-only event log with reconnection support
- **Prompt Injection Defense**: Lean injection for chat intent, Token reduced 10.5%

### v0.2.0

- Guided Mode outline confirmation
- Style Profile grayscale routing
- Post Review + Auto Fix
- Tiered memory system
- Prometheus metrics and Trace tracking

### v0.1.0

- React 19 workspace + Go Agent Pipeline
- WebSocket streaming events
- Multi-source retrieval and relevance filtering
- Docker Compose one-command deployment

---

## Project Disclaimer

- This is a runnable personal product and engineering project, not a validated commercial deployment.
- Code, builds, CI, and isolated real paths pass; this is not approval for production deployment or traffic.
- OSS shares the governed kernel, MCP, baseline retrieval, web fetching, and extension contracts; it excludes commercial paid-search implementations and credentials.
- The repository contains no production secrets; create local configuration from the example env files.

## License

[MIT](LICENSE)
