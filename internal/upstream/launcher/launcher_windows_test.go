//go:build windows

package launcher

import (
	"context"
	"io"
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
	// cmd.exe blocks on `set /p` (one line from stdin) until we release it
	// below, so Spawn's createJob has attached the Job BEFORE ping.exe is
	// spawned — otherwise the grandchild could legitimately escape the job.
	cmd := exec.Command("cmd.exe", "/c", "set /p x=& ping -n 30 127.0.0.1")
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)

	// A reply line looks like "Reply from 127.0.0.1: bytes=32 ..."; the
	// "127.0.0.1:" form is locale-neutral and cannot match the launcher's
	// startup banner, which echoes argv as "... 127.0.0.1]".
	sinkCh := make(chan struct{}, 1)
	sink := newRegexDetector(`127\.0\.0\.1:`, sinkCh)

	h, err := Spawn(context.Background(), &Spec{
		Cmd:       cmd,
		LogSink:   sink,
		Name:      "test-ping-tree",
		StopGrace: 2 * time.Second,
	}, zap.NewNop())
	require.NoError(t, err)
	require.Greater(t, h.Pid(), 0)

	_, err = io.WriteString(stdin, "\r\n")
	require.NoError(t, err)
	select {
	case <-sinkCh:
	case <-time.After(10 * time.Second):
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
