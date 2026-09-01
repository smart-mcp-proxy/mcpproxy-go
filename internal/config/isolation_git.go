package config

import "strings"

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
// It is also the hard fallback when the key is absent from the configured
// map — configs written before this key existed persist their own
// default_images and are not re-seeded on load, and falling through to the
// generic alpine fallback there would be worse than the bug being fixed.
const DefaultGitCapableImage = "ghcr.io/astral-sh/uv:python3.13-bookworm"

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
