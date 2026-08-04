#!/bin/bash
# ─── Writing Agent V2 — 1Panel 部署打包脚本 ──────────────────
#
# 功能：
#   1. 基于 git HEAD 创建干净的部署压缩包
#   2. 清除 macOS 风格文件（._ 开头、.DS_Store）
#   3. 排除不必要的大文件目录（node_modules、.git、docs/assets）
#   4. 输出 .tar.gz 压缩包，可直接上传到 1Panel 文件管理器
#
# 用法：
#   chmod +x scripts/pack-for-1panel.sh
#   ./scripts/pack-for-1panel.sh [输出目录]
#
# 参数：
#   $1 (可选) — 输出目录，默认为项目根目录
#
# 示例：
#   ./scripts/pack-for-1panel.sh                    # 输出到项目根
#   ./scripts/pack-for-1panel.sh ~/Downloads        # 输出到 ~/Downloads
#   ./scripts/pack-for-1panel.sh /tmp               # 输出到 /tmp

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

# ── 路径解析 ──────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OUTPUT_DIR="${1:-$PROJECT_DIR}"

cd "$PROJECT_DIR"

# ── 版本信息 ──────────────────────────────────────────
GIT_BRANCH="$(git branch --show-current 2>/dev/null || echo 'unknown')"
GIT_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
GIT_DATE="$(git log -1 --format='%cd' --date='format:%Y%m%d-%H%M' 2>/dev/null || echo "$(date +%Y%m%d-%H%M)")"
PROJECT_NAME="writing-agent-v2"
ARCHIVE_NAME="${PROJECT_NAME}-${GIT_BRANCH}-${GIT_COMMIT}-${GIT_DATE}.tar.gz"
ARCHIVE_PATH="${OUTPUT_DIR%/}/${ARCHIVE_NAME}"

info "项目目录:   $PROJECT_DIR"
info "Git 分支:   $GIT_BRANCH"
info "Git 提交:   $GIT_COMMIT"
info "构建时间:   $GIT_DATE"
info "输出文件:   $ARCHIVE_PATH"
echo ""

# ── Step 1: 预检查 ────────────────────────────────────
step "1/5 预检查"

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

# ── Step 2: 确定要打包的文件列表 ──────────────────────
step "2/5 构建文件清单"

# 临时文件列表
FILELIST="$(mktemp /tmp/writing-agent-filelist.XXXXXX)"
trap 'rm -f "$FILELIST"' EXIT

# 使用 git ls-files 获取版本控制文件，再补充未跟踪但需要的文件
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
    "DEPLOY.md" \
    "agentops-health-check-2026-08-03.md"; do
    if [ -f "$PROJECT_DIR/$extra" ] && ! grep -qxF "$extra" "$FILELIST"; then
        echo "$extra" >> "$FILELIST"
    fi
done

# 排除不需要的文件/目录
# - docs/assets/ 下的二进制资源文件（体积大，部署不需要）
# - .learnings/   学习记录
# - .meituan-catpaw/  IDE 缓存
# - agentops-awesome-list-main.zip  临时下载的压缩包
EXCLUDE_PATTERNS=(
    "docs/assets/"
    ".learnings/"
    ".meituan-catpaw/"
    "agentops-awesome-list-main.zip"
)

FILTERED_FILELIST="$(mktemp /tmp/writing-agent-filtered.XXXXXX)"
trap 'rm -f "$FILELIST" "$FILTERED_FILELIST"' EXIT

while IFS= read -r f; do
    skip=false
    for pattern in "${EXCLUDE_PATTERNS[@]}"; do
        if [[ "$f" == *"$pattern"* ]]; then
            skip=true
            break
        fi
    done
    if [ "$skip" = false ]; then
        echo "$f" >> "$FILTERED_FILELIST"
    fi
done < "$FILELIST"

FILE_COUNT=$(wc -l < "$FILTERED_FILELIST" | tr -d ' ')
info "待打包文件数: $FILE_COUNT"
echo ""

# ── Step 3: 创建 tar.gz 压缩包 ────────────────────────
step "3/5 创建压缩包"

