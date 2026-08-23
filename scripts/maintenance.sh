#!/bin/bash
# ─── LuminBuddy V2 — 维护模式切换脚本 ───────────────────
#
# 用法：
#   ./scripts/maintenance.sh start    # 启动维护模式（显示维护页面）
#   ./scripts/maintenance.sh stop     # 停止维护模式（恢复正常服务）
#   ./scripts/maintenance.sh status   # 查看当前状态
#
# 原理：
#   启动一个轻量级 nginx 容器（luminbuddy-maintenance），监听 8080 端口，
#   提供 maintenance.html 静态页面。
#   1Panel 代理到 8080 端口时会得到维护页面，而非 502 错误。
#
#   停止时移除该容器，恢复正常的 docker-compose 服务。
#
# 前提条件：
#   - 1Panel 代理的目标端口与 docker-compose 的 BACKEND_PORT/FRONTEND_PORT 一致
#   - 或者 1Panel 直接代理到 8080 端口

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}/.." && pwd)" 2>/dev/null || pwd"
cd "$PROJECT_DIR"

MAINTENANCE_CONTAINER="luminbuddy-maintenance"
MAINTENANCE_PORT="${MAINTENANCE_PORT:-8080}"

# ── start ───────────────────────────────────────────────
start_maintenance() {
    # 检查是否已在维护模式
    if docker ps --filter "name=$MAINTENANCE_CONTAINER" --filter "status=running" | grep -q "$MAINTENANCE_CONTAINER"; then
        info "已在维护模式中"
        exit 0
    fi

    step "启动维护模式..."

    # 停止正常服务（不删除数据卷）
    info "停止 LuminBuddy 服务..."
    docker compose down 2>/dev/null || true

    # 启动维护容器
    info "启动维护页面容器（端口 $MAINTENANCE_PORT）..."
    docker run -d \
        --name "$MAINTENANCE_CONTAINER" \
        -p "$MAINTENANCE_PORT:80" \
        -v "$PROJECT_DIR/frontend/public/maintenance.html:/usr/share/nginx/html/index.html:ro" \
        nginx:alpine \
        > /dev/null 2>&1 || error "启动维护容器失败"

    info "维护模式已启动"
    echo ""
    echo -e "  ${CYAN}用户访问时将看到:${NC}"
    echo -e "  ${GREEN}「笔润智谈正在维护中，预计 2-4 小时内恢复」${NC}"
    echo ""
    echo -e "  ${YELLOW}恢复服务:${NC} ./scripts/maintenance.sh stop"
}

# ── stop ────────────────────────────────────────────────
stop_maintenance() {
    step "停止维护模式..."

    # 停止维护容器
    if docker ps -a --filter "name=$MAINTENANCE_CONTAINER" | grep -q "$MAINTENANCE_CONTAINER"; then
        docker rm -f "$MAINTENANCE_CONTAINER" > /dev/null 2>&1 || true
        info "维护容器已移除"
    else
        info "维护容器不存在"
    fi

    # 恢复正常服务
    info "启动 LuminBuddy 服务..."
    docker compose up -d

    info "服务已恢复"
    echo ""
    echo -e "  ${GREEN}LuminBuddy 已恢复正常运行${NC}"
}

# ── status ──────────────────────────────────────────────
status_maintenance() {
    if docker ps --filter "name=$MAINTENANCE_CONTAINER" --filter "status=running" | grep -q "$MAINTENANCE_CONTAINER"; then
        echo -e "${YELLOW}维护模式：启动中${NC}（用户看到维护页面）"
        echo ""
        echo -e "  恢复服务: ${CYAN}./scripts/maintenance.sh stop${NC}"
    else
        echo -e "${GREEN}正常模式${NC}（LuminBuddy 正在运行）"
        echo ""
        echo -e "  启动维护: ${CYAN}./scripts/maintenance.sh start${NC}"
    fi
}

# ── main ────────────────────────────────────────────────
step() { echo -e "${CYAN}[STEP]${NC} $*"; }

case "${1:-}" in
    start)  start_maintenance ;;
    stop)   stop_maintenance ;;
    status) status_maintenance ;;
    *)
        echo "用法: ./scripts/maintenance.sh [start|stop|status]"
        echo ""
        echo "  start   启动维护模式（显示维护页面，停止服务）"
        echo "  stop    停止维护模式（恢复正常服务）"
        echo "  status  查看当前状态"
        exit 1
        ;;
esac
