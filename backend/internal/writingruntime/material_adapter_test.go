package writingruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

func TestMaterialAdapterCreatesDeterministicGovernedBundle(t *testing.T) {
	source := &materialSourceStub{bodies: map[string][]byte{
		"mat_a": []byte("用户材料 A"),
		"mat_b": []byte("用户材料 B"),
	}}
	gateway := newSnapshotGateway()
	adapter := mustMaterialAdapter(t, source, gateway)
	request := MaterialSnapshotRequest{
		RunID: "run_materials", OwnerID: "user_owner", ConflictHandling: "ask_user",
		Materials: []MaterialDescriptor{
			{MaterialID: "mat_b", OwnerID: "user_owner", Title: "B", SourceKind: MaterialSourceFile, SourceRef: "kb://doc_b", MediaType: "text/plain", UpdatedAt: fixedMaterialTime()},
			{MaterialID: "mat_a", OwnerID: "user_owner", Title: "A", SourceKind: MaterialSourceText, SourceRef: "kb://doc_a", MediaType: "text/plain", UpdatedAt: fixedMaterialTime()},
		},
	}
	bundle, err := adapter.Snapshot(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Artifact.ArtifactType != "materials" || len(bundle.Manifest.Materials) != 2 {
		t.Fatalf("bundle=%#v", bundle)
	}
	if bundle.Manifest.Materials[0].MaterialID != "mat_a" || bundle.Manifest.Materials[1].MaterialID != "mat_b" {
		t.Fatalf("material order is not canonical: %#v", bundle.Manifest.Materials)
	}
	if bundle.Artifact.ContentHash != contentHash(gateway.bodies[bundle.Artifact.ContentRef]) {
		t.Fatalf("manifest hash does not bind staged bytes")
	}
	second, err := adapter.Snapshot(context.Background(), request)
	if err != nil || second.Artifact.ContentHash != bundle.Artifact.ContentHash || second.Artifact.ArtifactID != bundle.Artifact.ArtifactID {
		t.Fatalf("snapshot is not deterministic: first=%#v second=%#v err=%v", bundle.Artifact, second.Artifact, err)
	}
	if err := adapter.Verify(context.Background(), bundle.Artifact); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestMaterialAdapterResolvesSnapshottedBodiesAndEmptyManifest(t *testing.T) {
	gateway := newSnapshotGateway()
	adapter := mustMaterialAdapter(t, &materialSourceStub{bodies: map[string][]byte{"mat_a": []byte("完整材料正文")}}, gateway)
	bundle, err := adapter.Snapshot(context.Background(), MaterialSnapshotRequest{
		RunID: "run_resolve", OwnerID: "user_owner", ConflictHandling: "ask_user",
		Materials: []MaterialDescriptor{{MaterialID: "mat_a", OwnerID: "user_owner", Title: "材料 A",
			SourceKind: MaterialSourceText, SourceRef: "kb://a", MediaType: "text/plain", UpdatedAt: fixedMaterialTime()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestBody := gateway.bodies[bundle.Artifact.ContentRef]
	resolved, err := adapter.ResolveMaterialSnapshots(context.Background(), manifestBody)
	if err != nil || len(resolved) != 1 || string(resolved[0]) != "[材料: 材料 A]\n来源: kb://a\n\n完整材料正文" {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}

	empty, err := adapter.Snapshot(context.Background(), MaterialSnapshotRequest{
		RunID: "run_empty", OwnerID: "user_owner", ConflictHandling: "ask_user", Materials: []MaterialDescriptor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	emptyResolved, err := adapter.ResolveMaterialSnapshots(context.Background(), gateway.bodies[empty.Artifact.ContentRef])
	if err != nil || len(emptyResolved) != 0 {
		t.Fatalf("empty resolved=%q err=%v", emptyResolved, err)
	}

	materialRef := bundle.Manifest.Materials[0].ContentRef
	gateway.bodies[materialRef] = []byte("tampered")
	if _, err := adapter.ResolveMaterialSnapshots(context.Background(), manifestBody); ErrorCodeOf(err) != CodeMaterialIntegrityFailed {
		t.Fatalf("tampered snapshot err=%v code=%s", err, ErrorCodeOf(err))
	}
}

func TestMaterialAdapterRejectsTenantMismatchBeforeReading(t *testing.T) {
	source := &materialSourceStub{bodies: map[string][]byte{"mat_other": []byte("secret")}}
	adapter := mustMaterialAdapter(t, source, newSnapshotGateway())
	_, err := adapter.Snapshot(context.Background(), MaterialSnapshotRequest{RunID: "run_materials", OwnerID: "user_owner", ConflictHandling: "ask_user", Materials: []MaterialDescriptor{{MaterialID: "mat_other", OwnerID: "user_other", Title: "private", SourceKind: MaterialSourceText, SourceRef: "kb://other", MediaType: "text/plain", UpdatedAt: fixedMaterialTime()}}})
	if ErrorCodeOf(err) != CodeMaterialAccessDenied || source.loads != 0 {
		t.Fatalf("error=%v code=%s loads=%d", err, ErrorCodeOf(err), source.loads)
	}
}

func TestMaterialAdapterFailsClosedOnSnapshotIntegrityMismatch(t *testing.T) {
	gateway := newSnapshotGateway()
	gateway.corruptStage = true
	adapter := mustMaterialAdapter(t, &materialSourceStub{bodies: map[string][]byte{"mat_a": []byte("body")}}, gateway)
	_, err := adapter.Snapshot(context.Background(), MaterialSnapshotRequest{RunID: "run_materials", OwnerID: "user_owner", ConflictHandling: "ask_user", Materials: []MaterialDescriptor{{MaterialID: "mat_a", OwnerID: "user_owner", Title: "A", SourceKind: MaterialSourceText, SourceRef: "kb://a", MediaType: "text/plain", UpdatedAt: fixedMaterialTime()}}})
	if ErrorCodeOf(err) != CodeMaterialIntegrityFailed {
		t.Fatalf("error=%v code=%s", err, ErrorCodeOf(err))
	}
}

func TestMaterialAdapterFailsClosedWhenSourceSnapshotCannotBePersisted(t *testing.T) {
	gateway := newSnapshotGateway()
	gateway.stageErr = errors.New("object store unavailable")
	adapter := mustMaterialAdapter(t, &materialSourceStub{bodies: map[string][]byte{"mat_a": []byte("body")}}, gateway)
	_, err := adapter.Snapshot(context.Background(), MaterialSnapshotRequest{RunID: "run_materials", OwnerID: "user_owner", ConflictHandling: "ask_user", Materials: []MaterialDescriptor{{MaterialID: "mat_a", OwnerID: "user_owner", Title: "A", SourceKind: MaterialSourceText, SourceRef: "kb://a", MediaType: "text/plain", UpdatedAt: fixedMaterialTime()}}})
	if ErrorCodeOf(err) != CodeSourceSnapshotFailed {
		t.Fatalf("error=%v code=%s", err, ErrorCodeOf(err))
	}
}

func TestClaimConflictPreservesBothSourcesAndRequiresDecision(t *testing.T) {
	findings := DetectClaimConflicts([]MaterialClaim{
		{ClaimID: "claim_user", Subject: "项目", Predicate: "发布时间", Value: "九月", UserMaterial: true, SourceRefs: []string{"material://user"}},
		{ClaimID: "claim_web", Subject: "项目", Predicate: "发布时间", Value: "十月", SourceRefs: []string{"https://example.test/source"}},
	})
	if len(findings) != 1 || len(findings[0].SourceRefs) != 2 || findings[0].PreferredClaimID != "claim_user" {
		t.Fatalf("findings=%#v", findings)
	}
	if err := EnforceConflictHandling("ask_user", findings); ErrorCodeOf(err) != CodeSourceConflictRequiresDecision {
		t.Fatalf("error=%v code=%s", err, ErrorCodeOf(err))
	}
}

func TestMaterialAdapterStagesTypedSourceArtifactsWithoutCommitting(t *testing.T) {
	gateway := newSnapshotGateway()
	adapter := mustMaterialAdapter(t, &materialSourceStub{bodies: map[string][]byte{}}, gateway)
	request := legacyRequest([]byte("contract"))
	request.Node.Capability = "core.retrieval.search"
	request.Node.OutputArtifactTypes = []writingplan.ArtifactType{"source_pack"}
	draft, err := adapter.StageTypedResult(context.Background(), request, "sources", "source_pack", SourcePack{Query: "governed writing", Sources: []SourceRecord{{SourceID: "source_1", Title: "Source", URL: "https://example.test/source", Excerpt: "evidence"}}}, []string{"https://example.test/source"})
	if err != nil {
		t.Fatal(err)
	}
	if draft.ArtifactType != "source_pack" || draft.Producer != request.Node.Capability || len(draft.Parents) != 1 || len(draft.InputHashes) != 1 || len(draft.SourceRefs) != 1 {
		t.Fatalf("draft=%#v", draft)
	}
	if _, ok := gateway.bodies[draft.ContentRef]; !ok {
		t.Fatalf("typed result was not staged")
	}
	if _, err := adapter.StageTypedResult(context.Background(), request, "draft", "full_draft", map[string]any{"body": "forbidden"}, []string{}); ErrorCodeOf(err) != CodeExecutorContractMismatch {
		t.Fatalf("error=%v code=%s", err, ErrorCodeOf(err))
	}
}

func TestMaterialAdapterEmitsIntegrityTelemetryWithoutIdentityLabels(t *testing.T) {
	source := &materialSourceStub{bodies: map[string][]byte{"mat_a": []byte("source")}}
	gateway := newSnapshotGateway()
	adapter := mustMaterialAdapter(t, source, gateway)
	metrics := &metricCapture{}
	adapter.WithTelemetry(metrics)
	request := MaterialSnapshotRequest{RunID: "run_materials", OwnerID: "user_owner", ConflictHandling: "ask_user",
		Materials: []MaterialDescriptor{{MaterialID: "mat_a", OwnerID: "user_owner", Title: "A",
			SourceKind: MaterialSourceText, SourceRef: "kb://a", MediaType: "text/plain", UpdatedAt: fixedMaterialTime()}}}
	bundle, err := adapter.Snapshot(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Verify(context.Background(), bundle.Artifact); err != nil {
		t.Fatal(err)
	}
	if !metrics.has(MetricMaterialIntegrity, "passed") {
		t.Fatalf("metrics=%#v", metrics.metrics)
	}
	for _, metric := range metrics.metrics {
		if metric.ExecutorID != "" || metric.Reason != "" {
			t.Fatalf("material identity leaked into metric dimensions: %#v", metric)
		}
	}
}

type materialSourceStub struct {
	bodies map[string][]byte
	loads  int
}

func (source *materialSourceStub) LoadMaterial(_ context.Context, ownerID string, material MaterialDescriptor) (MaterialContent, error) {
	source.loads++
	body, ok := source.bodies[material.MaterialID]
	if !ok || ownerID != material.OwnerID {
		return MaterialContent{}, errors.New("not found")
	}
	return MaterialContent{Body: append([]byte(nil), body...), SourceRefs: []string{material.SourceRef}}, nil
}

type snapshotGateway struct {
	bodies       map[string][]byte
	corruptStage bool
	stageErr     error
}

func newSnapshotGateway() *snapshotGateway { return &snapshotGateway{bodies: map[string][]byte{}} }

func (gateway *snapshotGateway) Load(_ context.Context, artifact InputArtifact) ([]byte, error) {
	body, ok := gateway.bodies[artifact.ContentRef]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), body...), nil
}

func (gateway *snapshotGateway) Stage(_ context.Context, key, _ string, body []byte) (string, string, error) {
	if gateway.stageErr != nil {
		return "", "", gateway.stageErr
	}
	ref := "snapshot://" + key
	gateway.bodies[ref] = append([]byte(nil), body...)
	hash := contentHash(body)
	if gateway.corruptStage {
		hash = hashForTest("corrupt")
	}
	return ref, hash, nil
}

func mustMaterialAdapter(t *testing.T, source MaterialContentSource, gateway ContentGateway) *MaterialAdapter {
	t.Helper()
	adapter, err := NewMaterialAdapter(source, gateway)
	if err != nil {
		t.Fatal(err)
	}
	adapter.now = fixedMaterialTime
	return adapter
}

func fixedMaterialTime() time.Time {
	return time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
}
