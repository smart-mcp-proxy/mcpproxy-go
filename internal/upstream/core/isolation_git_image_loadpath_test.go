package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

const mirroredUvImage = "mirror.internal/astral/uv:python3.13-bookworm-slim"

// THE REAL PATH. Everything else in this package builds the merged
// `default_images` map by hand, which is exactly where the previous fix hid: an
// operator's config file goes through LoadFromFile, whose decode merges INTO the
// built-in map, so the git-capable key is never absent and the "key absent →
// use the operator's own image" branch never runs. On that path a mirrored /
// air-gapped install still resolved a hardcoded public ghcr.io image — one its
// host cannot pull at all — while its own `default_images.uvx` sat unused.
func TestLoadedConfigWithMirroredImagesNeverResolvesAPublicGitImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.ConfigFileName)
	// Note what is NOT here: `uvx-git`. This is what an operator who mirrored
	// their registry before the key existed (or never heard of it) has on disk.
	body := `{
	  "listen": "127.0.0.1:8080",
	  "data_dir": "` + strings.ReplaceAll(dir, `\`, `\\`) + `",
	  "docker_isolation": {
	    "enabled": true,
	    "registry": "mirror.internal",
	    "default_images": {
	      "uvx": "` + mirroredUvImage + `",
	      "python": "` + mirroredUvImage + `",
	      "npx": "mirror.internal/library/node:22"
	    }
	  }
	}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile() error: %v", err)
	}

	core, logs := observer.New(zap.InfoLevel)
	im := NewIsolationManagerWithLogger(cfg.DockerIsolation, zap.New(core))
	srv := gitDepServer("uvx")

	got, err := im.GetDockerImage(srv, "uvx")
	if err != nil {
		t.Fatalf("GetDockerImage() error: %v", err)
	}
	if strings.Contains(got, "ghcr.io") {
		t.Fatalf("GetDockerImage() = %q — a public image an air-gapped host cannot pull", got)
	}
	if got != mirroredUvImage {
		t.Errorf("GetDockerImage() = %q, want the operator's own %q", got, mirroredUvImage)
	}

	// Silently running a git dependency on an image that may not contain git is
	// no better than the wrong pull if the operator is never told which knob
	// fixes it.
	if _, err := im.BuildDockerArgs(srv, "uvx"); err != nil {
		t.Fatalf("BuildDockerArgs() error: %v", err)
	}
	warned := logs.FilterMessageSnippet("git").Len() > 0
	if !warned {
		t.Error("no log line naming the git-capable image key for a git dependency left on the operator's own image")
	}
	for _, e := range logs.All() {
		for _, f := range e.Context {
			if s, ok := f.Interface.(string); ok && strings.Contains(s, "ghcr.io") {
				t.Errorf("log advertises a public image to a mirrored install: %s=%s", f.Key, s)
			}
			if strings.Contains(f.String, "ghcr.io") {
				t.Errorf("log advertises a public image to a mirrored install: %s=%s", f.Key, f.String)
			}
		}
	}
}

// The same trap seen from the other side: the built-in map must never carry a
// git-capable value of its own that outranks an operator who retargeted the
// runtime at their own registry. (mcpproxy no longer seeds the key at all —
// TestShippedDefaultImagesDoNotSeedTheGitKey pins that — so this is the shape a
// mirrored install really has after load.)
func TestUnsetGitKeyDoesNotOutrankAMirroredRuntimeImage(t *testing.T) {
	images := config.DefaultDockerIsolationConfig().DefaultImages
	if v, ok := images[config.GitCapableImageKey]; ok {
		t.Fatalf("precondition: the built-in map must not seed %q (got %q)", config.GitCapableImageKey, v)
	}
	images["uvx"] = mirroredUvImage
	images["python"] = mirroredUvImage

	im := gitTestManager(images)
	got, err := im.GetDockerImage(gitDepServer("uvx"), "uvx")
	if err != nil {
		t.Fatalf("GetDockerImage() error: %v", err)
	}
	if got != mirroredUvImage {
		t.Errorf("GetDockerImage() = %q, want the operator's %q", got, mirroredUvImage)
	}
}

// …but an operator who names their own git-capable image still wins outright:
// that is the documented knob, and the whole point of honouring their map.
func TestOperatorGitKeyWinsOverTheirRuntimeImage(t *testing.T) {
	images := config.DefaultDockerIsolationConfig().DefaultImages
	images["uvx"] = mirroredUvImage
	images[config.GitCapableImageKey] = "mirror.internal/astral/uv:python3.13-bookworm"

	im := gitTestManager(images)
	got, err := im.GetDockerImage(gitDepServer("uvx"), "uvx")
	if err != nil {
		t.Fatalf("GetDockerImage() error: %v", err)
	}
	if got != "mirror.internal/astral/uv:python3.13-bookworm" {
		t.Errorf("GetDockerImage() = %q, want the operator's git-capable image", got)
	}
}
