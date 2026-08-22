package database

import (
	"context"
	"time"
)

type PointBalance struct {
	UserID          string    `json:"user_id"`
	Balance         float64   `json:"balance"`
	TotalRecharged  float64   `json:"total_recharged"`
	TotalConsumed   float64   `json:"total_consumed"`
	TotalRefunded   float64   `json:"total_refunded"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ConsumptionLog struct {
	ID                string    `json:"id"`
	UserID            string    `json:"user_id"`
	TraceID           string    `json:"trace_id,omitempty"`
	TaskType          string    `json:"task_type"`
}

// BillingRepo — stub for billing repository.
type BillingRepo struct{}

func NewBillingRepo(db *DB) *BillingRepo {
	return &BillingRepo{}
}

func (r *BillingRepo) GetBalance(ctx context.Context, userID string) (*PointBalance, error) {
	return &PointBalance{UserID: userID}, nil
}

func (r *BillingRepo) InitUserBalance(ctx context.Context, userID string, points float64) error {
	return nil
}
