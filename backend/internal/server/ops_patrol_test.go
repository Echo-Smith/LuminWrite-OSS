package server

import (
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
)

// TestEvaluatePatrolRules covers the pure rule evaluator: healthy snapshots
// must stay silent, each rule must fire on breach, and absolute floors must
// suppress noise from tiny deployments.
func TestEvaluatePatrolRules(t *testing.T) {
	now := time.Now()
	defaults := defaultPatrolThresholds()

	t.Run("healthy snapshot produces no findings", func(t *testing.T) {
		snap := healthySnapshot(now)
		findings := evaluatePatrolRules(snap, defaults)
		if len(findings) != 0 {
			t.Fatalf("expected 0 findings, got %d: %+v", len(findings), findings)
		}
	})

	t.Run("unhealthy numbers in not-ok sections are ignored", func(t *testing.T) {
		snap := healthySnapshot(now)
		snap.TraceStatsOK = false // 数据源不可用
		snap.RecentFailed = 1000  // → must not fire
		findings := evaluatePatrolRules(snap, defaults)
		if len(findings) != 0 {
			t.Fatalf("expected 0 findings for not-ok sections, got %d", len(findings))
		}
	})

	t.Run("write failure rate fires above threshold", func(t *testing.T) {
		snap := healthySnapshot(now)
		snap.TraceStatsOK = true
		snap.RecentFailed = 6
		snap.RecentCompleted = 4 // rate 0.6 ≥ 0.5, samples 10 ≥ 5
		snap.BaselineFailed = 1
		snap.BaselineCompleted = 99
		findings := evaluatePatrolRules(snap, defaults)
		if len(findings) != 1 || findings[0].CheckType != "write_failure_rate" {
			t.Fatalf("expected write_failure_rate, got %+v", findings)
		}
		if findings[0].Severity != database.AlertSeverityWarning {
			t.Fatalf("expected warning severity, got %s", findings[0].Severity)
		}
	})

	t.Run("write failure rate suppressed below min samples", func(t *testing.T) {
		snap := healthySnapshot(now)
		snap.TraceStatsOK = true
		snap.RecentFailed = 2
		snap.RecentCompleted = 2 // samples 4 < 5 → floor applies
		findings := evaluatePatrolRules(snap, defaults)
		if len(findings) != 0 {
			t.Fatalf("expected 0 findings below floor, got %d", len(findings))
		}
	})

	t.Run("degradation spike fires", func(t *testing.T) {
		snap := healthySnapshot(now)
		snap.TraceStatsOK = true
		snap.RecentDegraded = 15
		findings := evaluatePatrolRules(snap, defaults)
		if len(findings) != 1 || findings[0].CheckType != "write_degradation" {
			t.Fatalf("expected write_degradation, got %+v", findings)
		}
	})

	t.Run("cron failure and overdue fire separately", func(t *testing.T) {
		snap := healthySnapshot(now)
		snap.CronJobsOK = true
		overdue := now.Add(-2 * time.Hour)
		snap.CronJobs = []patrolCronJob{
			{ID: "job-1", Name: "KB 导入", IsActive: true, LastStatus: "failed", NextRunAt: &overdue},
		}
		findings := evaluatePatrolRules(snap, defaults)
		got := map[string]bool{}
		for _, f := range findings {
			got[f.CheckType] = true
			if f.Fingerprint == "" {
				t.Fatalf("finding %s missing fingerprint", f.CheckType)
			}
		}
		if !got["cron_failure"] || !got["cron_overdue"] {
			t.Fatalf("expected cron_failure + cron_overdue, got %v", got)
		}
		for _, f := range findings {
			if f.CheckType == "cron_overdue" && f.Severity != database.AlertSeverityCritical {
				t.Fatalf("expected critical for overdue, got %s", f.Severity)
			}
		}
	})

	t.Run("inactive cron jobs are skipped", func(t *testing.T) {
		snap := healthySnapshot(now)
		snap.CronJobsOK = true
		snap.CronJobs = []patrolCronJob{{ID: "job-1", Name: "x", IsActive: false, LastStatus: "failed"}}
		findings := evaluatePatrolRules(snap, defaults)
		if len(findings) != 0 {
			t.Fatalf("expected 0 findings for inactive job, got %d", len(findings))
		}
	})

	t.Run("token spike fires above multiple and floor", func(t *testing.T) {
		snap := healthySnapshot(now)
		snap.TokenStatsOK = true
		snap.TodayTokens = 500_000
		snap.PrevDailyAvg = 100_000
		findings := evaluatePatrolRules(snap, defaults)
		if len(findings) != 1 || findings[0].CheckType != "token_spike" {
			t.Fatalf("expected token_spike, got %+v", findings)
		}
	})

	t.Run("token spike suppressed below floor", func(t *testing.T) {
		snap := healthySnapshot(now)
		snap.TokenStatsOK = true
		snap.TodayTokens = 50_000 // above multiple (baseline 5k) but below floor
		snap.PrevDailyAvg = 5_000
		findings := evaluatePatrolRules(snap, defaults)
		if len(findings) != 0 {
			t.Fatalf("expected 0 findings below token floor, got %d", len(findings))
		}
	})

	t.Run("mcp down fires for active disconnected server", func(t *testing.T) {
		snap := healthySnapshot(now)
		snap.MCPServersOK = true
		snap.MCPServers = []patrolMCPServer{{ID: "srv-1", Name: "search", IsActive: true, LastStatus: "failed"}}
		findings := evaluatePatrolRules(snap, defaults)
		if len(findings) != 1 || findings[0].CheckType != "mcp_down" {
			t.Fatalf("expected mcp_down, got %+v", findings)
		}
	})

	t.Run("sandbox violations fire above hourly threshold", func(t *testing.T) {
		snap := healthySnapshot(now)
		snap.ViolationsOK = true
		snap.Violations1h = 25
		snap.Violations24h = 30
		findings := evaluatePatrolRules(snap, defaults)
		if len(findings) != 1 || findings[0].CheckType != "sandbox_violations" {
			t.Fatalf("expected sandbox_violations, got %+v", findings)
		}
	})

	t.Run("security spike fires above multiple and floor", func(t *testing.T) {
		snap := healthySnapshot(now)
		snap.SecurityOK = true
		snap.Security24h = 300
		snap.Security7dAvg = 50
		findings := evaluatePatrolRules(snap, defaults)
		if len(findings) != 1 || findings[0].CheckType != "security_events" {
			t.Fatalf("expected security_events, got %+v", findings)
		}
	})

	t.Run("canary rollback is critical per candidate", func(t *testing.T) {
		snap := healthySnapshot(now)
		snap.CanaryOK = true
		snap.CanaryRollbacks = []patrolCanaryRollback{
			{CandidateID: "11111111-1111-1111-1111-111111111111", StyleSlug: "yinyue", ErrorRate: 0.2, CapturedAt: now},
			{CandidateID: "22222222-2222-2222-2222-222222222222", StyleSlug: "shenlun", ErrorRate: 0.3, CapturedAt: now},
		}
		findings := evaluatePatrolRules(snap, defaults)
		if len(findings) != 2 {
			t.Fatalf("expected 2 findings, got %d", len(findings))
		}
		for _, f := range findings {
			if f.CheckType != "canary_rollback" || f.Severity != database.AlertSeverityCritical {
				t.Fatalf("expected critical canary_rollback, got %+v", f)
			}
		}
	})
}

