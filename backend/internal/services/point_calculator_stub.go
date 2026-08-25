package services

import (
	"context"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
)

type ModelPointInfo struct {
	PointsPerKToken float64 `json:"points_per_k_token"`
	CostLevel       string  `json:"cost_level"`
}

// PointCalculator — stub for point calculation.
type PointCalculator struct{}

func NewPointCalculator(db *database.DB) *PointCalculator {
	return &PointCalculator{}
}

func (pc *PointCalculator) GetModelPointInfo(ctx context.Context, modelName string) (*ModelPointInfo, error) {
	return &ModelPointInfo{}, nil
}
