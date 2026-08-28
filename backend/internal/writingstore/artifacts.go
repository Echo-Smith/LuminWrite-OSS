package writingstore

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type ArtifactRef struct {
	ArtifactID string `json:"artifact_id"`
	Version    int    `json:"version"`
}

type ArtifactRecord struct {
	ArtifactID           string
	Version              int
	RunID                string
	PlanID               string
	PlanVersion          int
	NodeID               string
	Attempt              int
	IdempotencyKey       string
	OutputKey            string
	ArtifactType         string
	Status               string
	ContentHash          string
	MediaType            string
	ContentRef           string
	Parents              []ArtifactRef
	QualityReportID      string
	QualityReportVersion int
	SnapshotID           string
	SnapshotVersion      int
	Producer             string
	CapabilityVersion    string
	InputHashes          []string
	ModelRef             string
	PromptTemplateRef    string
	Trace                TraceContext
	CreatedAt            time.Time
	CommittedAt          time.Time
}

var validArtifactTypes = map[string]bool{
	"contract": true, "materials": true,
	"brief": true, "source_pack": true, "research_note": true,
	"claim_map": true, "outline": true, "section_draft": true,
	"full_draft": true, "review_report": true,
	"revision_set": true, "quality_report": true, "evidence_report": true,
	"fact_report": true,
}

var validArtifactStatuses = map[string]bool{
	"provisional": true, "generated": true, "parsed": true,
	"validated": true, "committed": true, "superseded": true,
}

var validArtifactMediaTypes = map[string]bool{
	"application/json": true, "text/markdown": true, "text/plain": true,
}

