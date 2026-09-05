// Canonical ContentGateway for the governed runtime (V3.0 M0b-2c). The
// baseline lane stages/loads governed artifact bodies through this gateway
// into the content-addressed writing_artifact_contents store (migration 104).
// It is the canonical counterpart to ShadowContentGateway: same ContentGateway
// contract, but durable, content-addressed, and not run/expiry scoped.
package writingruntime

import (
	"context"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// WritingStoreContentGateway implements ContentGateway over *writingstore.Store.
type WritingStoreContentGateway struct {
	Store *writingstore.Store
}

// Load reads an input artifact's bytes by its content hash.
func (gateway WritingStoreContentGateway) Load(ctx context.Context, artifact InputArtifact) ([]byte, error) {
	_, body, err := gateway.Store.GetArtifactContent(ctx, artifact.ContentHash)
	return body, err
}

// Stage persists an output body content-addressed and returns the canonical
// reference and hash. Re-staging identical bytes is idempotent.
func (gateway WritingStoreContentGateway) Stage(ctx context.Context, _, mediaType string, body []byte) (string, string, error) {
	hash := contentHash(body)
	if err := gateway.Store.PutArtifactContent(ctx, hash, mediaType, body); err != nil {
		return "", "", err
	}
	return "artifact://" + hash, hash, nil
}
