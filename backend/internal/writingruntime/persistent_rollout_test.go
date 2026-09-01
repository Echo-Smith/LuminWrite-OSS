package writingruntime

import (
	"context"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

type shadowStoreStub struct {
	record writingstore.ShadowContentRecord
}

func (s *shadowStoreStub) PutShadowContent(_ context.Context, record writingstore.ShadowContentRecord) error {
	s.record = record
	return nil
}
func (s *shadowStoreStub) GetShadowContent(_ context.Context, _ string) (writingstore.ShadowContentRecord, error) {
	return s.record, nil
}
func (s *shadowStoreStub) DeleteShadowContentPrefix(context.Context, string) (int, error) {
	return 1, nil
}
func (s *shadowStoreStub) DeleteShadowContentBefore(context.Context, time.Time) (int, error) {
	return 1, nil
}

func TestWritingStoreShadowSinkPreservesIsolationMetadata(t *testing.T) {
	store := &shadowStoreStub{}
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	sink := WritingStoreShadowContentSink{Store: store, TTL: time.Hour, Now: func() time.Time { return now }}
	policy := shadowPolicyForTest()
	key := policy.PolicyHash[len("sha256:"):] + "/run_task13-node_draft-1-draft/0123456789abcdef"
	if err := sink.Put(context.Background(), key, "text/markdown", []byte("body")); err != nil {
		t.Fatal(err)
	}
	if store.record.RunID != "run_task13" || store.record.PolicyHash != policy.PolicyHash || !store.record.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("record=%#v", store.record)
	}
	loaded, err := sink.Get(context.Background(), key)
	if err != nil || string(loaded) != "body" {
		t.Fatalf("loaded=%q err=%v", loaded, err)
	}
}
