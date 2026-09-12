package writingruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

type ShadowContentStore interface {
	PutShadowContent(context.Context, writingstore.ShadowContentRecord) error
	GetShadowContent(context.Context, string) (writingstore.ShadowContentRecord, error)
	DeleteShadowContentPrefix(context.Context, string) (int, error)
	DeleteShadowContentBefore(context.Context, time.Time) (int, error)
}

// WritingStoreShadowContentSink is the production PostgreSQL implementation
// of the isolated shadow content contracts. It deliberately exposes no
// canonical Artifact/Document write method.
type WritingStoreShadowContentSink struct {
	Store ShadowContentStore
	TTL   time.Duration
	Now   func() time.Time
}

func (sink WritingStoreShadowContentSink) Put(ctx context.Context, key, mediaType string, body []byte) error {
	if sink.Store == nil {
		return ErrRuntimeNotReady
	}
	policyHash, runID, err := parseShadowContentKey(key)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if sink.Now != nil {
		now = sink.Now().UTC()
	}
	ttl := sink.TTL
	if ttl <= 0 {
		ttl = DefaultShadowContentTTL
	}
	sum := sha256.Sum256(body)
	return sink.Store.PutShadowContent(ctx, writingstore.ShadowContentRecord{
		ContentKey: key, PolicyHash: policyHash, RunID: runID, MediaType: mediaType,
		ContentHash: "sha256:" + hex.EncodeToString(sum[:]), Body: append([]byte(nil), body...),
		StoredAt: now, ExpiresAt: now.Add(ttl),
	})
}

func (sink WritingStoreShadowContentSink) Get(ctx context.Context, key string) ([]byte, error) {
	if sink.Store == nil {
		return nil, ErrRuntimeNotReady
	}
	record, err := sink.Store.GetShadowContent(ctx, key)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), record.Body...), nil
}

func (sink WritingStoreShadowContentSink) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	if sink.Store == nil {
		return 0, ErrRuntimeNotReady
	}
	return sink.Store.DeleteShadowContentPrefix(ctx, prefix)
}

func (sink WritingStoreShadowContentSink) DeleteBefore(ctx context.Context, cutoff time.Time) (int, error) {
	if sink.Store == nil {
		return 0, ErrRuntimeNotReady
	}
	return sink.Store.DeleteShadowContentBefore(ctx, cutoff)
}

func parseShadowContentKey(key string) (string, string, error) {
	parts := strings.Split(key, "/")
	if len(parts) < 3 || len(parts[0]) != 64 || !strings.HasPrefix(parts[1], "run_") {
		return "", "", fmt.Errorf("%w: invalid persistent shadow key", ErrInvalidExecutionRequest)
	}
	runID := parts[1]
	if index := strings.Index(runID, "-node_"); index > len("run_") {
		runID = runID[:index]
	}
	return "sha256:" + parts[0], runID, nil
}

var _ ShadowContentSink = WritingStoreShadowContentSink{}
var _ ShadowContentReader = WritingStoreShadowContentSink{}
