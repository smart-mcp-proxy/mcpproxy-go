package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
)

// closeLogTransport is an MCP transport that records its Close, which for stdio is what closes the
// server's stdin. The embedded nil interface panics on anything else.
type closeLogTransport struct {
	transport.Interface
	log string
}

func (t closeLogTransport) Close() error {
	f, err := os.OpenFile(t.log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("close-stdin\n")
	return err
}

// installDockerLogging puts a docker on PATH that logs the command it got, answers `ps` with row
// and makes `wait` succeed (the container exited) or outlast its caller.
func installDockerLogging(t *testing.T, log, row string, exits bool) {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == osWindows {
		onWait := "exit /b 0"
		if !exits {
			onWait = `"` + filepath.Join(os.Getenv("SystemRoot"), "System32", "ping.exe") + `" -n 4 127.0.0.1 >nul`
		}
		script := fmt.Sprintf("@echo off\r\necho %%1>>\"%s\"\r\nif %%1==ps echo %s\r\nif %%1==wait %s\r\n", log, row, onWait)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "docker.cmd"), []byte(script), 0o600))
	} else {
		onWait := "exit 0"
		if !exits {
			onWait = "exec /bin/sleep 3"
		}
		script := fmt.Sprintf("#!/bin/sh\necho \"$1\" >> '%s'\n[ \"$1\" = ps ] && printf '%%s\\n' '%s'\n[ \"$1\" = wait ] && %s\nexit 0\n", log, row, onWait)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755))
	}
	t.Setenv("PATH", dir)
	useRealDockerResolver(t)
}

// The MCP lifecycle for stdio is: close stdin, wait, then signal. `docker stop` is the signal.
func TestDisconnectDocker_ClosesStdinBeforeStopping(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, tt := range []struct {
		name  string
		exits bool
	}{
		{"container that exits when its stdin closes", true},
		{"container that stays", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _, _ := newOwnershipClient("smoke", nil)
			log := filepath.Join(t.TempDir(), "calls.log")
			row := strings.Join([]string{id, "mcpproxy-smoke-ab12", "smoke", GetInstanceID(), "Up 5 minutes"}, "\t")
			installDockerLogging(t, log, row, tt.exits)

			proc, err := dialgate.New().TrackProcess(func() {})
			require.NoError(t, err)
			c.stdioProc = proc
			c.isDockerCommand = true
			c.containerID, c.containerOwner = id, "smoke"
			c.client = mcpclient.NewClient(closeLogTransport{log: log})

			begin := time.Now()
			require.NoError(t, c.DisconnectWithContext(context.Background()))
			elapsed := time.Since(begin)

			raw, err := os.ReadFile(log)
			require.NoError(t, err)
			calls := strings.Fields(string(raw))
			closeAt, waitAt := slices.Index(calls, "close-stdin"), slices.Index(calls, "wait")
			require.GreaterOrEqual(t, closeAt, 0, calls)
			require.Greater(t, waitAt, closeAt, calls)
			require.Empty(t, c.containerID)
			if tt.exits {
				require.NotContains(t, calls, "stop", "a container that already exited is not stopped")
				return
			}
			require.Equal(t, "stop", calls[len(calls)-1])
			require.GreaterOrEqual(t, elapsed, stdinCloseGrace, "the container gets time to exit on its own first")
		})
	}
}
