-- ============================================================================
-- LuminWrite 4 周用户试点 · 成功指标采集
-- 口径来源：docs/28-wp4-pilot-scenarios.md「Success Metrics (4-week pilot)」
-- ============================================================================
-- 用途与约束：
--   * 本文件全部为只读查询（仅 SELECT / WITH），不含任何写入语句、密钥或供应商信息。
--   * 指标定义、目标值与度量公式一律以 docs/28 为准；表名与列名以
--     backend/internal/database/migrations/ 的实际 migration 为准（已在注释中逐条标注）。
--
-- 时间窗参数怎么传：
--   使用 psql 变量 :start / :end 传入统计窗口（左闭右开 [start, end)），
--   两个参数必须每次显式提供，未提供时 psql 会直接报错，避免误统计全表。示例：
--
--     psql "$DATABASE_URL" \
--       -v start="2026-09-21 00:00:00+08" \
--       -v end="2026-09-28 00:00:00+08" \
--       -f backend/scripts/pilot-metrics.sql
--
--   建议每周执行一次，连续采集 4 周。
--
-- 涉及表（括号内为定义该表的 migration 编号）：
--   writing_runs (089)                  —— run 状态与时间戳
--   writing_quality_reports (090)       —— 质量门报告（含 report_payload.gate_checks）
--   writing_node_attempts (091)         —— 节点尝试台账
--   project_context_envelopes (102)     —— ContextEnvelope 持久化
--   feedback_segments (006)             —— 产品内用户反馈（1-5 分）
-- ============================================================================


-- ----------------------------------------------------------------------------
-- 查询 1 · 任务完成率（目标 ≥95%）
-- 度量公式（docs/28）：completed_runs / total_runs
-- 口径：按 run 终态统计——终态 = completed / failed / cancelled（见 089 的
--   chk_writing_run_status 约束；pausing / cancelling 等中间态不计入分母）。
--   窗口按 writing_runs.created_at 圈定本周期创建的 run。
-- ----------------------------------------------------------------------------
SELECT
    count(*) FILTER (
        WHERE status IN ('completed', 'failed', 'cancelled'))          AS total_runs,      -- 终态运行总数
    count(*) FILTER (WHERE status = 'completed')                        AS completed_runs,  -- 完成数
    count(*) FILTER (
        WHERE status NOT IN ('completed', 'failed', 'cancelled'))       AS still_active,    -- 参考：窗口内创建、期末仍未终态的 run
    round(
        count(*) FILTER (WHERE status = 'completed')::numeric
        / NULLIF(count(*) FILTER (
              WHERE status IN ('completed', 'failed', 'cancelled')), 0)
        * 100, 2)                                                       AS completion_rate_pct
FROM writing_runs
WHERE created_at >= :'start'::timestamptz
  AND created_at <  :'end'::timestamptz;


-- ----------------------------------------------------------------------------
-- 查询 2 · 质量门通过率（目标 ≥80%）
-- 度量公式（docs/28）：runs with all gates passing
-- 口径：窗口内产出质量报告的 run 中，「最新一份报告全部质量门通过」的占比。
--   全部通过 = blocker_count=0 且 open_error_count=0 且 assurance_satisfied
--   且 quality_state 达到 accepted_draft / verified_deliverable（090 中这两态
--   由 DB 触发器强制要求全部硬性门通过），且 report_payload.gate_checks
--   （QualityGateCheck{code,passed,message}，见 internal/writingkernel/quality.go）
--   中不存在 passed=false 的门项。
-- ----------------------------------------------------------------------------
WITH latest_reports AS (
    SELECT DISTINCT ON (run_id)
        run_id, blocker_count, open_error_count,
        quality_state, assurance_satisfied, report_payload
    FROM writing_quality_reports
    WHERE created_at >= :'start'::timestamptz
      AND created_at <  :'end'::timestamptz
    ORDER BY run_id, created_at DESC, report_version DESC
),
gate_eval AS (
    SELECT
        r.*,
        NOT EXISTS (
            SELECT 1
            FROM jsonb_array_elements(r.report_payload -> 'gate_checks') AS gc
            WHERE jsonb_typeof(gc -> 'passed') = 'boolean'
              AND (gc ->> 'passed')::boolean = FALSE
        ) AS all_gate_checks_passed
    FROM latest_reports r
)
SELECT
    count(*)                                                        AS runs_with_quality_report, -- 分母：有质量报告的 run
    count(*) FILTER (
        WHERE blocker_count = 0
          AND open_error_count = 0
          AND assurance_satisfied
          AND quality_state IN ('accepted_draft', 'verified_deliverable')
          AND all_gate_checks_passed)                               AS all_gates_passed_runs,    -- 全部质量门通过
    round(
        count(*) FILTER (
            WHERE blocker_count = 0
              AND open_error_count = 0
              AND assurance_satisfied
              AND quality_state IN ('accepted_draft', 'verified_deliverable')
              AND all_gate_checks_passed)::numeric
        / NULLIF(count(*), 0) * 100, 2)                             AS quality_gate_pass_rate_pct
FROM gate_eval;


