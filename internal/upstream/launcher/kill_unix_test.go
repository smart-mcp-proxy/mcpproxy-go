//go:build unix

package launcher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Sol review round 2 (Spec 116 PR4): a launcher that backgrounds a
// long-lived descendant with redirected streams and exits closes Done (the
// pumps reach EOF and the leader is reaped) while the descendant still runs
// in the launcher's process group. The retirement kill must still reach it.
func TestHandleKill_AfterLeaderExit_KillsDescendant(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "descendant.pid")
	cmd := exec.Command("/bin/sh", "-c",
		`sleep 300 </dev/null >/dev/null 2>&1 & echo $! > "`+pidFile+`"`)
	h, err := Spawn(context.Background(), &Spec{Cmd: cmd, Name: "bg-launcher"}, zap.NewNop())
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the launcher leader did not exit")
	}
	pid := readPid(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	require.NoError(t, syscall.Kill(pid, 0), "control: the descendant outlives the leader")

	require.NoError(t, h.Kill())
	require.Eventually(t, func() bool { return syscall.Kill(pid, 0) == syscall.ESRCH },
		5*time.Second, 20*time.Millisecond,
		"Kill after the leader exited must reach the descendant left in its process group")
}

// A process group ID is handed out again once the group is empty. After the
// leader was reaped and its group is gone, Kill must not signal a group that
// now belongs to an unrelated process which reused the number (review round
// 2). A live process holding the number as its PID proves the reuse: while
// any member of our group lives, the kernel does not allocate that number.
func TestHandleKill_AfterGroupGone_DoesNotSignalReusedID(t *testing.T) {
	h, err := Spawn(context.Background(), &Spec{Cmd: exec.Command("/bin/sh", "-c", "exit 0"), Name: "short"}, zap.NewNop())
	require.NoError(t, err)
	<-h.Done()
	hh := h.(*handle)
	require.Positive(t, hh.pgid, "the group is recorded at spawn")

	// Simulate the reuse: point the recorded group at a live, unrelated
	// group whose leader holds that number as its PID.
	other := exec.Command("sleep", "30")
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, other.Start())
	exited := make(chan struct{})
	go func() { _ = other.Wait(); close(exited) }()
	t.Cleanup(func() { _ = other.Process.Kill(); <-exited })
	hh.pgid = other.Process.Pid

	require.NoError(t, h.Kill())
	select {
	case <-exited:
		t.Fatal("an unrelated group with a reused ID was signalled")
	case <-time.After(300 * time.Millisecond):
	}
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	var raw []byte
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(path)
		raw = b
		return err == nil && strings.TrimSpace(string(b)) != ""
	}, 5*time.Second, 10*time.Millisecond)
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	return pid
}
