#!/usr/bin/env bash
# ─── sync-env.sh — 从「最新模板 + 真实密钥」生成 .env.docker ───
#
# .env.docker 是生成产物，请勿手工编辑：
#   - 模板 .env.docker.example 随版本更新（新增/删除键），本脚本负责合并，
#     你在 env.secrets.local 里填的值永远覆盖模板默认值。
#   - 密钥只维护在 env.secrets.local（默认在工作区根，即本目录的上一级，
#     oss 与 commercial 两份副本共用；也可用 -s 指定其他路径）。
#   - 日常换 key 也可以不改任何文件：在 Admin 后台
#     「模型配置」/「MCP 管理 → 服务密钥」修改，存数据库、优先级更高。
#
# 用法:
#   ./scripts/sync-env.sh              # 生成 .env.docker
#   ./scripts/sync-env.sh --check      # 只检查不写文件（有占位符残留时退出码 1）
#   ./scripts/sync-env.sh -s <secrets> # 显式指定密钥文件
#
# 另: docker compose 的 ${...} 插值只读项目根 .env（不读 .env.docker），
#     本脚本会把合并后的 POSTGRES_PASSWORD / POSTGRES_PORT / BACKEND_PORT /
#     FRONTEND_PORT 同步写入根 .env 托管块。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

TEMPLATE="$PROJECT_DIR/.env.docker.example"
OUT="$PROJECT_DIR/.env.docker"
OUT_COMPOSE="$PROJECT_DIR/.env"
SECRETS=""
CHECK=0

# 合并后仍为占位符、会影响核心功能的键（警告清单）
REQUIRED_KEYS="JWT_SECRET ADMIN_TOKEN API_KEY_ENCRYPTION_KEY AI_API_KEY DASHSCOPE_API_KEY TAVILY_API_KEY ZHIHU_ACCESS_SECRET TENCENT_NEWS_API_KEY JIAOZHEN_API_KEY WEIBO_APP_ID WEIBO_APP_SECRET"
# compose 插值键（同步写入项目根 .env）
INTERP_KEYS="POSTGRES_PASSWORD POSTGRES_PORT BACKEND_PORT FRONTEND_PORT"

usage() { grep '^#' "$0" | sed 's/^# \{0,1\}//'; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    -s) SECRETS="${2:-}"; shift 2 ;;
    --check) CHECK=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "✗ 未知参数: ${1}（--help 查看用法）" >&2; exit 2 ;;
  esac
done

[[ -f "$TEMPLATE" ]] || { echo "✗ 缺少模板: $TEMPLATE" >&2; exit 2; }

# 密钥文件定位：-s > 工作区根(../env.secrets.local) > 项目根(./env.secrets.local)
if [[ -z "$SECRETS" ]]; then
  for cand in "$PROJECT_DIR/../env.secrets.local" "$PROJECT_DIR/env.secrets.local"; do
    [[ -f "$cand" ]] && { SECRETS="$cand"; break; }
  done
fi
if [[ -z "$SECRETS" || ! -f "$SECRETS" ]]; then
  echo "✗ 未找到密钥文件 env.secrets.local" >&2
  echo "  首次使用: cp $PROJECT_DIR/../env.secrets.example $PROJECT_DIR/../env.secrets.local 并填入真实值" >&2
  exit 2
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
MERGED="$WORK/merged.env"
UNUSED="$WORK/unused.txt"

# 合并：以模板为基底，secrets 同名键覆盖值（保留模板注释与顺序）；
# secrets 中模板已不存在的键写入 UNUSED。
awk -v secrets="$SECRETS" -v out="$MERGED" -v unused="$UNUSED" '
BEGIN {
  while ((getline l < secrets) > 0) {
    sub(/\r$/, "", l)
    if (l !~ /^[[:space:]]*(export[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*=/) continue
    eq = index(l, "=")
    k = substr(l, 1, eq - 1)
    sub(/^[[:space:]]+/, "", k); sub(/^export[[:space:]]+/, "", k)
    v = substr(l, eq + 1)
    sub(/^[[:space:]]+/, "", v); sub(/[[:space:]]+$/, "", v)
    if (v ~ /^".*"$/ || v ~ /^\x27.*\x27$/) v = substr(v, 2, length(v) - 2)
    sec[k] = v; order[++n] = k
  }
  close(secrets)
}
{
  line = $0
  if (line ~ /^[[:space:]]*(export[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*=/) {
    eq = index(line, "=")
    k = substr(line, 1, eq - 1)
    sub(/^[[:space:]]+/, "", k); sub(/^export[[:space:]]+/, "", k)
    if (k in sec) { line = k "=" sec[k]; used[k] = 1 }
  }
  print line > out
}
END {
  for (i = 1; i <= n; i++) if (!(order[i] in used)) print order[i] > unused
}
' "$TEMPLATE"

chmod 600 "$MERGED"

# ── 报告 ──────────────────────────────────────────────────
status=0

# secrets 中模板已删除的键
if [[ -s "$UNUSED" ]]; then
  echo "⚠ 密钥文件中以下键在最新模板中已不存在（已忽略，可从 secrets 中删除）:"
  sed 's/^/    /' "$UNUSED"
fi

# 必填键占位符残留
missing=""
for key in $REQUIRED_KEYS; do
  val="$(sed -n "s/^${key}=//p" "$MERGED" | tail -n 1)"
  if [[ -z "$val" || "$val" == your-* || "$val" == change-this* ]]; then
    missing="$missing $key"
  fi
done
if [[ -n "$missing" ]]; then
  echo "⚠ 以下键仍为空或占位符，对应功能不可用:$missing"
  status=1
fi

if [[ "$CHECK" -eq 1 ]]; then
  if [[ "$status" -eq 0 ]]; then
    echo "✓ 检查通过：所有必填键均已配置"
  else
    echo "✗ 检查未通过（--check 模式未写入任何文件）" >&2
  fi
  exit "$status"
fi

# ── 原子写出 .env.docker ──────────────────────────────────
mv "$MERGED" "$OUT"
echo "✓ 已生成 ${OUT}（模板 .env.docker.example ＋ 密钥 ${SECRETS}）"

# ── 同步 compose 插值键到项目根 .env（托管块）─────────────
block_start="# ── 由 scripts/sync-env.sh 自动生成（docker compose 插值用），勿手工编辑 ──"
{
  echo "$block_start"
  for key in $INTERP_KEYS; do
    val="$(sed -n "s/^${key}=//p" "$OUT" | tail -n 1)"
    [[ -n "$val" ]] && echo "${key}=${val}"
  done
} > "$WORK/compose.env"

if [[ -f "$OUT_COMPOSE" ]] && grep -qF "$block_start" "$OUT_COMPOSE"; then
  awk -v bs="$block_start" -v repl="$WORK/compose.env" '
    BEGIN { inblock = 0 }
    $0 == bs {
      inblock = 1
      while ((getline l < repl) > 0) print l
      close(repl)
      next
    }
    inblock && /^[A-Za-z_][A-Za-z0-9_]*=/ { next }
    { inblock = 0; print }
  ' "$OUT_COMPOSE" > "$WORK/.env.new" && mv "$WORK/.env.new" "$OUT_COMPOSE"
elif [[ -f "$OUT_COMPOSE" ]]; then
  cat "$WORK/compose.env" >> "$OUT_COMPOSE"
else
  cp "$WORK/compose.env" "$OUT_COMPOSE"
fi
chmod 600 "$OUT_COMPOSE" 2>/dev/null || true

# 生成路径只警告不失败（缺失键的硬门禁用 --check / make env-check）
exit 0
