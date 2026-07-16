#!/usr/bin/env python3
"""
Agent V2 自动化性能基准测试套件

覆盖五大指标维度:
  1. 单篇文章平均生成或修改时间
  2. 相比人工流程减少了多少步骤或时间
  3. 自动评测通过率、自动修复率
  4. 定时任务成功率或故障数量
  5. 单篇文章 API 成本 (Token 消耗)

用法:
  python3 benchmark_test.py [--base-url http://localhost:8080] [--admin-token <token>]

输出:
  - 控制台实时日志
  - tests/reports/benchmark_report_<timestamp>.json  (结构化报告)
  - 控制台汇总表格
"""

import argparse
import json
import os
import sys
import time
import uuid
import statistics
from datetime import datetime, timezone
from pathlib import Path

import requests
import websocket  # websocket-client

# ────────────────────────────────────────────────────────────
#  常量 & 配置
# ────────────────────────────────────────────────────────────

DEFAULT_BASE_URL = "http://localhost:8080"
DEFAULT_WS_URL   = "ws://localhost:8080"
DEFAULT_ADMIN_TOKEN = "dev-admin-token"

# DeepSeek API 价格 (RMB / 1M tokens) — deepseek-v4-flash
#  input: 0.5 RMB / 1M, output: 2 RMB / 1M (cache miss)
#  https://api-docs.deepseek.com/quick_start/pricing
LLM_INPUT_PRICE_PER_1M  = 0.5   # RMB
LLM_OUTPUT_PRICE_PER_1M = 2.0   # RMB

# 人工流程基准 (基于行业经验估算)
MANUAL_STEPS = [
    "选题与搜索",       # ~10 min
    "阅读与整理素材",    # ~15 min
    "撰写初稿",          # ~25 min
    "自我审查",          # ~10 min
    "修改润色",          # ~10 min
    "最终校对",          # ~5 min
]
MANUAL_TOTAL_MIN = sum([10, 15, 25, 10, 10, 5])  # 75 min
MANUAL_STEP_COUNT = len(MANUAL_STEPS)

# Agent V2 流水线步骤 (auto mode)
AGENT_STEPS_AUTO = [
    "intent", "query_plan", "search", "relevance", "write", "post_review", "auto_fix"
]
AGENT_STEP_COUNT = len(AGENT_STEPS_AUTO)

# ────────────────────────────────────────────────────────────
#  测试用例集
# ────────────────────────────────────────────────────────────

