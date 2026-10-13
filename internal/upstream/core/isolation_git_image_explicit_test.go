package core

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Operator intent used to be inferred by comparing the configured value with
// the one mcpproxy ships, which cannot express "I explicitly chose the shipped
// image". An operator who mirrors `uvx` but deliberately points `uvx-git` at
// the public git-capable image had that choice read as "unset" and silently
// overridden by their mirrored runtime image — which has no git, so the server
// fails, and the remediation tells them to set the very key they did set.
func TestGetDockerImage_ExplicitGitKeyWinsEvenAtTheShippedValue(t *testing.T) {
	images := map[string]string{
		"uvx":                     "mirror.internal/astral/uv:python3.13-bookworm-slim",
		"python":                  "mirror.internal/astral/uv:python3.13-bookworm-slim",
		config.GitCapableImageKey: config.DefaultGitCapableImage,
	}
	im := gitTestManager(images)

	got, err := im.GetDockerImage(gitDepServer("uvx"), "uvx")
	if err != nil {
		t.Fatalf("GetDockerImage() error: %v", err)
	}
	if got != config.DefaultGitCapableImage {
		t.Errorf("GetDockerImage() = %q, want the operator's explicit %q", got, config.DefaultGitCapableImage)
	}
}

// The distinction only works if mcpproxy never writes that value into the
// operator's config itself: `default_images` is serialized back to disk in
// full on every config save, so any key mcpproxy seeds is indistinguishable
// from one the operator typed.
func TestShippedDefaultImagesDoNotSeedTheGitKey(t *testing.T) {
	images := config.DefaultDockerIsolationConfig().DefaultImages
	if v, ok := images[config.GitCapableImageKey]; ok {
		t.Errorf("default_images ships %q = %q; presence can no longer mean the operator chose it",
			config.GitCapableImageKey, v)
	}
}
