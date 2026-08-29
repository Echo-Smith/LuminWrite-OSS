package writingruntime

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// MetricKind identifies a bounded-cardinality runtime observation. Identity,
// user data, artifact IDs, source URLs, and document content deliberately do
// not exist on RuntimeMetric so metric exporters cannot turn them into labels.
type MetricKind string

const (
	MetricRouteDecision      MetricKind = "route_decision"
	MetricExecution          MetricKind = "execution"
	MetricMaterialIntegrity  MetricKind = "material_integrity"
	MetricShadowComparison   MetricKind = "shadow_comparison"
	MetricCanonicalCommit    MetricKind = "canonical_commit"
	MetricAuthorityViolation MetricKind = "authority_violation"
)

type ExecutionLane string

const (
	LaneBaseline  ExecutionLane = "baseline"
	LaneShadow    ExecutionLane = "shadow"
	LaneCandidate ExecutionLane = "candidate"
)

type RuntimeMetric struct {
	Kind                      MetricKind
	Family                    AdapterFamily
	ExecutorID                string
	Capability                string
	Mode                      RolloutMode
	Lane                      ExecutionLane
	Status                    string
	Reason                    string
	ErrorCode                 ErrorCode
	DurationMS                int64
	CostUSD                   float64
	InputTokens, OutputTokens int64
	MaterialSourceKind        MaterialSourceKind
}

type RuntimeTelemetry interface {
	Observe(context.Context, RuntimeMetric)
}

type RuntimeTelemetryFunc func(context.Context, RuntimeMetric)

func (function RuntimeTelemetryFunc) Observe(ctx context.Context, metric RuntimeMetric) {
	function(ctx, metric)
}

func observeRuntime(ctx context.Context, telemetry RuntimeTelemetry, metric RuntimeMetric) {
	if telemetry != nil {
		telemetry.Observe(ctx, metric)
	}
}

// RuntimeEvidence is the high-cardinality audit projection. Canonical truth
// remains in writingstore Attempt/Artifact/RunLedger records.
type RuntimeEvidence struct {
	EvidenceID    string            `json:"evidence_id"`
	Kind          string            `json:"kind"`
	Identity      ExecutionIdentity `json:"identity"`
	Adapter       AdapterPolicy     `json:"adapter_policy"`
	PolicyHash    string            `json:"policy_hash"`
	PolicyVersion int               `json:"policy_version"`
	Mode          RolloutMode       `json:"mode"`
	Lane          ExecutionLane     `json:"lane"`
	Decision      RouteDecision     `json:"decision"`
	Status        string            `json:"status"`
	ErrorCode     ErrorCode         `json:"error_code,omitempty"`
	Usage         ExecutionUsage    `json:"usage"`
	Outputs       []OutputManifest  `json:"outputs"`
	Comparison    *ShadowComparison `json:"comparison,omitempty"`
	RecordedAt    time.Time         `json:"recorded_at"`
}

type OutputManifest struct {
	OutputKey    string `json:"output_key"`
	ArtifactType string `json:"artifact_type"`
	ContentHash  string `json:"content_hash"`
	MediaType    string `json:"media_type"`
	SourceCount  int    `json:"source_count"`
}

// ValidatorSummaryLine carries one real validator result from a quality
// report output into the shadow evidence, per the specification's evidence
// contents (validator summary alongside manifest, usage, and error codes).
type ValidatorSummaryLine struct {
	ValidatorID string `json:"validator_id"`
	Version     string `json:"version,omitempty"`
	Status      string `json:"status"`
}

type ShadowComparison struct {
	BaselineStatus   string                `json:"baseline_status"`
	CandidateStatus  string                `json:"candidate_status"`
	ContractMatch    bool                  `json:"contract_match"`
	ContentMatch     bool                  `json:"content_match"`
	SourceDelta      int                   `json:"source_delta"`
	CostDeltaUSD     float64               `json:"cost_delta_usd"`
	DurationDeltaMS  int64                 `json:"duration_delta_ms"`
	BaselineError    ErrorCode             `json:"baseline_error,omitempty"`
	CandidateError   ErrorCode             `json:"candidate_error,omitempty"`
	ValidatorSummary []ValidatorSummaryLine `json:"validator_summary,omitempty"`
}

type RolloutEvidenceStore interface {
	Record(context.Context, RuntimeEvidence) error
}

type RuntimeEvidenceRecorder interface {
	RecordRuntimeEvidence(context.Context, writingstore.RuntimeEvidenceRecord) error
}

type WritingStoreEvidenceStore struct {
	Recorder RuntimeEvidenceRecorder
}

func (store WritingStoreEvidenceStore) Record(ctx context.Context, evidence RuntimeEvidence) error {
	if store.Recorder == nil {
		return ErrRuntimeNotReady
	}
	payloadBytes, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return err
	}
	return store.Recorder.RecordRuntimeEvidence(ctx, writingstore.RuntimeEvidenceRecord{
		EvidenceID: evidence.EvidenceID, RunID: evidence.Identity.RunID, NodeID: evidence.Identity.NodeID,
		Attempt: evidence.Identity.Attempt, Kind: evidence.Kind, Payload: payload, OccurredAt: evidence.RecordedAt})
}

// MemoryRolloutEvidenceStore is useful for contract tests and local shadow
// runs. Production wiring must provide durable storage before candidate traffic.
type MemoryRolloutEvidenceStore struct {
	mu      sync.Mutex
	records []RuntimeEvidence
	err     error
}

func (store *MemoryRolloutEvidenceStore) Record(_ context.Context, evidence RuntimeEvidence) error {
	if store == nil {
		return ErrRuntimeNotReady
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.err != nil {
		return store.err
	}
	store.records = append(store.records, cloneRuntimeEvidence(evidence))
	return nil
}

func (store *MemoryRolloutEvidenceStore) Records() []RuntimeEvidence {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]RuntimeEvidence, len(store.records))
	for index, evidence := range store.records {
		result[index] = cloneRuntimeEvidence(evidence)
	}
	return result
}

func cloneRuntimeEvidence(evidence RuntimeEvidence) RuntimeEvidence {
	evidence.Outputs = append([]OutputManifest(nil), evidence.Outputs...)
	if evidence.Comparison != nil {
		comparison := *evidence.Comparison
		evidence.Comparison = &comparison
	}
	return evidence
}
