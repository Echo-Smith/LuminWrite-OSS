package writingruntime

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrInvalidRolloutPolicy = errors.New("writingruntime: invalid rollout policy")

type RolloutMode string

const (
	RolloutOff        RolloutMode = "off"
	RolloutShadow     RolloutMode = "shadow"
	RolloutAllowlist  RolloutMode = "allowlist"
	RolloutPercentage RolloutMode = "percentage"
	RolloutEnabled    RolloutMode = "enabled"
)

type AdapterRolloutPolicy struct {
	PolicyVersion     int           `json:"policy_version"`
	PolicyHash        string        `json:"policy_hash"`
	Mode              RolloutMode   `json:"mode"`
	ExecutorID        string        `json:"executor_id"`
	Family            AdapterFamily `json:"adapter_family"`
	CapabilityID      string        `json:"capability_id"`
	CapabilityVersion string        `json:"capability_version"`
	ActivationKey     string        `json:"activation_key,omitempty"`
	KillSwitch        bool          `json:"kill_switch"`
	AllowSubjects     []string      `json:"allow_subjects"`
	BasisPoints       int           `json:"basis_points"`
	EffectiveAt       time.Time     `json:"effective_at,omitempty"`
	ExpiresAt         time.Time     `json:"expires_at,omitempty"`
	Reason            string        `json:"reason"`
}

func DefaultShadowPolicy(executorID string, family AdapterFamily, capabilityID, capabilityVersion string) AdapterRolloutPolicy {
	policy := AdapterRolloutPolicy{PolicyVersion: 1, Mode: RolloutShadow, ExecutorID: executorID,
		Family: family, CapabilityID: capabilityID, CapabilityVersion: capabilityVersion,
		AllowSubjects: []string{}, Reason: "task12_default_shadow"}
	policy, _ = policy.WithComputedHash()
	return policy
}

func (policy AdapterRolloutPolicy) WithComputedHash() (AdapterRolloutPolicy, error) {
	policy.PolicyHash = ""
	policy.AllowSubjects = canonicalStrings(policy.AllowSubjects)
	payload, err := json.Marshal(policy)
	if err != nil {
		return AdapterRolloutPolicy{}, err
	}
	sum := sha256.Sum256(payload)
	policy.PolicyHash = "sha256:" + hex.EncodeToString(sum[:])
	return policy, nil
}

func (policy AdapterRolloutPolicy) Validate() error {
	if policy.PolicyVersion < 1 || strings.TrimSpace(policy.ExecutorID) == "" ||
		strings.TrimSpace(policy.CapabilityID) == "" || strings.TrimSpace(policy.CapabilityVersion) == "" ||
		strings.TrimSpace(policy.Reason) == "" {
		return rolloutPolicyError("policy identity and reason are required")
	}
	if policy.Mode != RolloutOff && policy.Mode != RolloutShadow && policy.Mode != RolloutAllowlist && policy.Mode != RolloutPercentage && policy.Mode != RolloutEnabled {
		return rolloutPolicyError("unknown rollout mode")
	}
	if policy.Family != AdapterFamilyEngine && policy.Family != AdapterFamilyEditorial && policy.Family != AdapterFamilyHarness {
		return rolloutPolicyError("unknown adapter family")
	}
	if policy.BasisPoints < 0 || policy.BasisPoints > 10000 {
		return rolloutPolicyError("basis points must be between 0 and 10000")
	}
	if policy.Mode == RolloutPercentage && (policy.BasisPoints == 0 || strings.TrimSpace(policy.ActivationKey) == "") {
		return rolloutPolicyError("percentage rollout requires basis points and activation key")
	}
	if (policy.Mode == RolloutAllowlist || policy.Mode == RolloutEnabled) && strings.TrimSpace(policy.ActivationKey) == "" {
		return rolloutPolicyError("authoritative rollout requires activation key")
	}
	if policy.Mode == RolloutAllowlist && len(policy.AllowSubjects) == 0 {
		return rolloutPolicyError("allowlist rollout requires subjects")
	}
	if !policy.EffectiveAt.IsZero() && !policy.ExpiresAt.IsZero() && !policy.ExpiresAt.After(policy.EffectiveAt) {
		return rolloutPolicyError("expiry must follow effective time")
	}
	canonical, err := policy.WithComputedHash()
	if err != nil || canonical.PolicyHash != policy.PolicyHash {
		return rolloutPolicyError("policy hash mismatch")
	}
	if len(canonical.AllowSubjects) != len(policy.AllowSubjects) {
		return rolloutPolicyError("allowlist contains blank or duplicate subjects")
	}
	for index := range canonical.AllowSubjects {
		if canonical.AllowSubjects[index] != policy.AllowSubjects[index] {
			return rolloutPolicyError("allowlist must be canonical")
		}
	}
	return nil
}

