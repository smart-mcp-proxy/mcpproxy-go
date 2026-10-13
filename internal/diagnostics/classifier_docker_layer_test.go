package diagnostics

import (
	"errors"
	"fmt"
	"testing"
)

// wrapIsolated reproduces the EXACT error shape a Docker-isolated stdio server
// produces in production. connection_stdio.go wraps every isolated failure as
//
//	stdio transport (command=%q, docker_isolation=%t): %w
//
// with the RESOLVED argv[0] — so the literal substring "docker" is present in
// every isolated error mcpproxy formats, regardless of what actually failed.
// Tests written against bare stderr fragments cannot see the arms that key on
// that substring, which is how #1144's misdiagnosis survived a re-review.
func wrapIsolated(inner string) error {
	return fmt.Errorf(`stdio transport (command=%q, docker_isolation=%t): %s`,
		"/usr/local/bin/docker", true, inner)
}

// exitedWith builds the "child died before initialize" wrapper with a stderr tail.
func exitedWith(stderrTail string) string {
	return "server process exited before completing the MCP initialize handshake; recent stderr:\n  | " +
		stderrTail + ": transport error: transport closed"
}

// isolatedHintShapes are the two hint shapes a caller can plausibly build for a
// Docker-isolated server. The supervisor fills Transport from the resolved
// protocol, but DockerIsolated alone already implies stdio (config.ResolveIsolation
// only returns mode=docker for a server with a Command), so the classification
// must not depend on the caller having filled both.
var isolatedHintShapes = map[string]ClassifierHints{
	"transport+isolated": {Transport: "stdio", DockerIsolated: true, DockerCommand: "uvx"},
	"isolated only":      {DockerIsolated: true, DockerCommand: "uvx"},
}

// A git SSH credential failure inside the container is a missing/unauthorised
// deploy key. Diagnosing it as "Docker permission denied" sends the user to
// their Docker socket permissions — the exact class of misleading diagnosis
// #1144 exists to remove.
func TestGitSSHFailureUnderIsolationIsNotADockerPermissionProblem(t *testing.T) {
	err := wrapIsolated(exitedWith("git@github.com: Permission denied (publickey)."))

	for name, hints := range isolatedHintShapes {
		got := Classify(err, hints)
		if got == DockerNoPermission {
			t.Errorf("[%s] Classify(publickey) = %q — the docker socket is fine; the git key is not", name, got)
		}
		if got != STDIOExitBeforeInitialize {
			t.Errorf("[%s] Classify(publickey) = %q, want %q", name, got, STDIOExitBeforeInitialize)
		}
	}
}

// The docker-permission arm must key on evidence that the DAEMON or its SOCKET
// refused us. Keyed on the bare substring "docker" it is near-unfalsifiable
// under isolation, because mcpproxy's own wrapper injects that substring: ANY
// "permission denied" anywhere in the captured container stderr became a Docker
// socket diagnosis.
func TestContainerSidePermissionDeniedIsNotADockerSocketProblem(t *testing.T) {
	tails := []string{
		"git@github.com: Permission denied (publickey).",
		"/app/entrypoint.sh: line 3: /usr/local/bin/serve: Permission denied",
		"fatal: could not read Username for 'https://github.com': Permission denied",
	}

	for _, tail := range tails {
		err := wrapIsolated(exitedWith(tail))
		for name, hints := range isolatedHintShapes {
			if got := Classify(err, hints); got == DockerNoPermission {
				t.Errorf("[%s] Classify(%q) = %q — nothing here says the docker daemon refused us",
					name, tail, got)
			}
		}
	}
}

// The other half of the same contract: a REAL docker daemon/socket refusal must
// still reach MCPX_DOCKER_NO_PERMISSION — including on the stdio-hinted path,
// where the generic "permission denied" → MCPX_STDIO_SPAWN_EACCES arm used to
// claim it first and offer chmod advice for a server binary that is fine.
func TestRealDockerDaemonRefusalsClassifyAsDockerLayerFailures(t *testing.T) {
	cases := []struct {
		name string
		tail string
		want Code
	}{
		{
			name: "socket permission denied",
			tail: `docker: permission denied while trying to connect to the Docker daemon socket at ` +
				`unix:///var/run/docker.sock: Post "http://%2Fvar%2Frun%2Fdocker.sock/v1.43/containers/create": ` +
				`dial unix /var/run/docker.sock: connect: permission denied.`,
			want: DockerNoPermission,
		},
		{
			name: "windows named pipe access denied",
			tail: `docker: error during connect: Post "http://%2F%2F.%2Fpipe%2Fdocker_engine/v1.43/containers/create": ` +
				`open //./pipe/docker_engine: Access is denied.`,
			want: DockerNoPermission,
		},
		{
			name: "daemon not running",
			tail: "docker: Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?.",
			want: DockerDaemonDown,
		},
		{
			name: "pull access denied",
			tail: "docker: Error response from daemon: pull access denied for acme/private-mcp, " +
				"repository does not exist or may require 'docker login'.",
			want: DockerImagePullFailed,
		},
	}

	for _, tc := range cases {
		err := wrapIsolated(exitedWith(tc.tail))
		for name, hints := range isolatedHintShapes {
			if got := Classify(err, hints); got != tc.want {
				t.Errorf("[%s] %s: Classify() = %q, want %q", name, tc.name, got, tc.want)
			}
		}
	}
}

// Benign stderr noise under isolation must land on an honest, actionable code
// rather than MCPX_UNKNOWN_UNCLASSIFIED, whose CTA is "file a bug report".
// Both shapes here are mcpproxy's OWN wrappers: the cause is in the stderr the
// EXIT_BEFORE_INITIALIZE / HANDSHAKE_TIMEOUT remediation points at.
func TestBenignStderrUnderIsolationStillReachesAnHonestCode(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want Code
	}{
		{
			name: "dash has no source builtin, child died",
			msg:  exitedWith("/bin/sh: 1: source: not found"),
			want: STDIOExitBeforeInitialize,
		},
		{
			name: "child alive but silent, benign warning in the tail",
			msg:  "server did not respond to MCP initialize within 30s; recent stderr:\n  | warn: /etc/foo: no such file or directory",
			want: STDIOHandshakeTimeout,
		},
	}

	for _, tc := range cases {
		err := wrapIsolated(tc.msg)
		for name, hints := range isolatedHintShapes {
			got := Classify(err, hints)
			if got == UnknownUnclassified {
				t.Errorf("[%s] %s: Classify() = %q — mcpproxy wrote this wrapper itself, it is not an unknown failure",
					name, tc.name, got)
			}
			if got != tc.want {
				t.Errorf("[%s] %s: Classify() = %q, want %q", name, tc.name, got, tc.want)
			}
		}
	}
}

// The classifier is exported and its hints are advisory. DockerIsolated alone
// must be enough to reach the stdio rules: config.ResolveIsolation only reports
// mode=docker for a server that has a Command, i.e. a stdio server.
func TestDockerIsolatedHintImpliesStdioRules(t *testing.T) {
	err := errors.New(`stdio transport (command="/usr/local/bin/docker", docker_isolation=true): ` +
		"transport error: transport closed")
	if got := Classify(err, ClassifierHints{DockerIsolated: true}); got != STDIOExitBeforeInitialize {
		t.Errorf("Classify(transport closed, isolated only) = %q, want %q", got, STDIOExitBeforeInitialize)
	}
}