TEST_CASES = [
    # ── 写作类 ──
    {
        "id": "write_01",
        "category": "writing",
        "label": "AI教育变革评论",
        "payload": {
            "message": "写一篇关于AI教育变革的评论",
            "style": "yinyue",
            "mode": "auto",
        },
    },
    {
        "id": "write_02",
        "category": "writing",
        "label": "城市垃圾分类政策",
        "payload": {
            "message": "写一篇关于城市垃圾分类政策实施的文章",
            "style": "yinyue",
            "mode": "auto",
        },
    },
    {
        "id": "write_03",
        "category": "writing",
        "label": "新能源汽车市场竞争",
        "payload": {
            "message": "基于热搜写一篇关于新能源汽车市场竞争的评论",
            "style": "yinyue",
            "mode": "auto",
        },
    },
    {
        "id": "write_04",
        "category": "writing",
        "label": "远程办公效率管理",
        "payload": {
            "message": "写一篇关于远程办公效率管理的文章",
            "style": "yinyue",
            "mode": "auto",
        },
    },
    {
        "id": "write_05",
        "category": "writing",
        "label": "传统文化传承创新",
        "payload": {
            "message": "写一篇关于传统文化在现代社会的传承与创新的文章",
            "style": "yinyue",
            "mode": "auto",
        },
    },
    # ── 修改类 (polish) ──
    {
        "id": "polish_01",
        "category": "polish",
        "label": "润色科技短文",
        "payload": {
            "message": (
                "润色以下文章：\n\n"
                "## 科技改变生活\n\n"
                "科技的发展让我们的生活变得更好。手机让我们可以随时随地和别人联系。"
                "电脑帮助我们工作更高效。互联网让我们获取信息更加方便。"
                "人工智能正在改变我们的工作方式。这些技术进步让社会不断向前发展。\n"
            ),
            "style": "yinyue",
            "mode": "auto",
        },
    },
    {
        "id": "polish_02",
        "category": "polish",
        "label": "润色教育评论",
        "payload": {
            "message": (
                "润色以下文章：\n\n"
                "## 论教育的本质\n\n"
                "教育不仅仅是传授知识。教育更重要的是培养人的思维能力。"
                "好的教育应该让学生学会独立思考。现在的教育太注重考试分数。"
                "我们应该改变这种状况。教育应该回归本质，关注人的全面发展。"
                "只有这样，才能培养出真正有用的人才。\n"
            ),
            "style": "yinyue",
            "mode": "auto",
        },
    },
    # ── 缩写类 (shorten) ──
    {
        "id": "shorten_01",
        "category": "shorten",
        "label": "缩写长文",
        "payload": {
            "message": (
                "将以下文章精简到300字以内：\n\n"
                "## 数字经济的崛起与未来展望\n\n"
                "数字经济已经成为全球经济增长的重要引擎。随着互联网技术的飞速发展，"
                "各行各业都在经历数字化转型。从电子商务到移动支付，从共享经济到平台经济，"
                "数字技术正在深刻改变着商业模式和消费习惯。在中国，数字经济的规模已经超过GDP的三分之一，"
                "成为推动经济高质量发展的重要力量。数字经济的核心驱动力是数据。"
                "数据被称为新时代的石油，是重要的生产要素。通过对海量数据的收集、分析和应用，"
                "企业可以更好地理解市场需求，优化产品和服务，提高运营效率。"
                "同时，人工智能、云计算、区块链等新兴技术的融合发展，进一步释放了数字经济的潜力。"
                "然而，数字经济的发展也面临着诸多挑战。数据安全和隐私保护问题日益突出，"
                "数字鸿沟仍然存在，平台垄断引发公平竞争担忧。未来，数字经济的发展需要"
                "在创新与监管之间找到平衡，确保技术进步惠及更广泛的人群。"
            ),
            "style": "yinyue",
            "mode": "auto",
        },
    },
    # ── 闲聊类 (chat) ──
    {
        "id": "chat_01",
        "category": "chat",
        "label": "普通问答",
        "payload": {
            "message": "什么是三段式写作结构？",
            "style": "yinyue",
            "mode": "auto",
        },
    },
]

# ────────────────────────────────────────────────────────────
#  WebSocket 事件收集器
# ────────────────────────────────────────────────────────────

