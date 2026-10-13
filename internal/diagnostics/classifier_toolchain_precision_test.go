package diagnostics

import (
	"errors"
	"testing"
)

// The `<tool>: not found` arm added for #1144 is the loosest rule in
// classifyDockerIsolatedSpawn, and classifyDockerIsolatedSpawn runs BEFORE
// every generic stdio arm. Unanchored, it lets one line anywhere in the
// captured stderr tail claim the whole failure and outrank a strictly more
// specific classification.
func TestMissingToolchainDoesNotHijackMoreSpecificStdioCodes(t *testing.T) {
	hints := ClassifierHints{Transport: "stdio", DockerIsolated: true, DockerCommand: "uvx"}

	cases := []struct {
		name string
		err  string
		want Code
	}{
		{
			// A handshake timeout means the container is still RUNNING — it just
			// never answered `initialize`. A dash line from an earlier startup
			// script (`source` is a bash builtin dash does not have, and is
			// harmless) is not the terminal error.
			name: "handshake timeout with a benign shell line in the stderr tail",
			err: "server did not respond to MCP initialize within 30s (subprocess may have crashed " +
				"or printed to stderr instead of stdout); recent stderr:\n/bin/sh: 1: source: not found\n",
			want: STDIOHandshakeTimeout,
		},
		{
			// An application-level "<thing>: not found" is not a shell
			// missing-command line: the image is fine, the server's own config
			// is not. Diagnosing it as a missing container toolchain sends the
			// user to a docs page telling them to change images.
			name: "application-level not-found is not a shell missing-command line",
			err: "server process exited before completing the MCP initialize handshake; recent stderr:\n" +
				"[mcp-server] ERROR config profile production: not found\n: transport closed",
			want: STDIOExitBeforeInitialize,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(errors.New(tc.err), hints); got != tc.want {
				t.Fatalf("Classify() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Anchoring must not cost the shapes the rule exists for: every real shell
// wording for a missing command still has to reach the toolchain code.
func TestShellMissingCommandWordingsStillClassify(t *testing.T) {
	hints := ClassifierHints{Transport: "stdio", DockerIsolated: true, DockerCommand: "uvx"}

	for _, msg := range []string{
		"stdio transport (docker_isolation=true): recent stderr: sh: 1: cmake: not found",
		"stdio transport (docker_isolation=true): recent stderr: /bin/sh: 1: exec: cmake: not found",
		"stdio transport (docker_isolation=true): recent stderr: sh: cmake: not found",
		"stdio transport (docker_isolation=true): recent stderr: ash: cmake: not found",
		"stdio transport (docker_isolation=true): recent stderr: bash: line 1: cmake: command not found",
	} {
		if got := Classify(errors.New(msg), hints); got != DockerMissingToolchain {
			t.Errorf("Classify(%q) = %q, want %q", msg, got, DockerMissingToolchain)
		}
	}
}

// `uv` collapses every clone failure into "Git operation failed", so the
// exclusion list is the only thing keeping a credential or network failure out
// of the missing-git diagnosis. The list shipped with #1144 covered three
// wordings; these are the ones a private repo actually produces, and each was
// being sent to a docs page that says to change the Docker image.
func TestGitCloneFailuresAreNotDiagnosedAsAMissingImage(t *testing.T) {
	hints := ClassifierHints{Transport: "stdio", DockerIsolated: true, DockerCommand: "uvx"}

	wrap := func(cause string) string {
		return "server process exited before completing the MCP initialize handshake; recent stderr:\n" +
			"  × Failed to resolve `--with` requirement\n  ╰─▶ Git operation failed\n  ╰─▶ " + cause +
			"\n: transport closed"
	}

	causes := []string{
		"remote: Support for password authentication was removed on August 13, 2021.",
		"remote: Invalid username or password.",
		"fatal: could not read Username for 'https://github.com': terminal prompts disabled",
		"fatal: unable to access 'https://github.com/o/r/': The requested URL returned error: 403",
		"fatal: unable to access 'https://github.com/o/r/': The requested URL returned error: 401",
		"Host key verification failed.",
		"git@github.com: Permission denied (publickey).",
		"fatal: unable to access 'https://github.com/o/r/': Failed to connect to github.com port 443: Connection refused",
		"fatal: unable to access 'https://github.com/o/r/': Could not resolve host: github.com",
		"ssh: Could not resolve hostname github.com: Temporary failure in name resolution",
		"fatal: unable to access 'https://github.com/o/r/': server certificate verification failed. CAfile: none CRLfile: none",
		"fatal: unable to access 'https://github.com/o/r/': Network is unreachable",
		"fatal: repository 'https://github.com/o/r/' not found",
	}

	for _, cause := range causes {
		msg := wrap(cause)
		got := Classify(errors.New(msg), hints)
		if got == DockerMissingToolchain {
			t.Errorf("cause %q classified as %q — git ran, the image is not missing it", cause, got)
		}
		if got == UnknownUnclassified {
			t.Errorf("cause %q classified as %q — the #1144 bug-report CTA is back", cause, got)
		}
	}
}
