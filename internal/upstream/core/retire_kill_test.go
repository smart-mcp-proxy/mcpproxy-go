//go:build unix

package core

import (
	"context"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/launcher"
)

// Spec 116 PR4 round-1 review fixes: the retirement kill reaches the whole
// tree, and a launcher child stays tracked until it is reaped.

// treeScript starts a shell that leaves a descendant running. Both keep dir
// in their command lines (no exec; the trailing `:` stops sh from exec'ing
// its last command) and the descendant creates dir/child-<pid>. ignoreTerm
// makes both ignore SIGTERM, so only a SIGKILL ends them.
func treeScript(dir string, ignoreTerm bool) string {
	trap := ""
	if ignoreTerm {
		trap = "trap '' TERM; "
	}
	return trap + `/bin/sh -c 'touch "` + dir + `/child-$$"; sleep 30; :' & wait; : "` + dir + `"`
}

func waitDescendant(t *testing.T, dir string) {
	t.Helper()
	require.Eventually(t, func() bool {
		m, _ := filepath.Glob(filepath.Join(dir, "child-*"))
		return len(m) > 0
	}, 5*time.Second, 10*time.Millisecond, "the descendant started")
}

// treeAlive reports whether a live (non-zombie) process has dir in its
// command line.
func treeAlive(t *testing.T, dir string) bool {
	t.Helper()
	out, err := exec.Command("ps", "-axo", "stat=,command=").Output()
	require.NoError(t, err)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Z") || strings.Contains(line, "ps -axo") {
			continue
		}
		if strings.Contains(line, dir) {
			return true
		}
	}
	return false
}

// A stdio-style child registered with the gate: Retire alone (no teardown)
// kills its descendant too. A kill of the immediate child only fails this.
func TestRetire_StdioKillReachesDescendants(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("/bin/sh", "-c", treeScript(dir, false))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
	waitDescendant(t, dir)

	g := dialgate.New()
	pgid := extractProcessGroupID(cmd, zap.NewNop(), "tree")
	require.Equal(t, cmd.Process.Pid, pgid)
	_, err := g.TrackProcess(func() { killProcessTreeNow(pgid, cmd) })
	require.NoError(t, err)

	g.Retire()
	require.Eventually(t, func() bool { return !treeAlive(t, dir) }, 2*time.Second, 20*time.Millisecond,
		"Retire kills the child's descendants, not only the child")
}

// A child that died before its group was read (retirement cancelled the gate
// context during the launch section) still has its group killed: the group
// it led is known from how it was started.
func TestRetire_StartedProcessGroupFallback(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	assert.Equal(t, cmd.Process.Pid, startedProcessGroupID(cmd))
	assert.Zero(t, startedProcessGroupID(exec.Command("true")))
}

// The launcher's retirement kill goes through its handle (process group on
// Unix, the launcher's own Job on Windows) and reaches descendants.
func TestRetire_LauncherKillReachesDescendants(t *testing.T) {
	dir := t.TempDir()
	h, err := launcher.Spawn(context.Background(), &launcher.Spec{
		Cmd:  exec.Command("/bin/sh", "-c", treeScript(dir, true)),
		Name: "tree",
	}, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = h.Stop(ctx)
	})
	waitDescendant(t, dir)

	g := dialgate.New()
	_, err = g.TrackProcess(func() { _ = h.Kill() })
	require.NoError(t, err)
	g.Retire()
	require.Eventually(t, func() bool { return !treeAlive(t, dir) }, 2*time.Second, 20*time.Millisecond,
		"Retire kills the launcher child's whole tree")
	select {
	case <-h.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("the killed launcher child was not reaped")
	}
	assert.NoError(t, h.Kill(), "Kill after exit is a no-op")
}

// Review round 1: a launcher whose URL never comes up is stopped by the
// failure path. A retirement that lands while Stop waits out the SIGTERM
// grace period of a child that ignores SIGTERM kills it at once: the child is
// still tracked by the gate until Stop has reaped it.
func TestRetire_LauncherFailureStopKilledByRetire(t *testing.T) {
	dir := t.TempDir()
	ln := closedPort(t)
	c := newRetireCoreClient(t, &config.ServerConfig{
		Name:                "launcher-stop-retire",
		URL:                 "http://" + ln + "/mcp",
		Protocol:            "http",
		Command:             "/bin/sh",
		Args:                []string{"-c", treeScript(dir, true)},
		LauncherWaitTimeout: config.Duration(300 * time.Millisecond),
		Enabled:             true,
	}, nil)

	prev := launcherBeforeStopHook
	launcherBeforeStopHook = func() {
		waitDescendant(t, dir)
		c.Retire()
	}
	defer func() { launcherBeforeStopHook = prev }()

	start := time.Now()
	c.mu.Lock()
	err := c.connectWithLauncher(context.Background())
	c.mu.Unlock()
	elapsed := time.Since(start)
	require.Error(t, err)
	assert.Less(t, elapsed, launcher.DefaultStopGrace,
		"the retirement kill ends the child; Stop does not wait out the grace period")
	require.Eventually(t, func() bool { return !treeAlive(t, dir) }, 2*time.Second, 20*time.Millisecond)
}

// closedPort returns host:port of a TCP port nothing listens on.
func closedPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}
