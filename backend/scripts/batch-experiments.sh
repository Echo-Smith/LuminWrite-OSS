#!/usr/bin/env bash
# ─── 4.3: 批量对照实验脚本 ──────────────────────────────────
# 对 10 个真实选题依次运行 pipeline vs unified vs editorial 对照实验
# 用法: ./batch-experiments.sh [API_BASE_URL]
# 默认 API 地址: http://localhost:8080

set -euo pipefail

API_BASE="${1:-http://localhost:8080}"
RESULTS_DIR="./experiment-results"
mkdir -p "$RESULTS_DIR"

# 10 个真实选题 — 覆盖科技/财经/社会/文化/教育等领域
TOPICS=(
  "AI 大模型对软件开发流程的影响"
  "中国新能源汽车出海的机遇与挑战"
  "远程办公如何重塑企业管理模式"
  "数据要素市场化配置的路径探索"
  "平台经济反垄断的成效与展望"
  "银发经济崛起背后的消费升级"
  "生成式 AI 版权争议的核心问题"
  "碳排放交易市场的发展现状分析"
  "短视频对青少年阅读习惯的影响"
  "开源大模型生态的竞争格局"
)

echo "══════════════════════════════════════════════════════════════"
echo "  批量对照实验 — Pipeline vs Unified vs Editorial"
echo "  API: $API_BASE"
echo "  选题数: ${#TOPICS[@]}"
echo "══════════════════════════════════════════════════════════════"

# 检查 API 可用性
HEALTH=$(curl -sf "${API_BASE}/health" 2>/dev/null || echo "UNREACHABLE")
if [ "$HEALTH" = "UNREACHABLE" ]; then
  echo "❌ API 不可达: $API_BASE"
  echo "   请确保后端服务已启动"
  exit 1
fi
echo "✅ API 可达"

SUCCESS=0
FAILED=0
declare -a EXPERIMENT_IDS

for i in "${!TOPICS[@]}"; do
  TOPIC="${TOPICS[$i]}"
  NUM=$((i + 1))
  TIMESTAMP=$(date +%Y%m%d_%H%M%S)
  RESULT_FILE="${RESULTS_DIR}/experiment_${NUM}_${TIMESTAMP}.json"

  echo ""
  echo "─── 实验 ${NUM}/10: ${TOPIC} ───"

  # 创建实验
  CREATE_RESP=$(curl -sf -X POST "${API_BASE}/api/editorial/experiments" \
    -H "Content-Type: application/json" \
    -d "{\"title\": \"${TOPIC}\", \"description\": \"批量对照实验 #${NUM}\", \"style_slug\": \"yinyue\"}" 2>&1) || {
    echo "  ❌ 创建实验失败: ${CREATE_RESP}"
    FAILED=$((FAILED + 1))
    continue
  }

  EXP_ID=$(echo "$CREATE_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || echo "")
  if [ -z "$EXP_ID" ]; then
    echo "  ❌ 无法获取实验 ID: $CREATE_RESP"
    FAILED=$((FAILED + 1))
    continue
  fi

  EXPERIMENT_IDS+=("$EXP_ID")
  echo "  ✅ 实验已创建: $EXP_ID"

  # 启动实验
  curl -sf -X POST "${API_BASE}/api/editorial/experiments/${EXP_ID}/run" -H "Content-Type: application/json" -d "{}" > /dev/null 2>&1 || {
    echo "  ⚠️  实验启动可能失败（可能已在运行）"
  }
  echo "  🚀 实验已启动，等待完成..."

  # 轮询等待实验完成（最多 10 分钟）
  MAX_WAIT=600
  WAITED=0
  POLL_INTERVAL=15

  while [ $WAITED -lt $MAX_WAIT ]; do
    sleep $POLL_INTERVAL
    WAITED=$((WAITED + POLL_INTERVAL))

    STATUS_RESP=$(curl -sf "${API_BASE}/api/editorial/experiments/${EXP_ID}" 2>/dev/null || echo "{}")
    STATUS=$(echo "$STATUS_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status','unknown'))" 2>/dev/null || echo "unknown")

    echo "  ⏳ [${WAITED}s] 状态: ${STATUS}"

    if [ "$STATUS" = "completed" ] || [ "$STATUS" = "failed" ]; then
      echo "  📊 实验结束: ${STATUS}"

      # 保存完整结果
      echo "$STATUS_RESP" | python3 -m json.tool > "$RESULT_FILE" 2>/dev/null || echo "$STATUS_RESP" > "$RESULT_FILE"
      echo "  💾 结果已保存: ${RESULT_FILE}"

      if [ "$STATUS" = "completed" ]; then
        SUCCESS=$((SUCCESS + 1))
      else
        FAILED=$((FAILED + 1))
      fi
      break
    fi
  done

  if [ $WAITED -ge $MAX_WAIT ]; then
    echo "  ⏰ 实验超时（10分钟）"
    FAILED=$((FAILED + 1))
  fi

  # 选题间间隔 30 秒，避免 API 限流
  if [ $NUM -lt ${#TOPICS[@]} ]; then
    echo "  ⏸️  间隔 30 秒..."
    sleep 30
  fi
done

echo ""
echo "══════════════════════════════════════════════════════════════"
echo "  批量实验完成"
echo "  成功: ${SUCCESS} / 10"
echo "  失败: ${FAILED} / 10"
echo "  结果目录: ${RESULTS_DIR}/"
echo "══════════════════════════════════════════════════════════════"

# 生成汇总报告
SUMMARY_FILE="${RESULTS_DIR}/summary_$(date +%Y%m%d_%H%M%S).json"
python3 - <<PYTHON_EOF
import json, glob, os

results = []
for f in sorted(glob.glob("${RESULTS_DIR}/experiment_*.json")):
    with open(f) as fh:
        data = json.load(fh)
        results.append({
            "title": data.get("title", ""),
            "status": data.get("status", ""),
            "summary": data.get("summary", {}),
            "pipeline": data.get("pipeline_result", {}),
            "unified": data.get("unified_result", {}),
            "editorial": data.get("editorial_result", {}),
        })

# 汇总各模式的平均质量分
mode_scores = {"pipeline": [], "unified": [], "editorial": []}
mode_tokens = {"pipeline": [], "unified": [], "editorial": []}
mode_duration = {"pipeline": [], "unified": [], "editorial": []}

for r in results:
    for mode in mode_scores:
        m = r.get(mode, {})
        if isinstance(m, dict) and m.get("quality_score", 0) > 0:
            mode_scores[mode].append(m["quality_score"])
            mode_tokens[mode].append(m.get("token_cost", 0))
            mode_duration[mode].append(m.get("duration_ms", 0))

summary = {
    "total_experiments": len(results),
    "by_mode": {}
}

for mode in mode_scores:
    scores = mode_scores[mode]
    tokens = mode_tokens[mode]
    durations = mode_duration[mode]
    summary["by_mode"][mode] = {
        "avg_quality": sum(scores) / len(scores) if scores else 0,
        "avg_tokens": sum(tokens) / len(tokens) if tokens else 0,
        "avg_duration_ms": sum(durations) / len(durations) if durations else 0,
        "completed_count": len(scores),
    }

with open("${SUMMARY_FILE}", "w") as f:
    json.dump(summary, f, indent=2, ensure_ascii=False)

print(f"\n📋 汇总报告: ${SUMMARY_FILE}")
print(json.dumps(summary, indent=2, ensure_ascii=False))
PYTHON_EOF
