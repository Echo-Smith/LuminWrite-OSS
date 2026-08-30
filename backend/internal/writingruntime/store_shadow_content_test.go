package writingruntime

import (
	"context"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

type shadowRepositoryStub struct {
	record writingstore.ShadowContentRecord
}

func (stub *shadowRepositoryStub) PutShadowContent(_ context.Context, record writingstore.ShadowContentRecord) error {
	stub.record = record
	return nil
}
func (*shadowRepositoryStub) GetShadowContent(context.Context, string) ([]byte, error) {
	return []byte("body"), nil
}
func (*shadowRepositoryStub) DeleteShadowContentPrefix(context.Context, string) (int, error) {
	return 1, nil
}
func (*shadowRepositoryStub) DeleteShadowContentBefore(context.Context, time.Time) (int, error) {
	return 1, nil
}

func TestStoreShadowContentMapsIsolatedIdentityAndTTL(t *testing.T) {
	repository := &shadowRepositoryStub{}
	store, err := NewStoreShadowContent(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 30, 5, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	policy := hashForTest("policy")[len("sha256:"):]
	body := []byte("candidate")
	key := policy + "/run_shadow-node_write-1-draft/" + contentHash(body)[len("sha256:"):]
	if err := store.Put(context.Background(), key, "text/markdown", body); err != nil {
		t.Fatal(err)
	}
	record := repository.record
	if record.RunID != "run_shadow" || record.PolicyHash != "sha256:"+policy ||
		record.ContentHash != contentHash(body) || !record.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("record=%#v", record)
	}
}
