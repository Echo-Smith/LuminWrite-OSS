package services

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
)

// CronScheduler periodically polls the database for due cron jobs
// and executes them. It uses a ticker-based approach instead of
// robfig/cron because the DB is the source of truth — this ensures
// jobs survive restarts and can be managed dynamically through the
// admin API without restarting the server.
//
// 文档来源: docs/01-architecture.md — Phase 3 调度器
type CronScheduler struct {
	adminRepo *database.AdminRepo
	tickEvery time.Duration
	stopCh    chan struct{}
}

// NewCronScheduler creates a new scheduler that checks for due jobs
// at the specified interval (default: 30s).
func NewCronScheduler(adminRepo *database.AdminRepo) *CronScheduler {
	return &CronScheduler{
		adminRepo: adminRepo,
		tickEvery: 30 * time.Second,
		stopCh:    make(chan struct{}),
	}
}

// Start launches the scheduler in a background goroutine.
// It runs until ctx is cancelled or Stop() is called.
func (cs *CronScheduler) Start(ctx context.Context, execFn func(*database.CronJob) error) {
	slog.Info("cron scheduler started", "tick_interval", cs.tickEvery)

	ticker := time.NewTicker(cs.tickEvery)
	defer ticker.Stop()

	// Run immediately on start
	cs.runPendingJobs(ctx, execFn)

	for {
		select {
		case <-ctx.Done():
			slog.Info("cron scheduler stopped (context cancelled)")
			return
		case <-cs.stopCh:
			slog.Info("cron scheduler stopped (explicit stop)")
			return
		case <-ticker.C:
			cs.runPendingJobs(ctx, execFn)
		}
	}
}

// Stop signals the scheduler to stop.
func (cs *CronScheduler) Stop() {
	select {
	case <-cs.stopCh:
		// already closed
	default:
		close(cs.stopCh)
	}
}

// runPendingJobs queries the database for due jobs and executes them.
func (cs *CronScheduler) runPendingJobs(ctx context.Context, execFn func(*database.CronJob) error) {
	if cs.adminRepo == nil || execFn == nil {
		return
	}

	jobs, err := cs.adminRepo.GetPendingCronJobs(ctx)
	if err != nil {
		slog.Warn("cron scheduler: failed to query pending jobs", "error", err)
		return
	}

	if len(jobs) == 0 {
		return
	}

	slog.Info("cron scheduler: executing pending jobs", "count", len(jobs))

	for _, job := range jobs {
		// Execute each job in its own goroutine to avoid blocking others
		go cs.executeJob(job, execFn)
	}
}

// executeJob runs a single cron job and updates its status.
func (cs *CronScheduler) executeJob(job *database.CronJob, execFn func(*database.CronJob) error) {
	jobCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	slog.Info("cron scheduler: executing job",
		"job_id", job.ID, "name", job.Name, "task_type", job.TaskType)

	// Mark as running
	if err := cs.adminRepo.UpdateCronJobStatus(jobCtx, job.ID, "running", ""); err != nil {
		slog.Warn("cron scheduler: failed to mark job as running", "job_id", job.ID, "error", err)
	}

	// Calculate and set next_run_at
	cs.updateNextRunAt(job)

	// Execute
	if err := execFn(job); err != nil {
		slog.Warn("cron scheduler: job failed",
			"job_id", job.ID, "name", job.Name, "error", err)
		if err := cs.adminRepo.UpdateCronJobStatus(jobCtx, job.ID, "failed", err.Error()); err != nil {
			slog.Warn("cron scheduler: failed to update job status", "job_id", job.ID, "error", err)
		}
		return
	}

	// Mark as success
	if err := cs.adminRepo.UpdateCronJobStatus(jobCtx, job.ID, "success", ""); err != nil {
		slog.Warn("cron scheduler: failed to mark job as success", "job_id", job.ID, "error", err)
	}
}

// updateNextRunAt calculates the next run time based on the schedule
// and updates it in the database.
func (cs *CronScheduler) updateNextRunAt(job *database.CronJob) {
	next := calculateNextRun(job.Schedule, time.Now())
	if next.IsZero() || cs.adminRepo == nil {
		return
	}

	// Use a background context since the job ctx might be cancelled
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Actually update next_run_at in the database
	if err := cs.adminRepo.UpdateCronJobNextRun(ctx, job.ID, next); err != nil {
		slog.Warn("cron scheduler: failed to update next_run_at",
			"job_id", job.ID, "error", err)
		return
	}

	slog.Debug("cron scheduler: next_run_at updated",
		"job_id", job.ID, "next_run", next.Format(time.RFC3339))
}

