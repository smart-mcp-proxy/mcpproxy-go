package core

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// installDockerReporting puts a docker on PATH that answers every command with components, one
// per line, as `docker version` lists the daemon's components.
func installDockerReporting(t *testing.T, components ...string) {
	t.Helper()
	dir := t.TempDir()
	name, script := "docker", "#!/bin/sh\n"
	for _, c := range components {
		script += "echo '" + c + "'\n"
	}
	if runtime.GOOS == osWindows {
		name, script = "docker.cmd", "@echo off\r\n"
		for _, c := range components {
			script += "echo " + c + "\r\n"
		}
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755))
	t.Setenv("PATH", dir)
	useRealDockerResolver(t)
}

func TestDockerIsolation_InitOnlyWhereTheDaemonHasIt(t *testing.T) {
	tests := []struct {
		name       string
		components []string
		want       bool
	}{
		{"docker", []string{"Engine", "containerd", "runc", "docker-init"}, true},
		{"docker without an init binary", []string{"Engine", "containerd", "runc"}, false},
		{"podman", []string{"Podman Engine", "Conmon", "OCI Runtime (crun)"}, false},
		{"no answer", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installDockerReporting(t, tt.components...)
			require.Equal(t, tt.want, newIsolatedTestClient().dockerHasInit())
		})
	}
}

// Both defaults must come before extra_args: docker lets the last occurrence of a flag win.
func TestDockerIsolation_ExtraArgsOverrideStopDefaults(t *testing.T) {
	installDockerReporting(t, "Engine", "docker-init")
	forceDockerDaemonEnvGOOS(t, osDarwin) // direct exec: args stay a list

	c := newIsolatedTestClient()
	c.config.Isolation = &config.IsolationConfig{ExtraArgs: []string{"--init=false", "--stop-timeout", "20"}}
	_, args, _, _ := c.setupDockerIsolation(c.config.Command, c.config.Args)
	if len(args) < 4 { // Windows always wraps in a shell
		args = strings.Fields(args[len(args)-1])
		args = args[slices.Index(args, "run"):]
	}

	require.Equal(t, "run", args[0])
	initAt, stopAt := slices.Index(args, "--init"), slices.Index(args, "--stop-timeout")
	require.Positive(t, initAt)
	require.Positive(t, stopAt)
	require.Equal(t, containerStopTimeout, args[stopAt+1])
	overrideAt := slices.Index(args, "--init=false")
	require.Greater(t, overrideAt, initAt)
	require.Greater(t, overrideAt, stopAt)
}
