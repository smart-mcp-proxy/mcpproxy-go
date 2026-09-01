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

// DefaultGitCapableImage is mcpproxy's own git-capable image: the non-slim uv
// image, which ships git (verified: git version 2.39.5).
//
// It is deliberately NOT seeded into `docker_isolation.default_images`. Every
// key that map ships is written back into the operator's config file verbatim
// on the next save (SaveConfig marshals the whole merged map), which makes
// presence meaningless for a seeded key and leaves value equality as the only
// signal of intent — and value equality cannot express "I explicitly chose the
// image mcpproxy ships". An operator mirroring `uvx` who deliberately pointed
// `uvx-git` at this public image had that choice read as "unset" and silently
// replaced by their git-less mirror. Because the key is never seeded, its
// presence now means exactly one thing: the operator typed it.
//
// The registry half is split out so a configured `docker_isolation.registry`
// can qualify it — an air-gapped host that set only `registry` cannot pull
// ghcr.io at all. See core.IsolationManager.builtInGitCapableImage.
const (
	// GitCapableImageRegistry is the public registry the built-in image lives
	// on when the operator configured none of their own.
	GitCapableImageRegistry = "ghcr.io"
	// DefaultDockerRegistry is the value `docker_isolation.registry` ships
	// with. It is Docker Hub, i.e. "the public default", so it says nothing
	// about an operator running a mirror — and the built-in git-capable image
	// is not published there, so rewriting it onto this host would turn a
	// working pull into a guaranteed failure.
	DefaultDockerRegistry = "docker.io"
	// GitCapableImageRepo is DefaultGitCapableImage without its registry host.
	GitCapableImageRepo = "astral-sh/uv:python3.13-bookworm"
	// DefaultGitCapableImage is the fully-qualified built-in image.
	DefaultGitCapableImage = GitCapableImageRegistry + "/" + GitCapableImageRepo
)

var (
	builtInImagesOnce sync.Once
	builtInImages     map[string]string
)

// IsBuiltInDefaultImage reports whether image is exactly the value mcpproxy
// ships for that `default_images` key — i.e. whether the entry is still
// mcpproxy's own choice rather than the operator's.
//
// It answers that question only for the RUNTIME keys mcpproxy actually seeds
// ("uvx", "npx", …). For those, the merge makes presence meaningless — every
// built-in key is present in every loaded config, and is written back on the
// next save — so value equality is the only distinction that survives, and
// treating a shipped default as a deliberate choice is how an air-gapped
// install ends up pulling a public image.
//
// It is NOT how GitCapableImageKey is decided: that key is never seeded, so
// its presence is the operator's intent (see DefaultGitCapableImage).
//
// An operator who deliberately configures the shipped value for a runtime key
// is not harmed by being read as "unset": every branch that consults this then
// falls back to their other entries, which point at the same place the shipped
// value does unless they are mirroring — in which case the mirror is what they
// wanted.
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
