#!/bin/bash
# ─── Writing Agent V2 — 1Panel 部署打包脚本 ──────────────────
#
# 功能：
#   1. 基于 git HEAD 创建精简的部署压缩包（仅含部署必需文件）
#   2. 清除 macOS 风格文件（._ 开头、.DS_Store）
#   3. 排除测试、文档、技能、CI 等非部署文件
#   4. 可选：导出本地 Docker 镜像（--images），避免服务器拉镜像慢
#   5. 输出 .tar.gz 压缩包，可直接上传到 1Panel 文件管理器
#
# 用法：
#   chmod +x scripts/pack-for-1panel.sh
#   ./scripts/pack-for-1panel.sh                    # 仅打包源码
#   ./scripts/pack-for-1panel.sh --images           # 源码 + Docker 镜像（推荐国内使用）
#   ./scripts/pack-for-1panel.sh --images ~/Downloads  # 指定输出目录
#
# 参数：
#   --images   (可选) 同时导出 Docker 镜像（frontend + backend）
#   $1 (可选)  输出目录，默认为项目根目录

set -euo pipefail

# ── 颜色输出 ──────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }
step()  { echo -e "${CYAN}[STEP]${NC}  $*"; }

# ── 解析参数 ──────────────────────────────────────────
EXPORT_IMAGES=false
OUTPUT_DIR=""

for arg in "$@"; do
    case "$arg" in
        --images) EXPORT_IMAGES=true ;;
        *) OUTPUT_DIR="$arg" ;;
    esac
done

# ── 路径解析 ──────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OUTPUT_DIR="${OUTPUT_DIR:-$PROJECT_DIR}"

cd "$PROJECT_DIR"

# ── 版本信息 ──────────────────────────────────────────
GIT_BRANCH="$(git branch --show-current 2>/dev/null || echo 'unknown')"
GIT_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
GIT_DATE="$(git log -1 --format='%cd' --date='format:%Y%m%d-%H%M' 2>/dev/null || echo "$(date +%Y%m%d-%H%M)")"
PROJECT_NAME="luminbuddy-v2"
ARCHIVE_NAME="${PROJECT_NAME}-${GIT_BRANCH}-${GIT_COMMIT}-${GIT_DATE}.tar.gz"
ARCHIVE_PATH="${OUTPUT_DIR%/}/${ARCHIVE_NAME}"

info "项目目录:   $PROJECT_DIR"
info "Git 分支:   $GIT_BRANCH"
info "Git 提交:   $GIT_COMMIT"
info "构建时间:   $GIT_DATE"
info "导出镜像:   ${EXPORT_IMAGES}"
info "输出文件:   $ARCHIVE_PATH"
echo ""

# ── Step 1: 预检查 ────────────────────────────────────
step "1/6 预检查"

if [ ! -d "$PROJECT_DIR/.git" ]; then
    error "不在 Git 仓库中，请确保在项目根目录运行"
fi
if [ ! -f "$PROJECT_DIR/docker-compose.yml" ]; then
    error "未找到 docker-compose.yml，请确保在项目根目录运行"
fi

# 确保输出目录存在
mkdir -p "$OUTPUT_DIR"

info "预检查通过"
echo ""

# ── Step 2: 构建文件清单（精简模式） ──────────────────
step "2/6 构建文件清单（精简模式）"

# 临时文件列表
FILELIST="$(mktemp /tmp/luminbuddy-filelist.XXXXXX)"
FILTERED_FILELIST="$(mktemp /tmp/luminbuddy-filtered.XXXXXX)"
TEMP_TAR="$(mktemp /tmp/luminbuddy-archive.XXXXXX)"
trap 'rm -f "$FILELIST" "$FILTERED_FILELIST" "$TEMP_TAR"' EXIT

# 使用 git ls-files 获取版本控制文件
git ls-files > "$FILELIST"

# 补充未跟踪但部署需要的文件
for extra in \
    ".env.docker" \
    ".env.docker.example" \
    "docker-compose.yml" \
    "backend/Dockerfile" \
    "frontend/Dockerfile" \
    "frontend/nginx.conf" \
    "frontend/vite.config.ts" \
    "PROJECT_LEDGER.md" \
    "docs/runbook.md" \
    "DEPLOY.md"; do
    if [ -f "$PROJECT_DIR/$extra" ] && ! grep -qxF "$extra" "$FILELIST"; then
        echo "$extra" >> "$FILELIST"
    fi
done

# 补充 untracked 的部署必需源码文件（.go / .sql / .tsx / .ts / .sh / .yaml / .yml / .json / .mod / .sum）
# git ls-files 只包含已跟踪文件，但新增的源码文件也是部署必需的
while IFS= read -r uf; do
    if ! grep -qxF "$uf" "$FILELIST"; then
        echo "$uf" >> "$FILELIST"
    fi
done < <(git ls-files --others --exclude-standard | grep -E '\.(go|sql|tsx|ts|sh|yaml|yml|json|mod|sum)$')

