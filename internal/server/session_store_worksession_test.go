//go:build !nogui && !headless && !linux

package server

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// A connection that only shakes hands must leave nothing behind. This is the
// whole of User Story 1: on a real machine 99 of 100 session records were
// background agents doing exactly this, every ~15 minutes, around the clock.
func TestSessionStore_HandshakeOnlyIsNotPersisted(t *testing.T) {
	store := NewSessionStore(zap.NewNop())
	store.SetSession("s1", "claude-code", "1.0", false, false, nil)

	// It exists in memory (activity resolves the client name from here)...
	require.NotNil(t, store.GetSession("s1"))

	// ...but nothing has claimed it did any work, so nothing is persisted.
	// With no storage manager wired, EnsurePersisted must be a safe no-op.
	assert.Empty(t, store.WorkSessionID("s1"),
		"a session that never worked has no work session")

	store.RemoveSession("s1")
	assert.Zero(t, store.Count(), "a soft-closed session is not live")
}

// GetSession must hand back a COPY. The roots goroutine writes Workspace under
// the lock while callers read it; returning the live pointer is a data race.
// Run this with -race.
func TestSessionStore_GetSessionIsRaceFreeAgainstWorkspaceWrite(t *testing.T) {
	store := NewSessionStore(zap.NewNop())
	store.SetSession("s1", "claude-code", "1.0", true, false, nil)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			store.SetWorkspace("s1", "/repos/mcpproxy-go")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if info := store.GetSession("s1"); info != nil {
				_ = info.Workspace
			}
		}
	}()

	wg.Wait()
}

// A client that never declared roots must not make anything wait for them.
func TestSessionStore_WorkspaceSettledReturnsImmediatelyWithoutRoots(t *testing.T) {
	store := NewSessionStore(zap.NewNop())
	store.SetSession("s1", "codex", "1.0", false /* hasRoots */, false, nil)

	start := time.Now()
	store.WorkspaceSettled("s1", 2*time.Second)

	assert.Less(t, time.Since(start), 500*time.Millisecond,
		"a client with no roots capability must not be waited on")
}

// A client that declares roots but never answers must not block forever — the
// wait is bounded, and afterwards it is settled for good so we do not pay it
// again on every later call.
func TestSessionStore_WorkspaceSettledGivesUpAndStaysSettled(t *testing.T) {
	store := NewSessionStore(zap.NewNop())
	store.SetSession("s1", "claude-code", "1.0", true /* hasRoots */, false, nil)

	start := time.Now()
	store.WorkspaceSettled("s1", 150*time.Millisecond)
	first := time.Since(start)
	assert.GreaterOrEqual(t, first, 100*time.Millisecond, "it should actually wait")

	// Second call must be instant: we already gave up.
	start = time.Now()
	store.WorkspaceSettled("s1", 2*time.Second)
	assert.Less(t, time.Since(start), 50*time.Millisecond,
		"having given up once, we must not wait again")
}

// The roots answer arriving releases the waiter promptly.
func TestSessionStore_WorkspaceSettledUnblocksOnAnswer(t *testing.T) {
	store := NewSessionStore(zap.NewNop())
	store.SetSession("s1", "claude-code", "1.0", true, false, nil)

	go func() {
		time.Sleep(50 * time.Millisecond)
		store.SetWorkspace("s1", "/repos/mcpproxy-go")
	}()

	start := time.Now()
	store.WorkspaceSettled("s1", 5*time.Second)

	assert.Less(t, time.Since(start), 2*time.Second, "the answer must release the waiter")
	assert.Equal(t, "/repos/mcpproxy-go", store.GetSession("s1").Workspace)
}

func TestWorkspaceDisplayName(t *testing.T) {
	cases := map[string]string{
		"/Users/me/repos/mcpproxy-go":        "mcpproxy-go",
		"file:///Users/me/repos/mcpproxy-go": "mcpproxy-go",
		"":                                   "",
	}
	for in, want := range cases {
		assert.Equal(t, want, workspaceDisplayName(in))
	}
}

// Review round 3 (Spec 124): a connection pins a bounded number of work
// sessions over its life. Past the bound an id is still returned, from the
// resolver, and nothing more is kept; ids already pinned stay.
func TestSessionStore_ConnectionWorkSessionKeysAreBounded(t *testing.T) {
	for _, persisted := range []bool{true, false} {
		store := NewSessionStore(zap.NewNop())
		store.SetSession("c1", "ide", "1.0", false, false, nil)
		store.mu.Lock()
		info := store.sessions["c1"]
		info.persisted = persisted
		full := make(map[string]string, maxConnectionWorkSessionKeys)
		for i := 0; i < maxConnectionWorkSessionKeys; i++ {
			full[fmt.Sprintf("p\x00run-%d", i)] = fmt.Sprintf("ws-%d", i)
		}
		if persisted {
			info.workSessionByKey, info.workSessionID = full, "ws-0"
		} else {
			info.provisionalByKey = full
		}
		store.mu.Unlock()

		calls := 0
		resolve := func(_ *SessionInfo, _ func(string) (string, bool)) (string, string) {
			calls++
			return "ws-new", "p\x00run-new"
		}
		assert.Equal(t, "ws-new", store.WorkSessionForKey("c1", "p\x00run-new", resolve))
		assert.Equal(t, "ws-new", store.WorkSessionForKey("c1", "p\x00run-new", resolve))
		assert.Equal(t, 2, calls, "nothing was pinned, so the resolver answers each time")
		assert.Equal(t, "ws-7", store.WorkSessionForKey("c1", "p\x00run-7", resolve), "pinned ids are kept")

		store.mu.RLock()
		assert.Len(t, info.workSessionByKey, map[bool]int{true: maxConnectionWorkSessionKeys, false: 0}[persisted])
		assert.Len(t, info.provisionalByKey, map[bool]int{true: 0, false: maxConnectionWorkSessionKeys}[persisted])
		store.mu.RUnlock()
	}
}
