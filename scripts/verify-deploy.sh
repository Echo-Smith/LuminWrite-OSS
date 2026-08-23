#!/bin/bash
# ─── LuminBuddy V2 — 部署后健康检查 ───────────────────
#
# 用法：在部署目录（解压后的 source/ 目录内）执行：
#   bash verify.sh
#
# 检查项：
#   1. 容器运行状态
#   2. 后端 /health 端点
#   3. 前端可访问
#   4. 数据库迁移状态（通过后端日志间接确认）

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

ok()   { echo -e "${GREEN}✅ $1${NC}"; }
fail() { echo -e "${RED}❌ $1${NC}"; exit 1; }
warn() { echo -e "${YELLOW}⚠️  $1${NC}"; }

echo "━━━ LuminBuddy V2 部署健康检查 ━━━"
echo ""

# 1. 检查容器状态
echo "1. 检查容器状态..."
REQUIRED_SERVICES="postgres redis docreader backend frontend"
for svc in $REQUIRED_SERVICES; do
    status=$(docker compose ps "$svc" --format '{{.Status}}' 2>/dev/null || echo "")
    if echo "$status" | grep -q "running\|healthy"; then
        ok "$svc: $status"
    else
        fail "$svc 未运行 (status: ${status:-unknown})"
    fi
done
echo ""

# 2. 后端 /health
echo "2. 检查后端 API..."
BACKEND_PORT="${BACKEND_PORT:-8080}"
if curl -sf "http://localhost:$BACKEND_PORT/health" > /dev/null 2>&1; then
    ok "后端 /health 可达 (port $BACKEND_PORT)"
else
    fail "后端 /health 不可达 (port $BACKEND_PORT)"
fi
echo ""

# 3. 前端
echo "3. 检查前端..."
FRONTEND_PORT="${FRONTEND_PORT:-3000}"
if curl -sf "http://localhost:$FRONTEND_PORT/" > /dev/null 2>&1; then
    ok "前端可达 (port $FRONTEND_PORT)"
else
    warn "前端不可达 (port $FRONTEND_PORT)，可能还在启动中，稍后重试"
fi
echo ""

# 4. 后端日志中是否有错误
echo "4. 检查后端日志..."
BACKEND_LOGS=$(docker compose logs backend --tail 20 2>/dev/null || echo "")
if echo "$BACKEND_LOGS" | grep -qi "error\|panic\|fatal"; then
    # 排除 "error" 出现在正常日志中的情况（如 "rateLimitMiddleware" 等）
    REAL_ERRORS=$(echo "$BACKEND_LOGS" | grep -i '"level":"ERROR"' || true)
    if [ -n "$REAL_ERRORS" ]; then
        warn "后端日志中发现 ERROR 级别日志，请检查："
        echo "$REAL_ERRORS" | head -5
    else
        ok "后端日志正常"
    fi
else
    ok "后端日志正常"
fi
echo ""

# 5. 数据库迁移状态（通过后端启动日志确认）
echo "5. 检查数据库迁移..."
if echo "$BACKEND_LOGS" | grep -q "migration failed\|refusing to start"; then
    fail "数据库迁移失败，后端拒绝启动。请检查迁移文件。"
else
    ok "数据库迁移通过"
fi
echo ""

echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN}✅ 部署健康检查完成${NC}"
echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
