package writingruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

var ErrCheckpointNotFound = errors.New("writingruntime: checkpoint not found")

type Checkpoint struct {
	CheckpointID    string         `json:"checkpoint_id"`
	RunID           string         `json:"run_id"`
	PlanID          string         `json:"plan_id"`
	PlanVersion     int            `json:"plan_version"`
	PlanHash        string         `json:"plan_hash"`
	CompletedNodes  map[string]int `json:"completed_nodes"`
	ArtifactRefs    []string       `json:"artifact_refs"`
	SpentCostUSD    float64        `json:"spent_cost_usd"`
	SpentDurationMS int64          `json:"spent_duration_ms"`
	UnsafeInFlight  []string       `json:"unsafe_in_flight"`
	CreatedAt       time.Time      `json:"created_at"`
	// Delivery carries the M1.0 delivery payload (docs/21 §21.8) when the
	// checkpoint follows a quality node: the quality report binds the
	// candidate version and the promotion advances quality_state in the same
	// transaction as the snapshot. Nil for progress checkpoints.
	Delivery *QualityDelivery `json:"delivery,omitempty"`
}

func (checkpoint Checkpoint) Validate() error {
	if !hasIDPrefix(checkpoint.RunID, "run_") || !hasIDPrefix(checkpoint.PlanID, "plan_") || checkpoint.PlanVersion < 1 || !executionHashPattern.MatchString(checkpoint.PlanHash) || checkpoint.CompletedNodes == nil || checkpoint.ArtifactRefs == nil || checkpoint.UnsafeInFlight == nil || checkpoint.SpentCostUSD < 0 || checkpoint.SpentDurationMS < 0 || checkpoint.CreatedAt.IsZero() {
		return errors.New("writingruntime: invalid checkpoint")
	}
	return nil
}

type CheckpointRepository interface {
	Save(context.Context, Checkpoint) error
	LoadLatest(context.Context, string) (Checkpoint, error)
}

type PersistentCheckpointRepository struct {
	Store *writingstore.Store
	Trace writingstore.TraceContext
}

func (repository PersistentCheckpointRepository) Save(ctx context.Context, checkpoint Checkpoint) error {
	if repository.Store == nil {
		return writingstore.ErrNotFound
	}
	if err := checkpoint.Validate(); err != nil {
		return err
	}
	if existing, err := repository.LoadLatest(ctx, checkpoint.RunID); err == nil && existing.CheckpointID == checkpoint.CheckpointID {
		return nil
	} else if err != nil && !errors.Is(err, ErrCheckpointNotFound) {
		return err
	}
	run, err := repository.Store.LoadRuntimeRun(ctx, checkpoint.RunID)
	if err != nil {
		return err
	}
	manifestBytes, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return err
	}
	sum := sha256.Sum256(manifestBytes)
	contentHash := "sha256:" + hex.EncodeToString(sum[:])
	version := run.LastSnapshotVersion + 1
	if version < 1 {
		version = 1
	}
	snapshotID := writingstore.StableID("snap_", checkpoint.RunID, checkpoint.CheckpointID)
	bundle := writingstore.CheckpointBundle{Snapshot: writingstore.SnapshotRecord{
		SnapshotID: snapshotID, SnapshotVersion: version, RunID: checkpoint.RunID,
		CheckpointID: checkpoint.CheckpointID, PlanID: checkpoint.PlanID,
		PlanVersion: checkpoint.PlanVersion, ContractID: run.ContractID,
		ContractVersion: run.ContractVersion, ContractHash: run.ContractHash,
		DocumentID: run.DocumentID, ContentHash: contentHash, Status: "persisted",
		Complete: true, Manifest: manifest,
		StorageRef: "db://writing_snapshots/" + snapshotID, Trace: repository.Trace,
		CreatedAt: checkpoint.CreatedAt, PersistedAt: checkpoint.CreatedAt,
	}}
	// M1.0 delivery: a checkpoint following a quality node carries the
	// quality report + document promotion, committed in the same transaction
	// as the snapshot (docs/21 §21.8). The snapshot identity is resolved
	// here, so the report's bindings are stamped to match before the store's
	// cross-validation runs.
	if checkpoint.Delivery != nil {
		report := checkpoint.Delivery.Report
		report.SnapshotID = snapshotID
		report.SnapshotVersion = version
		report.SnapshotPersisted = true
		bundle.QualityReport = &report
		promotion := checkpoint.Delivery.Promotion
		bundle.DocumentPromotion = &promotion
		bundle.Snapshot.CandidateVersionID = report.CandidateVersionID
		bundle.Snapshot.QualityReportID = report.ReportID
		bundle.Snapshot.QualityReportVersion = report.ReportVersion
		bundle.AchievedAssurance = report.AchievedAssurance
	}
	_, err = repository.Store.CommitCheckpoint(ctx, bundle)
	return err
}

func (repository PersistentCheckpointRepository) LoadLatest(ctx context.Context, runID string) (Checkpoint, error) {
	if repository.Store == nil {
		return Checkpoint{}, writingstore.ErrNotFound
	}
	snapshot, err := repository.Store.LoadLatestSnapshot(ctx, runID)
	if errors.Is(err, writingstore.ErrNotFound) {
		return Checkpoint{}, ErrCheckpointNotFound
	}
	if err != nil {
		return Checkpoint{}, err
	}
	payload, err := json.Marshal(snapshot.Manifest)
	if err != nil {
		return Checkpoint{}, err
	}
	var checkpoint Checkpoint
	if err := json.Unmarshal(payload, &checkpoint); err != nil {
		return Checkpoint{}, fmt.Errorf("decode runtime checkpoint: %w", err)
	}
	// A JSON round-trip can turn empty slices into nil; normalize so the
	// decoded checkpoint satisfies the same invariants as a freshly built one.
	if checkpoint.ArtifactRefs == nil {
		checkpoint.ArtifactRefs = []string{}
	}
	if checkpoint.UnsafeInFlight == nil {
		checkpoint.UnsafeInFlight = []string{}
	}
	return checkpoint, checkpoint.Validate()
}

func checkpointID(runID, planHash string, completed map[string]int, artifacts []InputArtifact) string {
	nodes := make([]string, 0, len(completed))
	for nodeID, attempt := range completed {
		nodes = append(nodes, fmt.Sprintf("%s:%d", nodeID, attempt))
	}
	sort.Strings(nodes)
	refs := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		refs = append(refs, artifactIdentity(artifact.ArtifactID, artifact.Version)+":"+artifact.ContentHash)
	}
	sort.Strings(refs)
	payload, _ := json.Marshal([]any{runID, planHash, nodes, refs})
	sum := sha256.Sum256(payload)
	return "checkpoint_" + hex.EncodeToString(sum[:16])
}
