//go:build unix

package core

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Sol review round 4 (Spec 116 PR4): a stdio launch whose gate is retired
// before the child is registered has the child killed and reaped by the
// transport's Close, which releases its process-group ID. The recorded ID
// must be cleared before that reap, or Connect's failure cleanup signals a
// number the OS may already have handed to an unrelated group.
func TestStdioStart_RetiredBeforeRegistration_ReleasedGroupNotSignalled(t *testing.T) {
	dir := t.TempDir()
	c := newRetireCoreClient(t, &config.ServerConfig{
		Name:     "retired-pgid",
		Command:  "/bin/sh",
		Args:     []string{"-c", treeScript(dir, false)},
		Protocol: "stdio",
		Enabled:  true,
	}, nil)
	t.Cleanup(func() { _ = c.Disconnect() })

	// Retire while the launch section is open: Retire latches the gate and
	// then waits on the section, so TrackProcess refuses the child.
	retired := make(chan struct{})
	prevLaunch := stdioLaunchHook
	stdioLaunchHook = func() {
		waitDescendant(t, dir)
		go func() { c.Retire(); close(retired) }()
		require.Eventually(t, c.Gate().RetireWaiting, 5*time.Second, time.Millisecond)
	}
	t.Cleanup(func() { stdioLaunchHook = prevLaunch })

	// An unrelated, live process group standing in for one that reused the
	// released number.
	other := exec.Command("sleep", "30")
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, other.Start())
	exited := make(chan struct{})
	go func() { _ = other.Wait(); close(exited) }()
	t.Cleanup(func() { _ = other.Process.Kill(); <-exited })

	recorded := -1
	prevClose := stdioRetiredCloseHook
	stdioRetiredCloseHook = func(cl *Client) {
		// Parked after the transport closed and reaped the child.
		recorded = cl.processGroupID
		if cl.processGroupID > 0 {
			// Simulate the reuse of the still-recorded number.
			cl.processGroupID = other.Process.Pid
		}
	}
	t.Cleanup(func() { stdioRetiredCloseHook = prevClose })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.Error(t, c.Connect(ctx), "a launch cut by retirement fails")
	<-retired

	select {
	case <-exited:
		t.Fatal("Connect's failure cleanup signalled a released process-group ID")
	case <-time.After(300 * time.Millisecond):
	}
	require.Equal(t, 0, recorded, "the reaped child's group ID is no longer recorded")
	require.Eventually(t, func() bool { return !treeAlive(t, dir) }, 3*time.Second, 20*time.Millisecond,
		"control: the retired launch's tree is killed")
}
