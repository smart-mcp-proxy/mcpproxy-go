package core

import (
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func registryOnlyManager(registry string, images map[string]string) *IsolationManager {
	global := config.DefaultDockerIsolationConfig()
	global.Enabled = true
	global.Registry = registry
	if images != nil {
		global.DefaultImages = images
	}
	return NewIsolationManagerWithLogger(global, zap.NewNop())
}

// `docker_isolation.registry` is the one knob an air-gapped operator is
// guaranteed to set, and it is the whole point of the field. The built-in
// git-capable image was a fully-qualified public ghcr.io reference, and
// buildFullImageName hands any already-qualified reference back untouched — so
// an operator who set ONLY `registry` still had mcpproxy try to pull ghcr.io
// for every git-dependency server, from a host that cannot reach it.
func TestGetDockerImage_BuiltInGitImageHonoursConfiguredRegistry(t *testing.T) {
	const registry = "registry.airgap.local"

	cases := map[string]map[string]string{
		// The operator wrote nothing but `registry`.
		"no default_images at all": {},
		// The realistic shape after load: the built-in map merged in, still
		// entirely mcpproxy's own values.
		"built-in default_images merged in": config.DefaultDockerIsolationConfig().DefaultImages,
	}

	for name, images := range cases {
		t.Run(name, func(t *testing.T) {
			im := registryOnlyManager(registry, images)
			got, err := im.GetDockerImage(gitDepServer("uvx"), "uvx")
			if err != nil {
				t.Fatalf("GetDockerImage() error: %v", err)
			}
			if strings.Contains(got, "ghcr.io") {
				t.Fatalf("GetDockerImage() = %q — a public pull this host cannot make", got)
			}
			if !strings.HasPrefix(got, registry+"/") {
				t.Errorf("GetDockerImage() = %q, want it inside the configured registry %q", got, registry)
			}
		})
	}
}

// With no registry configured, the built-in image is unchanged: the shipped
// public reference is the right answer for an ordinary install.
func TestGetDockerImage_BuiltInGitImageUnchangedWithoutRegistry(t *testing.T) {
	im := registryOnlyManager("", nil)
	got, err := im.GetDockerImage(gitDepServer("uvx"), "uvx")
	if err != nil {
		t.Fatalf("GetDockerImage() error: %v", err)
	}
	if got != config.DefaultGitCapableImage {
		t.Errorf("GetDockerImage() = %q, want %q", got, config.DefaultGitCapableImage)
	}
}
