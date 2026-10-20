//go:build unix

package core

import (
	"os/exec"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Sol review round 2 (Spec 116 PR4): Disconnect reaps the stdio child (the
// MCP close waits for it). Once the child is reaped its process-group ID can
// be handed to an unrelated process, so the gate must have released the
// child BEFORE the reap: a retirement that lands after the reap signals
// nothing.
func TestDisconnect_RetireAfterReap_DoesNotSignalReleasedGroup(t *testing.T) {
	c := newRetireCoreClient(t, &config.ServerConfig{Name: "untrack-order", Command: "/bin/sh", Protocol: "stdio"}, nil)

	cmd := exec.Command("/bin/sh", "-c", "sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	var signalled atomic.Int32
	proc, err := c.Gate().TrackProcess(func() { signalled.Add(1) })
	require.NoError(t, err)
	c.mu.Lock()
	c.stdioProc = proc
	c.processGroupID = cmd.Process.Pid
	c.processCmd = cmd
	c.mu.Unlock()

	restore := setDisconnectAfterReapHookForTest(func() {
		// The teardown has reaped the child (simulated here; the MCP
		// close does it in production). Now the instance is retired.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		c.Retire()
	})
	defer restore()

	require.NoError(t, c.Disconnect())
	require.Zero(t, signalled.Load(), "a retirement after the reap must not signal the released process-group ID")
}

// Control: a retirement while the child is still tracked (before Disconnect)
// does kill it.
func TestDisconnect_RetireBeforeTeardown_SignalsGroup(t *testing.T) {
	c := newRetireCoreClient(t, &config.ServerConfig{Name: "untrack-ctl", Command: "/bin/sh", Protocol: "stdio"}, nil)
	var signalled atomic.Int32
	proc, err := c.Gate().TrackProcess(func() { signalled.Add(1) })
	require.NoError(t, err)
	c.mu.Lock()
	c.stdioProc = proc
	c.mu.Unlock()
	c.Retire()
	require.Positive(t, signalled.Load())
}

func setDisconnectAfterReapHookForTest(fn func()) (restore func()) {
	prev := disconnectAfterReapHook
	disconnectAfterReapHook = fn
	return func() { disconnectAfterReapHook = prev }
}