# 创建临时 tar 文件，然后压缩
TEMP_TAR="$(mktemp /tmp/writing-agent-archive.XXXXXX.tar)"
trap 'rm -f "$FILELIST" "$FILTERED_FILELIST" "$TEMP_TAR"' EXIT

# 使用 tar 从文件列表打包
tar -cf "$TEMP_TAR" \
    --no-recursion \
    -T "$FILTERED_FILELIST" \
    -C "$PROJECT_DIR" \
    2>/dev/null

info "tar 打包完成"
echo ""

# ── Step 4: 清除 macOS 风格文件 ───────────────────────
step "4/5 清除 macOS 风格文件 (.appledouble / ._ / .DS_Store)"

# 列出 tar 中的 macOS 风格文件
MACOS_FILES=$(tar -tf "$TEMP_TAR" | grep -E '(^\./\._|/\._|\.DS_Store$|\.AppleDouble$|Icon\r$)' || true)

if [ -n "$MACOS_FILES" ]; then
    MACOS_COUNT=$(echo "$MACOS_FILES" | wc -l | tr -d ' ')
    info "发现 $MACOS_COUNT 个 macOS 风格文件，正在删除..."

    # 逐个从 tar 中删除
    while IFS= read -r macfile; do
        tar --delete -f "$TEMP_TAR" "$macfile" 2>/dev/null || true
    done <<< "$MACOS_FILES"

    info "已删除 $MACOS_COUNT 个 macOS 风格文件"
else
    info "未发现 macOS 风格文件"
fi
echo ""

# ── Step 5: 压缩并输出 ────────────────────────────────
step "5/5 压缩并输出"

gzip -c "$TEMP_TAR" > "$ARCHIVE_PATH"
rm -f "$TEMP_TAR"

# 获取文件大小
if [ "$(uname)" = "Darwin" ]; then
    ARCHIVE_SIZE=$(stat -f%z "$ARCHIVE_PATH" 2>/dev/null || echo "0")
else
    ARCHIVE_SIZE=$(stat -c%s "$ARCHIVE_PATH" 2>/dev/null || echo "0")
fi

# 转换为人类可读大小
if [ "$ARCHIVE_SIZE" -ge 1048576 ]; then
    HUMAN_SIZE="$(echo "scale=1; $ARCHIVE_SIZE / 1048576" | bc) MB"
elif [ "$ARCHIVE_SIZE" -ge 1024 ]; then
    HUMAN_SIZE="$(echo "scale=1; $ARCHIVE_SIZE / 1024" | bc) KB"
else
    HUMAN_SIZE="$ARCHIVE_SIZE B"
fi

echo ""
echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN} ✅ 打包完成${NC}"
echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""
echo -e "  文件名:  ${CYAN}$ARCHIVE_NAME${NC}"
echo -e "  路径:    ${CYAN}$ARCHIVE_PATH${NC}"
echo -e "  大小:    ${YELLOW}$HUMAN_SIZE${NC}"
echo -e "  分支:    $GIT_BRANCH"
echo -e "  提交:    $GIT_COMMIT"
echo -e "  文件数:  $FILE_COUNT"
echo ""
echo -e "  ${CYAN}1Panel 上传步骤:${NC}"
echo -e "  1. 登录 1Panel 面板"
echo -e "  2. 进入「主机」→「文件」"
echo -e "  3. 导航到目标目录（如 /opt/writing-agent-v2）"
echo -e "  4. 点击「上传」，选择 ${ARCHIVE_NAME}"
echo -e "  5. 右键解压"
echo -e "  6. 在目录中执行: cp .env.docker.example .env.docker && 编辑配置"
echo -e "  7. 执行: docker compose up -d --build"
echo ""

# 可选：计算 sha256 校验和
SHA256=$(shasum -a 256 "$ARCHIVE_PATH" 2>/dev/null | awk '{print $1}' || echo "N/A")
if [ "$SHA256" != "N/A" ]; then
    echo -e "  SHA256:  $SHA256"
    echo "$SHA256" > "${ARCHIVE_PATH}.sha256"
    info "校验和已保存到 ${ARCHIVE_PATH}.sha256"
fi
echo ""
