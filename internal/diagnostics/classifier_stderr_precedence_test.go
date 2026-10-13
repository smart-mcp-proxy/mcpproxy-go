package diagnostics

import (
	"errors"
	"testing"
)

// The stderr tail mcpproxy attaches to a failure is a TAIL: it carries whatever
// the server logged before it stopped, most of it benign. Every arm that reads
// that tail for a "missing command" needs the same guard, or the guard is
// decoration — the one unguarded arm claims the failure exactly as the guarded
// ones would have.
//
// A handshake TIMEOUT is the case where this matters most: the container is
// still running, so nothing in the tail killed it and nothing in the tail may
// outrank MCPX_STDIO_HANDSHAKE_TIMEOUT.
func TestHandshakeTimeoutOutranksEveryStderrNoiseArm(t *testing.T) {
	const wrapper = "server did not respond to MCP initialize within 30s (subprocess may have " +
		"crashed or printed to stderr instead of stdout); recent stderr:\n"

	stderrTails := []string{
		"warn: /etc/mcp/profile.d/10-init: no such file or directory",
		"mise warn: mise: executable file not found, continuing without it",
		"/bin/sh: 1: source: not found",
		"bash: line 3: shopt: command not found",
	}

	for _, hints := range []ClassifierHints{
		{Transport: "stdio", DockerIsolated: true, DockerCommand: "uvx"},
		{Transport: "stdio"},
	} {
		for _, tail := range stderrTails {
			err := errors.New(wrapper + tail + "\n")
			if got := Classify(err, hints); got != STDIOHandshakeTimeout {
				t.Errorf("Classify(isolated=%v, tail=%q) = %q, want %q",
					hints.DockerIsolated, tail, got, STDIOHandshakeTimeout)
			}
		}
	}
}

// A shell reporting a BUILTIN missing is a portability bug in the server's own
// startup script — dash has no `source`, no `shopt`, no `pushd` — and says
// nothing about what the image contains. Diagnosing it as a missing container
// toolchain sends the user rebuilding a Docker image over a `#!/bin/sh` line.
func TestShellBuiltinNotFoundIsNotAMissingToolchain(t *testing.T) {
	hints := ClassifierHints{Transport: "stdio", DockerIsolated: true, DockerCommand: "uvx"}

	for _, line := range []string{
		"/bin/sh: 1: source: not found",
		"sh: 1: shopt: not found",
		"sh: 1: pushd: not found",
		"dash: 1: declare: not found",
		"bash: line 1: source: command not found",
	} {
		err := errors.New("server process exited before completing the MCP initialize handshake; " +
			"recent stderr:\n" + line + "\n: transport closed")
		got := Classify(err, hints)
		if got == DockerMissingToolchain || got == DockerExecNotFound {
			t.Errorf("Classify(%q) = %q — a shell builtin is not evidence the image lacks a toolchain", line, got)
		}
		if got != STDIOExitBeforeInitialize {
			t.Errorf("Classify(%q) = %q, want %q", line, got, STDIOExitBeforeInitialize)
		}
	}
}

// A real missing tool must still reach the toolchain code — the builtin guard
// is a carve-out, not an off switch.
func TestRealMissingToolStillReachesTheToolchainCode(t *testing.T) {
	hints := ClassifierHints{Transport: "stdio", DockerIsolated: true, DockerCommand: "uvx"}

	for _, line := range []string{
		"sh: 1: cmake: not found",
		"bash: line 1: gcc: command not found",
		"/bin/sh: 1: exec: git: not found",
		"sh: 1: source: not found\nsh: 1: cmake: not found",
	} {
		err := errors.New("server process exited before completing the MCP initialize handshake; " +
			"recent stderr:\n" + line + "\n: transport closed")
		if got := Classify(err, hints); got != DockerMissingToolchain {
			t.Errorf("Classify(%q) = %q, want %q", line, got, DockerMissingToolchain)
		}
	}
}

// `git@github.com: Permission denied (publickey).` is a git SSH CREDENTIAL
// failure. It contains "permission denied", which the generic stdio arm reads
// as the server's own executable being unreadable — so the user is told to fix
// file permissions on a binary that is fine, while the real cause (no deploy
// key for a private repo) is sitting in the stderr the code never points at.
// It must land where the other thirteen git-clone failure wordings land.
func TestGitSSHAuthFailureIsNotDiagnosedAsAFilePermissionProblem(t *testing.T) {
	wrap := func(cause string) error {
		return errors.New("server process exited before completing the MCP initialize handshake; recent stderr:\n" +
			"  × Failed to resolve `--with` requirement\n  ╰─▶ Git operation failed\n  ╰─▶ " + cause +
			"\n: transport closed")
	}

	for _, hints := range []ClassifierHints{
		{Transport: "stdio", DockerIsolated: true, DockerCommand: "uvx"},
		{Transport: "stdio"},
	} {
		got := Classify(wrap("git@github.com: Permission denied (publickey)."), hints)
		if got == STDIOSpawnEACCES {
			t.Errorf("Classify(isolated=%v) = %q — blames file permissions for a git auth failure",
				hints.DockerIsolated, got)
		}
		want := Classify(wrap("remote: Invalid username or password."), hints)
		if got != want {
			t.Errorf("Classify(publickey, isolated=%v) = %q, want %q — the same as its sibling git auth wordings",
				hints.DockerIsolated, got, want)
		}
	}
}
