package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
)

// ─── Ops Patrol Worker ──────────────────────────────────
//
// Background goroutine performing rule-based health checks
// (写 作失败率/降级、定时任务、Token 用量、MCP、沙箱、安全事件、canary 回滚).
// Findings are deduplicated by fingerprint + cooldown and persisted to
// admin_alerts; newly created alerts are pushed to admins over SSE.
//
// The rule evaluator (evaluatePatrolRules) is a pure function so it can be
// unit-tested without a database; this file only collects snapshots and
// persists/pushes the findings.

// OpsPatrol runs periodic rule-based health checks.
type OpsPatrol struct {
	server   *Server
	interval time.Duration
	cooldown time.Duration
	thresh   patrolThresholds
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewOpsPatrol creates the worker; interval <= 0 falls back to
// OPS_PATROL_INTERVAL (or 10 minutes when unset).
func NewOpsPatrol(server *Server, interval time.Duration) *OpsPatrol {
	if interval <= 0 {
		interval = patrolIntervalFromEnv()
	}
	return &OpsPatrol{
		server:   server,
		interval: interval,
		cooldown: 60 * time.Minute,
		thresh:   patrolThresholdsFromEnv(),
		stopCh:   make(chan struct{}),
	}
}

// Start launches the patrol goroutine.
func (p *OpsPatrol) Start() {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		slog.Info("ops patrol started", "interval", p.interval)
		for {
			select {
			case <-ticker.C:
				p.patrolOnce()
			case <-p.stopCh:
				slog.Info("ops patrol stopped")
				return
			}
		}
	}()
}

// Stop gracefully stops the patrol worker.
func (p *OpsPatrol) Stop() {
	close(p.stopCh)
	p.wg.Wait()
}

// patrolIntervalFromEnv reads OPS_PATROL_INTERVAL (Go duration, e.g. "10m").
func patrolIntervalFromEnv() time.Duration {
	if v := os.Getenv("OPS_PATROL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		slog.Warn("invalid OPS_PATROL_INTERVAL, using default", "value", v)
	}
	return 10 * time.Minute
}

// opsPatrolEnabledFromEnv reads OPS_PATROL_ENABLED (default: enabled).
// Set to "false" / "0" / "off" to skip loading the patrol worker entirely;
// this is the deploy-level kill switch, independent of the runtime toggle
// stored in admin_patrol_config.
func opsPatrolEnabledFromEnv() bool {
	v := strings.ToLower(os.Getenv("OPS_PATROL_ENABLED"))
	if v == "" {
		return true
	}
	return v != "false" && v != "0" && v != "off"
}

// patrolThresholds holds the alerting limits; all rules additionally carry
// absolute floors so tiny deployments never alert on noise.
type patrolThresholds struct {
	WriteFailureMinSamples int     // min finished traces in window before rate rule applies
	WriteFailureRate       float64 // failed / (failed + completed)
	WriteDegradedHourly    int     // degraded traces in the last hour
	CronOverdueAfter       time.Duration
	TokenSpikeMultiple     float64 // today vs 7-day daily average
	TokenSpikeFloor        int64   // min today tokens before the rule applies
	ViolationsHourly       int     // sandbox violations in the last hour
	SecurityDailyFloor     int     // min security events per day before spike rule applies
	SecuritySpikeMultiple  float64 // last-24h vs 7-day daily average
}

// defaultPatrolThresholds returns the built-in defaults.
func defaultPatrolThresholds() patrolThresholds {
	return patrolThresholds{
		WriteFailureMinSamples: 5,
		WriteFailureRate:       0.5,
		WriteDegradedHourly:    10,
		CronOverdueAfter:       time.Hour,
		TokenSpikeMultiple:     3.0,
		TokenSpikeFloor:        100_000,
		ViolationsHourly:       20,
		SecurityDailyFloor:     100,
		SecuritySpikeMultiple:  3.0,
	}
}

