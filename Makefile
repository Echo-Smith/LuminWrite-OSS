# ─── Writing Agent V2 — Root Makefile ───────────────────
# Common Docker commands for development and deployment.

.PHONY: help up down build rebuild logs ps shell clean dev weknora

# Default: show available commands
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

# ── Docker Compose ──────────────────────────────────────

up: ## Start all services (detached)
	docker compose up -d

down: ## Stop all services
	docker compose down

build: ## Build images without starting
	docker compose build

rebuild: ## Rebuild images (no cache) and restart
	docker compose build --no-cache
	docker compose up -d

logs: ## Tail logs for all services
	docker compose logs -f --tail=100

ps: ## Show container status
	docker compose ps

shell: ## Shell into backend container
	docker compose exec backend /bin/sh

# ── Development (hot reload) ────────────────────────────

dev: ## Start with dev overrides (hot reload)
	docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d

# ── WeKnora (optional RAG service) ──────────────────────

weknora: ## Start with WeKnora profile
	docker compose --profile weknora up -d

# ── Database ────────────────────────────────────────────

migrate: ## Run database migrations
	docker compose exec backend /app/server -migrate

dbshell: ## Connect to PostgreSQL shell
	docker compose exec postgres psql -U postgres -d writing_agent_v2

# ── Cleanup ─────────────────────────────────────────────

clean: ## Stop and remove containers + volumes (DESTRUCTIVE)
	docker compose down -v
	docker system prune -f --filter label=org.opencontainers.image.title=writing-agent-backend
	docker system prune -f --filter label=org.opencontainers.image.title=writing-agent-frontend
