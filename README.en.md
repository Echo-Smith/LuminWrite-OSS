# LuminWrite OSS (笔润智谈)

![License](https://img.shields.io/badge/License-MIT-green.svg)
![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)
![React](https://img.shields.io/badge/React-19-61DAFB?logo=react)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-17%20%2B%20pgvector-4169E1?logo=postgresql)

[中文](README.md)

**LuminWrite OSS** is a **self-hosted AI writing workspace** for Chinese content
creation. Instead of "one-click generation" magic, it breaks writing into an
**observable, interruptible, and iterative** engineering process: source
retrieval → outline confirmation → style-aware drafting → post-write review →
memory sedimentation — the creator stays in control at every key decision point.

![Writing workspace](docs/assets/8f2bb20334781e43c0bb23498f2dc02d.png)

## What problem it solves

| Pain point | Manifestation | LuminWrite's answer |
|---|---|---|
| Unstable intent understanding | Real requirements, length and style constraints get lost | Rule-first intent routing with LLM fallback |
| Opaque generation | Retrieval, organization and drafting compressed into one unobservable call | Harness single-layer orchestration — every step visible, pausable, resumable |
| Loss of control at key moments | No way to intervene at outline/style decision points | Guided mode: confirm the outline before drafting |
| Feedback doesn't accumulate | Past corrections keep recurring | Segmented feedback + A/B evaluation + tiered memory |

## Features

- **Multiple execution modes**: single-layer LLM session (Harness, default),
  classic step pipeline (Pipeline), and an editorial multi-agent DAG
  (research / writing / review roles);
- **Governed writing runtime** (experimental): the document is the primary
  object, chat only modifies the contract — WritingContract → ExecutablePlan →
  Artifact → quality gates (Candidate/Accepted/Verified) with off/shadow/
  allowlist rollout and crash recovery
  ([docs/19-governed-writing-runtime.md](docs/19-governed-writing-runtime.md));
- **Research-review path** (experimental): scholarly discovery
  (OpenAlex/CrossRef/Semantic Scholar) → human evidence gate → human outline
  gate → verifiable-citation drafting, fail-closed end to end
  (`RESEARCH_REVIEW_ENABLED`, off by default);
- **AR-012 candidate evaluation** (experimental): dispatch a completed research
  run to an external review-generation sidecar for isolated comparison
  candidates and mechanical metrics (`AR012_CANDIDATE_ENABLED`, off by default;
  the sidecar is a private component not distributed with this repository);
- **Tiered memory**: session → working → long-term → project memory, with
  post-write behavior extraction;
- **Local knowledge base (RAG)**: BM25 (ParadeDB) + vectors (pgvector) + RRF
  fusion + GraphRAG on plain PostgreSQL — no external vector service;
- **User materials first**: user-uploaded material outranks web results;
- **Style system**: hot-swappable style profiles, a style builder (upload
  sample writings, auto-extract), and grayscale publishing. The repository
  **ships no preset style content** — build your own catalog
  ([docs/04-style-profile.md](docs/04-style-profile.md));
- **Evaluation center**: WABench-contract writing-quality evaluation
  (datasets / candidates / blind review / releases) + red-team suites;
- **Admin console**: hot model-config updates (encrypted at rest), MCP
  management, audit center, RBAC.

## Tech stack

| Layer | Technology |
|---|---|
| Backend | Go 1.25 · chi · coder/websocket · pgx/v5 · go-redis (deliberately small dependency surface) |
| Database | PostgreSQL 17 (ParadeDB image: pgvector + pg_bm25) · Redis 7 |
| Document parsing | docreader sidecar (markitdown, TCP, ~150MB) |
| Models | OpenAI-compatible `/chat/completions` (DeepSeek / SenseNova verified; switching providers: [docs/provider-configuration.md](docs/provider-configuration.md)) |
| Embeddings | Alibaba DashScope text-embedding-v3 (optional, graceful degradation) |
| Frontend | React 19 · Vite 7 · TypeScript · Tailwind CSS · Tiptap/ProseMirror · zustand |
| Research sidecar | scholar-worker (Python 3.12, only httpx, enabled via the research profile) |

## Quick start

### Option 1: Quickstart Compose (recommended, ~3 minutes)

Prebuilt images + a bundled SearXNG search service (**no search API keys
required**):

```bash
git clone https://github.com/Echo-Smith/luminbuddy-writing-agent-v2.git
cd luminbuddy-writing-agent-v2
cp .env.docker.example .env.docker
vi .env.docker        # at minimum set DEEPSEEK_API_KEY
docker compose -f docker-compose.quickstart.yml up -d
```

Open `http://localhost:3002` and register. Images are published to GitHub
Packages (`ghcr.io/echo-smith/luminbuddy-v2-*`; publishing workflow in
`.github/workflows/docker-publish.yml`), or build locally with
`docker compose build`.

### Option 2: Main Compose (self-built)

```bash
cp .env.docker.example .env.docker && vi .env.docker
docker compose up -d
```

### Option 3: Local development

```bash
cd backend && cp .env.example .env && go run ./cmd/server/
cd frontend && npm ci && npm run dev
```

### Verification

```bash
cd backend && go test ./...
cd frontend && npm ci && npm test && npm run build
```

### Production deployment (1Panel)

See [DEPLOY.md](DEPLOY.md) (Chinese): image-bundle / CLI / source deployment,
domain + HTTPS reverse proxy, and multi-instance scaling. Backups:
[docs/ops-backup-restore.md](docs/ops-backup-restore.md).

## Search sources

Source retrieval runs through a multi-source concurrent framework
([adapter guide](docs/search-provider-adapter.md)):

| Source | OSS edition | Notes |
|---|---|---|
| **SearXNG** | ✅ fully implemented | self-hosted metasearch, zero API keys, bundled in quickstart |
| Tavily / Zhihu / Tencent News / Weibo / Bing / AnySearch | stub | full implementations are commercial; the interface is public — implement your own via the guide |

## Key configuration

Full annotated list in [.env.docker.example](.env.docker.example).

| Variable | Default | Purpose |
|---|---|---|
| `DEEPSEEK_BASE_URL` / `DEEPSEEK_API_KEY` / `DEEPSEEK_DEFAULT_MODEL` | — | LLM backend (OpenAI-compatible; switchable) |
| `SEARXNG_BASE_URL` | empty | SearXNG instance URL (the only out-of-the-box source; strongly recommended) |
| `WRITING_RUNTIME_MODE` | off | governed runtime: off / shadow / allowlist |
| `RESEARCH_REVIEW_ENABLED` | false | research-review path (plus `--profile research` for scholar-worker) |
| `AR012_CANDIDATE_ENABLED` | false | AR-012 candidate evaluation (sidecar deployed separately) |
| `DASHSCOPE_API_KEY` | empty | embeddings (optional) |

> Day-to-day key changes don't require file edits: update them in the Admin
> console (encrypted in PostgreSQL, takes precedence over env vars).

## Style catalog

This repository follows an "open engine, first-party content" boundary: the
style **system** (data structures, builder, hot-swap, grayscale) is fully open,
but **no preset style content is included**. Build your own catalog through the
in-app style builder or the Admin API.

## License

MIT
