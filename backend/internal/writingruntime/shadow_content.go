package writingruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ShadowContentScheme marks staged content that lives outside the canonical
// namespace. Refs with this scheme can never be committed to canonical
// artifacts: the orchestrator rejects them even if a miswired adapter leaks
// one into a result.
const ShadowContentScheme = "shadow://"

// DefaultShadowContentTTL bounds how long shadow bodies may outlive the run
// that produced them. Candidate-authoritative rollout must not be enabled
// while shadow content is still being swept.
const DefaultShadowContentTTL = 7 * 24 * time.Hour

// ErrShadowContentLeak guards the canonical commit boundary.
var ErrShadowContentLeak = errors.New("writingruntime: shadow content reference cannot enter canonical artifacts")

// IsShadowContentRef reports whether a content ref belongs to the shadow
// namespace. Canonical refs never carry the scheme, so the check is exact.
func IsShadowContentRef(ref string) bool {
	return strings.HasPrefix(ref, ShadowContentScheme)
}

// ShadowContentSink is the isolated write side for shadow-lane bodies. It is
// deliberately separate from ContentGateway: shadow staging must never share
// storage with canonical artifact content, and the sink owns cleanup so
// orphaned shadow bodies can be swept without touching canonical data.
type ShadowContentSink interface {
	Put(ctx context.Context, key, mediaType string, body []byte) error
	DeletePrefix(ctx context.Context, prefix string) (int, error)
	DeleteBefore(ctx context.Context, cutoff time.Time) (int, error)
}

// ShadowContentGateway gives a shadow-lane candidate adapter the same
// immutable input snapshots as the baseline while forcing every staged output
// into the shadow:// namespace of one policy version.
//
// Ref and sink key share one path layout:
//
//	<policy-hash>/<run-scoped stage key>/<content hash>
//
// so refs stay content-addressed, sink keys stay resolvable, and per-run
// cleanup is a prefix delete.
type ShadowContentGateway struct {
	reads      ContentGateway
	writes     ShadowContentSink
	policyHash string
	ttl        time.Duration
	now        func() time.Time
}

func NewShadowContentGateway(reads ContentGateway, writes ShadowContentSink, policy AdapterRolloutPolicy) (*ShadowContentGateway, error) {
	if reads == nil || writes == nil {
		return nil, ErrRuntimeNotReady
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	hash := strings.TrimPrefix(policy.PolicyHash, "sha256:")
	if hash == "" || hash == policy.PolicyHash {
		return nil, rolloutPolicyError("shadow namespace requires a computed policy hash")
	}
	return &ShadowContentGateway{reads: reads, writes: writes, policyHash: hash,
		ttl: DefaultShadowContentTTL, now: func() time.Time { return time.Now().UTC() }}, nil
}

// WithShadowContentTTL overrides the sweep age for local runs and tests.
func (gateway *ShadowContentGateway) WithShadowContentTTL(ttl time.Duration) *ShadowContentGateway {
	if ttl > 0 {
		gateway.ttl = ttl
	}
	return gateway
}

// Load reads through to the canonical input snapshots. Shadow candidates must
// consume the same immutable inputs as the baseline; only staging diverges.
func (gateway *ShadowContentGateway) Load(ctx context.Context, input InputArtifact) ([]byte, error) {
	return gateway.reads.Load(ctx, input)
}

func (gateway *ShadowContentGateway) Stage(ctx context.Context, key, mediaType string, body []byte) (string, string, error) {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" || strings.TrimSpace(mediaType) == "" || len(body) == 0 {
		return "", "", fmt.Errorf("%w: shadow stage requires key, media type, and body", ErrInvalidExecutionRequest)
	}
	hash := contentHash(body)
	path := gateway.policyHash + "/" + shadowPathSegment(trimmed) + "/" + strings.TrimPrefix(hash, "sha256:")
	if err := gateway.writes.Put(ctx, path, mediaType, body); err != nil {
		return "", "", fmt.Errorf("shadow content stage failed: %w", err)
	}
	return ShadowContentScheme + path, hash, nil
}

// SweepExpired removes shadow bodies older than the TTL.
func (gateway *ShadowContentGateway) SweepExpired(ctx context.Context) (int, error) {
	return gateway.writes.DeleteBefore(ctx, gateway.now().Add(-gateway.ttl))
}

