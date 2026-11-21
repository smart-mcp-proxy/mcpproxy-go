//go:build windows

package core

import (
	"context"
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secureenv"
)

// #1960: both upstream spawn paths must hand cmd.exe the wrapped command
// string verbatim, or a quoted path with a space fails to start.
func TestSpawnSitesUseVerbatimCmdLine(t *testing.T) {
	t.Setenv("SHELL", "") // force cmd.exe even under Git Bash
	command := `C:\Program Files\Tool\server.cmd`
	args := []string{"a b"}

	assertVerbatim := func(t *testing.T, cmd *exec.Cmd) {
		t.Helper()
		require.Len(t, cmd.Args, 3)
		require.NotNil(t, cmd.SysProcAttr)
		require.Equal(t, syscall.EscapeArg(cmd.Args[0])+` /s /c "`+cmd.Args[2]+`"`, cmd.SysProcAttr.CmdLine)
	}

	t.Run("stdio", func(t *testing.T) {
		c := &Client{config: &config.ServerConfig{Name: "s", Command: command, Args: args}, logger: zap.NewNop()}
		shell, shellArgs := c.wrapWithUserShell(command, args)
		cmd, err := createProcessGroupCommandFunc(nil, "", zap.NewNop())(context.Background(), shell, nil, shellArgs)
		require.NoError(t, err)
		assertVerbatim(t, cmd)
	})

	t.Run("launcher", func(t *testing.T) {
		c := &Client{
			config:     &config.ServerConfig{Name: "s", Command: command, Args: args},
			logger:     zap.NewNop(),
			envManager: secureenv.NewManager(nil),
		}
		cmd, _, _, err := c.buildLauncherCmd(context.Background(), false)
		require.NoError(t, err)
		assertVerbatim(t, cmd)
	})
}