// patrolThresholdsFromEnv applies OPS_PATROL_* overrides on top of defaults.
func patrolThresholdsFromEnv() patrolThresholds {
	th := defaultPatrolThresholds()
	envFloat := func(key string, dst *float64) {
		if v := os.Getenv(key); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
				*dst = f
			}
		}
	}
	envInt := func(key string, dst *int) {
		if v := os.Getenv(key); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				*dst = n
			}
		}
	}
	envFloat("OPS_PATROL_WRITE_FAILURE_RATE", &th.WriteFailureRate)
	envInt("OPS_PATROL_WRITE_FAILURE_MIN_SAMPLES", &th.WriteFailureMinSamples)
	envInt("OPS_PATROL_WRITE_DEGRADED_HOURLY", &th.WriteDegradedHourly)
	if v := os.Getenv("OPS_PATROL_CRON_OVERDUE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			th.CronOverdueAfter = d
		}
	}
	envFloat("OPS_PATROL_TOKEN_SPIKE_MULTIPLE", &th.TokenSpikeMultiple)
	if v := os.Getenv("OPS_PATROL_TOKEN_SPIKE_FLOOR"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			th.TokenSpikeFloor = n
		}
	}
	envInt("OPS_PATROL_VIOLATIONS_HOURLY", &th.ViolationsHourly)
	envInt("OPS_PATROL_SECURITY_DAILY_FLOOR", &th.SecurityDailyFloor)
	envFloat("OPS_PATROL_SECURITY_SPIKE_MULTIPLE", &th.SecuritySpikeMultiple)
	return th
}

// ─── Snapshot ───────────────────────────────────────────

// patrolSnapshot is the collected system state one patrol round evaluates.
// Zeroed + ok=false sections are skipped by the rules.
type patrolSnapshot struct {
	Now time.Time

	// 写作 (agent_traces): last hour vs previous 24h baseline.
	TraceStatsOK                       bool
	RecentFailed, RecentCompleted      int
	RecentDegraded                     int
	BaselineFailed, BaselineCompleted  int

	// 定时任务.
	CronJobsOK bool
	CronJobs   []patrolCronJob

	// Token 用量.
	TokenStatsOK  bool
	TodayTokens   int64
	PrevDailyAvg  int64

	// MCP 外部服务.
	MCPServersOK bool
	MCPServers   []patrolMCPServer

	// 沙箱违规.
	ViolationsOK            bool
	Violations1h            int
	Violations24h           int

	// 安全事件.
	SecurityOK     bool
	Security24h    int
	Security7dAvg  float64

	// Canary 回滚（近 1h 明细）.
	CanaryOK      bool
	CanaryRollbacks []patrolCanaryRollback
}

type patrolCronJob struct {
	ID         string
	Name       string
	IsActive   bool
	LastStatus string
	NextRunAt  *time.Time
}

type patrolMCPServer struct {
	ID         string
	Name       string
	IsActive   bool
	LastStatus string
}

type patrolCanaryRollback struct {
	CandidateID string
	StyleSlug   string
	ErrorRate   float64
	CapturedAt  time.Time
}

// patrolFinding is a rule hit awaiting persistence.
type patrolFinding struct {
	Severity    string
	CheckType   string
	Fingerprint string
	Title       string
	Detail      string
	Evidence    map[string]interface{}
}

// ─── Patrol round ───────────────────────────────────────