class AgentEventCollector:
    """连接 WebSocket，发送 agent.start，收集所有事件直到完成或超时。"""

    def __init__(self, ws_url: str, payload: dict, timeout: int = 180):
        self.ws_url = ws_url
        self.payload = payload
        self.timeout = timeout
        self.events = []
        self.trace_id = None
        self.start_time = None
        self.end_time = None
        self.article = ""
        self.review_result = None
        self.token_usage = {"total_tokens": 0}
        self.step_timings = {}
        self.step_results = {}
        self.error = None
        self.status = None

    def run(self) -> dict:
        """执行测试，返回收集结果。"""
        self.start_time = time.time()
        url = f"{self.ws_url}/api/v2/ws/agent"

        try:
            ws = websocket.create_connection(url, timeout=self.timeout)
        except Exception as e:
            self.error = f"WebSocket 连接失败: {e}"
            self.end_time = time.time()
            return self._result()

        try:
            # 发送 agent.start
            start_msg = {
                "type": "agent.start",
                "payload": self.payload,
            }
            ws.send(json.dumps(start_msg))

            # 接收消息循环 — 使用较长的心跳超时
            deadline = time.time() + self.timeout
            no_msg_count = 0
            while time.time() < deadline:
                # 每次最多等待 15 秒，然后检查 deadline
                ws.settimeout(15)
                try:
                    raw = ws.recv()
                    no_msg_count = 0
                except websocket.WebSocketTimeoutException:
                    no_msg_count += 1
                    if self.status in ("completed", "failed", "cancelled"):
                        break
                    # 连续 12 次 15s 超时 = 3 分钟无消息，才认为真正超时
                    if no_msg_count > 12:
                        self.error = "接收消息超时（连续3分钟无数据）"
                        break
                    continue
                except ConnectionResetError:
                    if self.status in ("completed", "failed", "cancelled"):
                        break
                    self.error = "连接被重置"
                    break
                except Exception as e:
                    if self.status in ("completed", "failed", "cancelled"):
                        break
                    self.error = f"WebSocket 接收错误: {type(e).__name__}: {e}"
                    break

                if not raw:
                    continue

                try:
                    msg = json.loads(raw)
                except json.JSONDecodeError:
                    continue

                self._handle_message(msg)

                if self.status in ("completed", "failed", "cancelled"):
                    break

        finally:
            try:
                ws.close()
            except Exception:
                pass
            self.end_time = time.time()

        return self._result()

    def _handle_message(self, msg: dict):
        msg_type = msg.get("type", "")
        payload = msg.get("payload", {})
        ts = time.time()

        self.events.append({
            "timestamp": ts,
            "type": msg_type,
            "payload_summary": self._summarize_payload(msg_type, payload),
        })

        if msg_type == "agent.created":
            self.trace_id = payload.get("trace_id")

        elif msg_type == "agent.step.start":
            step = payload.get("step", "")
            self.step_timings[step] = {"start": ts, "end": None, "duration_ms": 0}

        elif msg_type == "agent.step.complete":
            step = payload.get("step", "")
            duration_ms = payload.get("duration_ms", 0)
            result = payload.get("result", {})
            if step in self.step_timings:
                self.step_timings[step]["end"] = ts
                self.step_timings[step]["duration_ms"] = duration_ms
            self.step_results[step] = result

        elif msg_type == "agent.stream.done":
            full_text = payload.get("full_text", "")
            if full_text:
                self.article = full_text

        elif msg_type == "agent.completed":
            self.article = payload.get("article", self.article)
            self.review_result = payload.get("review")
            self.token_usage = payload.get("token_usage", {"total_tokens": 0})
            self.status = "completed"

        elif msg_type == "agent.error":
            self.error = payload.get("message", "unknown error")
            self.status = "failed"

        elif msg_type == "agent.cancelled":
            self.status = "cancelled"

    def _summarize_payload(self, msg_type: str, payload: dict) -> str:
        if msg_type == "agent.stream":
            return f"delta: {len(payload.get('delta', ''))} chars"
        if msg_type == "agent.step.start":
            return f"step: {payload.get('step')}"
        if msg_type == "agent.step.complete":
            return f"step: {payload.get('step')}, duration_ms: {payload.get('duration_ms')}"
        if msg_type == "agent.completed":
            return f"article_len: {len(payload.get('article', ''))}"
        return msg_type

    def _result(self) -> dict:
        total_duration = (self.end_time or time.time()) - self.start_time
        return {
            "trace_id": self.trace_id,
            "status": self.status or "timeout",
            "error": self.error,
            "total_duration_s": round(total_duration, 2),
            "article_length": len(self.article),
            "article_preview": self.article[:500] if self.article else "",
            "review_result": self.review_result,
            "token_usage": self.token_usage,
            "step_timings": self.step_timings,
            "step_results": self.step_results,
            "events_count": len(self.events),
        }


# ────────────────────────────────────────────────────────────
#  Admin API 查询
# ────────────────────────────────────────────────────────────

def query_admin_api(base_url: str, admin_token: str, endpoint: str, method="GET", body=None) -> dict:
    """查询 admin API。"""
    headers = {"Authorization": f"Bearer {admin_token}"}
    url = f"{base_url}/api/v2/admin/{endpoint}"
    try:
        if method == "GET":
            resp = requests.get(url, headers=headers, timeout=15)
        else:
            resp = requests.post(url, headers=headers, json=body, timeout=15)
        if resp.status_code == 200:
            return resp.json().get("data", resp.json())
        return {"error": f"HTTP {resp.status_code}", "body": resp.text[:200]}
    except Exception as e:
        return {"error": str(e)}


def query_prometheus_metrics(base_url: str) -> dict:
    """查询 /metrics 端点，解析关键 Prometheus 指标。"""
    try:
        resp = requests.get(f"{base_url}/metrics", timeout=15)
        text = resp.text
    except Exception as e:
        return {"error": str(e)}

    metrics = {}
    for line in text.split("\n"):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        # 格式: metric_name{labels} value  或  metric_name value
        parts = line.split()
        if len(parts) < 2:
            continue
        key = parts[0]
        val = parts[-1]
        try:
            val = float(val)
        except ValueError:
            pass
        metrics[key] = val
    return metrics


def query_cron_jobs(base_url: str, admin_token: str) -> list:
    """查询所有 cron 任务。"""
    result = query_admin_api(base_url, admin_token, "cron-jobs")
    if isinstance(result, dict):
        return result.get("jobs", [])
    return result or []


