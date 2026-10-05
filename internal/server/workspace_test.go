package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

func TestFetchRootsWithRetry_StopsWhenSessionSoftClosed(t *testing.T) {
	store := NewSessionStore(zap.NewNop())
	store.SetSession("s1", "client", "1", true, false, nil)

	calls := 0
	req := func(context.Context) (*mcp.ListRootsResult, error) {
		calls++
		store.RemoveSession("s1") // client disconnects after the first attempt
		return nil, errors.New("no roots")
	}

	start := time.Now()
	_, err := fetchRootsWithRetry(context.Background(), store, "s1", 5*time.Millisecond, req)
	if err == nil {
		t.Fatal("expected the last error to be returned")
	}
	if calls != 1 {
		t.Fatalf("RequestRoots calls = %d, want 1 (no fetch for a closed session)", calls)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("did not return promptly: %v", time.Since(start))
	}
}

func TestFetchRootsWithRetry_ReopenedSessionKeepsFetching(t *testing.T) {
	store := NewSessionStore(zap.NewNop())
	store.SetSession("s1", "client", "1", true, false, nil)
	store.RemoveSession("s1")
	store.Reopen("s1")

	calls := 0
	req := func(context.Context) (*mcp.ListRootsResult, error) {
		calls++
		return nil, errors.New("not yet")
	}
	_, _ = fetchRootsWithRetry(context.Background(), store, "s1", time.Millisecond, req)
	if calls != workspaceFetchAttempts {
		t.Fatalf("calls = %d, want %d", calls, workspaceFetchAttempts)
	}
}