func (tx *Tx) PutArtifact(ctx context.Context, artifact ArtifactRecord) error {
	if err := validateID(artifact.ArtifactID, "art_", "artifact_id"); err != nil {
		return err
	}
	if artifact.Version < 1 || artifact.PlanVersion < 1 || artifact.Attempt < 1 || strings.TrimSpace(artifact.OutputKey) == "" {
		return fmt.Errorf("%w: invalid artifact version, attempt, or output key", ErrInvalidRecord)
	}
	expectedKey, err := NodeAttemptKey(artifact.RunID, artifact.NodeID, artifact.Attempt)
	if err != nil {
		return err
	}
	if artifact.IdempotencyKey == "" {
		artifact.IdempotencyKey = expectedKey
	}
	if artifact.IdempotencyKey != expectedKey {
		return fmt.Errorf("%w: artifact idempotency key mismatch", ErrIdempotencyConflict)
	}
	if !validArtifactTypes[artifact.ArtifactType] {
		return fmt.Errorf("%w: invalid artifact type %q", ErrInvalidRecord, artifact.ArtifactType)
	}
	if err := validateHash(artifact.ContentHash, "content_hash"); err != nil {
		return err
	}
	if strings.TrimSpace(artifact.ContentRef) == "" || strings.TrimSpace(artifact.Producer) == "" || strings.TrimSpace(artifact.CapabilityVersion) == "" {
		return fmt.Errorf("%w: artifact content and producer binding are required", ErrInvalidRecord)
	}
	if artifact.Parents == nil {
		artifact.Parents = []ArtifactRef{}
	}
	if artifact.InputHashes == nil {
		artifact.InputHashes = []string{}
	}
	for _, inputHash := range artifact.InputHashes {
		if err := validateHash(inputHash, "input_hashes"); err != nil {
			return err
		}
	}
	if artifact.Status == "" {
		artifact.Status = "provisional"
	}
	if !validArtifactStatuses[artifact.Status] || !validArtifactMediaTypes[artifact.MediaType] {
		return fmt.Errorf("%w: invalid artifact status or media type", ErrInvalidRecord)
	}
	if artifact.Status == "committed" && (artifact.SnapshotID == "" || artifact.SnapshotVersion < 1 || artifact.CommittedAt.IsZero()) {
		return fmt.Errorf("%w: committed artifact requires persisted snapshot reference and timestamp", ErrInvalidRecord)
	}
	provenance, sources, err := artifact.Trace.values()
	if err != nil {
		return err
	}
	parentIDs := make([]string, len(artifact.Parents))
	for index, parent := range artifact.Parents {
		if err := validateID(parent.ArtifactID, "art_", "parent_artifact_id"); err != nil {
			return err
		}
		if parent.Version < 1 {
			return fmt.Errorf("%w: parent artifact version must be at least 1", ErrInvalidRecord)
		}
		parentIDs[index] = parent.ArtifactID
	}
	parents, err := marshalJSON(parentIDs, "parent artifact ids")
	if err != nil {
		return err
	}
	inputHashes, err := marshalJSON(artifact.InputHashes, "artifact input hashes")
	if err != nil {
		return err
	}
	createdAt := artifact.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	result, err := tx.tx.ExecContext(ctx, `
		INSERT INTO writing_artifacts (
			artifact_id, version, run_id, plan_id, plan_version, node_id,
			attempt, idempotency_key, output_key, artifact_type, status,
			content_hash, media_type, content_ref, parent_artifact_ids,
			quality_report_id, quality_report_version, snapshot_manifest_id,
			snapshot_version, producer, capability_version, input_hashes,
			model_ref, prompt_template_ref, provenance, source_refs,
			created_by_type, created_by_id, created_at, committed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
		          $16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30)
		ON CONFLICT (artifact_id, version) DO NOTHING
	`, artifact.ArtifactID, artifact.Version, artifact.RunID, artifact.PlanID,
		artifact.PlanVersion, artifact.NodeID, artifact.Attempt, artifact.IdempotencyKey,
		artifact.OutputKey, artifact.ArtifactType, artifact.Status, artifact.ContentHash,
		artifact.MediaType, artifact.ContentRef, parents, nullString(artifact.QualityReportID),
		nullVersion(artifact.QualityReportVersion), nullString(artifact.SnapshotID),
		nullVersion(artifact.SnapshotVersion), artifact.Producer, artifact.CapabilityVersion,
		inputHashes, nullString(artifact.ModelRef), nullString(artifact.PromptTemplateRef),
		provenance, sources, string(artifact.Trace.Actor.Type), nullString(artifact.Trace.Actor.ID),
		createdAt, nullTime(artifact.CommittedAt))
	if err != nil {
		return fmt.Errorf("insert writing artifact: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect artifact insert: %w", err)
	}
	if inserted == 0 {
		var existingRunID, existingPlanID, existingNodeID, existingKey string
		var existingType, existingHash, existingMediaType, existingContentRef string
		var existingProducer, existingCapabilityVersion string
		var existingPlanVersion, existingAttempt int
		var existingOutputKey string
		var immutablePayloadMatches bool
		if err := tx.tx.QueryRowContext(ctx, `
			SELECT run_id, plan_id, plan_version, node_id, attempt,
			       idempotency_key, output_key, artifact_type, content_hash,
			       media_type, content_ref, producer, capability_version,
			       parent_artifact_ids = $3::jsonb
			         AND input_hashes = $4::jsonb
			         AND COALESCE(model_ref, '') = $5
			         AND COALESCE(prompt_template_ref, '') = $6
			         AND provenance = $7::jsonb AND source_refs = $8::jsonb
			         AND created_by_type = $9 AND COALESCE(created_by_id, '') = $10
			FROM writing_artifacts WHERE artifact_id=$1 AND version=$2
		`, artifact.ArtifactID, artifact.Version, string(parents), string(inputHashes),
			artifact.ModelRef, artifact.PromptTemplateRef, string(provenance), string(sources),
			string(artifact.Trace.Actor.Type), artifact.Trace.Actor.ID).Scan(
			&existingRunID, &existingPlanID, &existingPlanVersion, &existingNodeID,
			&existingAttempt, &existingKey, &existingOutputKey, &existingType,
			&existingHash, &existingMediaType, &existingContentRef, &existingProducer,
			&existingCapabilityVersion, &immutablePayloadMatches); err != nil {
			return fmt.Errorf("load conflicting artifact: %w", err)
		}
		if existingRunID != artifact.RunID || existingPlanID != artifact.PlanID || existingPlanVersion != artifact.PlanVersion ||
			existingNodeID != artifact.NodeID || existingAttempt != artifact.Attempt || existingKey != artifact.IdempotencyKey ||
			existingOutputKey != artifact.OutputKey || existingType != artifact.ArtifactType || existingHash != artifact.ContentHash ||
			existingMediaType != artifact.MediaType || existingContentRef != artifact.ContentRef || existingProducer != artifact.Producer ||
			existingCapabilityVersion != artifact.CapabilityVersion || !immutablePayloadMatches {
			return fmt.Errorf("%w: artifact %s version %d has different immutable content", ErrImmutableConflict, artifact.ArtifactID, artifact.Version)
		}
		var edgeCount int
		if err := tx.tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM writing_artifact_edges
			WHERE run_id=$1 AND child_artifact_id=$2 AND child_artifact_version=$3
			  AND relation='derived_from'
		`, artifact.RunID, artifact.ArtifactID, artifact.Version).Scan(&edgeCount); err != nil {
			return fmt.Errorf("load artifact lineage: %w", err)
		}
		if edgeCount != len(artifact.Parents) {
			return fmt.Errorf("%w: artifact lineage differs", ErrImmutableConflict)
		}
		for index, parent := range artifact.Parents {
			var exists bool
			if err := tx.tx.QueryRowContext(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM writing_artifact_edges
					WHERE run_id=$1 AND child_artifact_id=$2 AND child_artifact_version=$3
					  AND parent_artifact_id=$4 AND parent_artifact_version=$5
					  AND relation='derived_from' AND ordinal=$6
				)
			`, artifact.RunID, artifact.ArtifactID, artifact.Version,
				parent.ArtifactID, parent.Version, index).Scan(&exists); err != nil {
				return fmt.Errorf("load artifact lineage binding: %w", err)
			}
			if !exists {
				return fmt.Errorf("%w: artifact lineage differs", ErrImmutableConflict)
			}
		}
		return nil
	}
	for index, parent := range artifact.Parents {
		_, err := tx.tx.ExecContext(ctx, `
			INSERT INTO writing_artifact_edges (
				run_id, child_artifact_id, child_artifact_version,
				parent_artifact_id, parent_artifact_version, relation, ordinal
			) VALUES ($1,$2,$3,$4,$5,'derived_from',$6)
			ON CONFLICT DO NOTHING
		`, artifact.RunID, artifact.ArtifactID, artifact.Version,
			parent.ArtifactID, parent.Version, index)
		if err != nil {
			return fmt.Errorf("insert artifact lineage: %w", err)
		}
	}
	return nil
}

func nullVersion(version int) any {
	if version == 0 {
		return nil
	}
	return version
}
