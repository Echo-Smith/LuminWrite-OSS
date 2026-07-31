#!/bin/bash
# ─── WeKnora → V2 本地知识库 数据迁移脚本 ───────────────
# 用法：
#   1. 确保旧 WeKnora PostgreSQL 容器还在运行
#   2. 确保新 V2 PostgreSQL 已启动并执行了 045 migration
#   3. 运行: bash backend/scripts/migrate_weknora_to_local.sh
#
# 本脚本从旧 WeKnora PostgreSQL 中导出知识条目，
# 导入到 V2 的 PostgreSQL knowledge_base 表中。

set -euo pipefail

# 配置
WEKNORA_DB_HOST="${WEKNORA_DB_HOST:-localhost}"
WEKNORA_DB_PORT="${WEKNORA_DB_PORT:-5433}"
WEKNORA_DB_USER="${WEKNORA_DB_USER:-postgres}"
WEKNORA_DB_NAME="${WEKNORA_DB_NAME:-weknora}"
WEKNORA_DB_PASSWORD="${WEKNORA_DB_PASSWORD:-weknora123}"

V2_DB_HOST="${V2_DB_HOST:-localhost}"
V2_DB_PORT="${V2_DB_PORT:-5432}"
V2_DB_USER="${V2_DB_USER:-postgres}"
V2_DB_NAME="${V2_DB_NAME:-writing_agent_v2}"
V2_DB_PASSWORD="${V2_DB_PASSWORD:-postgres}"

TEMP_DIR="${TEMP_DIR:-/tmp/weknora_migration}"

echo "═══════════════════════════════════════════════════════"
echo "  WeKnora → V2 本地知识库 数据迁移"
echo "═══════════════════════════════════════════════════════"
echo ""
echo "源 (WeKnora): ${WEKNORA_DB_HOST}:${WEKNORA_DB_PORT}/${WEKNORA_DB_NAME}"
echo "目标 (V2):    ${V2_DB_HOST}:${V2_DB_PORT}/${V2_DB_NAME}"
echo ""

mkdir -p "$TEMP_DIR"

# ─── Step 1: 导出 WeKnora 知识条目 ──────────────────────
echo "▶ Step 1: 从 WeKnora 导出知识条目..."

WEKNORA_EXPORT="${TEMP_DIR}/weknora_knowledge.csv"

PGPASSWORD="$WEKNORA_DB_PASSWORD" psql \
  -h "$WEKNORA_DB_HOST" \
  -p "$WEKNORA_DB_PORT" \
  -U "$WEKNORA_DB_USER" \
  -d "$WEKNORA_DB_NAME" \
  -c "\COPY (
    SELECT
      id,
      title,
      content,
      COALESCE(source, ''),
      COALESCE(status, 'active'),
      created_at,
      updated_at
    FROM knowledge
    WHERE deleted_at IS NULL
  ) TO '$WEKNORA_EXPORT' WITH CSV HEADER" \
  2>&1 || {
    echo "⚠ 无法从 WeKnora 导出（表名可能不同或数据库不可达）"
    echo "  尝试查找正确的表名..."
    PGPASSWORD="$WEKNORA_DB_PASSWORD" psql \
      -h "$WEKNORA_DB_HOST" \
      -p "$WEKNORA_DB_PORT" \
      -U "$WEKNORA_DB_USER" \
      -d "$WEKNORA_DB_NAME" \
      -c "\dt" 2>&1 || true
    echo ""
    echo "如果 WeKnora 已停止，请手动从备份导入数据。"
    exit 1
  }

EXPORTED_COUNT=$(wc -l < "$WEKNORA_EXPORT")
echo "  导出了 $((EXPORTED_COUNT - 1)) 条知识"

# ─── Step 2: 导入到 V2 knowledge_base 表 ───────────────
echo "▶ Step 2: 导入到 V2 knowledge_base 表..."

PGPASSWORD="$V2_DB_PASSWORD" psql \
  -h "$V2_DB_HOST" \
  -p "$V2_DB_PORT" \
  -U "$V2_DB_USER" \
  -d "$V2_DB_NAME" \
  -c "\COPY knowledge_base (
    source,
    source_id,
    title,
    content,
    source_type,
    status,
    created_at,
    updated_at
  ) FROM '$WEKNORA_EXPORT' WITH CSV HEADER" \
  2>&1

echo "  导入完成"

# ─── Step 3: 更新 source 字段 ────────────────────────────
echo "▶ Step 3: 更新数据源标记..."

PGPASSWORD="$V2_DB_PASSWORD" psql \
  -h "$V2_DB_HOST" \
  -p "$V2_DB_PORT" \
  -U "$V2_DB_USER" \
  -d "$V2_DB_NAME" \
  -c "UPDATE knowledge_base SET source = 'weknora_migrated', source_type = 'text' WHERE source = '' OR source IS NULL;" \
  2>&1

echo "  标记完成"

# ─── Step 4: 生成缺失的 embeddings ──────────────────────
echo "▶ Step 4: 触发 embedding 生成（通过 V2 后端 API）..."

# This can also be triggered via the admin panel or cron job
curl -s -X POST "http://localhost:8080/api/v2/admin/kb/generate-embeddings" \
  -H "Authorization: Bearer ${ADMIN_TOKEN:-}" \
  -H "Content-Type: application/json" \
  -d '{"batch_size": 25}' 2>&1 || echo "  (API 调用失败，可稍后通过 cron 或管理面板触发)"

echo ""
echo "═══════════════════════════════════════════════════════"
echo "  迁移完成！"
echo "═══════════════════════════════════════════════════════"
echo ""
echo "下一步："
echo "  1. 在 Admin 面板 > 知识库 中检查导入的数据"
echo "  2. 等待 embedding 生成完成（或通过 cron 自动处理）"
echo "  3. 确认无误后可停止旧 WeKnora 容器"
echo ""
echo "清理临时文件: rm -rf $TEMP_DIR"
