//go:build unix

package launcher

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Sol review round 3 (Spec 116 FR-025a): a child whose tree cannot be
// contained (on Windows, a Job Object that cannot be created or assigned)
// is killed and the launch fails, so the gate never tracks a kill that
// reaches only the immediate process. Unix has no containment step of its
// own; the seam injects the failure into Spawn's shared path.
func TestSpawn_ContainmentFailure_KillsChildAndFails(t *testing.T) {
	prev := containTree
	containTree = func(*exec.Cmd) (io.Closer, error) { return nil, errors.New("no job (test)") }
	t.Cleanup(func() { containTree = prev })

	cmd := exec.Command("sleep", "30")
	h, err := Spawn(context.Background(), &Spec{Cmd: cmd, Name: "uncontained"}, zap.NewNop())
	require.Error(t, err)
	require.ErrorContains(t, err, "no job (test)")
	require.Nil(t, h)
	require.NotNil(t, cmd.ProcessState, "the child was killed and reaped before Spawn returned")
	require.False(t, cmd.ProcessState.Success())
}