def query_eval_runs(base_url: str, admin_token: str) -> list:
    """查询评测运行记录。"""
    try:
        resp = requests.get(
            f"{base_url}/api/v2/evaluation/runs?page=1&page_size=50",
            timeout=15,
        )
        if resp.status_code == 200:
            return resp.json().get("data", {}).get("runs", [])
    except Exception:
        pass
    return []


def query_token_usage(base_url: str, admin_token: str) -> dict:
    """查询 token 使用量统计。"""
    return query_admin_api(base_url, admin_token, "token-usage")


def query_admin_stats(base_url: str, admin_token: str) -> dict:
    """查询管理后台统计数据。"""
    return query_admin_api(base_url, admin_token, "stats")


# ────────────────────────────────────────────────────────────
#  报告生成
# ────────────────────────────────────────────────────────────

def generate_report(
    test_results: list,
    prometheus_metrics: dict,
    cron_jobs: list,
    eval_runs: list,
    admin_stats: dict,
    token_usage: dict,
) -> dict:
    """生成结构化报告。"""

    # ── 1. 单篇文章平均生成/修改时间 ──
    completed_results = [r for r in test_results if r["status"] == "completed"]
    failed_results = [r for r in test_results if r["status"] != "completed"]

    all_durations = [r["total_duration_s"] for r in completed_results]
    write_durations = [r["total_duration_s"] for r in completed_results if r["category"] == "writing"]
    polish_durations = [r["total_duration_s"] for r in completed_results if r["category"] == "polish"]
    shorten_durations = [r["total_duration_s"] for r in completed_results if r["category"] == "shorten"]
    chat_durations = [r["total_duration_s"] for r in completed_results if r["category"] == "chat"]

    avg_duration = statistics.mean(all_durations) if all_durations else 0
    median_duration = statistics.median(all_durations) if all_durations else 0
    p95_duration = sorted(all_durations)[int(len(all_durations) * 0.95)] if len(all_durations) >= 2 else (all_durations[0] if all_durations else 0)

    # 各步骤平均耗时
    step_avg_timings = {}
    for step in AGENT_STEPS_AUTO:
        durations = []
        for r in completed_results:
            st = r.get("step_timings", {}).get(step, {})
            if st.get("duration_ms"):
                durations.append(st["duration_ms"])
        if durations:
            step_avg_timings[step] = {
                "avg_ms": round(statistics.mean(durations), 0),
                "max_ms": max(durations),
                "min_ms": min(durations),
            }

    # ── 2. 相比人工流程的步骤/时间减少 ──
    avg_agent_minutes = avg_duration / 60.0
    time_saved_minutes = MANUAL_TOTAL_MIN - avg_agent_minutes
    time_saved_pct = (time_saved_minutes / MANUAL_TOTAL_MIN * 100) if MANUAL_TOTAL_MIN > 0 else 0
    steps_reduced = MANUAL_STEP_COUNT - AGENT_STEP_COUNT

    # ── 3. 自动评测通过率、自动修复率 ──
    review_pass_count = 0
    review_fail_count = 0
    autofix_triggered = 0
    autofix_success = 0  # review failed but after autofix, passed
    autofix_fail = 0

    for r in completed_results:
        review = r.get("review_result")
        if not review:
            continue
        passed = review.get("passed", True)
        if passed:
            review_pass_count += 1
        else:
            review_fail_count += 1

        # 检查 auto_fix 步骤是否执行了修复
        autofix_result = r.get("step_results", {}).get("auto_fix", {})
        if autofix_result and autofix_result.get("fixed") == True:
            autofix_triggered += 1
            autofix_success += 1
        elif not passed:
            autofix_fail += 1

    total_reviewed = review_pass_count + review_fail_count
    review_pass_rate = (review_pass_count / total_reviewed * 100) if total_reviewed > 0 else 0
    autofix_rate = (autofix_success / max(autofix_triggered, 1)) * 100

    # ── 4. 定时任务成功率/故障数量 ──
    cron_success = sum(1 for j in cron_jobs if j.get("status") == "success")
    cron_failed = sum(1 for j in cron_jobs if j.get("status") == "failed")
    cron_running = sum(1 for j in cron_jobs if j.get("status") == "running")
    cron_pending = sum(1 for j in cron_jobs if j.get("status") == "pending")
    cron_total = len(cron_jobs)
    cron_success_rate = (cron_success / cron_total * 100) if cron_total > 0 else 0

    # 从 Prometheus 指标提取
    agent_completed_total = 0
    agent_failed_total = 0
    for key, val in prometheus_metrics.items():
        if key.startswith("agent_executions_total") and "completed" in key:
            agent_completed_total = int(val)
        elif key.startswith("agent_executions_total") and "failed" in key:
            agent_failed_total = int(val)

    # ── 5. 单篇文章 API 成本 ──
    token_costs = []
    for r in completed_results:
        tokens = r.get("token_usage", {}).get("total_tokens", 0)
        if tokens > 0:
            # 假设 input:output = 3:1 比例
            input_tokens = int(tokens * 0.75)
            output_tokens = tokens - input_tokens
            cost = (input_tokens / 1_000_000 * LLM_INPUT_PRICE_PER_1M +
                    output_tokens / 1_000_000 * LLM_OUTPUT_PRICE_PER_1M)
            token_costs.append({
                "case_id": r["case_id"],
                "label": r["label"],
                "category": r["category"],
                "tokens": tokens,
                "cost_rmb": round(cost, 4),
            })

    avg_tokens = statistics.mean([c["tokens"] for c in token_costs]) if token_costs else 0
    total_tokens = sum(c["tokens"] for c in token_costs)
    avg_cost = statistics.mean([c["cost_rmb"] for c in token_costs]) if token_costs else 0
    total_cost = sum(c["cost_rmb"] for c in token_costs)

    # ── 组装报告 ──
    report = {
        "report_meta": {
            "generated_at": datetime.now(timezone.utc).isoformat(),
            "total_test_cases": len(test_results),
            "completed": len(completed_results),
            "failed": len(failed_results),
            "success_rate": round(len(completed_results) / len(test_results) * 100, 1) if test_results else 0,
        },

        "metric_1_article_time": {
            "title": "单篇文章平均生成/修改时间",
            "overall": {
                "avg_seconds": round(avg_duration, 2),
                "median_seconds": round(median_duration, 2),
                "p95_seconds": round(p95_duration, 2),
                "min_seconds": round(min(all_durations), 2) if all_durations else 0,
                "max_seconds": round(max(all_durations), 2) if all_durations else 0,
            },
            "by_category": {
                "writing": {
                    "avg_seconds": round(statistics.mean(write_durations), 2) if write_durations else None,
                    "count": len(write_durations),
                },
                "polish": {
                    "avg_seconds": round(statistics.mean(polish_durations), 2) if polish_durations else None,
                    "count": len(polish_durations),
                },
                "shorten": {
                    "avg_seconds": round(statistics.mean(shorten_durations), 2) if shorten_durations else None,
                    "count": len(shorten_durations),
                },
                "chat": {
                    "avg_seconds": round(statistics.mean(chat_durations), 2) if chat_durations else None,
                    "count": len(chat_durations),
                },
            },
            "step_breakdown": step_avg_timings,
            "individual_cases": [
                {
                    "case_id": r["case_id"],
                    "label": r["label"],
                    "category": r["category"],
                    "duration_s": r["total_duration_s"],
                    "status": r["status"],
                    "article_length": r["article_length"],
                }
                for r in completed_results
            ],
        },

        "metric_2_manual_comparison": {
            "title": "相比人工流程的步骤/时间减少",
            "manual_process": {
                "total_minutes": MANUAL_TOTAL_MIN,
                "step_count": MANUAL_STEP_COUNT,
                "steps": MANUAL_STEPS,
            },
            "agent_process": {
                "avg_minutes": round(avg_agent_minutes, 2),
                "step_count": AGENT_STEP_COUNT,
                "steps": AGENT_STEPS_AUTO,
            },
            "comparison": {
                "time_saved_minutes": round(time_saved_minutes, 2),
                "time_saved_percentage": round(time_saved_pct, 1),
                "steps_reduced": steps_reduced,
                "speedup_factor": round(MANUAL_TOTAL_MIN / avg_agent_minutes, 1) if avg_agent_minutes > 0 else 0,
            },
        },

        "metric_3_review_autofix": {
            "title": "自动评测通过率、自动修复率",
            "review_stats": {
                "total_reviewed": total_reviewed,
                "passed": review_pass_count,
                "failed": review_fail_count,
                "pass_rate_pct": round(review_pass_rate, 1),
            },
            "autofix_stats": {
                "triggered": autofix_triggered,
                "succeeded": autofix_success,
                "failed": autofix_fail,
                "fix_rate_pct": round(autofix_rate, 1),
            },
            "individual_reviews": [
                {
                    "case_id": r["case_id"],
                    "label": r["label"],
                    "passed": r.get("review_result", {}).get("passed") if r.get("review_result") else None,
                    "scores": r.get("review_result", {}).get("scores") if r.get("review_result") else None,
                    "issues_count": len(r.get("review_result", {}).get("issues", [])) if r.get("review_result") else 0,
                    "autofix_applied": r.get("step_results", {}).get("auto_fix", {}).get("fixed", False),
                }
                for r in completed_results
            ],
        },

        "metric_4_cron_jobs": {
            "title": "定时任务成功率/故障数量",
            "cron_jobs": {
                "total": cron_total,
                "success": cron_success,
                "failed": cron_failed,
                "running": cron_running,
                "pending": cron_pending,
                "success_rate_pct": round(cron_success_rate, 1),
                "jobs_detail": cron_jobs,
            },
            "agent_executions_from_prometheus": {
                "completed_total": agent_completed_total,
                "failed_total": agent_failed_total,
            },
        },

        "metric_5_api_cost": {
            "title": "单篇文章 API 成本",
            "pricing_model": {
                "model": "deepseek-v4-flash",
                "input_price_per_1m_tokens_rmb": LLM_INPUT_PRICE_PER_1M,
                "output_price_per_1m_tokens_rmb": LLM_OUTPUT_PRICE_PER_1M,
            },
            "overall": {
                "avg_tokens_per_article": round(avg_tokens),
                "total_tokens": total_tokens,
                "avg_cost_rmb": round(avg_cost, 4),
                "total_cost_rmb": round(total_cost, 4),
            },
            "individual_costs": token_costs,
        },

        "system_metrics": {
            "prometheus_summary": {
                k: v for k, v in prometheus_metrics.items()
                if any(kw in k for kw in ["agent_", "llm_", "websocket_", "eval_", "http_"])
            },
            "admin_stats": admin_stats,
            "token_usage": token_usage,
        },

        "failed_cases": [
            {
                "case_id": r["case_id"],
                "label": r["label"],
                "category": r["category"],
                "status": r["status"],
                "error": r.get("error"),
                "duration_s": r["total_duration_s"],
            }
            for r in failed_results
        ],

        "raw_test_results": test_results,
    }

    return report