// calculateNextRun parses simple schedule patterns and returns the next run time.
// Supported patterns:
//   - "@every <duration>" (e.g., "@every 30s", "@every 5m", "@every 1h")
//   - "@hourly"          → every hour
//   - "@daily" / "@midnight" → every day at midnight
//   - "@weekly"          → every week (Sunday midnight)
//   - Standard cron: "*/30 * * * *" (5-field cron expression)
func calculateNextRun(schedule string, from time.Time) time.Time {
	schedule = strings.TrimSpace(schedule)
	if schedule == "" {
		return time.Time{}
	}

	// @every pattern
	if strings.HasPrefix(schedule, "@every ") {
		durStr := strings.TrimPrefix(schedule, "@every ")
		dur, err := time.ParseDuration(durStr)
		if err != nil {
			return time.Time{}
		}
		return from.Add(dur)
	}

	// @hourly
	if schedule == "@hourly" {
		return time.Date(from.Year(), from.Month(), from.Day(), from.Hour()+1, 0, 0, 0, from.Location())
	}

	// @daily / @midnight
	if schedule == "@daily" || schedule == "@midnight" {
		next := time.Date(from.Year(), from.Month(), from.Day()+1, 0, 0, 0, 0, from.Location())
		return next
	}

	// @weekly
	if schedule == "@weekly" {
		daysUntilSunday := int(time.Sunday - from.Weekday())
		if daysUntilSunday <= 0 {
			daysUntilSunday += 7
		}
		next := time.Date(from.Year(), from.Month(), from.Day()+daysUntilSunday, 0, 0, 0, 0, from.Location())
		return next
	}

	// Standard 5-field cron expression: "minute hour day month weekday"
	// We parse each field to compute the next run time.
	parts := strings.Fields(schedule)
	if len(parts) == 5 {
		return calculateCronNextRun(parts, from)
	}

	// Unknown schedule, default to 1 hour
	return from.Add(1 * time.Hour)
}

// calculateCronNextRun computes the next run time for a 5-field cron expression.
// Supports: exact values (0), wildcards (*), step values (*/N), and lists (1,3,5).
// Fields: minute hour day-of-month month day-of-week
func calculateCronNextRun(parts []string, from time.Time) time.Time {
	minuteField, hourField, dayField, monthField, dowField := parts[0], parts[1], parts[2], parts[3], parts[4]

	// Start from the next minute after 'from'
	candidate := time.Date(from.Year(), from.Month(), from.Day(), from.Hour(), from.Minute(), 0, 0, from.Location())
	candidate = candidate.Add(1 * time.Minute)

	// Search up to 7 days ahead (covers weekly schedules)
	for i := 0; i < 7*24*60; i++ {
		if cronFieldMatches(minuteField, candidate.Minute()) &&
			cronFieldMatches(hourField, candidate.Hour()) &&
			cronFieldMatches(dayField, candidate.Day()) &&
			cronFieldMatches(monthField, int(candidate.Month())) &&
			cronFieldMatches(dowField, int(candidate.Weekday())) {
			return candidate
		}
		candidate = candidate.Add(1 * time.Minute)
	}

	// Fallback: should not happen for valid cron expressions
	return from.Add(1 * time.Hour)
}

// cronFieldMatch checks if a cron field matches a value.
// Supports: * (wildcard), exact number, */N (step), N,M,L (list).
func cronFieldMatch(field string, val int) bool {
	field = strings.TrimSpace(field)
	if field == "*" {
		return true
	}

	// Handle lists: 1,3,5
	if strings.Contains(field, ",") {
		for _, f := range strings.Split(field, ",") {
			if cronFieldMatch(strings.TrimSpace(f), val) {
				return true
			}
		}
		return false
	}

	// Handle step: */N
	if strings.HasPrefix(field, "*/") {
		stepStr := strings.TrimPrefix(field, "*/")
		var step int
		if _, err := fmt.Sscanf(stepStr, "%d", &step); err == nil && step > 0 {
			return val%step == 0
		}
		return false
	}

	// Handle range: N-M
	if strings.Contains(field, "-") {
		rangeParts := strings.SplitN(field, "-", 2)
		var lo, hi int
		if _, err := fmt.Sscanf(rangeParts[0], "%d", &lo); err != nil {
			return false
		}
		if _, err := fmt.Sscanf(rangeParts[1], "%d", &hi); err != nil {
			return false
		}
		return val >= lo && val <= hi
	}

	// Exact match
	var num int
	if _, err := fmt.Sscanf(field, "%d", &num); err == nil {
		return val == num
	}

	return false
}

// cronFieldMatches is an alias for cronFieldMatch (used in calculateCronNextRun).
func cronFieldMatches(field string, val int) bool {
	return cronFieldMatch(field, val)
}
