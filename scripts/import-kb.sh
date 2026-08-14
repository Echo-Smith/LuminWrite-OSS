#!/bin/bash
# ─── 知识库批量导入脚本 ───────────────────────────────
# 用法：
#   ./import-kb.sh knowledge-yinyue.json
#
# 前提：
#   1. 后端服务已启动 (docker compose up -d)
#   2. DASHSCOPE_API_KEY 已配置（embedding 需要）
#
# 功能：
#   1. 读取 JSON 文件中的文章列表
#   2. 通过 API 逐篇导入到知识库
#   3. 显示导入进度和结果

set -euo pipefail

JSON_FILE="${1:-data/knowledge-yinyue.json}"
API_BASE="${API_BASE:-http://localhost:8080/api/v2}"
KB_ID="${KB_ID:-yinyue}"
KB_NAME="${KB_NAME:-预设评论风格}"
KB_DESC="${KB_DESC:-杭州网「预设评论风格」时评专栏文章集 — 用于写作风格参考与知识检索}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; }

if [ ! -f "$JSON_FILE" ]; then
    error "文件不存在: $JSON_FILE"
    echo "用法: $0 <knowledge-json-file>"
    exit 1
fi

# 检查后端是否可用
if ! curl -sf "${API_BASE}/kb/status" >/dev/null 2>&1; then
    error "后端不可达: ${API_BASE}"
    echo "请确保 docker compose 服务已启动"
    exit 1
fi

info "API 地址: ${API_BASE}"
info "知识库 ID: ${KB_ID}"
info "数据文件: ${JSON_FILE}"
echo ""

# ── Step 1: 创建知识库 ────────────────────────────────
info "Step 1: 创建知识库「${KB_NAME}」..."

# 检查是否已存在
EXISTING=$(curl -sf "${API_BASE}/kb/kbs" 2>/dev/null | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    kbs = data.get('data', {}).get('knowledge_bases', [])
    for kb in kbs:
        if kb.get('id') == '${KB_ID}':
            print('exists')
            break
    else:
        print('not_found')
except:
    print('error')
" 2>/dev/null || echo "error")

if [ "$EXISTING" = "exists" ]; then
    info "知识库「${KB_NAME}」已存在，跳过创建"
elif [ "$EXISTING" = "not_found" ]; then
    curl -sf -X POST "${API_BASE}/kb/manage" \
        -H "Content-Type: application/json" \
        -d "{\"id\":\"${KB_ID}\",\"name\":\"${KB_NAME}\",\"description\":\"${KB_DESC}\"}" \
        >/dev/null 2>&1 && info "知识库创建成功" || warn "知识库创建失败（可能已存在）"
else
    warn "无法检查知识库状态，尝试直接创建..."
    curl -sf -X POST "${API_BASE}/kb/manage" \
        -H "Content-Type: application/json" \
        -d "{\"id\":\"${KB_ID}\",\"name\":\"${KB_NAME}\",\"description\":\"${KB_DESC}\"}" \
        >/dev/null 2>&1 || true
fi
echo ""

# ── Step 2: 批量导入文章 ──────────────────────────────
info "Step 2: 批量导入文章..."

# 用 python3 解析 JSON 并逐条调用 API
python3 - "$JSON_FILE" "$API_BASE" "$KB_ID" << 'PYEOF'
import json, sys, urllib.request, urllib.error, time

json_file = sys.argv[1]
api_base = sys.argv[2]
kb_id = sys.argv[3]

with open(json_file, 'r', encoding='utf-8') as f:
    data = json.load(f)

articles = data.get('articles', [])
total = len(articles)
success = 0
failed = 0
skipped = 0

for i, article in enumerate(articles, 1):
    title = article.get('title', f'文章{i}')
    content = article.get('content', '')
    url = article.get('url', '')

    if not content or len(content) < 50:
        print(f"  [{i}/{total}] SKIP: {title} (内容过短)")
        skipped += 1
        continue

    # 先尝试 URL 导入，失败后用文本导入
    try:
        if url:
            req = urllib.request.Request(
                f"{api_base}/kb/knowledge/url",
                data=json.dumps({"url": url, "title": title, "kb_id": kb_id}).encode('utf-8'),
                headers={"Content-Type": "application/json"},
                method="POST"
            )
            resp = urllib.request.urlopen(req, timeout=30)
            result = json.loads(resp.read())
            if result.get('success'):
                print(f"  [{i}/{total}] OK (URL): {title}")
                success += 1
                time.sleep(0.3)
                continue
    except Exception as e:
        pass

    # Fallback: 文本导入
    try:
        req = urllib.request.Request(
            f"{api_base}/kb/knowledge",
            data=json.dumps({"title": title, "content": content, "kb_id": kb_id}).encode('utf-8'),
            headers={"Content-Type": "application/json"},
            method="POST"
        )
        resp = urllib.request.urlopen(req, timeout=60)
        result = json.loads(resp.read())
        if result.get('success'):
            print(f"  [{i}/{total}] OK (text): {title}")
            success += 1
        else:
            print(f"  [{i}/{total}] FAIL: {title}")
            failed += 1
    except Exception as e:
        print(f"  [{i}/{total}] FAIL: {title} ({e})")
        failed += 1

    time.sleep(0.3)

print(f"\n{'='*50}")
print(f"导入完成: {success} 成功, {failed} 失败, {skipped} 跳过")
print(f"总计: {total} 篇文章")
PYEOF

echo ""
info "导入完成！"
echo ""
info "查看知识库状态:"
echo "  curl ${API_BASE}/kb/status"
echo "  curl ${API_BASE}/kb/kbs"
echo ""
info "在 Admin 界面查看: https://luminbuddy2.ericdocmic.top/admin → 知识库"
