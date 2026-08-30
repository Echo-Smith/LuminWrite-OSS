package writingruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

type ShadowContentRepository interface {
	PutShadowContent(context.Context, writingstore.ShadowContentRecord) error
	GetShadowContent(context.Context, string) ([]byte, error)
	DeleteShadowContentPrefix(context.Context, string) (int, error)
	DeleteShadowContentBefore(context.Context, time.Time) (int, error)
}

type StoreShadowContent struct {
	repository ShadowContentRepository
	ttl        time.Duration
	now        func() time.Time
}

func NewStoreShadowContent(repository ShadowContentRepository, ttl time.Duration) (*StoreShadowContent, error) {
	if repository == nil {
		return nil, ErrRuntimeNotReady
	}
	if ttl <= 0 {
		ttl = DefaultShadowContentTTL
	}
	return &StoreShadowContent{repository: repository, ttl: ttl, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (store *StoreShadowContent) Put(ctx context.Context, key, mediaType string, body []byte) error {
	if store == nil || store.repository == nil {
		return ErrRuntimeNotReady
	}
	parts := strings.Split(key, "/")
	if len(parts) != 3 || len(parts[0]) != 64 || len(parts[2]) != 64 {
		return fmt.Errorf("%w: invalid shadow content key", ErrInvalidExecutionRequest)
	}
	separator := strings.Index(parts[1], "-node_")
	if separator < 0 {
		separator = strings.Index(parts[1], "-")
	}
	if separator <= len("run_") {
		return fmt.Errorf("%w: shadow key has no run boundary", ErrInvalidExecutionRequest)
	}
	runID := parts[1][:separator]
	now := store.now()
	return store.repository.PutShadowContent(ctx, writingstore.ShadowContentRecord{
		Key: key, PolicyHash: "sha256:" + parts[0], RunID: runID, MediaType: mediaType,
		Body: append([]byte(nil), body...), ContentHash: contentHash(body),
		CreatedAt: now, ExpiresAt: now.Add(store.ttl),
	})
}

func (store *StoreShadowContent) Get(ctx context.Context, key string) ([]byte, error) {
	if store == nil || store.repository == nil {
		return nil, ErrRuntimeNotReady
	}
	return store.repository.GetShadowContent(ctx, key)
}

func (store *StoreShadowContent) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	if store == nil || store.repository == nil {
		return 0, ErrRuntimeNotReady
	}
	return store.repository.DeleteShadowContentPrefix(ctx, prefix)
}

func (store *StoreShadowContent) DeleteBefore(ctx context.Context, cutoff time.Time) (int, error) {
	if store == nil || store.repository == nil {
		return 0, ErrRuntimeNotReady
	}
	return store.repository.DeleteShadowContentBefore(ctx, cutoff)
}
