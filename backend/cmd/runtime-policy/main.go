// runtime-policy persists operator intent only. It never grants approval.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
	"os"
	"strings"
	"time"
)

func main() {
	action := flag.String("action", "show", "show, install (observation only), activate, or deactivate")
	path := flag.String("policy", "", "exact policy JSON")
	operator := flag.String("operator", "", "operator identity for mutation")
	flag.Parse()
	if err := run(*action, *path, *operator); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(action, path, operator string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var policy writingruntime.AdapterRolloutPolicy
	if err = json.Unmarshal(raw, &policy); err != nil {
		return err
	}
	if err = policy.Validate(); err != nil {
		return err
	}
	if policy.Mode != writingruntime.RolloutOff && policy.Mode != writingruntime.RolloutShadow && policy.Mode != writingruntime.RolloutAllowlist {
		return fmt.Errorf("P0 supports only off, shadow and allowlist")
	}
	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	db, err := database.NewPostgres(os.Getenv("DATABASE_URL"), 3, 1)
	if err != nil {
		return err
	}
	defer db.Close()
	store, err := writingstore.New(db)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if action == "show" {
		v, err := store.LatestRuntimePolicy(ctx, policy.CapabilityID)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(v)
	}
	if strings.TrimSpace(operator) == "" {
		return fmt.Errorf("operator required")
	}
	if action != "install" && action != "activate" && action != "deactivate" {
		return fmt.Errorf("unknown action")
	}
	if action == "activate" {
		assessment, err := (writingruntime.AllowlistPromotionGate{Store: store}).Assess(ctx, policy)
		if err != nil {
			return err
		}
		if !assessment.Allowed {
			return fmt.Errorf("activation denied: %v", assessment.Reasons)
		}
	}
	err = store.AppendRuntimePolicy(ctx, writingstore.RuntimePolicyRevision{CapabilityID: policy.CapabilityID, PolicyHash: policy.PolicyHash, Policy: raw, Active: action == "activate", OperatorID: operator})
	if err == nil {
		fmt.Printf("%s recorded for %s (%s); service mode remains separately configured\n", action, policy.CapabilityID, policy.PolicyHash)
	}
	return err
}
