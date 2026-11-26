package core

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// installDockerReporting puts a docker on PATH that logs each call and answers it with components,
// one per line, as `docker version` lists the daemon's components. With DOCKER_HOST=podman it
// answers as another daemon, one that has no docker-init. It returns the log.
func installDockerReporting(t *testing.T, components ...string) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls.log")
	name, script := "docker", "#!/bin/sh\necho \"$1\" >> '"+log+"'\n[ \"$DOCKER_HOST\" = podman ] && echo 'Podman Engine' && exit 0\n"
	for _, c := range components {
		script += "echo '" + c + "'\n"
	}
	if runtime.GOOS == osWindows {
		name, script = "docker.cmd", "@echo off\r\necho %1>>\""+log+"\"\r\nif \"%DOCKER_HOST%\"==\"podman\" echo Podman Engine& exit /b 0\r\n"
		for _, c := range components {
			script += "echo " + c + "\r\n"
		}
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755))
	t.Setenv("PATH", dir)
	t.Setenv("SHELL", "")
	t.Setenv("DOCKER_HOST", "")
	forceDockerDaemonEnvGOOS(t, osDarwin) // direct exec where the platform allows it
	useRealDockerResolver(t)
	clearDockerInitKnown()
	t.Cleanup(clearDockerInitKnown)
	return log
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
			require.Equal(t, tt.want, newIsolatedTestClient().dockerHasInit(context.Background(), os.Environ()))
		})
	}
}

// The probe must ask the daemon `docker run` will use, and each daemon only once.
func TestDockerIsolation_InitProbeFollowsTheSpawnEnvironment(t *testing.T) {
	log := installDockerReporting(t, "Engine", "docker-init")
	c := newIsolatedTestClient()
	local, podman := os.Environ(), append(os.Environ(), "DOCKER_HOST=podman")
	for range 3 {
		require.True(t, c.dockerHasInit(context.Background(), local))
		require.False(t, c.dockerHasInit(context.Background(), podman))
	}
	raw, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Len(t, strings.Fields(string(raw)), 2, "one probe per daemon")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	clearDockerInitKnown()
	require.False(t, c.dockerHasInit(cancelled, local), "a cancelled connect does not probe")
	require.True(t, c.dockerHasInit(context.Background(), local), "and a failed probe is not remembered")
}

// The defaults must come before extra_args: docker lets the last occurrence of a flag win.
func TestDockerIsolation_ExtraArgsOverrideStopDefaults(t *testing.T) {
	installDockerReporting(t, "Engine", "docker-init")

	c := newIsolatedTestClient()
	c.dockerInit = true
	c.config.Isolation = &config.IsolationConfig{ExtraArgs: []string{"--init=false", "--stop-timeout", "20"}}
	_, args, _, _ := c.setupDockerIsolation(c.config.Command, c.config.Args)
	if len(args) < 4 { // Windows always wraps in a shell
		args = strings.Fields(args[len(args)-1])
		args = args[slices.Index(args, "run"):]
	}

	require.Equal(t, "run", args[0])
	require.Contains(t, args, "--sig-proxy=false")
	initAt, overrideAt := slices.Index(args, "--init"), slices.Index(args, "--init=false")
	require.Positive(t, initAt)
	require.Greater(t, overrideAt, initAt)

	var stops []string
	for i, a := range args {
		if a == "--stop-timeout" {
			stops = append(stops, args[i+1])
		}
	}
	require.Equal(t, []string{containerStopTimeout, "20"}, stops)
}
