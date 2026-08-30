package writingruntime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStoreRolloutEvidenceRequiresRecorder(t *testing.T) {
	if _, err := NewStoreRolloutEvidence(nil); !errors.Is(err, ErrRuntimeNotReady) {
		t.Fatalf("error=%v", err)
	}
}

func TestStoreRolloutEvidenceRejectsInvalidIdentityAndKindBeforeWrite(t *testing.T) {
	recorder := &evidenceRecorderStub{}
	store, err := NewStoreRolloutEvidence(recorder)
	if err != nil {
		t.Fatal(err)
	}
	evidence := RuntimeEvidence{EvidenceID: "evt_invalid", Kind: "route_decision", RecordedAt: time.Now().UTC()}
	if err := store.Record(context.Background(), evidence); ErrorCodeOf(err) != CodeRolloutEvidenceFailed {
		t.Fatalf("identity error=%v", err)
	}
	if recorder.calls != 0 {
		t.Fatalf("invalid evidence reached recorder: calls=%d", recorder.calls)
	}

	evidence.Identity = legacyRequest([]byte("contract")).Identity()
	evidence.Kind = "invented_kind"
	if err := store.Record(context.Background(), evidence); ErrorCodeOf(err) != CodeRolloutEvidenceFailed {
		t.Fatalf("kind error=%v", err)
	}
	if recorder.calls != 0 {
		t.Fatalf("unsupported kind reached recorder: calls=%d", recorder.calls)
	}
}
