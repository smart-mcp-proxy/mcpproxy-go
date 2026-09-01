package core

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func gitDepServer(command string) *config.ServerConfig {
	return &config.ServerConfig{
		Name:    "git-server",
		Command: command,
		Args:    []string{"--from", "srv@git+https://github.com/o/r", "srv"},
	}
}

// A mirrored / air-gapped install retargets `default_images` at its own
// registry. When the git-capable key is absent from that map, falling back to a
// hardcoded public ghcr.io URL silently sends the container pull outside the
// operator's registry — an image that host cannot reach at all — and ignores
// the `uvx` entry they did configure. The operator's own map must win.
func TestGetDockerImage_AbsentGitKeyRespectsOperatorRuntimeDefault(t *testing.T) {
	im := gitTestManager(map[string]string{
		"uvx":    "mirror.internal/astral/uv:python3.13-bookworm",
		"python": "mirror.internal/astral/uv:python3.13-bookworm",
		"npx":    "mirror.internal/library/node:22",
	})

	got, err := im.GetDockerImage(gitDepServer("uvx"), "uvx")
	if err != nil {
		t.Fatalf("GetDockerImage() error: %v", err)
	}
	if got == gitUvImage {
		t.Fatalf("GetDockerImage() = %q — a hardcoded public image the mirrored host cannot pull", got)
	}
	if got != "mirror.internal/astral/uv:python3.13-bookworm" {
		t.Errorf("GetDockerImage() = %q, want the operator's configured uvx image", got)
	}
}

// An operator who does not want the substitution needs a way to say so without
// editing every server. An explicitly empty value for the key is that opt-out;
// treating empty as "absent" and substituting anyway leaves them no lever.
func TestGetDockerImage_EmptyGitKeyIsAnOptOut(t *testing.T) {
	images := config.DefaultDockerIsolationConfig().DefaultImages
	images[config.GitCapableImageKey] = ""
	im := gitTestManager(images)

	got, err := im.GetDockerImage(gitDepServer("uvx"), "uvx")
	if err != nil {
		t.Fatalf("GetDockerImage() error: %v", err)
	}
	if got != slimUvImage {
		t.Errorf("GetDockerImage() = %q, want the runtime default %q (substitution opted out)", got, slimUvImage)
	}
}

// The built-in git-capable image stays the fallback where there is nothing
// better to fall back TO: with no runtime entry the alternative is alpine,
// which has neither git nor python.
func TestGetDockerImage_NoRuntimeDefaultFallsBackToBuiltInGitImage(t *testing.T) {
	im := gitTestManager(map[string]string{"npx": "node:22"})

	got, err := im.GetDockerImage(gitDepServer("uvx"), "uvx")
	if err != nil {
		t.Fatalf("GetDockerImage() error: %v", err)
	}
	if got != gitUvImage {
		t.Errorf("GetDockerImage() = %q, want the built-in git-capable fallback %q", got, gitUvImage)
	}
}

// The "we swapped your image" log line is the only warning a user gets before a
// multi-hundred-MB pull. It must track the substitution that actually happened,
// not the server merely qualifying for one.
func TestBuildDockerArgs_GitImageLogTracksTheRealSubstitution(t *testing.T) {
	run := func(t *testing.T, images map[string]string) (logged bool, image string) {
		t.Helper()
		core, logs := observer.New(zap.InfoLevel)
		global := config.DefaultDockerIsolationConfig()
		global.Enabled = true
		global.DefaultImages = images
		im := NewIsolationManagerWithLogger(global, zap.New(core))

		args, err := im.BuildDockerArgs(gitDepServer("uvx"), "uvx")
		if err != nil {
			t.Fatalf("BuildDockerArgs() error: %v", err)
		}
		return logs.FilterMessage("using git-capable isolation image for a git dependency").Len() > 0, args[len(args)-1]
	}

	t.Run("substitution happens: say so", func(t *testing.T) {
		logged, image := run(t, config.DefaultDockerIsolationConfig().DefaultImages)
		if !logged {
			t.Error("no log line for a substituted git-capable image")
		}
		if image != gitUvImage {
			t.Errorf("image = %q, want %q", image, gitUvImage)
		}
	})

	t.Run("opted out: stay quiet", func(t *testing.T) {
		images := config.DefaultDockerIsolationConfig().DefaultImages
		images[config.GitCapableImageKey] = ""
		logged, image := run(t, images)
		if logged {
			t.Error("logged an image swap that did not happen")
		}
		if image != slimUvImage {
			t.Errorf("image = %q, want the runtime default %q", image, slimUvImage)
		}
	})
}
