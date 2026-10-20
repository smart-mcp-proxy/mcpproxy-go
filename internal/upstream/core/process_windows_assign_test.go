//go:build windows

package core

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/winjob"
)

// Sol review round 2 (Spec 116 PR4, FR-025a): a Job Object that cannot be
// created is a containment failure, not a PID to carry on with. The stdio
// launch section kills the child and fails the start on this error
// (TestStdioStart_ContainmentFailure_KillsChildAndFails covers the section).
func TestAssignProcessTree_JobCreationFailureIsAnError(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "ping -n 30 127.0.0.1")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	prev := newWindowsJob
	newWindowsJob = func() (*winjob.Job, error) { return nil, errors.New("no job (test)") }
	t.Cleanup(func() { newWindowsJob = prev })

	pid, err := assignProcessTree(cmd, zap.NewNop(), "assign-fail")
	require.Error(t, err)
	require.Zero(t, pid)
	require.Nil(t, takeWindowsJob(cmd.Process.Pid, cmd), "nothing is registered for an uncontained child")
}
