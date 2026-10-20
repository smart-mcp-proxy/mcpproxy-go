//go:build unix

package core

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Sol review round 2 (Spec 116 PR4, FR-025a): when the stdio child cannot be
// contained (on Windows: the Job Object cannot be created or the child cannot
// be assigned to it), the admitted launch section kills the child and the
// start fails. Before the fix the failure was logged and the child kept
// running outside any Job, so retirement later killed only the immediate
// process. The containment step is swapped for a failing one, so the
// section's handling runs on every platform.
func TestStdioStart_ContainmentFailure_KillsChildAndFails(t *testing.T) {
	dir := t.TempDir()
	prev := assignStdioProcessTree
	assignStdioProcessTree = func(*exec.Cmd, *zap.Logger, string) (int, error) {
		// The child has started its descendant by now, so a kill of
		// the immediate process alone would leave it running.
		waitDescendant(t, dir)
		return 0, errors.New("job assignment failed (test)")
	}
	t.Cleanup(func() { assignStdioProcessTree = prev })

	c := newRetireCoreClient(t, &config.ServerConfig{
		Name:     "contain-fail",
		Command:  "/bin/sh",
		Args:     []string{"-c", treeScript(dir, false)},
		Protocol: "stdio",
		Enabled:  true,
	}, nil)
	t.Cleanup(func() { _ = c.Disconnect() })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := c.Connect(ctx)
	require.Error(t, err, "a child that cannot be contained fails the start")
	require.ErrorContains(t, err, "job assignment failed (test)")
	require.Eventually(t, func() bool { return !treeAlive(t, dir) }, 3*time.Second, 20*time.Millisecond,
		"the uncontained child and its descendant are killed")
}
