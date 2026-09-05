// Finalize runner (V3.0 M1.1, docs/21 §21.8 simplified form): the kernel
// bookkeeping executor for the document.finalize capability. By the time
// finalize runs, the delivery protocol has already committed the candidate
// version and promoted it to accepted — finalize only records the outcome as
// a revision_set artifact, so the lineage carries a durable record of which
// document version each plan delivery produced. Actor discipline: kernel
// behavior (ActorSystem), never model/capability.
package writingruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// FinalizeRunner is the kernel bookkeeping executor for document.finalize.
type FinalizeRunner struct {
	Store   *writingstore.Store
	Content ContentGateway
	// Actor records the store writes; zero falls back to ActorSystem.
	Actor writingstore.Actor
	// Now overrides the wall clock in tests. Zero means time.Now.
	Now func() time.Time
}

// revisionSetPayload is the durable revision_set content: which document
// version this plan delivery produced, and from which base.
type revisionSetPayload struct {
	DocumentID    string `json:"document_id"`
	BaseVersionID string `json:"base_version_id,omitempty"`
	VersionID     string `json:"version_id"`
	QualityState  string `json:"quality_state"`
	QualityReport string `json:"quality_report_id,omitempty"`
	FinalizedAt   string `json:"finalized_at"`
}

func (runner *FinalizeRunner) Run(ctx context.Context, input LegacyNodeInput) ([]LegacyPayload, LegacyUsage, error) {
	if runner.Store == nil {
		return nil, LegacyUsage{}, ErrRuntimeNotReady
	}
	run, err := runner.Store.LoadRuntimeRun(ctx, input.Request.RunID)
	if err != nil {
		return nil, LegacyUsage{}, fmt.Errorf("%w: finalize could not load the run: %v", ErrInvalidExecutionResult, err)
	}
	versionID, err := runner.Store.CurrentDocumentVersionID(ctx, run.DocumentID)
	if err != nil {
		return nil, LegacyUsage{}, fmt.Errorf("%w: finalize could not resolve the delivered version: %v", ErrInvalidExecutionResult, err)
	}
	payload := revisionSetPayload{
		DocumentID:    run.DocumentID,
		BaseVersionID: run.BaseVersionID,
		VersionID:     versionID,
		QualityState:  "accepted_draft",
		FinalizedAt:   runner.clock().Format(time.RFC3339),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, LegacyUsage{}, fmt.Errorf("marshal revision set: %w", err)
	}
	qualityReport, ok := findArtifactPayload(input.Payloads, "quality_report")
	if ok {
		payload.QualityReport = contentHash(qualityReport)
		if body, err = json.Marshal(payload); err != nil {
			return nil, LegacyUsage{}, fmt.Errorf("re-marshal revision set: %w", err)
		}
	}
	mediaType := "application/json"
	outputs := []LegacyPayload{{OutputKey: "revision_set", ArtifactType: writingplan.ArtifactType("revision_set"),
		MediaType: mediaType, Body: body,
		Provenance: map[string]any{"version_id": versionID, "document_id": run.DocumentID}}}
	return outputs, LegacyUsage{Measured: true}, nil
}

func findArtifactPayload(payloads map[writingplan.ArtifactType][][]byte, artifactType string) ([]byte, bool) {
	for _, body := range payloads[writingplan.ArtifactType(artifactType)] {
		return body, true
	}
	return nil, false
}

func (runner *FinalizeRunner) actor() writingstore.Actor {
	if runner.Actor.Type != "" {
		return runner.Actor
	}
	return writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime.finalize"}
}

func (runner *FinalizeRunner) clock() time.Time {
	if runner.Now != nil {
		return runner.Now()
	}
	return time.Now().UTC()
}
