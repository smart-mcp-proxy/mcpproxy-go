//go:build windows

package launcher

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestSpawn_Stop_KillsGrandchildHoldingPipe reproduces F2 of the #1234
// review: `cmd.exe /c ping ...` leaves ping.exe holding the inherited
// stdout/stderr pipes. Process.Kill() on cmd.exe alone never makes the log
// pumps reach EOF, so reap() never runs, Done() never closes and Stop()
// used to block until the caller's ctx expired. Stop must instead
// terminate the whole Job so the pipes close and the child is reaped.
func TestSpawn_Stop_KillsGrandchildHoldingPipe(t *testing.T) {
	// ping.exe prints "Reply from 127.0.0.1" once per second; the launcher
	// banner echoes argv un-expanded, so it cannot false-positive on this.
	sinkCh := make(chan struct{}, 1)
	sink := newRegexDetector(`Reply from 127\.0\.0\.1`, sinkCh)

	h, err := Spawn(context.Background(), &Spec{
		Cmd:       exec.Command("cmd.exe", "/c", "ping -n 30 127.0.0.1"),
		LogSink:   sink,
		Name:      "test-ping-tree",
		StopGrace: 2 * time.Second,
	}, zap.NewNop())
	require.NoError(t, err)
	require.Greater(t, h.Pid(), 0)

	// Let cmd.exe actually spawn ping.exe before we stop it, otherwise the
	// test could pass by killing cmd.exe before the grandchild exists.
	select {
	case <-sinkCh:
	case <-time.After(5 * time.Second):
		t.Fatal("ping.exe never produced output; grandchild not running")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	err = h.Stop(stopCtx)
	assert.NoError(t, err, "Stop must not time out while a grandchild holds the pipes")
	assert.Less(t, time.Since(start), 8*time.Second, "Stop should not need the full ctx")

	select {
	case <-h.Done():
	case <-time.After(time.Second):
		t.Fatal("Done() not closed after Stop returned")
	}
	assert.Equal(t, 0, h.Pid())
}