# ── 排除规则：部署不需要的文件 ────────────────────────
# tests/          测试和基准报告
# skills/         技能定义（部署后从 GitHub 拉取）
# docs/0*         开发文档（仅保留 runbook.md）
# docs/responses* 迁移报告
# .github/        CI 配置
# config/weknora/ 旧版 WeKnora 配置（已内化）
# .learnings/     学习记录
# .meituan-catpaw/ IDE 缓存
# agentops-*      临时文件
# *_test.go       单元测试
# .dockerignore   Docker 构建时不需要
# ._              macOS AppleDouble 元数据文件
# .DS_Store       macOS 目录元数据
EXCLUDE_PATTERNS=(
    "tests/"
    "skills/"
    "docs/0"
    "docs/responses"
    ".github/"
    "config/weknora/"
    ".learnings/"
    ".meituan-catpaw/"
    "agentops-"
    "_test.go"
    "/._"
    ".DS_Store"
    "node_modules/"
    "dist/"
    ".tar.gz"
)

while IFS= read -r f; do
    skip=false
    for pattern in "${EXCLUDE_PATTERNS[@]}"; do
        if [[ "$f" == *"$pattern"* ]]; then
            skip=true
            break
        fi
    done
    # 跳过已删除的文件（git ls-files 包含已暂存删除但工作区不存在的文件）
    if [ "$skip" = false ] && [ ! -e "$PROJECT_DIR/$f" ]; then
        skip=true
    fi
    if [ "$skip" = false ]; then
        echo "$f" >> "$FILTERED_FILELIST"
    fi
done < "$FILELIST"

FILE_COUNT=$(wc -l < "$FILTERED_FILELIST" | tr -d ' ')
info "待打包文件数: $FILE_COUNT (精简后)"
echo ""

# ── Step 3: 创建 tar 压缩包 ───────────────────────────
step "3/6 创建源码压缩包"

# COPYFILE_DISABLE=1 阻止 macOS tar 自动生成 ._ 前缀的 AppleDouble 元数据文件
# 这是 macOS 专有行为，会导致 Linux 服务器上出现无用的 ._ 文件
COPYFILE_DISABLE=1 tar -cf "$TEMP_TAR" \
    --no-recursion \
    -T "$FILTERED_FILELIST" \
    -C "$PROJECT_DIR" \
    --exclude='._*' \
    --exclude='.DS_Store' \
    2>/dev/null

info "tar 打包完成"
echo ""

# ── Step 4: 清除 macOS 风格文件 ───────────────────────
step "4/6 清除 macOS 风格文件"

# 匹配所有 macOS 元数据文件：
# - ._ 开头的文件（AppleDouble 元数据，可出现在任何路径下）
# - .DS_Store（目录元数据）
# - .AppleDouble（AppleDouble 目录）
# - Icon\r（自定义图标）
MACOS_FILES=$(tar -tf "$TEMP_TAR" | grep -E '(^|/)\._[^/]+$|(^|/)\.DS_Store$|(^|/)\.AppleDouble$|^Icon\r$' || true)

if [ -n "$MACOS_FILES" ]; then
    MACOS_COUNT=$(echo "$MACOS_FILES" | wc -l | tr -d ' ')
    info "发现 $MACOS_COUNT 个 macOS 风格文件，正在删除..."
    while IFS= read -r macfile; do
        tar --delete -f "$TEMP_TAR" "$macfile" 2>/dev/null || true
    done <<< "$MACOS_FILES"
    info "已删除 $MACOS_COUNT 个 macOS 风格文件"
else
    info "未发现 macOS 风格文件"
fi

# ── Step 4.5: 最终验证（确保零 macOS 文件） ──────────
REMAINING=$(tar -tf "$TEMP_TAR" | grep -E '(^|/)\._|(^|/)\.DS_Store' || true)
if [ -n "$REMAINING" ]; then
    warn "警告：仍有 macOS 文件残留！"
    echo "$REMAINING"
    error "打包失败：存在无法清除的 macOS 文件"
fi
echo ""

# ── Step 5: 压缩源码包 ────────────────────────────────
step "5/6 压缩源码包"

gzip -c "$TEMP_TAR" > "$ARCHIVE_PATH"
rm -f "$TEMP_TAR"

# 获取源码包大小
if [ "$(uname)" = "Darwin" ]; then
    ARCHIVE_SIZE=$(stat -f%z "$ARCHIVE_PATH" 2>/dev/null || echo "0")
else
    ARCHIVE_SIZE=$(stat -c%s "$ARCHIVE_PATH" 2>/dev/null || echo "0")
fi

human_size() {
    local bytes=$1
    if [ "$bytes" -ge 1048576 ]; then
        echo "$(echo "scale=1; $bytes / 1048576" | bc) MB"
    elif [ "$bytes" -ge 1024 ]; then
        echo "$(echo "scale=1; $bytes / 1024" | bc) KB"
    else
        echo "$bytes B"
    fi
}

HUMAN_SIZE=$(human_size "$ARCHIVE_SIZE")
info "源码包大小: $HUMAN_SIZE"
echo ""

