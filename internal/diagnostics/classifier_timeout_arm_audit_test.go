package diagnostics

import (
	"errors"
	"testing"
)

// Every arm of classifyDockerIsolatedSpawn that reads the captured stderr TAIL
// needs the handshake-timeout guard, not just the ones a review happened to
// name. A handshake timeout means the container is ALIVE and merely silent, so
// nothing it logged can be the terminal cause — including wordings about the
// docker layer itself, which the container is perfectly able to print (a
// docker-in-docker server, a script that shells out to `docker`, an SDK that
// logs "OCI runtime" warnings).
//
// This table walks the whole switch: one stderr line per arm.
func TestHandshakeTimeoutOutranksEveryDockerArm(t *testing.T) {
	const wrapper = "server did not respond to MCP initialize within 30s (subprocess may have " +
		"crashed or printed to stderr instead of stdout); recent stderr:\n"

	// One line per arm of classifyDockerIsolatedSpawn, in switch order.
	tails := map[string]string{
		"(1) docker cli, sh wording":      "app: docker: command not found",
		"(1) docker cli, zsh wording":     "app: zsh:1: command not found: docker",
		"(1) docker cli, dash wording":    "app: sh: 1: docker: not found",
		"(1) docker cli, go exec wording": `app: exec: "docker": executable file not found in $PATH`,
		"(1b) daemon down":                "app: Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
		"(1b) daemon permission":          "app: dial unix /var/run/docker.sock: connect: permission denied",
		"(1b) image pull":                 "app: Error response from daemon: pull access denied for acme/x",
		"(2) container git missing":       "app: Git executable not found. Ensure that Git is installed and available.",
		"(3) shell tool missing":          "app: sh: 1: cmake: not found",
		"(4) exec not found":              `app: exec: "uvx": executable file not found in $PATH`,
		"(5) oci runtime":                 "app: OCI runtime warning ignored",
		"(5) exec format":                 "app: exec format error while probing plugin",
		"(5) runc":                        "app: runc reported a stale bundle",
	}

	for _, hints := range []ClassifierHints{
		{Transport: "stdio", DockerIsolated: true, DockerCommand: "uvx"},
		{DockerIsolated: true},
	} {
		for arm, tail := range tails {
			err := errors.New(wrapper + tail + "\n")
			if got := Classify(err, hints); got != STDIOHandshakeTimeout {
				t.Errorf("%s (isolated hints %+v): Classify() = %q, want %q — a live container's stderr hijacked the timeout",
					arm, hints, got, STDIOHandshakeTimeout)
			}
		}
	}
}

// The guard is a guard, not an off switch: the same wordings on the EXIT
// wrapper (the child really died) must still reach their specific code.
func TestDockerArmsStillClassifyOnTheExitPath(t *testing.T) {
	hints := ClassifierHints{Transport: "stdio", DockerIsolated: true, DockerCommand: "uvx"}

	cases := []struct {
		tail string
		want Code
	}{
		{"docker: command not found", DockerCLINotFound},
		{"zsh:1: command not found: docker", DockerCLINotFound},
		{"docker: Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?", DockerDaemonDown},
		{"docker: Error response from daemon: pull access denied for acme/x", DockerImagePullFailed},
		{"Git executable not found. Ensure that Git is installed and available.", DockerMissingToolchain},
		{"sh: 1: cmake: not found", DockerMissingToolchain},
		{`docker: Error response from daemon: OCI runtime create failed: exec: "uvx": executable file not found in $PATH`, DockerExecNotFound},
		{"docker: Error response from daemon: OCI runtime create failed: exec format error", DockerOCIRuntime},
	}

	for _, tc := range cases {
		err := errors.New("server process exited before completing the MCP initialize handshake; recent stderr:\n" +
			tc.tail + "\n: transport closed")
		if got := Classify(err, hints); got != tc.want {
			t.Errorf("Classify(%q) = %q, want %q", tc.tail, got, tc.want)
		}
	}
}

// mcpproxy's OWN resolution failures are the arms that stay exempt: it emits
// them itself when it cannot resolve `docker` before any container exists, so
// they can never appear in a container's stderr tail.
func TestMCPProxyOwnDockerResolutionFailuresStayExempt(t *testing.T) {
	hints := ClassifierHints{Transport: "stdio", DockerIsolated: true}
	for _, msg := range []string{
		"docker not found in PATH",
		"docker not found in login shell",
	} {
		if got := Classify(errors.New(msg), hints); got != DockerCLINotFound {
			t.Errorf("Classify(%q) = %q, want %q", msg, got, DockerCLINotFound)
		}
	}
}
