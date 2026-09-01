package config

import (
	"strings"
	"sync"
)

// GitCapableImageKey is the `docker_isolation.default_images` key naming the
// image used for Python package runners that install from a git URL.
//
// The shipped Python default is Astral's *slim* uv image, which does not
// contain git:
//
//	$ docker run --rm --entrypoint sh ghcr.io/astral-sh/uv:python3.13-bookworm-slim -c 'git --version'
//	sh: 1: git: not found
//
// so `uvx --from …git+https://…` (the only distribution channel for a large
// share of MCP servers) failed under Docker isolation with "Git executable not
// found" / "Git operation failed" (#1143). Raising the global Python default to
// the non-slim image would cost every user a ~1.5GB pull for a case many never
// hit, so the git-capable image is selected per-server, from its own named key,
// which operators can retarget to a mirror or a custom build.
const GitCapableImageKey = "uvx-git"

// DefaultGitCapableImage is the built-in value for GitCapableImageKey: the
// non-slim uv image, which ships git (verified: git version 2.39.5).
//
// It is a PUBLIC ghcr.io URL, which makes it the wrong answer for a mirrored or
// air-gapped install: that host cannot pull it at all. Key absence is no
// protection either — a config file's `default_images` is decoded INTO the
// built-in map rather than replacing it, so this value is present in the merged
// map of every install, including one whose own `uvx` entry points at an
// internal registry (TestDefaultImagesMergeOverBuiltInsOnLoad). The resolver
// therefore treats "the key still holds this exact value" as "nobody chose it"
// and lets the operator's own runtime image win instead — see
// IsBuiltInDefaultImage and core.IsolationManager.resolveDefaultImage.
const DefaultGitCapableImage = "ghcr.io/astral-sh/uv:python3.13-bookworm"

var (
	builtInImagesOnce sync.Once
	builtInImages     map[string]string
)

// IsBuiltInDefaultImage reports whether image is exactly the value mcpproxy
// ships for that `default_images` key — i.e. whether the entry is still
// mcpproxy's own choice rather than the operator's.
//
// The comparison exists because the merge makes presence meaningless: every
// built-in key is present in every loaded config, so "is this key set?" cannot
// distinguish a deliberate operator choice from a shipped default, and treating
// a shipped default as a choice is how an air-gapped install ends up pulling a
// public image. Value equality is the distinction that survives the merge.
//
// An operator who deliberately configures the shipped value is not harmed by
// being read as "unset": every branch that consults this then falls back to
// their other entries, which point at the same place the shipped value does
// unless they are mirroring — in which case the mirror is what they wanted.
func IsBuiltInDefaultImage(key, image string) bool {
	builtInImagesOnce.Do(func() {
		builtInImages = DefaultDockerIsolationConfig().DefaultImages
	})
	return builtInImages[key] == strings.TrimSpace(image)
}

// gitCapableRuntimeTypes are the runtime types whose default image is the
// git-less slim uv image. Node's default (node:22) already ships git — verified
// git version 2.39.5 — so npx/node git dependencies work today and must not be
// pushed onto a second large image.
var gitCapableRuntimeTypes = map[string]bool{
	"uvx":     true,
	"python":  true,
	"python3": true,
	"pip":     true,
	"pipx":    true,
}

// NeedsGitCapableImage reports whether a server with the given detected runtime
// type and args needs an isolation image that contains git — i.e. it is a
// Python package runner asked to install from a git URL (`git+https://`,
// `pkg@git+ssh://`, …).
//
// It deliberately looks only at the `git+` marker that pip/uv URLs require: a
// package merely *named* something with "git" in it does not match.
func NeedsGitCapableImage(runtimeType string, args []string) bool {
	if !gitCapableRuntimeTypes[runtimeType] {
		return false
	}
	for _, arg := range args {
		if strings.Contains(strings.ToLower(arg), "git+") {
			return true
		}
	}
	return false
}
