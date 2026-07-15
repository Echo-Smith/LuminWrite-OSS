package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// CacheBackend is an optional L2 cache backend (e.g., Redis).
// If not configured, the L2 cache is a no-op and only the in-memory L1 cache is used.
type CacheBackend interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration)
	Delete(ctx context.Context, key string)
}

// NoopCacheBackend is a no-op implementation of CacheBackend.
type NoopCacheBackend struct{}

func (NoopCacheBackend) Get(ctx context.Context, key string) ([]byte, bool) { return nil, false }
func (NoopCacheBackend) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
}
func (NoopCacheBackend) Delete(ctx context.Context, key string) {}

// LRUCacheBackend is an in-process L2 cache that can be used when Redis is not available.
// It's more persistent than the L1 loader cache and can be shared across goroutines.
type LRUCacheBackend struct {
	mu    sync.RWMutex
	items map[string]*lruItem
	ttl   time.Duration
	max   int
}

type lruItem struct {
	value   []byte
	expires time.Time
}

// NewLRUCacheBackend creates a new in-process LRU cache backend.
func NewLRUCacheBackend(maxItems int, ttl time.Duration) *LRUCacheBackend {
	return &LRUCacheBackend{
		items: make(map[string]*lruItem),
		ttl:   ttl,
		max:   maxItems,
	}
}

func (c *LRUCacheBackend) Get(ctx context.Context, key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	item, ok := c.items[key]
	if !ok || time.Now().After(item.expires) {
		return nil, false
	}
	return item.value, true
}

func (c *LRUCacheBackend) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Evict expired items if at capacity
	if len(c.items) >= c.max {
		for k, v := range c.items {
			if time.Now().After(v.expires) {
				delete(c.items, k)
			}
		}
		// If still at capacity, remove a random item
		if len(c.items) >= c.max {
			for k := range c.items {
				delete(c.items, k)
				break
			}
		}
	}
	c.items[key] = &lruItem{
		value:   value,
		expires: time.Now().Add(ttl),
	}
}

func (c *LRUCacheBackend) Delete(ctx context.Context, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

// ─── L2 Cache for Style Profiles ─────────────────────────

// ProfileL2Cache provides an optional L2 cache layer for style profiles.
// It uses a CacheBackend (Redis or in-process LRU) to cache serialized profiles.
type ProfileL2Cache struct {
	backend CacheBackend
	ttl     time.Duration
}

// NewProfileL2Cache creates a new L2 cache for profiles.
func NewProfileL2Cache(backend CacheBackend, ttl time.Duration) *ProfileL2Cache {
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	return &ProfileL2Cache{backend: backend, ttl: ttl}
}

// Get retrieves a profile from the L2 cache.
func (c *ProfileL2Cache) Get(ctx context.Context, slug string) (*StyleProfile, bool) {
	if c == nil || c.backend == nil {
		return nil, false
	}
	key := fmt.Sprintf("style:%s:published", slug)
	data, ok := c.backend.Get(ctx, key)
	if !ok || len(data) == 0 {
		return nil, false
	}
	var p StyleProfile
	if err := json.Unmarshal(data, &p); err != nil {
		slog.Warn("failed to unmarshal L2 cached profile", "slug", slug, "error", err)
		return nil, false
	}
	return &p, true
}

// Set stores a profile in the L2 cache.
func (c *ProfileL2Cache) Set(ctx context.Context, slug string, p *StyleProfile) {
	if c == nil || c.backend == nil || p == nil {
		return
	}
	data, err := json.Marshal(p)
	if err != nil {
		return
	}
	key := fmt.Sprintf("style:%s:published", slug)
	c.backend.Set(ctx, key, data, c.ttl)
}

// Invalidate removes a profile from the L2 cache.
func (c *ProfileL2Cache) Invalidate(ctx context.Context, slug string) {
	if c == nil || c.backend == nil {
		return
	}
	key := fmt.Sprintf("style:%s:published", slug)
	c.backend.Delete(ctx, key)
}

// IsAvailable returns true if the L2 cache backend is configured.
func (c *ProfileL2Cache) IsAvailable() bool {
	if c == nil || c.backend == nil {
		return false
	}
	_, ok := c.backend.(NoopCacheBackend)
	return !ok
}
