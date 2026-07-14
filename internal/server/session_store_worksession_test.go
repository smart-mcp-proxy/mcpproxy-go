//go:build !nogui && !headless && !linux

package server

import (
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
	assert.Nil(t, store.GetSession("s1"))
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
