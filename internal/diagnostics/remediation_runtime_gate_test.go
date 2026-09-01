package diagnostics

import (
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The automatic git-capable image selection is gated on the runtime type as
// well as the `git+` marker: config.NeedsGitCapableImage only fires for Python
// package runners, because node:22 already ships git and must not be pushed
// onto a second large image. A remediation that drops that half of the gate
// promises a `node`/`go`/`ruby` user an automatic fix that will never run, and
// points them at `docker_isolation.default_images.uvx-git` — a key that has no
// effect on their server at all.
func TestMissingToolchainRemediationHonoursTheRuntimeGate(t *testing.T) {
	images := map[string]string{
		"uvx":     "ghcr.io/astral-sh/uv:python3.13-bookworm-slim",
		"uvx-git": "ghcr.io/astral-sh/uv:python3.13-bookworm",
		"npx":     "node:22",
	}

	nonPython := ClassifierHints{
		Transport:           "stdio",
		DockerIsolated:      true,
		DockerCommand:       "npx",
		DockerArgs:          []string{"git+https://github.com/o/r"},
		DockerDefaultImages: images,
	}

	msg := RuntimeAwareRemediation(DockerMissingToolchain, nonPython)
	if msg == "" {
		t.Fatal("no remediation for a Docker-isolated node server")
	}
	if strings.Contains(strings.ToLower(msg), "automatic") {
		t.Errorf("claims an automatic fix that NeedsGitCapableImage never runs for npx: %s", msg)
	}
	if strings.Contains(msg, gitCapableImageKey) {
		t.Errorf("points at %s, which does not apply to a node runtime: %s", gitCapableImageKey, msg)
	}
}

// The mirrored gate has to stay in lockstep with the resolver it describes, or
// the message drifts back into promising a fix that does not run.
func TestMirroredGitRuntimeGateMatchesConfig(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		{"uvx", true},
		{"python", true},
		{"python3", true},
		{"pip", true},
		{"pipx", true},
		{"npx", false},
		{"node", false},
		{"go", false},
		{"ruby", false},
	}

	args := []string{"--from", "srv@git+https://github.com/o/r", "srv"}
	for _, tc := range cases {
		runtimeType := detectDockerRuntimeType(tc.command)
		got := hintsHaveGitDependency(ClassifierHints{DockerCommand: tc.command, DockerArgs: args})
		want := config.NeedsGitCapableImage(runtimeType, args)
		if want != tc.want {
			t.Fatalf("config.NeedsGitCapableImage(%q) = %v, test table says %v", runtimeType, want, tc.want)
		}
		if got != want {
			t.Errorf("hintsHaveGitDependency(%q) = %v, config.NeedsGitCapableImage = %v", tc.command, got, want)
		}
	}
}