# ── Step 6: 可选 — 导出 Docker 镜像 ───────────────────
IMAGES_PATH=""
if [ "$EXPORT_IMAGES" = true ]; then
    step "6/6 导出 Docker 镜像（避免服务器拉镜像慢）"

    IMAGES_NAME="${PROJECT_NAME}-images-${GIT_COMMIT}.tar.gz"
    IMAGES_PATH="${OUTPUT_DIR%/}/${IMAGES_NAME}"

    # 确保本地镜像存在
    info "检查本地 Docker 镜像..."

    # 构建（如果不存在）
    if ! docker image inspect luminbuddy-v2-frontend:latest >/dev/null 2>&1; then
        warn "前端镜像不存在，正在构建..."
        docker compose build frontend
    fi
    if ! docker image inspect luminbuddy-v2-backend:latest >/dev/null 2>&1; then
        warn "后端镜像不存在，正在构建..."
        docker compose build backend
    fi

    info "导出镜像（frontend + backend）..."
    # 导出两个镜像到单个 tar
    docker save luminbuddy-v2-frontend:latest luminbuddy-v2-backend:latest \
        | gzip > "$IMAGES_PATH"

    if [ "$(uname)" = "Darwin" ]; then
        IMAGES_SIZE=$(stat -f%z "$IMAGES_PATH" 2>/dev/null || echo "0")
    else
        IMAGES_SIZE=$(stat -c%s "$IMAGES_PATH" 2>/dev/null || echo "0")
    fi
    IMAGES_HUMAN=$(human_size "$IMAGES_SIZE")

    info "镜像包大小: $IMAGES_HUMAN"
    info "镜像包路径: $IMAGES_PATH"
else
    step "6/6 跳过镜像导出（使用 --images 启用）"
fi
echo ""

# ── 输出汇总 ──────────────────────────────────────────
echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN} ✅ 打包完成${NC}"
echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""
echo -e "  源码包:  ${CYAN}$ARCHIVE_NAME${NC}"
echo -e "  路径:    ${CYAN}$ARCHIVE_PATH${NC}"
echo -e "  大小:    ${YELLOW}$HUMAN_SIZE${NC}"
echo -e "  分支:    $GIT_BRANCH"
echo -e "  提交:    $GIT_COMMIT"
echo -e "  文件数:  $FILE_COUNT"
echo ""

if [ -n "$IMAGES_PATH" ]; then
    echo -e "  镜像包:  ${CYAN}$(basename "$IMAGES_PATH")${NC}"
    echo -e "  路径:    ${CYAN}$IMAGES_PATH${NC}"
    echo -e "  大小:    ${YELLOW}$IMAGES_HUMAN${NC}"
    echo ""
fi

# ── 部署说明 ──────────────────────────────────────────
echo -e "  ${CYAN}部署步骤:${NC}"
echo ""

if [ "$EXPORT_IMAGES" = true ]; then
    echo -e "  ${YELLOW}【有镜像包 — 无需服务器 build】${NC}"
    echo -e "  1. 上传两个文件到服务器: $(basename "$ARCHIVE_PATH") + $(basename "$IMAGES_PATH")"
    echo -e "  2. SSH 到服务器，进入项目目录（如 /opt/luminbuddy-v2）"
    echo -e "  3. 解压源码: tar -xzf $(basename "$ARCHIVE_PATH")"
    echo -e "  4. 加载镜像: docker load -i $(basename "$IMAGES_PATH")"
    echo -e "  5. 配置环境: cp .env.docker.example .env.docker && vi .env.docker"
    echo -e "  6. 启动:     docker compose up -d  # 无需 --build！"
else
    echo -e "  ${YELLOW}【仅源码包 — 服务器需要 build】${NC}"
    echo -e "  1. 上传 $(basename "$ARCHIVE_PATH") 到服务器"
    echo -e "  2. SSH 到服务器，进入项目目录"
    echo -e "  3. 解压: tar -xzf $(basename "$ARCHIVE_PATH")"
    echo -e "  4. 配置: cp .env.docker.example .env.docker && vi .env.docker"
    echo -e "  5. 启动: docker compose up -d --build"
    echo ""
    echo -e "  ${YELLOW}提示: 国内服务器构建慢，建议使用 --images 参数导出镜像${NC}"
    echo -e "       ./scripts/pack-for-1panel.sh --images"
fi
echo ""

# SHA256
SHA256=$(shasum -a 256 "$ARCHIVE_PATH" 2>/dev/null | awk '{print $1}' || echo "N/A")
if [ "$SHA256" != "N/A" ]; then
    echo -e "  SHA256 (源码):  $SHA256"
    echo "$SHA256" > "${ARCHIVE_PATH}.sha256"
fi

if [ -n "$IMAGES_PATH" ]; then
    SHA256_IMG=$(shasum -a 256 "$IMAGES_PATH" 2>/dev/null | awk '{print $1}' || echo "N/A")
    if [ "$SHA256_IMG" != "N/A" ]; then
        echo -e "  SHA256 (镜像):  $SHA256_IMG"
        echo "$SHA256_IMG" > "${IMAGES_PATH}.sha256"
    fi
fi
echo ""
