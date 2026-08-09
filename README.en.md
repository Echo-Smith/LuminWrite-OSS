# LuminBuddy V2

[中文](README.md) · [Live workspace](https://luminbuddy2.ericdocmic.top/v2/)

An AI writing workspace for Chinese content creators. It turns a one-shot generation request into an observable and interruptible product flow: intent routing, source retrieval, outline confirmation, style-aware writing, post-review, feedback, evaluation, and memory.

![LuminBuddy writing workspace](docs/assets/luminbuddy-workspace.png)

> **Maturity: engineering beta.** The repository contains a working frontend and backend, an orchestrated writing pipeline, guided outlines, style profiles, feedback and evaluation surfaces, tiered memory, traces, and metrics. It does not yet claim public evidence of adoption, retention, or business outcomes.

## Product problem

General chat models can draft quickly, but real writing work still fails when the system misunderstands constraints, hides retrieval and reasoning inside one generation, offers no control at high-cost decisions, and loses user feedback after the session.

LuminBuddy separates those problems into explicit product mechanisms:

```text
request → intent → retrieval plan → sources → relevance
        → outline gate → style-aware streaming draft
        → post-review → feedback, evaluation, and memory
```

## Product decisions

- **Rules before models where possible.** Deterministic intent signals run first; low-confidence cases can fall back to an LLM.
- **Human control at the outline.** Guided Mode lets the user confirm, edit, or regenerate structure before the expensive drafting step.
- **Style as a versioned product object.** Style Profiles can be managed, evaluated, rolled out, and rolled back independently of the pipeline.
- **Governed memory.** Hard preferences, learned patterns, and feedback signals have quality gates and conflict states instead of becoming permanent by default.
- **Evaluation and observability as product infrastructure.** Step traces and Prometheus-format metrics make failures diagnosable; real feedback quality remains an evidence gap to close.

## Current capability map

| Status | Capability |
|---|---|
| Implemented | React workspace, Go pipeline, WebSocket events, pause/resume/cancel, Guided Mode, Style Profiles, post-review, auto-fix, feedback, evaluation admin, tiered memory, traces, metrics, and grayscale routing |
| Requires configuration | PostgreSQL / pgvector, model APIs, embeddings, external search sources, and authentication providers |
| Early-stage | Multi-source reliability, labeled evaluation quality, long-term memory value, and production feedback loops |
| Missing public evidence | Active users, repeat usage, measured quality lift, cost benefit, content adoption, and business outcomes |

## Architecture

```text
React 19 + Vite
       │ REST / WebSocket
Go Agent Engine
       ├─ Intent / Memory Gate / Query Plan / Search / Relevance
       ├─ Outline / Write / Post Review / Auto Fix / Memory Extract
       ├─ Style / Feedback / Evaluation / Admin
       └─ Traces / Metrics / Rollout routing
       │
PostgreSQL 17 + pgvector
```

See the [architecture blueprint](docs/01-architecture.md) for implementation details.

## Run locally

```bash
cp .env.docker.example .env.docker
docker compose up -d
```

Or run the backend and frontend separately:

```bash
cd backend && cp .env.example .env && go run ./cmd/server/
cd frontend && npm ci && npm run dev
```

Validation:

```bash
cd backend && go test ./...
cd frontend && npm ci && npm run build
```

## License

[MIT](LICENSE)
