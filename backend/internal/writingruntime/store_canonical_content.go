package writingruntime

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// CanonicalContentRepository is the persistence boundary for canonical
// artifact bodies. writingstore.Store implements it on top of migration 097.
type CanonicalContentRepository interface {
	PutCanonicalContent(context.Context, writingstore.CanonicalContentRecord) error
	GetCanonicalContent(context.Context, string) ([]byte, error)
}

var canonicalStageKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,500}$`)

// StoreContentGateway is the production ContentGateway for the authoritative
// lane: staged baseline outputs land in writing_canonical_content, and loads
// re-verify the artifact's declared content hash. Recovery after a restart
// reads the same rows, so committed artifacts never depend on process memory.
type StoreContentGateway struct {
	repository CanonicalContentRepository
	now        func() time.Time
}

func NewStoreContentGateway(repository CanonicalContentRepository) (*StoreContentGateway, error) {
	if repository == nil {
		return nil, ErrRuntimeNotReady
	}
	return &StoreContentGateway{repository: repository, now: func() time.Time { return time.Now().UTC() }}, nil
}

// Load resolves a canonical ref back to its body and enforces the recovery
// boundary: the stored body must hash exactly to the artifact's declared
// content hash.
func (gateway *StoreContentGateway) Load(ctx context.Context, input InputArtifact) ([]byte, error) {
	if gateway == nil || gateway.repository == nil {
		return nil, ErrRuntimeNotReady
	}
	if !strings.HasPrefix(input.ContentRef, writingstore.CanonicalContentRefPrefix) {
		return nil, fmt.Errorf("%w: canonical loads require a %s ref", ErrInvalidExecutionRequest, writingstore.CanonicalContentRefPrefix)
	}
	key := strings.TrimPrefix(input.ContentRef, writingstore.CanonicalContentRefPrefix)
	body, err := gateway.repository.GetCanonicalContent(ctx, key)
	if err != nil {
		return nil, err
	}
	if contentHash(body) != input.ContentHash {
		return nil, fmt.Errorf("%w: canonical body does not match declared content hash for %s", ErrLegacyContentIntegrity, input.ArtifactID)
	}
	return body, nil
}

// Stage persists one canonical body and returns its stable ref. Replays with
// identical content are idempotent; different content under the same stage
// key fails closed in the repository.
func (gateway *StoreContentGateway) Stage(ctx context.Context, key, mediaType string, body []byte) (string, string, error) {
	if gateway == nil || gateway.repository == nil {
		return "", "", ErrRuntimeNotReady
	}
	trimmed := strings.TrimSpace(key)
	if trimmed == "" || strings.TrimSpace(mediaType) == "" || len(body) == 0 {
		return "", "", fmt.Errorf("%w: canonical stage requires key, media type, and body", ErrInvalidExecutionRequest)
	}
	if !canonicalStageKeyPattern.MatchString(trimmed) {
		return "", "", fmt.Errorf("%w: canonical stage key contains unsupported characters", ErrInvalidExecutionRequest)
	}
	hash := contentHash(body)
	if err := gateway.repository.PutCanonicalContent(ctx, writingstore.CanonicalContentRecord{
		Key: trimmed, MediaType: mediaType, Body: append([]byte(nil), body...), ContentHash: hash,
		CreatedAt: gateway.now(),
	}); err != nil {
		return "", "", fmt.Errorf("canonical stage failed: %w", err)
	}
	return writingstore.CanonicalContentRefPrefix + trimmed, hash, nil
}
