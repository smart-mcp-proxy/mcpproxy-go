//go:build windows

package winjob

import (
	"bufio"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// pingTree is a deterministic cmd.exe -> ping.exe process tree.
//
//   - The shell first runs `set /p x=`, which blocks reading one line from
//     stdin, so the caller can assign cmd.exe to a Job BEFORE ping.exe
//     exists (release() writes that line). Without the gate, ping.exe could
//     be spawned before Assign and legitimately escape the job.
//   - ping.exe inherits the stdout pipe's write end from cmd.exe, so the
//     read side only reaches EOF once EVERY holder has exited. That makes
//     "did the grandchild die?" observable without PID discovery: kill
//     cmd.exe alone and the pipe stays open; kill the job and it closes.
//   - pinging is closed once ping.exe has written a line naming the target
//     address (locale-neutral, unlike "Pinging"/"Reply from"), i.e. once
//     the grandchild provably exists and holds the pipe.
type pingTree struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	pinging <-chan struct{}
	eof     <-chan struct{}
}

func startPingTree(t *testing.T) *pingTree {
	t.Helper()
	cmd := exec.Command("cmd.exe", "/c", "set /p x=& ping -n 30 127.0.0.1")
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	pinging := make(chan struct{})
	eof := make(chan struct{})
	go func() {
		defer close(eof)
		seen := false
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if !seen && strings.Contains(sc.Text(), "127.0.0.1") {
				seen = true
				close(pinging)
			}
		}
	}()
	return &pingTree{cmd: cmd, stdin: stdin, pinging: pinging, eof: eof}
}

// release lets cmd.exe past `set /p` and waits until ping.exe is running.
func (p *pingTree) release(t *testing.T) {
	t.Helper()
	_, err := io.WriteString(p.stdin, "\r\n")
	require.NoError(t, err)
	select {
	case <-p.pinging:
	case <-time.After(10 * time.Second):
		t.Fatal("ping.exe never produced output; grandchild not running")
	}
}

func TestJob_CloseKillsGrandchildren(t *testing.T) {
	job, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = job.Close() })

	tree := startPingTree(t)
	require.NoError(t, job.Assign(tree.cmd.Process.Pid))
	tree.release(t)

	// Kill only the immediate child. The grandchild (ping.exe) must still
	// hold the pipe open — otherwise the assertion below is vacuous.
	require.NoError(t, tree.cmd.Process.Kill())
	select {
	case <-tree.eof:
		t.Fatal("stdout hit EOF after killing cmd.exe alone; the grandchild did not inherit the pipe, test cannot prove anything")
	case <-time.After(1500 * time.Millisecond):
	}

	// Closing the job terminates every remaining member, including ping.exe.
	require.NoError(t, job.Close())
	select {
	case <-tree.eof:
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