func rolloutPolicyError(message string) error {
	return runtimeError(CodeRolloutPolicyInvalid, RetryNever, message, ErrInvalidRolloutPolicy)
}

type RouteDecision struct {
	Mode          RolloutMode   `json:"mode"`
	Lane          ExecutionLane `json:"lane"`
	RunShadow     bool          `json:"run_shadow"`
	Reason        string        `json:"reason"`
	SubjectBucket int           `json:"subject_bucket"`
	PolicyHash    string        `json:"policy_hash"`
}

func DecideRoute(policy AdapterRolloutPolicy, request ExecutionRequest, now time.Time) (RouteDecision, error) {
	if err := policy.Validate(); err != nil {
		return RouteDecision{}, err
	}
	if err := request.Validate(); err != nil {
		return RouteDecision{}, err
	}
	base := RouteDecision{Mode: policy.Mode, Lane: LaneBaseline, Reason: "baseline", SubjectBucket: -1, PolicyHash: policy.PolicyHash}
	if policy.KillSwitch {
		base.Reason = "kill_switch"
		return base, nil
	}
	if !policy.EffectiveAt.IsZero() && now.Before(policy.EffectiveAt) {
		base.Reason = "not_effective"
		return base, nil
	}
	if !policy.ExpiresAt.IsZero() && !now.Before(policy.ExpiresAt) {
		base.Reason = "expired"
		return base, nil
	}
	if policy.CapabilityID != request.Node.Capability || policy.CapabilityVersion != request.Node.CapabilityVersion {
		base.Reason = "binding_mismatch"
		return base, nil
	}
	switch policy.Mode {
	case RolloutOff:
		base.Reason = "off"
	case RolloutShadow:
		base.RunShadow = true
		base.Reason = "shadow"
	case RolloutAllowlist:
		if containsString(policy.AllowSubjects, routeSubject(request)) {
			base.Lane, base.Reason = LaneCandidate, "allowlist_match"
		} else {
			base.Reason = "allowlist_miss"
		}
	case RolloutPercentage:
		base.SubjectBucket = stableBucket(policy, routeSubject(request))
		if base.SubjectBucket < policy.BasisPoints {
			base.Lane, base.Reason = LaneCandidate, "percentage_match"
		} else {
			base.Reason = "percentage_miss"
		}
	case RolloutEnabled:
		base.Lane, base.Reason = LaneCandidate, "enabled"
	}
	return base, nil
}

// routeSubject resolves the rollout audience: an explicit execution subject
// (user/tenant) when present, otherwise the run id.
func routeSubject(request ExecutionRequest) string {
	if subject := strings.TrimSpace(request.Subject); subject != "" {
		return subject
	}
	return request.RunID
}

func stableBucket(policy AdapterRolloutPolicy, subject string) int {
	payload := strings.Join([]string{policy.PolicyHash, policy.ActivationKey, subject}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return int(binary.BigEndian.Uint64(sum[:8]) % 10000)
}

func containsString(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

type RolloutPolicyProvider interface {
	Policy(context.Context, ExecutionIdentity) (AdapterRolloutPolicy, error)
}

type MutableRolloutPolicyProvider struct {
	mu     sync.RWMutex
	policy AdapterRolloutPolicy
}

func NewMutableRolloutPolicyProvider(policy AdapterRolloutPolicy) (*MutableRolloutPolicyProvider, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &MutableRolloutPolicyProvider{policy: policy}, nil
}

func (provider *MutableRolloutPolicyProvider) Policy(context.Context, ExecutionIdentity) (AdapterRolloutPolicy, error) {
	if provider == nil {
		return AdapterRolloutPolicy{}, ErrRuntimeNotReady
	}
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	return provider.policy, nil
}

func (provider *MutableRolloutPolicyProvider) Update(policy AdapterRolloutPolicy) error {
	if provider == nil {
		return ErrRuntimeNotReady
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	provider.mu.Lock()
	provider.policy = policy
	provider.mu.Unlock()
	return nil
}

func bindRolloutPolicy(policy AdapterRolloutPolicy, candidate ExecutorAdapter) error {
	if candidate == nil {
		return rolloutPolicyError("candidate adapter is required")
	}
	if policy.ExecutorID != candidate.Descriptor().ExecutorID || policy.Family != candidate.AdapterPolicy().Family {
		return rolloutPolicyError(fmt.Sprintf("policy does not bind candidate %s", candidate.Descriptor().ExecutorID))
	}
	return nil
}
