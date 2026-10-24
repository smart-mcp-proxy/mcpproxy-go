package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