func (p *OpsPatrol) patrolOnce() {
	if p.server.db == nil || p.server.adminRepo == nil || p.server.adminAlertRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// 运行时开关：面板关闭巡检时跳过本轮；API 与历史告警仍可用。
	// 读取失败按 fail-open 处理（继续巡检），只记日志。
	if cfg, err := p.server.adminAlertRepo.GetPatrolConfig(ctx); err != nil {
		slog.Warn("ops patrol: read config failed, running anyway", "error", err)
	} else if !cfg.Enabled {
		return
	}

	snap := p.collectSnapshot(ctx)
	findings := evaluatePatrolRules(snap, p.thresh)
	if len(findings) == 0 {
		return
	}

	adminIDs, err := p.server.adminAlertRepo.ListAdminUserIDs(ctx, AdminUserID)
	if err != nil {
		slog.Warn("ops patrol: list admin user ids failed", "error", err)
		adminIDs = nil
	}

	for _, f := range findings {
		_, created, err := p.server.adminAlertRepo.InsertAlertWithDedupe(ctx, &database.AdminAlert{
			Severity:    f.Severity,
			CheckType:   f.CheckType,
			Fingerprint: f.Fingerprint,
			Title:       f.Title,
			Detail:      f.Detail,
			Evidence:    f.Evidence,
			Status:      database.AlertStatusPending,
		}, p.cooldown)
		if err != nil {
			slog.Warn("ops patrol: persist alert failed", "fingerprint", f.Fingerprint, "error", err)
			continue
		}
		slog.Info("ops patrol: finding", "check", f.CheckType, "fingerprint", f.Fingerprint, "created", created)
		if created {
			p.notifyAdmins(ctx, adminIDs, f)
		}
	}
}

// notifyAdmins pushes a newly created alert to admins over SSE.
func (p *OpsPatrol) notifyAdmins(ctx context.Context, adminIDs []string, f patrolFinding) {
	if p.server.sseHub == nil || len(adminIDs) == 0 {
		return
	}
	payload := map[string]interface{}{
		"severity":  f.Severity,
		"check_type": f.CheckType,
		"title":     f.Title,
		"detail":    f.Detail,
		"timestamp": time.Now().Format(time.RFC3339),
	}
	for _, uid := range adminIDs {
		// Non-blocking per design: Broadcast/SendToUser drop on full channel.
		p.server.sseHub.SendToUser(uid, &SSEEvent{Event: "admin:alert", Data: payload})
	}
}

// collectSnapshot gathers all signal sections; per-section failures are
// logged and marked not-ok so one broken source never blocks the others.
func (p *OpsPatrol) collectSnapshot(ctx context.Context) patrolSnapshot {
	snap := patrolSnapshot{Now: time.Now()}

	// ── 写作失败/降级 ──
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	recentFailed, recentCompleted, recentDegraded, err1 := p.traceCounts(queryCtx,
		"created_at >= NOW() - INTERVAL '1 hour'")
	baselineFailed, baselineCompleted, _, err2 := p.traceCounts(queryCtx,
		"created_at >= NOW() - INTERVAL '25 hours' AND created_at < NOW() - INTERVAL '1 hour'")
	cancel()
	if err1 != nil || err2 != nil {
		slog.Warn("ops patrol: trace stats unavailable", "recent_err", err1, "baseline_err", err2)
	} else {
		snap.TraceStatsOK = true
		snap.RecentFailed, snap.RecentCompleted, snap.RecentDegraded = recentFailed, recentCompleted, recentDegraded
		snap.BaselineFailed, snap.BaselineCompleted = baselineFailed, baselineCompleted
	}

	// ── 定时任务 ──
	if jobs, err := p.server.adminRepo.ListCronJobs(ctx); err != nil {
		slog.Warn("ops patrol: cron jobs unavailable", "error", err)
	} else {
		snap.CronJobsOK = true
		for _, j := range jobs {
			snap.CronJobs = append(snap.CronJobs, patrolCronJob{
				ID: j.ID, Name: j.Name, IsActive: j.IsActive,
				LastStatus: j.LastStatus, NextRunAt: j.NextRunAt,
			})
		}
	}

	// ── Token 用量 ──
	if stats, err := p.server.adminRepo.GetTokenUsageStats(ctx, 8); err != nil {
		slog.Warn("ops patrol: token usage unavailable", "error", err)
	} else {
		snap.TokenStatsOK = true
		snap.TodayTokens = stats.TodayTokens
		var sum int64
		var days int
		today := snap.Now.Format("2006-01-02")
		for _, d := range stats.DailyTokens {
			if d.Date == today {
				continue
			}
			sum += int64(d.Count)
			days++
		}
		if days > 0 {
			snap.PrevDailyAvg = sum / int64(days)
		}
	}

	// ── MCP 外部服务 ──
	if servers, err := p.server.adminRepo.ListMCPServers(ctx); err != nil {
		slog.Warn("ops patrol: mcp servers unavailable", "error", err)
	} else {
		snap.MCPServersOK = true
		for _, s := range servers {
			snap.MCPServers = append(snap.MCPServers, patrolMCPServer{
				ID: s.ID, Name: s.Name, IsActive: s.IsActive, LastStatus: s.LastStatus,
			})
		}
	}

	// ── 沙箱违规 ──
	queryCtx2, cancel2 := context.WithTimeout(ctx, 10*time.Second)
	v1h, err1 := p.countRows(queryCtx2, "mcp_tool_violations", "created_at >= NOW() - INTERVAL '1 hour'")
	v24h, err2 := p.countRows(queryCtx2, "mcp_tool_violations", "created_at >= NOW() - INTERVAL '24 hours'")
	cancel2()
	if err1 != nil || err2 != nil {
		slog.Warn("ops patrol: sandbox violations unavailable", "recent_err", err1, "day_err", err2)
	} else {
		snap.ViolationsOK = true
		snap.Violations1h, snap.Violations24h = v1h, v24h
	}

	// ── 安全事件 ──
	if sec := p.server.getSecurityDBStats(); sec != nil {
		s24, ok1 := toInt(sec["total_24h"])
		s7, ok2 := toInt(sec["total_7d"])
		if ok1 && ok2 {
			snap.SecurityOK = true
			snap.Security24h = s24
			snap.Security7dAvg = float64(s7) / 7.0
		}
	}

	// ── Canary 回滚 ──
	if rollbacks, err := p.recentCanaryRollbacks(ctx); err != nil {
		slog.Warn("ops patrol: canary rollbacks unavailable", "error", err)
	} else {
		snap.CanaryOK = true
		snap.CanaryRollbacks = rollbacks
	}

	return snap
}

