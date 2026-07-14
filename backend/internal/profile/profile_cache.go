package profile

import (
	"container/list"
	"fmt"
	"sync"
	"sync/atomic"
)

// profileLRUCache is a thread-safe LRU cache for style profiles.
// It is used to cache fallback version profiles loaded from the database,
// avoiding repeated DB queries for the same slug+version combination.
type profileLRUCache struct {
	maxEntries int
	mu         sync.Mutex
	ll         *list.List
	items      map[string]*list.Element
	hits       atomic.Int64
	misses     atomic.Int64
}

// cacheEntry holds the key and value for an LRU entry.
type cacheEntry struct {
	key   string
	value *StyleProfile
}

// newProfileLRUCache creates a new LRU cache with the given max size.
func newProfileLRUCache(maxEntries int) *profileLRUCache {
	if maxEntries <= 0 {
		maxEntries = 64
	}
	return &profileLRUCache{
		maxEntries: maxEntries,
		ll:         list.New(),
		items:      make(map[string]*list.Element),
	}
}

// cacheKey builds a composite key from slug and version.
func cacheKey(slug string, version int) string {
	return fmt.Sprintf("%s:%d", slug, version)
}

// Get returns the cached profile for the given slug+version, or nil if not found.
func (c *profileLRUCache) Get(slug string, version int) (*StyleProfile, bool) {
	key := cacheKey(slug, version)

	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		c.ll.MoveToFront(elem)
		c.hits.Add(1)
		return elem.Value.(*cacheEntry).value, true
	}

	c.misses.Add(1)
	return nil, false
}

// Put adds a profile to the cache, evicting the least recently used entry if needed.
func (c *profileLRUCache) Put(slug string, version int, profile *StyleProfile) {
	key := cacheKey(slug, version)

	c.mu.Lock()
	defer c.mu.Unlock()

	// If already exists, update and move to front
	if elem, ok := c.items[key]; ok {
		elem.Value.(*cacheEntry).value = profile
		c.ll.MoveToFront(elem)
		return
	}

	// Add new entry
	elem := c.ll.PushFront(&cacheEntry{key: key, value: profile})
	c.items[key] = elem

	// Evict LRU if over capacity
	if c.ll.Len() > c.maxEntries {
		oldest := c.ll.Back()
		if oldest != nil {
			c.ll.Remove(oldest)
			delete(c.items, oldest.Value.(*cacheEntry).key)
		}
	}
}

// Invalidate removes a specific slug+version from the cache.
func (c *profileLRUCache) Invalidate(slug string, version int) {
	key := cacheKey(slug, version)

	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		c.ll.Remove(elem)
		delete(c.items, key)
	}
}

// InvalidateSlug removes all entries for a given slug (all versions).
func (c *profileLRUCache) InvalidateSlug(slug string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Collect keys to remove (can't delete while iterating map directly in Go safely,
	// but we can iterate and delete since we're not using the map after)
	for key, elem := range c.items {
		if entry, ok := elem.Value.(*cacheEntry); ok && entry.key != "" {
			// Check if this entry's slug matches
			// Key format is "slug:version", so we check prefix
			if entry.key == cacheKey(slug, 0) || hasSlugPrefix(entry.key, slug) {
				c.ll.Remove(elem)
				delete(c.items, key)
			}
		}
	}
}

// hasSlugPrefix checks if the cache key starts with "slug:".
func hasSlugPrefix(key, slug string) bool {
	prefix := slug + ":"
	if len(key) <= len(prefix) {
		return false
	}
	return key[:len(prefix)] == prefix
}

// Clear removes all entries from the cache.
func (c *profileLRUCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ll = list.New()
	c.items = make(map[string]*list.Element)
}

// Stats returns cache hit and miss counts.
func (c *profileLRUCache) Stats() (hits, misses int64) {
	return c.hits.Load(), c.misses.Load()
}

// Len returns the current number of cached entries.
func (c *profileLRUCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
