package diagnostics

import (
	"errors"
	"strings"
	"testing"
)

// Container stderr is a DIFFERENT failure surface from mcpproxy's own errors:
// anything the isolated image lacks lands here, and every shape below reached
// the user as MCPX_UNKNOWN_UNCLASSIFIED — whose catalog entry asks them to file
// a bug report — for a one-line config/image problem (#1144).
//
// The git samples are verbatim from `uv` inside the shipped default image:
//
//	$ docker run --rm ghcr.io/astral-sh/uv:python3.13-bookworm-slim \
//	    uvx --from git+https://github.com/o/r srv
//	× Failed to resolve `--with` requirement
//	╰─▶ Git operation failed
//	$ docker run --rm --entrypoint sh ghcr.io/astral-sh/uv:python3.13-bookworm-slim -c 'git --version'
//	sh: 1: git: not found
//
// ("Git executable not found. Ensure that Git is installed and available." is
// the same failure from an older uv, and is the wording in the bug report.)
func TestContainerToolchainStderrNeverClassifiesAsUnknown(t *testing.T) {
	dockerHints := ClassifierHints{Transport: "stdio", DockerIsolated: true, DockerCommand: "uvx"}

	cases := []struct {
		name  string
		err   string
		hints ClassifierHints
		want  Code
	}{
		{
			name:  "uv reports git missing from the image",
			err:   "Git executable not found. Ensure that Git is installed and available.",
			hints: dockerHints,
			want:  DockerMissingToolchain,
		},
		{
			name:  "uv git resolution failure as mcpproxy wraps it",
			err:   "server process exited before completing the MCP initialize handshake; recent stderr:\n  × Failed to resolve `--with` requirement\n  ╰─▶ Git operation failed: transport closed",
			hints: dockerHints,
			want:  DockerMissingToolchain,
		},
		{
			name:  "dash reports the missing tool",
			err:   "stdio transport (docker_isolation=true): recent stderr: sh: 1: git: not found",
			hints: dockerHints,
			want:  DockerMissingToolchain,
		},
		{
			name:  "bash reports the missing tool",
			err:   "stdio transport (docker_isolation=true): recent stderr: bash: line 1: git: command not found",
			hints: dockerHints,
			want:  DockerMissingToolchain,
		},
		{
			name:  "a non-git tool missing from the image is the same class of failure",
			err:   "stdio transport (docker_isolation=true): recent stderr: sh: 1: make: not found",
			hints: dockerHints,
			want:  DockerMissingToolchain,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(errors.New(tc.err), tc.hints)
			if got == UnknownUnclassified {
				t.Fatal("container-toolchain stderr must never ask the user to file a bug report")
			}
			if got != tc.want {
				t.Fatalf("Classify() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The new rule must not eat the codes that already worked: the docker BINARY
// missing (#696) and the container ENTRYPOINT interpreter missing are
// different fixes with different docs pages.
func TestContainerToolchainDoesNotSwallowExistingDockerCodes(t *testing.T) {
	dockerHints := ClassifierHints{Transport: "stdio", DockerIsolated: true}

	cases := []struct {
		name string
		err  string
		want Code
	}{
		{
			name: "docker binary missing (sh wording)",
			err:  "stdio transport (docker_isolation=true): recent stderr: docker: command not found",
			want: DockerCLINotFound,
		},
		{
			name: "docker binary missing (zsh wording)",
			err:  "stdio transport (docker_isolation=true): recent stderr: zsh:1: command not found: docker",
			want: DockerCLINotFound,
		},
		{
			name: "docker binary missing (dash wording)",
			err:  "stdio transport (docker_isolation=true): recent stderr: sh: 1: docker: not found",
			want: DockerCLINotFound,
		},
		{
			name: "entrypoint interpreter missing from the image",
			err:  `docker: Error response from daemon: failed to create task: OCI runtime create failed: runc create failed: exec: "uvx": executable file not found in $PATH: unknown`,
			want: DockerExecNotFound,
		},
		{
			name: "OCI architecture mismatch",
			err:  "docker: Error response from daemon: failed to create task: OCI runtime create failed: exec format error",
			want: DockerOCIRuntime,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(errors.New(tc.err), dockerHints); got != tc.want {
				t.Fatalf("Classify() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The rule is gated on the Docker-isolation hint: the same wording from a HOST
// stdio server means "install git on this machine", which is a different fix
// and a different code.
func TestContainerToolchainIsGatedOnDockerHint(t *testing.T) {
	err := errors.New("Git executable not found. Ensure that Git is installed and available.")
	if got := Classify(err, ClassifierHints{Transport: "stdio"}); got == DockerMissingToolchain {
		t.Fatalf("Classify() = %q for a non-isolated server; the DOCKER code must stay gated", got)
	}
}

// A git clone that failed for a NETWORK/auth reason is not a missing toolchain —
// telling the user to change images would send them down the wrong path.
func TestGitOperationFailedWithAuthCauseIsNotToolchain(t *testing.T) {
	for _, msg := range []string{
		"Git operation failed: fatal: could not read Username for 'https://github.com': No such device or address",
		"Git operation failed: fatal: Authentication failed for 'https://github.com/o/r/'",
		"Git operation failed: fatal: could not resolve host: github.com",
	} {
		if got := Classify(errors.New(msg), ClassifierHints{Transport: "stdio", DockerIsolated: true}); got == DockerMissingToolchain {
			t.Errorf("Classify(%q) = %q, want anything but the missing-toolchain code", msg, got)
		}
	}
}

// A registered code, or the API emits a diagnostic with no message, no fix
// steps and no docs URL.
func TestMissingToolchainIsRegistered(t *testing.T) {
	entry, ok := Get(DockerMissingToolchain)
	if !ok {
		t.Fatalf("%s is not registered in the catalog", DockerMissingToolchain)
	}
	if entry.UserMessage == "" {
		t.Error("no user message")
	}
	if len(entry.FixSteps) == 0 {
		t.Error("no fix steps")
	}
	if entry.DocsURL == "" {
		t.Error("no docs URL")
	}
}

// #1143 fixed the git case automatically, so the remediation has to say so —
// otherwise it sends the user editing config for a problem mcpproxy already
// solved, or leaves them wondering why the override they pinned still fails.
func TestMissingToolchainRemediationNamesTheAutomaticFix(t *testing.T) {
	base := ClassifierHints{
		Transport:      "stdio",
		DockerIsolated: true,
		DockerCommand:  "uvx",
		DockerArgs:     []string{"--from", "srv@git+https://github.com/o/r", "srv"},
		DockerDefaultImages: map[string]string{
			"uvx":     "ghcr.io/astral-sh/uv:python3.13-bookworm-slim",
			"uvx-git": "ghcr.io/astral-sh/uv:python3.13-bookworm",
		},
	}

	t.Run("no override: automatic selection already handles it", func(t *testing.T) {
		msg := RuntimeAwareRemediation(DockerMissingToolchain, base)
		if msg == "" {
			t.Fatal("no runtime-aware remediation for a git-dependency server")
		}
		if !strings.Contains(msg, "ghcr.io/astral-sh/uv:python3.13-bookworm") {
			t.Errorf("remediation does not name the git-capable image: %s", msg)
		}
		if !strings.Contains(strings.ToLower(msg), "automatic") {
			t.Errorf("remediation does not mention the automatic image selection: %s", msg)
		}
	})

	t.Run("override present: name it as the culprit", func(t *testing.T) {
		hints := base
		hints.DockerImageOverride = "python:3.11"
		msg := RuntimeAwareRemediation(DockerMissingToolchain, hints)
		if !strings.Contains(msg, "python:3.11") {
			t.Errorf("remediation does not name the pinned image: %s", msg)
		}
		if !strings.Contains(msg, "isolation.image") {
			t.Errorf("remediation does not point at the override: %s", msg)
		}
	})

	t.Run("non-git toolchain: still concrete", func(t *testing.T) {
		hints := base
		hints.DockerArgs = []string{"mcp-server-fetch"}
		msg := RuntimeAwareRemediation(DockerMissingToolchain, hints)
		if msg == "" {
			t.Fatal("no remediation for a non-git missing tool")
		}
		if strings.Contains(strings.ToLower(msg), "automatic") {
			t.Errorf("must not claim the automatic git selection covers a non-git tool: %s", msg)
		}
	})
}
