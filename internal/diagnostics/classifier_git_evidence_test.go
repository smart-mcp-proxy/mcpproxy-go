package diagnostics

import (
	"errors"
	"testing"
)

// `uv` collapses every git failure into "Git operation failed". Defaulting
// that to "the image has no git" unless the cause appears on a finite
// exclusion list inverts the burden of proof: the list can never be complete,
// and every wording missing from it tells the user to rebuild their Docker
// image over a failure that PROVES git ran.
//
// Anything git itself reported — a fatal:/error:/remote:/warning: line, an ssh
// or curl diagnostic — is that proof.
func TestGitReportedFailuresAreNeverAMissingBinary(t *testing.T) {
	hints := ClassifierHints{Transport: "stdio", DockerIsolated: true, DockerCommand: "uvx"}

	wrap := func(cause string) error {
		return errors.New("server process exited before completing the MCP initialize handshake; recent stderr:\n" +
			"  × Failed to resolve `--with` requirement\n  ╰─▶ Git operation failed\n  ╰─▶ " + cause +
			"\n: transport closed")
	}

	for _, cause := range []string{
		"fatal: could not read from remote repository.",
		"fatal: unable to update url base from redirection",
		"error: RPC failed; HTTP 429 curl 22 The requested URL returned error: 429",
		"fatal: protocol error: bad pack header",
		"warning: redirecting to https://github.com/o/r.git/\nfatal: expected flush after ref listing",
		"remote: Repository moved permanently",
		"error: 503 Service Unavailable while accessing the git mirror",
		"Host key verification failed.",
		"git@github.com: Permission denied (publickey).",
	} {
		got := Classify(wrap(cause), hints)
		if got == DockerMissingToolchain {
			t.Errorf("cause %q classified as %q — git demonstrably ran", cause, got)
		}
		if got == UnknownUnclassified {
			t.Errorf("cause %q classified as %q — the bug-report CTA is back", cause, got)
		}
	}
}

// The inversion must keep the #1143 case it was built for: uv reporting a git
// operation with NO diagnostic of git's own, and every wording that names the
// missing binary outright.
func TestMissingGitBinaryEvidenceStillClassifies(t *testing.T) {
	hints := ClassifierHints{Transport: "stdio", DockerIsolated: true, DockerCommand: "uvx"}

	for _, tail := range []string{
		"  × Failed to resolve `--with` requirement\n  ╰─▶ Git operation failed",
		"Git executable not found. Ensure that Git is installed and available.",
		"  × Failed to spawn: `git`\n  ╰─▶ No such file or directory (os error 2)",
		"sh: 1: git: not found",
		"bash: line 1: git: command not found",
	} {
		err := errors.New("server process exited before completing the MCP initialize handshake; recent stderr:\n" +
			tail + "\n: transport closed")
		if got := Classify(err, hints); got != DockerMissingToolchain {
			t.Errorf("Classify(%q) = %q, want %q", tail, got, DockerMissingToolchain)
		}
	}
}
