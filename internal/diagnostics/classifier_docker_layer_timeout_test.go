package diagnostics

import (
	"errors"
	"testing"
)

// A handshake timeout means the container IS alive and merely silent. Docker
// daemon wording appearing in that container's own stderr tail is therefore the
// server's log line, not evidence that OUR docker layer failed — a real daemon
// refusal never produces a handshake timeout, because no container would exist
// to time out. Reporting MCPX_DOCKER_DAEMON_DOWN there sends the user restarting
// Docker over an application log message.
func TestClassify_DockerLayerWordingInLiveContainerStderrDoesNotHijackTimeout(t *testing.T) {
	const prefix = `stdio transport (command="/usr/local/bin/docker", docker_isolation=true): `
	const timeout = "server did not respond to MCP initialize within 30s (subprocess may have crashed); recent stderr:\n  | "

	t.Run("daemon wording from the container's own log is not a layer failure", func(t *testing.T) {
		err := errors.New(prefix + timeout +
			"app log: Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?")
		if got := Classify(err, ClassifierHints{DockerIsolated: true}); got == DockerDaemonDown {
			t.Fatalf("live container's stderr hijacked the diagnosis: got %s, want anything but %s", got, DockerDaemonDown)
		}
	})

	t.Run("a genuine daemon refusal still classifies", func(t *testing.T) {
		err := errors.New(prefix +
			"server process exited before completing the MCP initialize handshake; recent stderr:\n  | " +
			"Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?: transport error: transport closed")
		if got := Classify(err, ClassifierHints{DockerIsolated: true}); got != DockerDaemonDown {
			t.Fatalf("genuine daemon failure lost its code: got %s, want %s", got, DockerDaemonDown)
		}
	})
}
