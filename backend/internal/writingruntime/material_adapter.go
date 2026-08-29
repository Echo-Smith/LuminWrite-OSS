package writingruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

type MaterialSourceKind string

const (
	MaterialSourceText      MaterialSourceKind = "text"
	MaterialSourceFile      MaterialSourceKind = "file"
	MaterialSourceURL       MaterialSourceKind = "url"
	MaterialSourceKnowledge MaterialSourceKind = "knowledge"
)

type MaterialDescriptor struct {
	MaterialID string             `json:"material_id"`
	OwnerID    string             `json:"owner_id"`
	Title      string             `json:"title"`
	SourceKind MaterialSourceKind `json:"source_kind"`
	SourceRef  string             `json:"source_ref"`
	MediaType  string             `json:"media_type"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

type MaterialContent struct {
	Body       []byte
	SourceRefs []string
}

type MaterialContentSource interface {
	LoadMaterial(context.Context, string, MaterialDescriptor) (MaterialContent, error)
}

type MaterialSnapshotRequest struct {
	RunID            string
	OwnerID          string
	ConflictHandling string
	Materials        []MaterialDescriptor
}

type MaterialSnapshot struct {
	MaterialID  string             `json:"material_id"`
	Title       string             `json:"title"`
	SourceKind  MaterialSourceKind `json:"source_kind"`
	SourceRef   string             `json:"source_ref"`
	MediaType   string             `json:"media_type"`
	ContentRef  string             `json:"content_ref"`
	ContentHash string             `json:"content_hash"`
	SourceRefs  []string           `json:"source_refs"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

type MaterialManifest struct {
	SchemaVersion    string             `json:"schema_version"`
	RunID            string             `json:"run_id"`
	OwnerID          string             `json:"owner_id"`
	ConflictHandling string             `json:"conflict_handling"`
	CapturedAt       time.Time          `json:"captured_at"`
	Materials        []MaterialSnapshot `json:"materials"`
}

type MaterialBundle struct {
	Artifact InputArtifact
	Manifest MaterialManifest
}

type SourceRecord struct {
	SourceID string  `json:"source_id"`
	Title    string  `json:"title"`
	URL      string  `json:"url,omitempty"`
	Excerpt  string  `json:"excerpt"`
	Score    float64 `json:"score,omitempty"`
}

type SourcePack struct {
	Query   string         `json:"query"`
	Sources []SourceRecord `json:"sources"`
}

type ResearchNote struct {
	NoteID     string   `json:"note_id"`
	Body       string   `json:"body"`
	SourceRefs []string `json:"source_refs"`
}

type ClaimMap struct {
	Claims   []MaterialClaim   `json:"claims"`
	Findings []MaterialFinding `json:"findings"`
}

type MaterialAdapter struct {
	source  MaterialContentSource
	content ContentGateway
	now     func() time.Time
}

func NewMaterialAdapter(source MaterialContentSource, content ContentGateway) (*MaterialAdapter, error) {
	if source == nil || content == nil {
		return nil, ErrRuntimeNotReady
	}
	return &MaterialAdapter{source: source, content: content, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (adapter *MaterialAdapter) Snapshot(ctx context.Context, request MaterialSnapshotRequest) (MaterialBundle, error) {
	if adapter == nil || adapter.source == nil || adapter.content == nil || !hasIDPrefix(request.RunID, "run_") || strings.TrimSpace(request.OwnerID) == "" || len(request.Materials) == 0 || strings.TrimSpace(request.ConflictHandling) == "" {
		return MaterialBundle{}, runtimeError(CodeExecutorContractMismatch, RetryNever, "invalid material snapshot request", ErrInvalidExecutionRequest)
	}
	materials := append([]MaterialDescriptor(nil), request.Materials...)
	sort.Slice(materials, func(i, j int) bool { return materials[i].MaterialID < materials[j].MaterialID })
	manifest := MaterialManifest{SchemaVersion: writingplan.SchemaVersion, RunID: request.RunID, OwnerID: request.OwnerID, ConflictHandling: request.ConflictHandling, Materials: make([]MaterialSnapshot, 0, len(materials))}
	seen := map[string]struct{}{}
	for _, material := range materials {
		if err := validateMaterialDescriptor(material); err != nil {
			return MaterialBundle{}, err
		}
		if material.OwnerID != request.OwnerID {
			return MaterialBundle{}, runtimeError(CodeMaterialAccessDenied, RetryNever, "material owner does not match run owner", nil)
		}
		if _, duplicate := seen[material.MaterialID]; duplicate {
			return MaterialBundle{}, runtimeError(CodeExecutorContractMismatch, RetryNever, "duplicate material reference", nil)
		}
		seen[material.MaterialID] = struct{}{}
		loaded, err := adapter.source.LoadMaterial(ctx, request.OwnerID, material)
		if err != nil {
			return MaterialBundle{}, runtimeError(CodeMaterialAccessDenied, RetryNever, "material cannot be read", err)
		}
		if len(loaded.Body) == 0 {
			return MaterialBundle{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "material body is empty", nil)
		}
		hash := contentHash(loaded.Body)
		ref, stagedHash, err := adapter.content.Stage(ctx, request.RunID+":material:"+material.MaterialID+":"+hash, material.MediaType, loaded.Body)
		if err != nil {
			return MaterialBundle{}, runtimeError(CodeSourceSnapshotFailed, RetrySafe, "material snapshot could not be persisted", err)
		}
		if stagedHash != hash || strings.TrimSpace(ref) == "" {
			return MaterialBundle{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "staged material hash differs", nil)
		}
		refs := canonicalStrings(append(append([]string(nil), loaded.SourceRefs...), material.SourceRef))
		manifest.Materials = append(manifest.Materials, MaterialSnapshot{MaterialID: material.MaterialID, Title: material.Title, SourceKind: material.SourceKind, SourceRef: material.SourceRef, MediaType: material.MediaType, ContentRef: ref, ContentHash: hash, SourceRefs: refs, UpdatedAt: material.UpdatedAt.UTC()})
		if manifest.CapturedAt.Before(material.UpdatedAt) {
			manifest.CapturedAt = material.UpdatedAt.UTC()
		}
	}
	if manifest.CapturedAt.IsZero() {
		manifest.CapturedAt = adapter.now().UTC()
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return MaterialBundle{}, runtimeError(CodeSourceSnapshotFailed, RetryNever, "material manifest cannot be encoded", err)
	}
	hash := contentHash(payload)
	ref, stagedHash, err := adapter.content.Stage(ctx, request.RunID+":materials:"+hash, "application/json", payload)
	if err != nil {
		return MaterialBundle{}, runtimeError(CodeSourceSnapshotFailed, RetrySafe, "material manifest could not be persisted", err)
	}
	if stagedHash != hash || strings.TrimSpace(ref) == "" {
		return MaterialBundle{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "staged manifest hash differs", nil)
	}
	artifact := InputArtifact{ArtifactID: writingstore.StableID("art_", request.RunID, "materials", hash), Version: 1, ArtifactType: "materials", ContentHash: hash, MediaType: "application/json", ContentRef: ref}
	return MaterialBundle{Artifact: artifact, Manifest: manifest}, nil
}

func (adapter *MaterialAdapter) Verify(ctx context.Context, artifact InputArtifact) error {
	if artifact.ArtifactType != "materials" || artifact.MediaType != "application/json" {
		return runtimeError(CodeMaterialIntegrityFailed, RetryNever, "artifact is not a material manifest", nil)
	}
	payload, err := adapter.content.Load(ctx, artifact)
	if err != nil {
		return runtimeError(CodeSourceSnapshotFailed, RetrySafe, "material manifest cannot be loaded", err)
	}
	if contentHash(payload) != artifact.ContentHash {
		return runtimeError(CodeMaterialIntegrityFailed, RetryNever, "material manifest was modified", nil)
	}
	var manifest MaterialManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return runtimeError(CodeMaterialIntegrityFailed, RetryNever, "material manifest is invalid", err)
	}
	for _, material := range manifest.Materials {
		body, err := adapter.content.Load(ctx, InputArtifact{ArtifactID: writingstore.StableID("art_", manifest.RunID, material.MaterialID, material.ContentHash), Version: 1, ArtifactType: "materials", ContentHash: material.ContentHash, MediaType: material.MediaType, ContentRef: material.ContentRef})
		if err != nil {
			return runtimeError(CodeSourceSnapshotFailed, RetrySafe, "material snapshot cannot be loaded", err)
		}
		if contentHash(body) != material.ContentHash {
			return runtimeError(CodeMaterialIntegrityFailed, RetryNever, "material snapshot was modified", nil)
		}
	}
	return nil
}

// StageTypedResult normalizes material-derived executor output without making
// it authoritative. The returned draft can only become a canonical Artifact
// through Orchestrator.CompleteNodeAttempt.
func (adapter *MaterialAdapter) StageTypedResult(ctx context.Context, request ExecutionRequest, outputKey string, artifactType writingplan.ArtifactType, value any, sourceRefs []string) (OutputArtifactDraft, error) {
	if adapter == nil || adapter.content == nil {
		return OutputArtifactDraft{}, ErrRuntimeNotReady
	}
	if err := request.Validate(); err != nil {
		return OutputArtifactDraft{}, err
	}
	if artifactType != "source_pack" && artifactType != "research_note" && artifactType != "claim_map" {
		return OutputArtifactDraft{}, runtimeError(CodeExecutorContractMismatch, RetryNever, "material adapter only stages material-derived artifact types", nil)
	}
	declared := false
	for _, output := range request.Node.OutputArtifactTypes {
		if output == artifactType {
			declared = true
			break
		}
	}
	if !declared || strings.TrimSpace(outputKey) == "" {
		return OutputArtifactDraft{}, runtimeError(CodeExecutorContractMismatch, RetryNever, "typed artifact was not declared by the plan node", nil)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return OutputArtifactDraft{}, runtimeError(CodeExecutorOutputInvalid, RetryNever, "typed artifact cannot be encoded", err)
	}
	hash := contentHash(payload)
	ref, stagedHash, err := adapter.content.Stage(ctx, request.IdempotencyKey+":"+outputKey+":"+hash, "application/json", payload)
	if err != nil {
		return OutputArtifactDraft{}, runtimeError(CodeSourceSnapshotFailed, RetrySafe, "typed source artifact could not be persisted", err)
	}
	if stagedHash != hash || strings.TrimSpace(ref) == "" {
		return OutputArtifactDraft{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "typed source artifact hash differs", nil)
	}
	parents := make([]writingstore.ArtifactRef, len(request.Inputs))
	inputHashes := make([]string, len(request.Inputs))
	for index, input := range request.Inputs {
		parents[index] = writingstore.ArtifactRef{ArtifactID: input.ArtifactID, Version: input.Version}
		inputHashes[index] = input.ContentHash
	}
	return OutputArtifactDraft{OutputKey: outputKey, ArtifactType: artifactType, ContentHash: hash, MediaType: "application/json", ContentRef: ref, Parents: parents, Producer: request.Node.Capability, CapabilityVersion: request.Node.CapabilityVersion, InputHashes: inputHashes, Provenance: map[string]any{"adapter": "material", "schema_version": writingplan.SchemaVersion}, SourceRefs: canonicalStrings(sourceRefs)}, nil
}

func validateMaterialDescriptor(material MaterialDescriptor) error {
	validKind := material.SourceKind == MaterialSourceText || material.SourceKind == MaterialSourceFile || material.SourceKind == MaterialSourceURL || material.SourceKind == MaterialSourceKnowledge
	if strings.TrimSpace(material.MaterialID) == "" || strings.TrimSpace(material.OwnerID) == "" || strings.TrimSpace(material.Title) == "" || !validKind || strings.TrimSpace(material.SourceRef) == "" || !validExecutionMediaType(material.MediaType) || material.UpdatedAt.IsZero() {
		return runtimeError(CodeExecutorContractMismatch, RetryNever, "invalid material descriptor", ErrInvalidExecutionRequest)
	}
	return nil
}

func canonicalStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

type MaterialClaim struct {
	ClaimID      string   `json:"claim_id"`
	Subject      string   `json:"subject"`
	Predicate    string   `json:"predicate"`
	Value        string   `json:"value"`
	UserMaterial bool     `json:"user_material"`
	SourceRefs   []string `json:"source_refs"`
}

type MaterialFinding struct {
	FindingID        string    `json:"finding_id"`
	Code             ErrorCode `json:"code"`
	Severity         string    `json:"severity"`
	Subject          string    `json:"subject"`
	Predicate        string    `json:"predicate"`
	ClaimIDs         []string  `json:"claim_ids"`
	PreferredClaimID string    `json:"preferred_claim_id,omitempty"`
	SourceRefs       []string  `json:"source_refs"`
}

func DetectClaimConflicts(claims []MaterialClaim) []MaterialFinding {
	groups := map[string][]MaterialClaim{}
	for _, claim := range claims {
		key := normalizeClaimPart(claim.Subject) + "\x00" + normalizeClaimPart(claim.Predicate)
		if key == "\x00" || strings.TrimSpace(claim.ClaimID) == "" || strings.TrimSpace(claim.Value) == "" {
			continue
		}
		groups[key] = append(groups[key], claim)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := []MaterialFinding{}
	for _, key := range keys {
		group := groups[key]
		values := map[string]struct{}{}
		for _, claim := range group {
			values[normalizeClaimPart(claim.Value)] = struct{}{}
		}
		if len(values) < 2 {
			continue
		}
		ids, refs, preferred := []string{}, []string{}, ""
		for _, claim := range group {
			ids = append(ids, claim.ClaimID)
			refs = append(refs, claim.SourceRefs...)
			if claim.UserMaterial && preferred == "" {
				preferred = claim.ClaimID
			}
		}
		sort.Strings(ids)
		result = append(result, MaterialFinding{FindingID: writingstore.StableID("finding_", key, strings.Join(ids, ",")), Code: CodeSourceConflictRequiresDecision, Severity: "error", Subject: group[0].Subject, Predicate: group[0].Predicate, ClaimIDs: ids, PreferredClaimID: preferred, SourceRefs: canonicalStrings(refs)})
	}
	return result
}

func EnforceConflictHandling(conflictHandling string, findings []MaterialFinding) error {
	if conflictHandling == "ask_user" && len(findings) > 0 {
		return runtimeError(CodeSourceConflictRequiresDecision, RetryAfterHuman, fmt.Sprintf("%d source conflict(s) require a decision", len(findings)), nil)
	}
	return nil
}

func normalizeClaimPart(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}
