package diagnostics

import (
	"errors"
	"testing"
)

// The EXIT wrapper is mcpproxy's own: it is written only when the transport
// closed, i.e. the child process STARTED and then died. A spawn-class code
// (ENOENT / EACCES / ENOEXEC — "we never got the process off the ground") is
// therefore provably wrong there, and the words that produce it — "no such
// file or directory", "permission denied" — are ordinary lines any program
// writes about its own optional files. Reading one out of the stderr tail
// outranks the authoritative fact that the child ran, and hands the user chmod
// advice for a binary that is fine.
func TestExitWrapperOutranksStderrNoiseSpawnArms(t *testing.T) {
	wrap := func(tail string) error {
		return errors.New("server process exited before completing the MCP initialize handshake; " +
			"recent stderr:\n" + tail + "\n: transport closed")
	}

	tails := []string{
		"warn: optional cache /var/cache/uv: no such file or directory\nfatal: config key `api_key` is required",
		"warn: could not read /etc/mcp/extra.toml: permission denied\nfatal: config key `api_key` is required",
		"[server] WARN plugin dir /opt/plugins: no such file or directory",
	}

	for _, hints := range []ClassifierHints{
		{Transport: "stdio", DockerIsolated: true, DockerCommand: "uvx"},
		{Transport: "stdio"},
	} {
		for _, tail := range tails {
			got := Classify(wrap(tail), hints)
			if got != STDIOExitBeforeInitialize {
				t.Errorf("Classify(isolated=%v, tail=%q) = %q, want %q",
					hints.DockerIsolated, tail, got, STDIOExitBeforeInitialize)
			}
		}
	}
}

// The tightening must not cost the failures the arms exist for: a container
// whose ENTRYPOINT interpreter is genuinely absent reports it with exec/OCI
// context, and that still has to reach MCPX_DOCKER_EXEC_NOT_FOUND.
func TestRealEntrypointMissingWordingsStillClassify(t *testing.T) {
	hints := ClassifierHints{Transport: "stdio", DockerIsolated: true, DockerCommand: "uvx"}

	for _, tail := range []string{
		`docker: Error response from daemon: failed to create task: OCI runtime create failed: runc create failed: exec: "uvx": executable file not found in $PATH: unknown`,
		"exec /usr/local/bin/uvx: no such file or directory",
		"docker: Error response from daemon: failed to create task for container: OCI runtime create failed: no such file or directory: unknown",
	} {
		err := errors.New("server process exited before completing the MCP initialize handshake; recent stderr:\n" +
			tail + "\n: transport closed")
		if got := Classify(err, hints); got != DockerExecNotFound {
			t.Errorf("Classify(%q) = %q, want %q", tail, got, DockerExecNotFound)
		}
	}
}
