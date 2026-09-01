package diagnostics

import (
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// A mirrored install does NOT get the public git-capable image: mcpproxy leaves
// the server on the operator's own configured image rather than attempting a
// pull their host cannot make. The remediation must describe that, or it names
// an image this install never ran and calls it "automatic" — sending the
// operator looking for a pull that never happened instead of at the one key
// that fixes it.
func TestMissingToolchainRemediationDoesNotClaimASubstitutionThatDidNotHappen(t *testing.T) {
	const mirrored = "mirror.internal/astral/uv:python3.13-bookworm-slim"
	hints := ClassifierHints{
		Transport:      "stdio",
		DockerIsolated: true,
		DockerCommand:  "uvx",
		DockerArgs:     []string{"--from", "srv@git+https://github.com/o/r", "srv"},
		// Exactly what a mirrored config looks like after load: the operator's
		// runtime entry, and the git key still holding its shipped value
		// because the merge put it there.
		DockerDefaultImages: map[string]string{
			"uvx":     mirrored,
			"uvx-git": config.DefaultGitCapableImage,
		},
	}

	msg := RuntimeAwareRemediation(DockerMissingToolchain, hints)
	if msg == "" {
		t.Fatal("no runtime-aware remediation for a git-dependency server")
	}
	if strings.Contains(msg, config.DefaultGitCapableImage) {
		t.Errorf("remediation names a public image this install never ran: %s", msg)
	}
	if !strings.Contains(msg, mirrored) {
		t.Errorf("remediation does not name the image the server actually ran: %s", msg)
	}
	if !strings.Contains(msg, gitCapableImageKey) {
		t.Errorf("remediation does not name the key that fixes it: %s", msg)
	}
}

// The mirrored built-in values this package compares against must match the
// ones config actually ships, or the "did mcpproxy substitute?" question is
// answered from stale strings.
func TestMirroredBuiltInImagesMatchConfig(t *testing.T) {
	images := config.DefaultDockerIsolationConfig().DefaultImages
	if builtInGitCapableImage != config.DefaultGitCapableImage {
		t.Errorf("builtInGitCapableImage = %q, config ships %q", builtInGitCapableImage, config.DefaultGitCapableImage)
	}
	for runtimeType := range gitCapableRuntimeTypes {
		if got := images[runtimeType]; got != builtInPythonRunnerImage {
			t.Errorf("built-in default_images[%q] = %q, mirrored value is %q — the mirror is stale",
				runtimeType, got, builtInPythonRunnerImage)
		}
	}
}

// The documented opt-out (`uvx-git: ""`) is the one case where the key is
// present and empty. Telling that user mcpproxy "selects a git-capable image
// automatically" describes the behaviour they explicitly turned off.
func TestMissingToolchainRemediationNamesTheOptOut(t *testing.T) {
	hints := ClassifierHints{
		Transport:      "stdio",
		DockerIsolated: true,
		DockerCommand:  "uvx",
		DockerArgs:     []string{"--from", "srv@git+https://github.com/o/r", "srv"},
		DockerDefaultImages: map[string]string{
			"uvx":     "ghcr.io/astral-sh/uv:python3.13-bookworm-slim",
			"uvx-git": "",
		},
	}

	msg := RuntimeAwareRemediation(DockerMissingToolchain, hints)
	if !strings.Contains(msg, gitCapableImageKey) {
		t.Errorf("remediation does not name the key that was emptied: %s", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "opts out") {
		t.Errorf("remediation does not say the selection is opted out: %s", msg)
	}
}