// ─── Collectors ─────────────────────────────────────────

func (p *OpsPatrol) traceCounts(ctx context.Context, where string) (failed, completed, degraded int, err error) {
	row := p.server.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'failed'),
			COUNT(*) FILTER (WHERE status = 'completed'),
			COUNT(*) FILTER (WHERE step_history::text ILIKE '%degraded%')
		FROM agent_traces WHERE ` + where)
	err = row.Scan(&failed, &completed, &degraded)
	return
}

func (p *OpsPatrol) countRows(ctx context.Context, table, where string) (int, error) {
	var n int
	err := p.server.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+table+" WHERE "+where).Scan(&n)
	return n, err
}

func (p *OpsPatrol) recentCanaryRollbacks(ctx context.Context) ([]patrolCanaryRollback, error) {
	rows, err := p.server.db.QueryContext(ctx, `
		SELECT candidate_id::text, COALESCE(style_slug, ''), COALESCE(error_rate, 0), captured_at
		FROM canary_health_snapshots
		WHERE triggered_rollback = TRUE AND captured_at >= NOW() - INTERVAL '1 hour'
		ORDER BY captured_at DESC
		LIMIT 20
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rollbacks := make([]patrolCanaryRollback, 0)
	for rows.Next() {
		var r patrolCanaryRollback
		if err := rows.Scan(&r.CandidateID, &r.StyleSlug, &r.ErrorRate, &r.CapturedAt); err != nil {
			return nil, err
		}
		rollbacks = append(rollbacks, r)
	}
	return rollbacks, rows.Err()
}

func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// ─── Rule evaluation (pure) ─────────────────────────────

