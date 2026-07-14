package toolsig

import (
	"sync"
	"sync/atomic"
)

// Cache memoizes compiled Signatures keyed by the indexed per-tool hash
// (ToolMetadata.Hash — the Spec-032 SHA-256 covering description + schemas,
// so definition changes and index rebuilds naturally invalidate entries by
// changing the key). Process-local, never persisted; re-warms on restart via
// the normal reindex (FR-008).
//
// Exactly ONE instance must exist per process: Runtime owns it, the indexing
// path warms it, and the MCP request path reads it — see the wiring test in
// internal/server. A read-mostly RWMutex is used instead of a channel-owned
// actor per the plan's Complexity Tracking (pure memoized derivation of an
// immutable input).
type Cache struct {
	mu sync.RWMutex
	m  map[string]Signature

	// compiles counts stored compilations (once per unique hash). Test hook
	// for FR-008: a post-index retrieve must not move it.
	compiles atomic.Int64
}

// NewCache creates an empty signature cache.
func NewCache() *Cache {
	return &Cache{m: make(map[string]Signature)}
}

// Get returns the Signature for hash, compiling (and memoizing) it from
// paramsJSON/description on a miss. Concurrent callers may compile
// redundantly under contention, but exactly one result is stored and counted.
func (c *Cache) Get(hash, paramsJSON, description string) Signature {
	c.mu.RLock()
	sig, ok := c.m[hash]
	c.mu.RUnlock()
	if ok {
		return sig
	}

	// Compile outside the lock — rendering is pure and deterministic, so a
	// lost race stores an identical value anyway.
	compiled, _ := Render(paramsJSON, description)

	c.mu.Lock()
	defer c.mu.Unlock()
	if sig, ok := c.m[hash]; ok {
		return sig
	}
	c.m[hash] = compiled
	c.compiles.Add(1)
	return compiled
}

// Warm pre-compiles the Signature for hash so later Gets are pure cache hits.
// Called from the indexing path (FR-008 "compiled at index time").
func (c *Cache) Warm(hash, paramsJSON, description string) {
	c.Get(hash, paramsJSON, description)
}

// RetainHashes reconciles the cache to the live tool set: every entry whose
// hash is NOT in live is evicted, and the number of evictions is returned.
// The indexing path calls this after index rebuilds/differential updates so
// stale hashes (removed or redefined tools) do not accumulate for the life of
// the process — Warm only ever adds. A nil/empty live set clears the cache.
// Safe for concurrent use with Get/Warm: eviction holds the write lock, and a
// racing Get for an evicted hash simply recompiles (rendering is pure).
func (c *Cache) RetainHashes(live map[string]struct{}) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	evicted := 0
	for hash := range c.m {
		if _, ok := live[hash]; !ok {
			delete(c.m, hash)
			evicted++
		}
	}
	return evicted
}

// CompileCount reports how many signatures were compiled-and-stored (one per
// unique hash). Test/observability hook — FR-008's falsifier.
func (c *Cache) CompileCount() int64 {
	return c.compiles.Load()
}

// Len reports the number of cached signatures (bounded by live tool count).
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.m)
}
