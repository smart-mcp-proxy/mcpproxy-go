package core

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// A Windows config names the CLI docker.exe, often by its full backslash path.
// Missing it routes the server through the plain shell wrap, so config env is
// never injected as -e flags and the container is not tracked for cleanup.
func TestIsDockerRunCommand(t *testing.T) {
	run := []string{"run", "--rm", "-i", "ghcr.io/some/image"}
	cases := []struct {
		command string
		args    []string
		want    bool
	}{
		{"docker", run, true},
		{"/usr/local/bin/docker", run, true},
		{"docker.exe", run, true},
		{"DOCKER.EXE", run, true},
		{`C:\Program Files\Docker\Docker\resources\bin\docker.exe`, run, true},
		{`C:\Program Files\Docker\Docker\resources\bin\docker`, run, true},
		{"docker", []string{"ps"}, false},
		{"docker", nil, false},
		{"/usr/bin/docker-compose", run, false},
		{"dockerize", run, false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, isDockerRunCommand(tc.command, tc.args), "%q %v", tc.command, tc.args)
	}
}

// A user-supplied `docker run` whose command is an absolute path to the docker
// CLI must spawn that binary, not whichever docker the resolver finds. Before
// this, C:\custom\docker.exe (or /opt/x/bin/docker) was replaced by the
// resolved or bare `docker`, failing when that one was absent from PATH.
func TestResolveDockerSpawnHonorsConfiguredAbsoluteDocker(t *testing.T) {
	configured := writeFakeDockerExecutable(t)
	other := writeFakeDockerExecutable(t)

	orig := resolveDockerBinary
	t.Cleanup(func() { resolveDockerBinary = orig })

	for _, tc := range []struct {
		name     string
		goos     string
		resolve  func(*zap.Logger) (string, error)
		wantWrap bool
	}{
		{"direct exec, resolver finds another docker", osDarwin, func(*zap.Logger) (string, error) { return other, nil }, false},
		{"direct exec, resolver fails", osDarwin, func(*zap.Logger) (string, error) { return "", fmt.Errorf("docker not found") }, false},
		{"shell wrap keeps the configured binary", "linux", func(*zap.Logger) (string, error) { return other, nil }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forceDockerDaemonEnvGOOS(t, tc.goos)
			t.Setenv("DOCKER_HOST", "")
			t.Setenv("DOCKER_CONTEXT", "")
			resolveDockerBinary = tc.resolve
			c := newUserSuppliedDockerTestClient()
			c.config.Command = configured

			cmd, args, shellWrapped, dir := c.resolveDockerSpawn(c.config.Args)
			require.Equal(t, tc.wantWrap, shellWrapped)
			require.Equal(t, filepath.Dir(configured), dir)
			if shellWrapped {
				require.Contains(t, args[len(args)-1], configured)
				return
			}
			require.Equal(t, configured, cmd)
		})
	}
}
