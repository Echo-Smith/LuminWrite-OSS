package database

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// RedTeamReportData is a database-layer representation of a red-team report.
// It is decoupled from services.RedTeamReport to avoid import cycles.
type RedTeamReportData struct {
	TotalCases      int
	PassedCases     int
	FailedCases     int
	PassRate        float64
	Results         []map[string]interface{}
	CategorySummary map[string]int
	RunAt           time.Time
}

// RedTeamRepo handles persistence of red-team evaluation reports.
type RedTeamRepo struct {
	db *DB
}

// NewRedTeamRepo creates a new RedTeamRepo.
func NewRedTeamRepo(db *DB) *RedTeamRepo {
	return &RedTeamRepo{db: db}
}

// SaveReport persists a red-team evaluation report to the database.
func (r *RedTeamRepo) SaveReport(ctx context.Context, report *RedTeamReportData, systemPrompt string) (string, error) {
	if r.db == nil {
		return "", fmt.Errorf("database not available")
	}

	// Generate an ID based on timestamp
	id := fmt.Sprintf("rt_%d", report.RunAt.UnixNano())

	resultsJSON, err := json.Marshal(report.Results)
	if err != nil {
		return "", fmt.Errorf("failed to marshal results: %w", err)
	}

	categoryJSON, err := json.Marshal(report.CategorySummary)
	if err != nil {
		return "", fmt.Errorf("failed to marshal category summary: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO redteam_reports (id, total_cases, passed_cases, failed_cases, pass_rate, results, category_summary, system_prompt, status, created_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'completed', NOW(), NOW())
	`, id, report.TotalCases, report.PassedCases, report.FailedCases, report.PassRate, resultsJSON, categoryJSON, systemPrompt)
	if err != nil {
		slog.Warn("failed to save red-team report", "error", err)
		return "", err
	}

	slog.Info("red-team report saved", "id", id, "pass_rate", report.PassRate)
	return id, nil
}

// GetReport retrieves a red-team report by ID.
func (r *RedTeamRepo) GetReport(ctx context.Context, id string) (map[string]interface{}, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var (
		reportID      string
		totalCases    int
		passedCases   int
		failedCases   int
		passRate      float64
		resultsJSON   []byte
		categoryJSON  []byte
		systemPrompt  string
		status        string
		createdAt     time.Time
		completedAt   *time.Time
	)

	err := r.db.QueryRowContext(ctx, `
		SELECT id, total_cases, passed_cases, failed_cases, pass_rate,
		       results, category_summary, system_prompt, status, created_at, completed_at
		FROM redteam_reports WHERE id = $1
	`, id).Scan(
		&reportID, &totalCases, &passedCases, &failedCases, &passRate,
		&resultsJSON, &categoryJSON, &systemPrompt, &status, &createdAt, &completedAt,
	)
	if err != nil {
		return nil, err
	}

	result := map[string]interface{}{
		"id":             reportID,
		"total_cases":    totalCases,
		"passed_cases":   passedCases,
		"failed_cases":   failedCases,
		"pass_rate":      passRate,
		"status":         status,
		"system_prompt":  systemPrompt,
		"created_at":     createdAt,
	}

	if completedAt != nil {
		result["completed_at"] = *completedAt
	}

	if len(resultsJSON) > 0 {
		var results interface{}
		json.Unmarshal(resultsJSON, &results)
		result["results"] = results
	}

	if len(categoryJSON) > 0 {
		var summary interface{}
		json.Unmarshal(categoryJSON, &summary)
		result["category_summary"] = summary
	}

	return result, nil
}

// ListReports lists recent red-team reports with pagination.
func (r *RedTeamRepo) ListReports(ctx context.Context, page, pageSize int) ([]map[string]interface{}, int, error) {
	if r.db == nil {
		return []map[string]interface{}{}, 0, nil
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, total_cases, passed_cases, failed_cases, pass_rate, status, created_at, completed_at
		FROM redteam_reports
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var reports []map[string]interface{}
	for rows.Next() {
		var (
			id           string
			totalCases   int
			passedCases  int
			failedCases  int
			passRate     float64
			status       string
			createdAt    time.Time
			completedAt  *time.Time
		)

		if err := rows.Scan(&id, &totalCases, &passedCases, &failedCases, &passRate, &status, &createdAt, &completedAt); err != nil {
			continue
		}

		report := map[string]interface{}{
			"id":            id,
			"total_cases":   totalCases,
			"passed_cases":  passedCases,
			"failed_cases":  failedCases,
			"pass_rate":     passRate,
			"status":        status,
			"created_at":    createdAt,
		}
		if completedAt != nil {
			report["completed_at"] = *completedAt
		}

		reports = append(reports, report)
	}

	// Get total count
	var total int
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM redteam_reports`).Scan(&total)

	return reports, total, nil
}