// PurgeRun removes every shadow body staged for one run, including orphans
// whose run never reached a canonical commit. The separator is explicit so a
// run id that prefixes another (run_fresh vs run_fresh2) cannot purge its
// neighbour: sanitized stage keys always continue with "-" after the run id,
// which holds because a ":" inside a run id would already be rejected here.
func (gateway *ShadowContentGateway) PurgeRun(ctx context.Context, runID string) (int, error) {
	if !strings.HasPrefix(runID, "run_") || strings.ContainsAny(runID, "/: \t") {
		return 0, fmt.Errorf("%w: shadow purge requires a valid run id", ErrInvalidExecutionRequest)
	}
	return gateway.writes.DeletePrefix(ctx, gateway.policyHash+"/"+runID+"-")
}

// PolicyHash reports the full policy hash this gateway stages into. Rollout
// executors compare it against the live policy so a rotated policy can never
// stage content into a stale namespace.
func (gateway *ShadowContentGateway) PolicyHash() string {
	return "sha256:" + gateway.policyHash
}

// LoadShadow reads a body back from this gateway's own shadow namespace by
// ref. Reading a canonical ref, a foreign policy namespace, or a sink without
// read support fails closed.
func (gateway *ShadowContentGateway) LoadShadow(ctx context.Context, ref string) ([]byte, error) {
	if !IsShadowContentRef(ref) {
		return nil, fmt.Errorf("%w: shadow reads require a shadow content ref", ErrInvalidExecutionRequest)
	}
	key := strings.TrimPrefix(ref, ShadowContentScheme)
	if !strings.HasPrefix(key, gateway.policyHash+"/") {
		return nil, fmt.Errorf("%w: shadow ref belongs to a different policy namespace", ErrInvalidExecutionRequest)
	}
	reader, ok := gateway.writes.(ShadowContentReader)
	if !ok {
		return nil, fmt.Errorf("%w: shadow sink does not support reads", ErrInvalidExecutionRequest)
	}
	return reader.Get(ctx, key)
}

func shadowPathSegment(key string) string {
	mapped := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		default:
			return '-'
		}
	}, key)
	for strings.Contains(mapped, "..") {
		mapped = strings.ReplaceAll(mapped, "..", "-")
	}
	return mapped
}

func containsShadowContentRef(artifacts []OutputArtifactDraft) bool {
	for _, artifact := range artifacts {
		if IsShadowContentRef(artifact.ContentRef) {
			return true
		}
	}
	return false
}

// ShadowContentReader is an optional sink capability: reading shadow bodies
// back by key, used to lift validator summaries out of quality reports.
type ShadowContentReader interface {
	Get(ctx context.Context, key string) ([]byte, error)
}

type memoryShadowEntry struct {
	mediaType string
	body      []byte
	storedAt  time.Time
}

// MemoryShadowContentSink is the local-shadow storage used by contract tests
// and local runs. Production wiring must provide a durable, separately
// provisioned sink before any candidate-authoritative traffic.
type MemoryShadowContentSink struct {
	mu      sync.Mutex
	entries map[string]memoryShadowEntry
	clock   func() time.Time
}

func NewMemoryShadowContentSink() *MemoryShadowContentSink {
	return &MemoryShadowContentSink{entries: map[string]memoryShadowEntry{}}
}

func (sink *MemoryShadowContentSink) Put(_ context.Context, key, mediaType string, body []byte) error {
	if sink == nil {
		return ErrRuntimeNotReady
	}
	now := time.Now().UTC
	if sink.clock != nil {
		now = sink.clock
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.entries[key] = memoryShadowEntry{mediaType: mediaType, body: append([]byte(nil), body...),
		storedAt: now()}
	return nil
}

func (sink *MemoryShadowContentSink) DeletePrefix(_ context.Context, prefix string) (int, error) {
	if sink == nil {
		return 0, ErrRuntimeNotReady
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	removed := 0
	for key := range sink.entries {
		if strings.HasPrefix(key, prefix) {
			delete(sink.entries, key)
			removed++
		}
	}
	return removed, nil
}

func (sink *MemoryShadowContentSink) DeleteBefore(_ context.Context, cutoff time.Time) (int, error) {
	if sink == nil {
		return 0, ErrRuntimeNotReady
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	removed := 0
	for key, entry := range sink.entries {
		if entry.storedAt.Before(cutoff) {
			delete(sink.entries, key)
			removed++
		}
	}
	return removed, nil
}

func (sink *MemoryShadowContentSink) Get(_ context.Context, key string) ([]byte, error) {
	if sink == nil {
		return nil, ErrRuntimeNotReady
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	entry, ok := sink.entries[key]
	if !ok {
		return nil, ErrRuntimeNotReady
	}
	return append([]byte(nil), entry.body...), nil
}

func (sink *MemoryShadowContentSink) Keys() []string {
	if sink == nil {
		return nil
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	keys := make([]string, 0, len(sink.entries))
	for key := range sink.entries {
		keys = append(keys, key)
	}
	return keys
}
