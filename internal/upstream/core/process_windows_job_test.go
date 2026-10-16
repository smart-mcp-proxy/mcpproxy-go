//go:build windows

package core

import (
	"bufio"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/winjob"
)

// T085b / T4.12 (Spec 116 PR4 follow-up, T093b): the stdio child is created
// suspended, so it runs no code (and spawns no descendant) until
// assignProcessTree has put it in its Job Object and resumed it. Retirement
// (killProcessTreeNow) then reaches the whole tree, including a grandchild.
func TestStdioChild_SuspendedUntilJobAssigned_RetirementKillsTree(t *testing.T) {
	logger := zap.NewNop()
	cmd, err := createProcessGroupCommandFunc(nil, "", logger)(
		context.Background(), "cmd.exe", os.Environ(), []string{"/c", "ping -n 30 127.0.0.1"})
	require.NoError(t, err)
	require.True(t, winjob.IsSuspended(cmd), "the stdio command must be created suspended")

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

	select {
	case <-pinging:
		t.Fatal("suspended child ran before it was assigned to a Job")
	case <-time.After(1500 * time.Millisecond):
	}

	pgid, err := assignProcessTree(cmd, logger, "suspended")
	require.NoError(t, err)
	require.Equal(t, cmd.Process.Pid, pgid)

	select {
	case <-pinging:
	case <-time.After(10 * time.Second):
		t.Fatal("child never ran after the Job assignment resumed it")
	}

	// Retirement: kills the Job, so the grandchild (which holds the stdout
	// pipe) dies too and the pipe reaches EOF.
	killProcessTreeNow(pgid, cmd)
	select {
	case <-eof:
	case <-time.After(10 * time.Second):
		t.Fatal("grandchild survived the retirement kill; it escaped the Job")
	}
}