-- ----------------------------------------------------------------------------
-- 查询 3 · ContextEnvelope 编译成功率（目标 ≥99%）
-- 度量公式（docs/28）：envelopes / node_attempts
-- 口径：分母 = 窗口内已实际启动的节点尝试（writing_node_attempts.started_at
--   IS NOT NULL；envelope 在尝试启动后编译，见 internal/writingruntime/context.go）。
--   分子 = 其中成功留存了 project_context_envelopes（102）记录的尝试，
--   按 (run_id, node_id, attempt) 关联。附列统计 compiler_version=2（试点版本，
--   见 internal/contextcompiler/compiler.go 的 const CompilerVersion = 2）。
-- ----------------------------------------------------------------------------
SELECT
    count(*)                                                        AS node_attempts,            -- 分母：已启动的节点尝试
    count(e.envelope_id)                                            AS attempts_with_envelope,   -- 分子：留有 envelope
    count(e.envelope_id) FILTER (WHERE e.compiler_version = 2)      AS attempts_with_envelope_v2,
    round(
        count(e.envelope_id)::numeric / NULLIF(count(*), 0) * 100, 2) AS compile_success_rate_pct
FROM writing_node_attempts a
LEFT JOIN project_context_envelopes e
       ON e.run_id  = a.run_id
      AND e.node_id = a.node_id
      AND e.attempt = a.attempt
WHERE a.created_at >= :'start'::timestamptz
  AND a.created_at <  :'end'::timestamptz
  AND a.started_at IS NOT NULL;


-- ----------------------------------------------------------------------------
-- 查询 4 · 离线回放成功率（目标 100%）——【表结构待确认：代理口径】
-- 度量公式（docs/28）：replay_runs / sampled_runs
--
-- 重要说明：离线回放由 OfflineReplayer（backend/internal/writingruntime/
--   offline_replay.go）在进程内执行，回放结果（OfflineReplayResult.HashMatch，
--   重编译 hash 是否等于留存 envelope_hash）目前【不落库】。已核查以下位置，
--   均未发现持久化回放结果的表，故 replay_runs / sampled_runs 无法直接从库中
--   统计，正式表结构待确认：
--     * migrations 095_governed_rollout_evidence / 096_governance_productionization
--       / 096_shadow_content / 097_canonical_content / 097_percentage_promotion
--       / 098_production_promotion / 116_memory_telemetry；
--     * migrations 063_wabench_data_layer（wabench_runs.traffic_type='replay'
--       属 WA 压测数据层，与本指标无关）；
--     * backend/scripts/ 现有脚本（无回放结果记录）。
--
-- 下列查询为【代理口径】：对窗口内已完成的 run（回放抽样对象），核对每个
--   已启动节点尝试的 envelope 留存与版本完整性——这是回放可执行的前提。
--   HashMatch 本身需运行 OfflineReplayer 后按 run 汇总（试点期可人工记录到
--   试算表后与下列 coverage 一并报告）。
-- ----------------------------------------------------------------------------
WITH sampled_runs AS (
    SELECT run_id
    FROM writing_runs
    WHERE status = 'completed'
      AND completed_at >= :'start'::timestamptz
      AND completed_at <  :'end'::timestamptz
),
attempt_envelope AS (
    SELECT
        a.run_id, a.node_id, a.attempt,
        (e.envelope_id IS NOT NULL)      AS has_envelope,
        (e.compiler_version = 2)         AS compiled_by_v2
    FROM writing_node_attempts a
    LEFT JOIN project_context_envelopes e
           ON e.run_id  = a.run_id
          AND e.node_id = a.node_id
          AND e.attempt = a.attempt
    WHERE a.run_id IN (SELECT run_id FROM sampled_runs)
      AND a.started_at IS NOT NULL
)
SELECT
    (SELECT count(*) FROM sampled_runs)                                 AS sampled_runs,          -- 抽样（已完成）run 数
    count(*)                                                            AS replayable_attempts,   -- 待回放节点尝试数
    count(*) FILTER (WHERE has_envelope)                                AS attempts_with_envelope,
    count(*) FILTER (WHERE has_envelope AND compiled_by_v2)             AS attempts_with_envelope_v2,
    round(
        count(*) FILTER (WHERE has_envelope)::numeric
        / NULLIF(count(*), 0) * 100, 2)                                 AS envelope_coverage_pct  -- 代理指标：100% 才谈得上回放 100%
FROM attempt_envelope;


-- ----------------------------------------------------------------------------
-- 查询 5 · 用户满意度（目标 ≥4.0，1-5 分）
-- 度量公式（docs/28）：pilot feedback form
-- 口径：库内来源为 feedback_segments.rating（1-5，migration 006；对应产品内
--   反馈 POST /api/v2/feedback，segment_type 可为 overall/paragraph 等）。
--   注意：纸质/外部试点问卷（pilot feedback form）不在库内，如需并入口径，
--   须另行人工汇总后与下述结果合并报告。
-- ----------------------------------------------------------------------------
SELECT
    round(avg(rating)::numeric, 2)      AS avg_rating,      -- 1-5 分均值
    count(*)                            AS sample_count,    -- 样本数
    count(*) FILTER (WHERE rating >= 4) AS rating_4plus,    -- 参考：≥4 分样本数
    min(rating)                         AS min_rating,
    max(rating)                         AS max_rating
FROM feedback_segments
WHERE created_at >= :'start'::timestamptz
  AND created_at <  :'end'::timestamptz;