// TestOpsPatrolEnabledFromEnv covers the deploy-level kill switch parsing.
func TestOpsPatrolEnabledFromEnv(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", true},
		{"true", true},
		{"1", true},
		{"on", true},
		{"false", false},
		{"FALSE", false},
		{"0", false},
		{"off", false},
		{"Off", false},
		{"garbage", true}, // 未识别的值按开启处理，避免误禁用
	}
	for _, tc := range cases {
		t.Setenv("OPS_PATROL_ENABLED", tc.value)
		if got := opsPatrolEnabledFromEnv(); got != tc.want {
			t.Errorf("OPS_PATROL_ENABLED=%q: got %v, want %v", tc.value, got, tc.want)
		}
	}
}

// healthySnapshot returns an all-sections-ok snapshot whose numbers are
// below every threshold — the "system is fine" baseline for the tests.
func healthySnapshot(now time.Time) patrolSnapshot {
	next := now.Add(5 * time.Minute)
	return patrolSnapshot{
		Now:              now,
		TraceStatsOK:     true,
		RecentFailed:     1,
		RecentCompleted:  99,
		RecentDegraded:   0,
		BaselineFailed:   10,
		BaselineCompleted: 990,
		CronJobsOK:       true,
		CronJobs:         []patrolCronJob{{ID: "ok", Name: "正常任务", IsActive: true, LastStatus: "success", NextRunAt: &next}},
		TokenStatsOK:     true,
		TodayTokens:      10_000,
		PrevDailyAvg:     20_000,
		MCPServersOK:     true,
		MCPServers:       []patrolMCPServer{{ID: "ok", Name: "kb", IsActive: true, LastStatus: "connected"}},
		ViolationsOK:     true,
		Violations1h:     0,
		Violations24h:    1,
		SecurityOK:       true,
		Security24h:      10,
		Security7dAvg:    12,
		CanaryOK:         true,
		CanaryRollbacks:  nil,
	}
}