# ────────────────────────────────────────────────────────────
#  控制台输出
# ────────────────────────────────────────────────────────────

def print_separator(title: str = "", char="═", width=80):
    if title:
        padding = (width - len(title) - 2) // 2
        print(f"\n{'═' * padding} {title} {'═' * (width - padding - len(title) - 2)}")
    else:
        print(char * width)


def print_report_summary(report: dict):
    """打印汇总报告到控制台。"""

    print_separator("Agent V2 性能基准测试报告")
    print(f"\n  生成时间: {report['report_meta']['generated_at']}")
    print(f"  测试用例: {report['report_meta']['total_test_cases']} 个")
    print(f"  成功: {report['report_meta']['completed']} | 失败: {report['report_meta']['failed']}")
    print(f"  成功率: {report['report_meta']['success_rate']}%")

    # ── 1. 时间指标 ──
    m1 = report["metric_1_article_time"]
    print_separator("指标 1: 单篇文章平均生成/修改时间", "─")
    print(f"  总体平均:   {m1['overall']['avg_seconds']}s")
    print(f"  中位数:     {m1['overall']['median_seconds']}s")
    print(f"  P95:        {m1['overall']['p95_seconds']}s")
    print(f"  最短/最长:  {m1['overall']['min_seconds']}s / {m1['overall']['max_seconds']}s")
    print()
    print("  按类别:")
    for cat, data in m1["by_category"].items():
        if data["avg_seconds"] is not None:
            print(f"    {cat:10s}: 平均 {data['avg_seconds']}s ({data['count']} 篇)")
    print()
    print("  各步骤平均耗时:")
    for step, timing in m1["step_breakdown"].items():
        print(f"    {step:15s}: 平均 {timing['avg_ms']:.0f}ms (max {timing['max_ms']}ms)")
    print()
    print("  各用例详情:")
    for case in m1["individual_cases"]:
        print(f"    [{case['category']:8s}] {case['label']:25s} → {case['duration_s']}s | {case['article_length']}字 | {case['status']}")

    # ── 2. 人工对比 ──
    m2 = report["metric_2_manual_comparison"]
    print_separator("指标 2: 相比人工流程的步骤/时间减少", "─")
    print(f"  人工流程:   {m2['manual_process']['total_minutes']}分钟 / {m2['manual_process']['step_count']}步")
    print(f"  Agent流程:  {m2['agent_process']['avg_minutes']}分钟 / {m2['agent_process']['step_count']}步")
    print(f"  节省时间:   {m2['comparison']['time_saved_minutes']}分钟 ({m2['comparison']['time_saved_percentage']}%)")
    print(f"  减少步骤:   {m2['comparison']['steps_reduced']}步")
    print(f"  加速倍数:   {m2['comparison']['speedup_factor']}x")

    # ── 3. 评测 & 修复 ──
    m3 = report["metric_3_review_autofix"]
    print_separator("指标 3: 自动评测通过率、自动修复率", "─")
    print(f"  评测总数:   {m3['review_stats']['total_reviewed']}")
    print(f"  通过:       {m3['review_stats']['passed']} ({m3['review_stats']['pass_rate_pct']}%)")
    print(f"  不通过:     {m3['review_stats']['failed']}")
    print(f"  自动修复:   触发 {m3['autofix_stats']['triggered']} | 成功 {m3['autofix_stats']['succeeded']} | 失败 {m3['autofix_stats']['failed']}")
    print(f"  修复率:     {m3['autofix_stats']['fix_rate_pct']}%")
    print()
    print("  各用例评测详情:")
    for rev in m3["individual_reviews"]:
        scores_str = ""
        if rev.get("scores"):
            scores_str = ", ".join(f"{k}={v:.2f}" for k, v in rev["scores"].items() if isinstance(v, (int, float)))
        print(f"    {rev['label']:25s} → {'PASS' if rev['passed'] else 'FAIL'} | issues={rev['issues_count']} | autofix={'Y' if rev['autofix_applied'] else 'N'} | {scores_str}")

    # ── 4. 定时任务 ──
    m4 = report["metric_4_cron_jobs"]
    print_separator("指标 4: 定时任务成功率/故障数量", "─")
    cj = m4["cron_jobs"]
    print(f"  任务总数:   {cj['total']}")
    print(f"  成功:       {cj['success']}")
    print(f"  失败:       {cj['failed']}")
    print(f"  运行中:     {cj['running']}")
    print(f"  待执行:     {cj['pending']}")
    print(f"  成功率:     {cj['success_rate_pct']}%")
    pm = m4["agent_executions_from_prometheus"]
    print(f"  [Prometheus] Agent 完成总数: {pm['completed_total']}")
    print(f"  [Prometheus] Agent 失败总数: {pm['failed_total']}")
    if cj["jobs_detail"]:
        print()
        print("  任务详情:")
        for job in cj["jobs_detail"]:
            print(f"    [{job.get('status', '?'):8s}] {job.get('name', 'N/A'):20s} | schedule={job.get('schedule', 'N/A')} | task_type={job.get('task_type', 'N/A')}")

    # ── 5. API 成本 ──
    m5 = report["metric_5_api_cost"]
    print_separator("指标 5: 单篇文章 API 成本", "─")
    print(f"  模型:       {m5['pricing_model']['model']}")
    print(f"  输入价格:   ¥{m5['pricing_model']['input_price_per_1m_tokens_rmb']}/1M tokens")
    print(f"  输出价格:   ¥{m5['pricing_model']['output_price_per_1m_tokens_rmb']}/1M tokens")
    print(f"  平均Token:  {m5['overall']['avg_tokens_per_article']}")
    print(f"  总Token:    {m5['overall']['total_tokens']}")
    print(f"  平均成本:   ¥{m5['overall']['avg_cost_rmb']}")
    print(f"  总成本:     ¥{m5['overall']['total_cost_rmb']}")
    print()
    print("  各用例成本:")
    for c in m5["individual_costs"]:
        print(f"    [{c['category']:8s}] {c['label']:25s} → {c['tokens']:6d} tokens | ¥{c['cost_rmb']}")

    # ── 失败用例 ──
    if report["failed_cases"]:
        print_separator("失败用例", "─")
        for fc in report["failed_cases"]:
            print(f"  [{fc['category']:8s}] {fc['label']:25s} → {fc['status']} | {fc['duration_s']}s | {fc['error']}")

    print_separator()


