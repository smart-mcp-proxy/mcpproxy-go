//go:build windows

package winjob

import (
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// startPingTree starts `cmd.exe /c ping -n 30 127.0.0.1` with a stdout pipe.
// ping.exe (the grandchild) inherits the pipe's write end from cmd.exe, so
// the read side only reaches EOF once EVERY holder — cmd.exe AND ping.exe —
// has exited. That makes "did the grandchild die?" observable without PID
// discovery: kill cmd.exe alone and the pipe stays open; kill the job and
// it closes.
func startPingTree(t *testing.T) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command("cmd.exe", "/c", "ping -n 30 127.0.0.1")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	eof := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, stdout)
		close(eof)
	}()
	return cmd, eof
}

func TestJob_CloseKillsGrandchildren(t *testing.T) {
	job, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = job.Close() })

	cmd, eof := startPingTree(t)
	require.NoError(t, job.Assign(cmd.Process.Pid))

	// Kill only the immediate child. The grandchild (ping.exe) must still
	// hold the pipe open — otherwise the assertion below is vacuous.
	require.NoError(t, cmd.Process.Kill())
	select {
	case <-eof:
		t.Fatal("stdout hit EOF after killing cmd.exe alone; the grandchild did not inherit the pipe, test cannot prove anything")
	case <-time.After(1500 * time.Millisecond):
	}

	// Closing the job terminates every remaining member, including ping.exe.
	require.NoError(t, job.Close())
	select {
	case <-eof:
	case <-time.After(5 * time.Second):
		t.Fatal("grandchild survived Job.Close(): stdout pipe never reached EOF")
	}
}

func TestJob_CloseIsIdempotentAndNilSafe(t *testing.T) {
	var nilJob *Job
	require.NoError(t, nilJob.Close())

	job, err := New()
	require.NoError(t, err)
	require.NoError(t, job.Close())
	require.NoError(t, job.Close(), "second Close must be a no-op")
	require.Error(t, job.Assign(1), "Assign after Close must fail, not touch a stale handle")
}