// evaluatePatrolRules is a pure function: snapshot in, findings out.
// Rules with insufficient data (section not ok) or below absolute floors
// never fire — small deployments must not alert on noise.
func evaluatePatrolRules(snap patrolSnapshot, th patrolThresholds) []patrolFinding {
	findings := make([]patrolFinding, 0)

	// 1. 写作失败率（近 1h vs 前 24h 基线）
	if snap.TraceStatsOK {
		samples := snap.RecentFailed + snap.RecentCompleted
		if samples >= th.WriteFailureMinSamples {
			rate := float64(snap.RecentFailed) / float64(samples)
			if rate >= th.WriteFailureRate {
				baselineSamples := snap.BaselineFailed + snap.BaselineCompleted
				baselineRate := -1.0
				if baselineSamples > 0 {
					baselineRate = float64(snap.BaselineFailed) / float64(baselineSamples)
				}
				findings = append(findings, patrolFinding{
					Severity:    database.AlertSeverityWarning,
					CheckType:   "write_failure_rate",
					Fingerprint: "write_failure_rate:1h",
					Title:       "写作失败率异常升高",
					Detail: fmt.Sprintf("最近 1 小时失败 %d/%d 次（失败率 %.0f%%），基线（前 24h）失败率 %s。请检查 Trace 历史与模型配置。",
						snap.RecentFailed, samples, rate*100, formatRate(baselineRate)),
					Evidence: map[string]interface{}{
						"recent_failed": snap.RecentFailed, "recent_completed": snap.RecentCompleted,
						"failure_rate": rate, "baseline_failure_rate": baselineRate,
					},
				})
			}
		}
		if snap.RecentDegraded >= th.WriteDegradedHourly {
			findings = append(findings, patrolFinding{
				Severity:    database.AlertSeverityWarning,
				CheckType:   "write_degradation",
				Fingerprint: "write_degradation:1h",
				Title:       "写作降级次数激增",
				Detail:      fmt.Sprintf("最近 1 小时出现 %d 次降级执行（阈值 %d），请检查退出机制监控中的步骤级统计。", snap.RecentDegraded, th.WriteDegradedHourly),
				Evidence:    map[string]interface{}{"degraded_1h": snap.RecentDegraded, "threshold": th.WriteDegradedHourly},
			})
		}
	}

	// 2. 定时任务
	if snap.CronJobsOK {
		for _, j := range snap.CronJobs {
			if !j.IsActive {
				continue
			}
			if j.LastStatus == "failed" {
				findings = append(findings, patrolFinding{
					Severity:    database.AlertSeverityWarning,
					CheckType:   "cron_failure",
					Fingerprint: "cron_failure:" + j.ID,
					Title:       fmt.Sprintf("定时任务「%s」执行失败", j.Name),
					Detail:      "该任务最近一次执行状态为 failed，请到「定时任务」面板查看错误详情并手动重跑。",
					Evidence:    map[string]interface{}{"job_id": j.ID, "job_name": j.Name, "last_status": j.LastStatus},
				})
			}
			if j.NextRunAt != nil && snap.Now.Sub(*j.NextRunAt) > th.CronOverdueAfter {
				findings = append(findings, patrolFinding{
					Severity:    database.AlertSeverityCritical,
					CheckType:   "cron_overdue",
					Fingerprint: "cron_overdue:" + j.ID,
					Title:       fmt.Sprintf("定时任务「%s」调度停滞", j.Name),
					Detail: fmt.Sprintf("计划执行时间已逾期 %s，调度器可能停滞。请检查服务实例与定时任务配置。",
						snap.Now.Sub(*j.NextRunAt).Round(time.Minute)),
					Evidence: map[string]interface{}{
						"job_id": j.ID, "job_name": j.Name,
						"next_run_at": j.NextRunAt.Format(time.RFC3339), "overdue": snap.Now.Sub(*j.NextRunAt).String(),
					},
				})
			}
		}
	}

	// 3. Token 用量异常
	if snap.TokenStatsOK && snap.PrevDailyAvg > 0 {
		if snap.TodayTokens >= th.TokenSpikeFloor && float64(snap.TodayTokens) >= th.TokenSpikeMultiple*float64(snap.PrevDailyAvg) {
			findings = append(findings, patrolFinding{
				Severity:    database.AlertSeverityWarning,
				CheckType:   "token_spike",
				Fingerprint: "token_spike:today",
				Title:       "Token 用量异常",
				Detail: fmt.Sprintf("今日已消耗 %s tokens，为前 7 天日均（%s）的 %.1f 倍。请到「用量统计」确认是正常增长还是滥用。",
					formatTokens(snap.TodayTokens), formatTokens(snap.PrevDailyAvg), float64(snap.TodayTokens)/float64(snap.PrevDailyAvg)),
				Evidence: map[string]interface{}{
					"today_tokens": snap.TodayTokens, "prev_daily_avg": snap.PrevDailyAvg,
					"multiple": float64(snap.TodayTokens) / float64(snap.PrevDailyAvg),
				},
			})
		}
	}

	// 4. MCP 外部服务掉线
	if snap.MCPServersOK {
		for _, s := range snap.MCPServers {
			if s.IsActive && s.LastStatus != "connected" {
				findings = append(findings, patrolFinding{
					Severity:    database.AlertSeverityWarning,
					CheckType:   "mcp_down",
					Fingerprint: "mcp_down:" + s.ID,
					Title:       fmt.Sprintf("MCP 服务「%s」未连接", s.Name),
					Detail:      "该 MCP 服务已启用但当前状态不是 connected，相关工具不可用。请到「MCP 管理」检查配置与网络。",
					Evidence:    map[string]interface{}{"server_id": s.ID, "server_name": s.Name, "last_status": s.LastStatus},
				})
			}
		}
	}

	// 5. 沙箱违规激增
	if snap.ViolationsOK && snap.Violations1h >= th.ViolationsHourly {
		findings = append(findings, patrolFinding{
			Severity:    database.AlertSeverityWarning,
			CheckType:   "sandbox_violations",
			Fingerprint: "sandbox_violations:1h",
			Title:       "MCP 沙箱违规激增",
			Detail:      fmt.Sprintf("最近 1 小时记录 %d 次沙箱违规（24h 共 %d 次），请到「MCP 管理 → 安全沙箱」查看违规类型与来源。", snap.Violations1h, snap.Violations24h),
			Evidence:    map[string]interface{}{"violations_1h": snap.Violations1h, "violations_24h": snap.Violations24h},
		})
	}

	// 6. 安全事件激增
	if snap.SecurityOK && snap.Security24h >= th.SecurityDailyFloor && snap.Security7dAvg > 0 &&
		float64(snap.Security24h) >= th.SecuritySpikeMultiple*snap.Security7dAvg {
		findings = append(findings, patrolFinding{
			Severity:    database.AlertSeverityWarning,
			CheckType:   "security_events",
			Fingerprint: "security_events:24h",
			Title:       "安全事件激增",
			Detail: fmt.Sprintf("近 24h 安全事件 %d 次，为 7 天日均（%.0f 次）的 %.1f 倍。请到「审计中心 → 安全审计」查看明细。",
				snap.Security24h, snap.Security7dAvg, float64(snap.Security24h)/snap.Security7dAvg),
			Evidence: map[string]interface{}{
				"events_24h": snap.Security24h, "events_7d_avg": snap.Security7dAvg,
			},
		})
	}

	// 7. Canary 自动回滚
	if snap.CanaryOK {
		for _, r := range snap.CanaryRollbacks {
			findings = append(findings, patrolFinding{
				Severity:    database.AlertSeverityCritical,
				CheckType:   "canary_rollback",
				Fingerprint: "canary_rollback:" + r.CandidateID + ":" + r.CapturedAt.Format("200601021504"),
				Title:       fmt.Sprintf("Canary 自动回滚触发（%s）", r.StyleSlug),
				Detail: fmt.Sprintf("灰度候选 %s 在近 1 小时内触发自动回滚，错误率 %.1f%%。请到「自演进」面板查看候选详情。",
					r.CandidateID[:min(8, len(r.CandidateID))], r.ErrorRate*100),
				Evidence: map[string]interface{}{
					"candidate_id": r.CandidateID, "style_slug": r.StyleSlug,
					"error_rate": r.ErrorRate, "captured_at": r.CapturedAt.Format(time.RFC3339),
				},
			})
		}
	}

	return findings
}

func formatRate(rate float64) string {
	if rate < 0 {
		return "无样本"
	}
	return fmt.Sprintf("%.0f%%", rate*100)
}

func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return strconv.FormatInt(n, 10)
	}
}