# ────────────────────────────────────────────────────────────
#  主函数
# ────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="Agent V2 性能基准测试")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL, help="HTTP base URL")
    parser.add_argument("--ws-url", default=None, help="WebSocket base URL (defaults to ws://<host>)")
    parser.add_argument("--admin-token", default=DEFAULT_ADMIN_TOKEN, help="Admin token")
    parser.add_argument("--timeout", type=int, default=180, help="单用例超时(秒)")
    parser.add_argument("--skip-api-checks", action="store_true", help="跳过 admin API 和 Prometheus 查询")
    parser.add_argument("--cases", default=None, help="只运行指定用例ID(逗号分隔)")
    args = parser.parse_args()

    ws_url = args.ws_url or args.base_url.replace("http://", "ws://").replace("https://", "wss://")

    # 健康检查
    print(f"\n🔍 正在检查服务器健康状态: {args.base_url}/health")
    try:
        health = requests.get(f"{args.base_url}/health", timeout=10).json()
        print(f"   ✅ 服务器状态: {health.get('data', {}).get('status', 'unknown')}")
        print(f"   LLM: {'✅' if health.get('data', {}).get('llm_configured') else '❌'}")
        print(f"   搜索: {'✅' if health.get('data', {}).get('search_configured') else '❌'}")
        print(f"   数据库: {'✅' if health.get('data', {}).get('db_configured') else '❌'}")
    except Exception as e:
        print(f"   ❌ 服务器不可达: {e}")
        sys.exit(1)

    # 筛选用例
    cases = TEST_CASES
    if args.cases:
        ids = set(args.cases.split(","))
        cases = [c for c in TEST_CASES if c["id"] in ids]

    print(f"\n📋 共 {len(cases)} 个测试用例\n")

    # 运行测试
    test_results = []
    for i, case in enumerate(cases):
        print(f"  [{i+1}/{len(cases)}] 运行: {case['label']} ({case['id']}) ...", end=" ", flush=True)
        collector = AgentEventCollector(ws_url, case["payload"], timeout=args.timeout)
        result = collector.run()
        result["case_id"] = case["id"]
        result["label"] = case["label"]
        result["category"] = case["category"]

        status_icon = "✅" if result["status"] == "completed" else "❌"
        print(f"{status_icon} {result['total_duration_s']}s | {result['article_length']}字 | {result['status']}")
        if result.get("error"):
            print(f"          ⚠️  {result['error']}")

        test_results.append(result)

        # 用例间间隔，避免速率限制
        if i < len(cases) - 1:
            time.sleep(2)

    # 查询系统指标
    print("\n📊 收集系统指标...")
    prometheus_metrics = {}
    cron_jobs = []
    eval_runs = []
    admin_stats = {}
    token_usage = {}

    if not args.skip_api_checks:
        print("  - 查询 Prometheus 指标...", end=" ")
        prometheus_metrics = query_prometheus_metrics(args.base_url)
        print(f"✅ ({len(prometheus_metrics)} 个指标)")

        print("  - 查询 Cron 任务...", end=" ")
        cron_jobs = query_cron_jobs(args.base_url, args.admin_token)
        print(f"✅ ({len(cron_jobs)} 个任务)")

        print("  - 查询评测运行...", end=" ")
        eval_runs = query_eval_runs(args.base_url, args.admin_token)
        print(f"✅ ({len(eval_runs)} 条记录)")

        print("  - 查询管理统计...", end=" ")
        admin_stats = query_admin_stats(args.base_url, args.admin_token)
        print(f"✅")

        print("  - 查询 Token 用量...", end=" ")
        token_usage = query_token_usage(args.base_url, args.admin_token)
        print(f"✅")
    else:
        print("  ⏭️  已跳过 API 查询")

    # 生成报告
    print("\n📝 生成报告...")
    report = generate_report(
        test_results=test_results,
        prometheus_metrics=prometheus_metrics,
        cron_jobs=cron_jobs,
        eval_runs=eval_runs,
        admin_stats=admin_stats,
        token_usage=token_usage,
    )

    # 保存报告
    reports_dir = Path(__file__).parent / "reports"
    reports_dir.mkdir(exist_ok=True)
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    report_file = reports_dir / f"benchmark_report_{timestamp}.json"
    with open(report_file, "w", encoding="utf-8") as f:
        json.dump(report, f, ensure_ascii=False, indent=2)
    print(f"  📁 报告已保存: {report_file}")

    # 控制台汇总
    print_report_summary(report)

    return 0 if report["report_meta"]["failed"] == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
