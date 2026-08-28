package toolsig

import "testing"

// Spec 102 T011: Peek is the read path the deferred direct listing needs and
// the cache does not expose today.
//
// Get compiles and memoizes on a miss, which would violate FR-005's "served
// from the index-time cache — no per-request compilation" on every cache miss,
// and would make the miss invisible: the caller cannot distinguish "warm" from
// "just compiled for you", so it can never render the signature-less entry
// FR-004 requires. Peek reports the miss instead.

const (
	peekParams = `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`
	peekDesc   = "Read a file"
)

func TestPeek_WarmHashHits(t *testing.T) {
	c := NewCache()
	c.Warm("h1", peekParams, peekDesc)

	sig, ok := c.Peek("h1")
	if !ok {
		t.Fatal("a warmed hash must hit")
	}
	if want := c.Get("h1", peekParams, peekDesc); sig != want {
		t.Errorf("Peek must return the same Signature as Get: got %+v want %+v", sig, want)
	}
}

func TestPeek_MissIsReportedAndDoesNotCompile(t *testing.T) {
	c := NewCache()

	before := c.CompileCount()
	sig, ok := c.Peek("never-warmed")
	if ok {
		t.Error("an unwarmed hash must report a miss")
	}
	if sig != (Signature{}) {
		t.Errorf("a miss must return the zero Signature, got %+v", sig)
	}

	// The two assertions that make this different from Get. A Peek that
	// silently compiled would satisfy the caller and break FR-005 invisibly.
	if after := c.CompileCount(); after != before {
		t.Errorf("Peek must not compile: CompileCount %d -> %d", before, after)
	}
	if c.Len() != 0 {
		t.Errorf("Peek must not memoize: cache grew to %d entries", c.Len())
	}
}

// TestPeek_DoesNotResurrectEvictedHashes pins Peek against the same eviction
// rule Get follows: after RetainHashes drops a hash, it is gone.
func TestPeek_DoesNotResurrectEvictedHashes(t *testing.T) {
	c := NewCache()
	c.Warm("h1", peekParams, peekDesc)
	c.RetainHashes(map[string]struct{}{}) // evict everything

	if _, ok := c.Peek("h1"); ok {
		t.Error("an evicted hash must miss")
	}
	if c.Len() != 0 {
		t.Errorf("Peek must not repopulate after eviction; got %d entries", c.Len())
	}
}
