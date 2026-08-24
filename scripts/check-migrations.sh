#!/bin/bash
# ─── 迁移文件 PostgreSQL 兼容性检查 ─────────────────────
#
# 扫描所有迁移 .up.sql 文件，检测 MySQL 特有函数和语法，
# 防止 PostgreSQL 不兼容的 SQL 导致迁移链卡住。
#
# 用法：./scripts/check-migrations.sh
#        或在 CI 中作为 pre-check 步骤

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
MIGRATIONS_DIR="$PROJECT_DIR/backend/internal/database/migrations"

if [ ! -d "$MIGRATIONS_DIR" ]; then
    echo -e "${RED}[ERROR] 迁移目录不存在: $MIGRATIONS_DIR${NC}"
    exit 1
fi

echo "扫描迁移文件: $MIGRATIONS_DIR"
echo ""

# MySQL 特有函数/语法 → PostgreSQL 等价物 对照表
# 格式: "MySQL 函数|说明|PostgreSQL 等价"
MYSQL_FUNCTIONS=(
    "LAST_DAY|MySQL 月末函数|DATE_TRUNC('month', ...) + INTERVAL '1 month - 1 day'"
    "GROUP_CONCAT|MySQL 聚合拼接|STRING_AGG(col, ', ')"
    "IFNULL|MySQL 空值替换|COALESCE(col, default)"
    "ISNULL(|MySQL 空值判断|col IS NULL"
    "DATE_FORMAT(|MySQL 日期格式化|TO_CHAR(col, 'YYYY-MM-DD')"
    "STR_TO_DATE(|MySQL 字符串转日期|TO_DATE(col, 'YYYY-MM-DD')"
    "UNIX_TIMESTAMP(|MySQL 时间戳|EXTRACT(EPOCH FROM col)"
    "FROM_UNIXTIME(|MySQL 时间戳转日期|TO_TIMESTAMP(col)"
    "DATE_SUB(|MySQL 日期减法|col - INTERVAL '...'"
    "DATE_ADD(|MySQL 日期加法|col + INTERVAL '...'"
    "TIMESTAMPDIFF(|MySQL 时间差|EXTRACT(EPOCH FROM col2 - col1)"
    "CONCAT_WS(|MySQL 带分隔符拼接|STRING 的 || 操作符"
    "FIND_IN_SET(|MySQL 集合查找|col = ANY(STRING_TO_ARRAY(...))"
)

# 也在 Go 代码的 SQL 查询中检查
GO_DIR="$PROJECT_DIR/backend"

ERRORS=0
WARNINGS=0

# 检查迁移文件
for func_entry in "${MYSQL_FUNCTIONS[@]}"; do
    func=$(echo "$func_entry" | cut -d'|' -f1)
    desc=$(echo "$func_entry" | cut -d'|' -f2)
    pg_eq=$(echo "$func_entry" | cut -d'|' -f3)

    # 扫描 .up.sql 文件（排除注释行）
    matches=$(grep -rn "$func" "$MIGRATIONS_DIR"/*.up.sql 2>/dev/null | grep -v '^\s*--' | grep -vi '注意.*不支持' | grep -vi 'PostgreSQL.*不' || true)

    if [ -n "$matches" ]; then
        echo -e "${RED}[ERROR]${NC} 发现 MySQL 特有函数: $func ($desc)"
        echo -e "  PostgreSQL 等价: ${pg_eq}"
        echo "$matches" | while IFS= read -r line; do
            echo "  $line"
        done
        echo ""
        ERRORS=$((ERRORS + 1))
    fi

    # 扫描 Go 文件中的 SQL 字符串
    go_matches=$(grep -rn "$func" "$GO_DIR" --include='*.go' 2>/dev/null | grep -v '_test.go' | grep -v '^\s*//' || true)
    if [ -n "$go_matches" ]; then
        # 过滤掉注释和字符串中的提及
        real_matches=$(echo "$go_matches" | grep -vi '说明\|注释\|等价\|不支持' || true)
        if [ -n "$real_matches" ]; then
            echo -e "${YELLOW}[WARN]${NC} Go 代码中可能使用了 MySQL 函数: $func"
            echo "$real_matches" | head -5
            echo ""
            WARNINGS=$((WARNINGS + 1))
        fi
    fi
done

# 检查 MIN(id) 直接用于 UUID 列（PostgreSQL 不支持 MIN(uuid)）
min_uuid=$(grep -rn 'MIN(id)' "$MIGRATIONS_DIR"/*.up.sql 2>/dev/null | grep -v '::text' || true)
if [ -n "$min_uuid" ]; then
    echo -e "${RED}[ERROR]${NC} 发现 MIN(id) 用于 UUID 列（PostgreSQL 不支持 MIN(uuid) 聚合）"
    echo "  修复方式: MIN(id::text)::uuid"
    echo "$min_uuid"
    echo ""
    ERRORS=$((ERRORS + 1))
fi

# 检查迁移文件命名规范（NNN_description.up.sql）
bad_names=$(find "$MIGRATIONS_DIR" -name '*.up.sql' | while read -r f; do
    basename=$(basename "$f")
    if ! echo "$basename" | grep -qE '^[0-9]{3}_[a-z_]+\.up\.sql$'; then
        echo "$f"
    fi
done || true)

if [ -n "$bad_names" ]; then
    echo -e "${YELLOW}[WARN]${NC} 迁移文件命名不符合规范 (NNN_description.up.sql):"
    echo "$bad_names"
    echo ""
    WARNINGS=$((WARNINGS + 1))
fi

# 检查每个 .up.sql 是否有对应的 .down.sql
for up_file in "$MIGRATIONS_DIR"/*.up.sql; do
    [ -f "$up_file" ] || continue
    down_file="${up_file%.up.sql}.down.sql"
    if [ ! -f "$down_file" ]; then
        echo -e "${YELLOW}[WARN]${NC} 缺少 down 迁移文件: $(basename "$down_file")"
        WARNINGS=$((WARNINGS + 1))
    fi
done

# 汇总
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
if [ "$ERRORS" -gt 0 ]; then
    echo -e "${RED}❌ 发现 $ERRORS 个错误${NC}"
    exit 1
elif [ "$WARNINGS" -gt 0 ]; then
    echo -e "${YELLOW}⚠️  发现 $WARNINGS 个警告${NC}"
    echo "（警告不阻断打包，但建议检查）"
else
    echo -e "${GREEN}✅ 所有迁移文件检查通过${NC}"
fi
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
