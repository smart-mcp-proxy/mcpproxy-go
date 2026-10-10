package managed

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// UX-01 r8 (finding 4): retirement must be synchronized with the actual launch.
// Connect passes its last retired check, then retirement completes (Retire +
// Disconnect, as Manager.RemoveServer / same-name replacement do), then the
// dial resumes: the stdio command must never execute.
func TestRetire_AfterFinalPreDialCheck_NeverLaunchesStdioCommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "launched")
	cfg := &config.ServerConfig{
		Name: "late", Command: "/bin/sh", Protocol: "stdio", Enabled: true,
		Args: []string{"-c", "touch " + marker + "; exec cat"},
	}
	mc, err := NewClient(cfg.Name, cfg, zap.NewNop(), nil, &config.Config{}, nil, secret.NewResolver())
	require.NoError(t, err)

	reached := make(chan struct{})
	resume := make(chan struct{})
	// Park at the last instant before the child would start: after Connect's
	// retired check and after every context check on the way to the spawn.
	core.BeforeLaunchHook = func(name string) {
		if name != cfg.Name {
			return
		}
		close(reached)
		<-resume
	}
	var launches atomic.Int32
	core.AfterLaunchHook = func(string) { launches.Add(1) }
	t.Cleanup(func() { core.BeforeLaunchHook = nil; core.AfterLaunchHook = nil })

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		done <- mc.Connect(ctx)
	}()
	<-reached

	// Manager.RemoveServer order: retire, then disconnect. Disconnect waits for
	// the in-flight core Connect (it holds the core client lock), so it runs
	// beside the resumed dial.
	mc.Retire()
	disconnected := make(chan error, 1)
	go func() { disconnected <- mc.Disconnect() }()
	close(resume)
	require.NoError(t, <-disconnected)

	select {
	case err := <-done:
		require.ErrorIs(t, err, ErrClientRetired)
	case <-time.After(15 * time.Second):
		t.Fatal("Connect never returned")
	}
	time.Sleep(700 * time.Millisecond) // let a wrongly spawned shell reach its touch
	require.Zero(t, launches.Load(), "a retired client's command must never be spawned")
	require.NoFileExists(t, marker, "a retired client's command must never launch")
	require.NotEqual(t, types.StateReady, mc.GetState())
	require.False(t, mc.IsConnected())
}
