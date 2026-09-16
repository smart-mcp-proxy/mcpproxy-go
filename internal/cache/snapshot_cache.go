package cache

import (
	"container/list"
	"crypto/sha256"
	"sync"
)

// snapshotCache is a small LRU of decoded producer snapshots keyed by content
// hash. It exists so the gated read's same-kind containment check decodes a
// given snapshot ONCE: after that first load every probe of any entry stamped
// with it costs a map lookup, however many servers the snapshot names, so the
// work of a refusal is independent of both payload and fleet size after
// warm-up (Spec 105 Definitions, "non-disclosing refusal"; codex round 4).
// Values are content-addressed and immutable: callers never mutate what they
// get back.
type snapshotCache struct {
	mu    sync.Mutex
	max   int
	order *list.List
	items map[[sha256.Size]byte]*list.Element
}

type snapshotEntry struct {
	hash  [sha256.Size]byte
	value *Authorization
}

func newSnapshotCache(max int) *snapshotCache {
	return &snapshotCache{max: max, order: list.New(), items: make(map[[sha256.Size]byte]*list.Element, max)}
}

func (c *snapshotCache) get(hash [sha256.Size]byte) (*Authorization, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[hash]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*snapshotEntry).value, true
}

func (c *snapshotCache) put(hash [sha256.Size]byte, value *Authorization) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[hash]; ok {
		c.order.MoveToFront(el)
		return
	}
	c.items[hash] = c.order.PushFront(&snapshotEntry{hash: hash, value: value})
	for c.order.Len() > c.max {
		last := c.order.Back()
		c.order.Remove(last)
		delete(c.items, last.Value.(*snapshotEntry).hash)
	}
}
