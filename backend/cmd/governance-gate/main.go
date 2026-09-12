package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

func main() {
	action := flag.String("action", "assess", "assess or approve")
	policyPath := flag.String("policy", "", "path to an allowlist, percentage, or enabled policy JSON file")
	operator := flag.String("operator", "", "operator identity required for approve")
	reason := flag.String("reason", "", "approval reason required for approve")
	approvalTTL := flag.Duration("approval-ttl", 24*time.Hour, "approval lifetime")
	flag.Parse()
	if err := run(context.Background(), *action, *policyPath, *operator, *reason, *approvalTTL); err != nil {
		fmt.Fprintln(os.Stderr, "governance gate:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, action, policyPath, operator, reason string, approvalTTL time.Duration) error {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" || strings.TrimSpace(policyPath) == "" {
		return fmt.Errorf("DATABASE_URL and --policy are required")
	}
	payload, err := os.ReadFile(policyPath)
	if err != nil {
		return err
	}
	var policy writingruntime.AdapterRolloutPolicy
	if err := json.Unmarshal(payload, &policy); err != nil {
		return fmt.Errorf("decode policy: %w", err)
	}
	switch policy.Mode {
	case writingruntime.RolloutAllowlist, writingruntime.RolloutPercentage, writingruntime.RolloutEnabled:
	default:
		return fmt.Errorf("only allowlist, percentage, or enabled policies can be assessed or approved")
	}
	db, err := database.NewPostgres(databaseURL, 3, 1)
	if err != nil {
		return err
	}
	defer db.Close()
	store, err := writingstore.New(db)
	if err != nil {
		return err
	}
	criteria := writingruntime.DefaultPromotionCriteria()
	var assessment writingruntime.PromotionAssessment
	switch policy.Mode {
	case writingruntime.RolloutPercentage:
		gate := writingruntime.PercentagePromotionGate{Store: store, Criteria: criteria}
		assessment, err = gate.EvidenceAssessment(ctx, policy)
	case writingruntime.RolloutEnabled:
		gate := writingruntime.ProductionPromotionGate{Store: store, Criteria: criteria}
		assessment, err = gate.EvidenceAssessment(ctx, policy)
	default:
		gate := writingruntime.AllowlistPromotionGate{Store: store, Criteria: criteria}
		assessment, err = gate.EvidenceAssessment(ctx, policy)
	}
	if err != nil {
		return err
	}
	if action == "assess" {
		return json.NewEncoder(os.Stdout).Encode(assessment)
	}
	if action != "approve" {
		return fmt.Errorf("unsupported action %q", action)
	}
	if !assessment.Allowed {
		return fmt.Errorf("evidence gate denied promotion: %v", assessment.Reasons)
	}
	if strings.TrimSpace(operator) == "" || strings.TrimSpace(reason) == "" || approvalTTL <= 0 {
		return fmt.Errorf("--operator, --reason, and positive --approval-ttl are required")
	}
	// Higher rungs must be able to show the ladder: the same change (activation
	// key) has to have passed every earlier stage already.
	switch policy.Mode {
	case writingruntime.RolloutPercentage:
		if _, err := store.LatestRolloutApprovalByActivationKey(ctx, policy.ActivationKey, string(writingruntime.RolloutAllowlist)); errors.Is(err, writingstore.ErrNotFound) {
			return fmt.Errorf("allowlist stage approval missing for activation key %q", policy.ActivationKey)
		} else if err != nil {
			return err
		}
	case writingruntime.RolloutEnabled:
		if _, err := store.LatestRolloutApprovalByActivationKey(ctx, policy.ActivationKey, string(writingruntime.RolloutPercentage)); errors.Is(err, writingstore.ErrNotFound) {
			return fmt.Errorf("percentage stage approval missing for activation key %q", policy.ActivationKey)
		} else if err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	record := writingstore.RolloutApprovalRecord{
		ApprovalID: writingstore.StableID("approval_", policy.PolicyHash, fmt.Sprint(policy.PolicyVersion), policy.ActivationKey, operator, now.Format(time.RFC3339Nano)),
		PolicyHash: policy.PolicyHash, PolicyVersion: policy.PolicyVersion, ActivationKey: policy.ActivationKey,
		TargetMode: string(policy.Mode), ApprovedBy: operator, Reason: reason,
		EvidenceHealth: assessment.Health, EvidenceCutoff: assessment.Health.Cutoff,
		EvidenceLastRecordedAt: assessment.Health.LastRecordedAt, CreatedAt: now, ExpiresAt: now.Add(approvalTTL),
	}
	if err := store.RecordRolloutApproval(ctx, record); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"approval_id": record.ApprovalID, "policy_hash": policy.PolicyHash, "target_mode": record.TargetMode, "expires_at": record.ExpiresAt})
}
