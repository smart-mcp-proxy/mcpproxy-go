//go:build windows

package launcher

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/winjob"
)

// Sol review round 3 (Spec 116 FR-025a): a Job Object that cannot be
// created fails the launch and the child is killed, instead of degrading to
// a single-process kill that leaves descendants of a retired instance.
func TestSpawn_JobCreationFailure_KillsChildAndFails(t *testing.T) {
	prev := newJob
	newJob = func() (*winjob.Job, error) { return nil, errors.New("no job (test)") }
	t.Cleanup(func() { newJob = prev })

	cmd := exec.Command("cmd.exe", "/c", "ping -n 30 127.0.0.1")
	h, err := Spawn(context.Background(), &Spec{Cmd: cmd, Name: "uncontained"}, zap.NewNop())
	require.Error(t, err)
	require.Nil(t, h)
	require.NotNil(t, cmd.ProcessState, "the child was killed and reaped before Spawn returned")
}
