package core

import (
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

const (
	slimUvImage = "ghcr.io/astral-sh/uv:python3.13-bookworm-slim"
	gitUvImage  = "ghcr.io/astral-sh/uv:python3.13-bookworm"
)

func gitTestManager(images map[string]string) *IsolationManager {
	global := config.DefaultDockerIsolationConfig()
	global.Enabled = true
	if images != nil {
		global.DefaultImages = images
	}
	return NewIsolationManagerWithLogger(global, zap.NewNop())
}

// #1143: a `uvx --from …git+https://…` server is unusable under Docker
// isolation because the default uv image is -slim and has no git:
//
//	× Failed to resolve `--with` requirement
//	╰─▶ Git operation failed
//
// The runtime already has the whole ServerConfig here, so it can pick the
// git-capable image for exactly the servers that need it instead of making
// every user pull a ~1.5GB image.
func TestGetDockerImage_GitDependencySelectsGitCapableImage(t *testing.T) {
	im := gitTestManager(nil)
	srv := &config.ServerConfig{
		Name:    "git-server",
		Command: "uvx",
		Args:    []string{"--from", "mcp-thing@git+https://github.com/o/r", "mcp-thing"},
	}

	got, err := im.GetDockerImage(srv, im.DetectRuntimeType(srv.Command))
	if err != nil {
		t.Fatalf("GetDockerImage() error: %v", err)
	}
	if got != gitUvImage {
		t.Errorf("GetDockerImage() = %q, want the git-capable %q", got, gitUvImage)
	}
}

// The swap must be narrow: a plain PyPI server keeps the small slim image.
func TestGetDockerImage_NoGitDependencyKeepsSlimImage(t *testing.T) {
	im := gitTestManager(nil)
	srv := &config.ServerConfig{Name: "plain", Command: "uvx", Args: []string{"mcp-server-fetch"}}

	got, err := im.GetDockerImage(srv, im.DetectRuntimeType(srv.Command))
	if err != nil {
		t.Fatalf("GetDockerImage() error: %v", err)
	}
	if got != slimUvImage {
		t.Errorf("GetDockerImage() = %q, want %q", got, slimUvImage)
	}
}

// An explicit per-server image override always wins — the user asked for it.
func TestGetDockerImage_PerServerOverrideBeatsGitSelection(t *testing.T) {
	im := gitTestManager(nil)
	srv := &config.ServerConfig{
		Name:      "pinned",
		Command:   "uvx",
		Args:      []string{"--from", "git+https://github.com/o/r", "srv"},
		Isolation: &config.IsolationConfig{Image: "my-registry.test/uv-with-git:1"},
	}

	got, err := im.GetDockerImage(srv, im.DetectRuntimeType(srv.Command))
	if err != nil {
		t.Fatalf("GetDockerImage() error: %v", err)
	}
	if got != "my-registry.test/uv-with-git:1" {
		t.Errorf("GetDockerImage() = %q, want the per-server override", got)
	}
}

// Operators retarget the git-capable image through its named default_images
// key rather than editing every server.
func TestGetDockerImage_GitCapableKeyIsRetargetable(t *testing.T) {
	images := config.DefaultDockerIsolationConfig().DefaultImages
	images[config.GitCapableImageKey] = "my-registry.test/uv-git:pinned"
	im := gitTestManager(images)

	srv := &config.ServerConfig{
		Name:    "git-server",
		Command: "uvx",
		Args:    []string{"--from", "git+https://github.com/o/r", "srv"},
	}

	got, err := im.GetDockerImage(srv, im.DetectRuntimeType(srv.Command))
	if err != nil {
		t.Fatalf("GetDockerImage() error: %v", err)
	}
	if got != "my-registry.test/uv-git:pinned" {
		t.Errorf("GetDockerImage() = %q, want the retargeted git image", got)
	}
}

// Upgrade path. A config written before this key existed persists its own
// default_images map, but that map is decoded INTO the built-in one, so the
// key is present after load and the substitution happens
// (config.TestDefaultImagesMergeOverBuiltInsOnLoad pins the merge).
//
// Here the key is genuinely absent — a hand-built or PATCH-replaced map — and
// the operator's own entry for the runtime is the answer, not a hardcoded
// public image. See TestGetDockerImage_AbsentGitKeyRespectsOperatorRuntimeDefault
// for why: a mirrored host cannot pull ghcr.io at all.
func TestGetDockerImage_MapWithoutGitKeyUsesTheConfiguredRuntimeImage(t *testing.T) {
	legacy := map[string]string{
		"uvx":    slimUvImage,
		"python": slimUvImage,
		"npx":    "node:22",
	}
	im := gitTestManager(legacy)

	srv := &config.ServerConfig{
		Name:    "git-server",
		Command: "uvx",
		Args:    []string{"--from", "git+https://github.com/o/r", "srv"},
	}

	got, err := im.GetDockerImage(srv, im.DetectRuntimeType(srv.Command))
	if err != nil {
		t.Fatalf("GetDockerImage() error: %v", err)
	}
	if got != slimUvImage {
		t.Errorf("GetDockerImage() = %q, want the configured runtime image %q", got, slimUvImage)
	}
}

// node:22 already has git (verified: git version 2.39.5) — no swap, no 1.6GB
// second image.
func TestGetDockerImage_NpxGitDependencyUnchanged(t *testing.T) {
	im := gitTestManager(nil)
	srv := &config.ServerConfig{
		Name:    "node-git",
		Command: "npx",
		Args:    []string{"git+https://github.com/o/r"},
	}

	got, err := im.GetDockerImage(srv, im.DetectRuntimeType(srv.Command))
	if err != nil {
		t.Fatalf("GetDockerImage() error: %v", err)
	}
	if got != "docker.io/library/node:22" {
		t.Errorf("GetDockerImage() = %q, want %q", got, "docker.io/library/node:22")
	}
}

// The REST/tray placeholder surface must show the image that would actually be
// used, or the user reads "…-slim" while the container runs the git image.
func TestResolveDefaults_ReflectsGitCapableSelection(t *testing.T) {
	im := gitTestManager(nil)
	srv := &config.ServerConfig{
		Name:    "git-server",
		Command: "uvx",
		Args:    []string{"--from", "git+https://github.com/o/r", "srv"},
	}

	defaults := im.ResolveDefaults(srv)
	if defaults == nil {
		t.Fatal("ResolveDefaults() = nil")
	}
	if defaults.Image != gitUvImage {
		t.Errorf("ResolveDefaults().Image = %q, want %q", defaults.Image, gitUvImage)
	}
}
