# LuminBuddy V2

[中文](README.md) · [Live workspace](https://luminbuddy2.ericdocmic.top/v2/) · [Changelog](#changelog)

> **An AI writing workspace for Chinese content creators**: from intent understanding, source retrieval, and outline confirmation, to style-aware drafting, post-review, feedback, and memory sedimentation—transforming one-shot generation into an observable, interruptible, and iterative writing process.

![LuminBuddy writing workspace](docs/assets/luminbuddy-workspace.png)

---

## About LuminBuddy

**LuminBuddy** is an AI writing assistant designed for Chinese content production. It doesn't pursue "one-click generation" magic, but instead breaks down the writing process into observable, interruptible, and iterative engineering workflows—keeping creators in control at key decision points while AI assists in source gathering, structure planning, style adaptation, and quality checking.

**Current maturity: Engineering Beta**. The repository contains a complete frontend and backend, Harness single-layer Agent orchestration, writing Pipeline, guided outlines, style profiles, A/B evaluation, feedback system, tiered memory, and monitoring metrics; continuously iterating.

### Governed Writing Runtime: Target Architecture and Migration Boundary

V2 evolves toward the [Governed Writing Runtime](docs/19-governed-writing-runtime.md) as its single target architecture and protocol baseline, delivered through the [implementation plan](docs/plans/2026-08-27-governed-writing-runtime-implementation.md). New capabilities must bind a versioned WritingContract, ExecutablePlan, Artifact, quality state, and Snapshot. The document is the primary product object; chat is the channel for changing the contract, explaining decisions, and controlling execution.

During migration, the existing Harness, Pipeline, and Editorial DAG remain available as executors or compatibility adapters:

- `agent.start` and `workflow.start` retain wire compatibility for now, but they are not two parallel authoritative runtimes.
- Legacy `mode` and `agent_mode` are not the new `task_mode`, `orchestration_mode`, and `assurance_level`; old fields must not carry the new semantics.
- `agent.completed` and `workflow.completed` only mean the current executor path ended. They do not mean the content is Accepted, Verified, or formally committed.
- The Article Output Contract only defines the streaming Markdown parsing boundary. Successful parsing does not imply LCP validation, Document AST construction, version commit, or quality acceptance.

---

## Problems Solved

General chat models can draft quickly, but real writing work usually fails at four points:

| Pain Point | Manifestation | LuminBuddy's Solution |
|------------|--------------|----------------------|
| **Unstable intent understanding** | User's real needs, length, and style constraints not accurately captured | Rule-first intent routing + low-confidence LLM fallback |
| **Black-box generation** | Source retrieval, opinion organization, and drafting compressed into one unobservable generation | Harness single-layer orchestration, every step visible, pausable, resumable |
| **Loss of control at key points** | Users can't intervene at high-cost decision points like outline confirmation or style adjustment | Guided Mode: confirm outline before drafting |
| **Feedback doesn't accumulate** | Good/bad feedback doesn't enter next generation; team can't locate failure points | Section feedback + A/B evaluation + tiered memory system |

---

## How It Works

### Core Architecture: Harness-LLM Single-Layer Continuous Session

Inspired by DeepSeek Harness (dsh), adopts a **single-layer architecture**:

```
User Request
  → Harness (intent routing, tool registration, state management, circuit breaking)
    → LLM continuous session (autonomously decides which tools to call, when to write, when to revise)
      ←→ Tool execution (search/knowledge base/write/review/revise)
  → Streaming output to frontend
```

**Key designs**:
- **No nested ReAct + inner agent loop**, reducing latency and output drift
- **1 LLM continuous session** replaces traditional Pipeline's 10+ independent calls, time-to-first-token from 30-60s to 3-5s
- **Cross-turn session persistence**: articles, sources, search records accumulate and reuse within the same dialog

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
Writing Request
  → Intent Recognition (rule-first, ms-level routing)
  → Memory Retrieval (user preference injection)
  → Source Retrieval (multi-source search + knowledge base + relevance filtering)
  → Outline Confirmation (guided mode: confirm/edit/redo)
  → Style-aware Streaming Writing (generate according to Profile rules)
  → Post-review (quality scoring + safety check)
  → Auto-fix (low-severity issues auto-fixed)
  → Section Feedback + A/B Evaluation + Memory Sedimentation
```

---

## Features

### Writing Core

| Feature | Description |
|---------|-------------|
| **Intent Recognition** | Rule-first routing, supports writing/polish/compress/expand/free chat |
| **Multi-source Retrieval** | Extensible multi-source search framework + internal knowledge base (BM25 + Dense + GraphRAG) |
| **Guided Mode** | Outline confirmation, editing, up to five regenerations |
| **Style Configuration** | Style Profile independently managed, supports versioning, grayscale release and rollback |
| **Streaming Output** | WebSocket real-time push, supports pause/resume/cancel |
| **Rich Text Editor** | Tiptap/ProseMirror editor with bold, lists, blockquotes, code blocks |
| **Quality Review** | 6-dimension scoring (factuality/structure/style/rhetoric/length/safety) |
| **Auto-fix** | Auto-correct when review fails, up to 3 attempts |

### Writing Toolset

Tools LLM autonomously calls during continuous session:

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

> **Search Source Extension**: This repo provides the complete `SearchClient` interface and multi-source concurrent search framework. Search sources are integrated as independent modules. Developers can refer to the stub implementations in `search_stubs.go` to connect their own search sources.

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

A complete three-Agent orchestration system for editorial content production:

```
Human editor creates task → Research Agent → Writing Agent → Review Agent
  → Quality routing: pass → pending publish / minor issues → return / severe → escalate to human
```

- **Role-based Agent Executor** (RoleAgentRunner): Each Agent has independent Persona, toolset, and signal tools
- **Tool Registry Management** (EditorialToolRegistry): Add new tools with `Register`, no switch-case modification needed
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
┌─────────────────────────────────────────────────────────────┐
│                    Frontend (React 19 + Vite)                 │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌────────────────┐  │
│  │ Writing  │ │ Topic    │ │ Personal │ │ Admin          │  │
│  │ Workspace│ │ Center   │ │ Center   │ │ Dashboard      │  │
│  └─────┬────┘ └─────┬────┘ └─────┬────┘ └───────┬────────┘  │
│        └────────────┴────────────┘              │           │
│                     │                           │           │
│              WebSocket + REST                   │           │
└─────────────────────┼───────────────────────────┼───────────┘
                      │                           │
┌─────────────────────┼───────────────────────────┼───────────┐
│              Go Backend (chi router)             │           │
│                     │                           │           │
│  ┌──────────────────┴───────────────────────────┴────────┐  │
│  │                   Harness Agent Engine                 │  │
│  │  ┌────────┐  ┌─────────┐  ┌────────┐  ┌────────────┐  │  │
│  │  │ Intent │→ │ Search  │→ │ Write  │→ │PostReview  │  │  │
│  │  │Routing │  │  Plan   │  │ Step   │  │   Step     │  │  │
│  │  └────────┘  └─────────┘  └────────┘  └────────────┘  │  │
│  │                                                        │  │
│  │  ┌────────┐  ┌─────────┐  ┌────────┐  ┌────────────┐  │  │
│  │  │ Memory │  │  Style  │  │  A/B   │  │  Feedback  │  │  │
│  │  │  Gate  │  │ Profile │  │  Eval  │  │  System    │  │  │
│  │  └────────┘  └─────────┘  └────────┘  └────────────┘  │  │
│  └────────────────────────────────────────────────────────┘  │
│                     │                                       │
│  ┌──────────┬──────────┬──────────┬──────────┬──────────┐  │
│  │ DeepSeek │  Search  │ IMA KB   │ DashScope│ MCP Server│  │
│  │  Client  │  Client  │  Client  │ Embedding│ (JSON-RPC) │  │
│  └──────────┴──────────┴──────────┴──────────┴──────────┘  │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────┼───────────────────────────────────────┐
│              PostgreSQL 17 + pgvector + paradedb             │
│  ┌─────────┬──────────┬──────────┬──────────┬────────────┐  │
│  │ User    │ Style    │ Knowledge│ Memory   │ Session    │  │
│  │ Data    │ Profiles │ Base     │ System   │ Logs       │  │
│  └─────────┴──────────┴──────────┴──────────┴────────────┘  │
└─────────────────────────────────────────────────────────────┘
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
| Monitoring | Prometheus metrics + slog structured logging + Trace tracking |

---

## Quick Start

### Docker Compose (recommended)

```bash
cp .env.docker.example .env.docker
# Edit .env.docker: fill in model API keys and database password
docker compose up -d
```

Frontend at `http://localhost:3002`, backend health at `http://localhost:8080/api/v2/health`.

### Local Development

```bash
# Backend
cd backend && cp .env.example .env && go run ./cmd/server/

# Frontend
cd frontend && npm ci && npm run dev
```

### Validation

```bash
cd backend && go test ./...
cd frontend && npm ci && npm run build
```

### Deployment Packaging

```bash
# Source only
./scripts/pack-for-1panel.sh

# Source + Docker images (recommended for China servers)
./scripts/pack-for-1panel.sh --images
```

### Search Source Extension

This repo provides the search client core framework (`backend/internal/tools/search.go`), including:
- `SearchClient` multi-source concurrent search struct
- `NewSearchClient` constructor
- `Search` / `FetchHotTopics` concurrent search methods
- `KnowledgeSearcher` knowledge base search interface

Search sources are integrated as independent modules. This repo provides stub implementations for all search source types in `search_stubs.go`. Developers can refer to these stubs to connect their own search sources:

```go
// Example: implement a custom search source
type MySearchClient struct { /* ... */ }

func NewMySearchClient(/* params */) *MySearchClient { /* ... */ }

func (c *MySearchClient) Search(ctx context.Context, query string, limit int) ([]engine.SearchResult, error) {
    // Your search logic
}
```

Then initialize your search source in `NewSearchClient`.

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

---

## Changelog

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
- Search sources are integrated as independent modules; refer to `search_stubs.go` stub implementations to connect your own.
- The repository contains no production secrets; create local configuration from the example env files.

## License

[MIT](LICENSE)
