package diagnostics

import (
	"fmt"
	"path/filepath"
	"strings"
)

// RuntimeAwareRemediation returns an enriched, context-specific remediation
// message for codes that support per-server enrichment, or "" to fall back to
// the static CatalogEntry.UserMessage.
//
// Today it enriches only DockerExecNotFound (MCP-2909): the in-container
// interpreter was missing because the chosen Docker image lacks it. The field
// report that motivated this — a `uvx` server pinned via a per-server
// `isolation.image: "python:3.11"` override — failed at exec time because stock
// `python:3.11` has no `uvx` (uv is a separate Astral tool). The static catalog
// message is too generic to self-resolve, so we name (a) the detected runtime,
// (b) the recommended runtime-default image, and (c) when a per-server image
// override is the likely culprit.
//
// This is diagnostics-only: it never changes classification or image selection.
func RuntimeAwareRemediation(code Code, hints ClassifierHints) string {
	if code == DockerMissingToolchain {
		return missingToolchainRemediation(hints)
	}
	if code != DockerExecNotFound || hints.DockerCommand == "" {
		return ""
	}

	runtimeType := detectDockerRuntimeType(hints.DockerCommand)
	recommended := hints.DockerDefaultImages[runtimeType]
	override := strings.TrimSpace(hints.DockerImageOverride)

	var b strings.Builder
	fmt.Fprintf(&b, "This `%s` server's Docker image has no `%s` interpreter, so the container could not start it.", runtimeType, runtimeType)

	if override != "" {
		fmt.Fprintf(&b, " The per-server `isolation.image` override `%s` is the likely culprit", override)
		if recommended != "" && override != recommended {
			fmt.Fprintf(&b, " — it differs from the recommended image for `%s`", runtimeType)
		}
		b.WriteString(".")
	}

	switch {
	case recommended != "" && override != "":
		fmt.Fprintf(&b, " The recommended image for `%s` is `%s`. Remove the per-server `isolation.image` override to inherit it, or pick an image that includes `%s`.", runtimeType, recommended, runtimeType)
	case recommended != "":
		fmt.Fprintf(&b, " The recommended image for `%s` is `%s`. Pick an image that includes `%s`.", runtimeType, recommended, runtimeType)
	default:
		fmt.Fprintf(&b, " Pick an image that includes `%s`.", runtimeType)
	}

	return b.String()
}

// detectDockerRuntimeType maps a server's configured command to its runtime
// type key (the same keys used by config.DockerIsolationConfig.DefaultImages).
//
// It is a deliberately small, side-effect-free mirror of
// core.IsolationManager.DetectRuntimeType (internal/upstream/core/isolation.go)
// — the diagnostics package must not import upstream/core, and faithfulness for
// the display path matters more than sharing the implementation. (The sibling
// question, "is this server Docker-isolated at all", is NOT mirrored:
// supervisor.usesDockerIsolation delegates to config.ResolveIsolation, the
// resolver the spawn path branches on.) Unknown commands
// fall back to the base command name so the message still names something
// concrete rather than a generic "interpreter".
func detectDockerRuntimeType(command string) string {
	cmdName := filepath.Base(command)
	switch cmdName {
	case "python", "python3", "python3.11", "python3.12", "python3.13":
		return "python"
	case "uvx":
		return "uvx"
	case "pip", "pip3":
		return "pip"
	case "pipx":
		return "pipx"
	case "node":
		return "node"
	case "npm":
		return "npm"
	case "npx":
		return "npx"
	case "yarn":
		return "yarn"
	case "go":
		return "go"
	case "cargo":
		return "cargo"
	case "rustc":
		return "rustc"
	case "ruby":
		return "ruby"
	case "gem":
		return "gem"
	case "php":
		return "php"
	case "composer":
		return "composer"
	default:
		lower := strings.ToLower(cmdName)
		if strings.Contains(lower, "python") {
			return "python"
		}
		if strings.Contains(lower, "node") {
			return "node"
		}
		return cmdName
	}
}

// gitCapableImageKey mirrors config.GitCapableImageKey — the default_images key
// naming the git-capable image mcpproxy substitutes for a git dependency
// (#1143). It is mirrored rather than imported for the same reason
// detectDockerRuntimeType is: this package stays dependency-free so it can be
// used from every layer. TestGitCapableImageKeyMatchesConfig pins the two
// together.
const gitCapableImageKey = "uvx-git"

// builtInGitCapableImage / builtInPythonRunnerImage mirror the VALUES config
// ships for the git-capable key and for every Python package runner.
//
// Values, not presence, are what say whether mcpproxy's automatic selection
// actually ran: a config file's `default_images` is merged INTO the built-in
// map, so both keys are populated in every install, and an operator who
// retargeted their runtime at a mirror keeps a shipped `uvx-git` they never
// chose. In that case core.IsolationManager.resolveDefaultImage deliberately
// does NOT substitute — it will not pull a public image behind a mirrored host
// — so claiming an automatic swap here would name an image the install never
// ran. TestMirroredBuiltInImagesMatchConfig pins these to config.
const (
	builtInGitCapableImage   = "ghcr.io/astral-sh/uv:python3.13-bookworm"
	builtInPythonRunnerImage = "ghcr.io/astral-sh/uv:python3.13-bookworm-slim"
)

// missingToolchainRemediation explains a DockerMissingToolchain failure: the
// container ran, but the image lacks a tool the server calls. The git case is
// special — mcpproxy now selects a git-capable image automatically (#1143), so
// the message must say so, or it sends the user editing config for a problem
// that is already handled (and leaves the real remaining cause — a per-server
// `isolation.image` override, which opts OUT of that selection — unnamed).
func missingToolchainRemediation(hints ClassifierHints) string {
	runtimeType := ""
	if hints.DockerCommand != "" {
		runtimeType = detectDockerRuntimeType(hints.DockerCommand)
	}
	override := strings.TrimSpace(hints.DockerImageOverride)

	var b strings.Builder
	if runtimeType != "" {
		fmt.Fprintf(&b, "This Docker-isolated `%s` server ran, but its image is missing a command it needs.", runtimeType)
	} else {
		b.WriteString("This Docker-isolated server ran, but its image is missing a command it needs.")
	}

	if !hintsHaveGitDependency(hints) {
		if override != "" {
			fmt.Fprintf(&b, " The per-server `isolation.image` override `%s` is the likely culprit — remove it to inherit the runtime default, or pick an image that ships the missing tool.", override)
		} else {
			b.WriteString(" Pin an `isolation.image` that ships the missing tool (or build one) for this server.")
		}
		return b.String()
	}

	rawGitImage, gitKeySet := hints.DockerDefaultImages[gitCapableImageKey]
	gitImage := strings.TrimSpace(rawGitImage)
	runtimeImage := strings.TrimSpace(hints.DockerDefaultImages[runtimeType])
	// An explicitly emptied key is the documented opt-out, not an absent one.
	optedOut := gitKeySet && gitImage == ""
	// The operator retargeted this runtime at their own registry and left the
	// git key at the value mcpproxy ships: no substitution happened, and the
	// server ran on their image. Mirrors the resolver's own rule.
	deferredToRuntimeImage := !optedOut && runtimeImage != "" && runtimeImage != builtInPythonRunnerImage &&
		(gitImage == "" || gitImage == builtInGitCapableImage)

	b.WriteString(" It installs from a git URL, so the image must contain `git`.")
	switch {
	case override != "" && gitImage != "":
		fmt.Fprintf(&b, " The per-server `isolation.image` override `%s` is the likely culprit: it opts out of the automatic git-capable image selection. Remove the override to inherit `%s`, or pin an image that ships git.", override, gitImage)
	case override != "":
		fmt.Fprintf(&b, " The per-server `isolation.image` override `%s` is the likely culprit: it opts out of the automatic git-capable image selection. Remove the override, or pin an image that ships git.", override)
	case optedOut:
		fmt.Fprintf(&b, " `docker_isolation.default_images.%s` is set to `\"\"`, which opts out of the automatic git-capable image selection, so this server runs on your `%s` image. Remove the empty value, or point that key at an image that ships git.", gitCapableImageKey, runtimeType)
	case deferredToRuntimeImage:
		fmt.Fprintf(&b, " No git-capable image was substituted here: your `%s` entry (`%s`) is not the image mcpproxy ships, so this install pulls from its own registry and mcpproxy kept the server on your image rather than reaching outside it for the public default. Set `docker_isolation.default_images.%s` to an image in your registry that ships git.", runtimeType, runtimeImage, gitCapableImageKey)
	case gitImage != "":
		fmt.Fprintf(&b, " mcpproxy selects a git-capable image (`%s`, from `docker_isolation.default_images.%s`) automatically for git dependencies — if this still fails, that key points at an image without git, or this server is running an older mcpproxy.", gitImage, gitCapableImageKey)
	default:
		fmt.Fprintf(&b, " mcpproxy selects a git-capable image automatically for git dependencies — set `docker_isolation.default_images.%s` to an image that ships git if this still fails.", gitCapableImageKey)
	}
	return b.String()
}

// gitCapableRuntimeTypes mirrors config.gitCapableRuntimeTypes — the runtime
// types whose default image is the git-less slim uv image, and therefore the
// only ones config.NeedsGitCapableImage substitutes an image for. Mirrored for
// the same reason gitCapableImageKey is; TestMirroredGitRuntimeGateMatchesConfig
// pins the two together.
var gitCapableRuntimeTypes = map[string]bool{
	"uvx":     true,
	"python":  true,
	"python3": true,
	"pip":     true,
	"pipx":    true,
}

// hintsHaveGitDependency reports whether mcpproxy's automatic git-capable image
// selection applies to this server — i.e. whether config.NeedsGitCapableImage
// would fire for it. BOTH halves of that gate matter: the `git+` marker pip/uv
// URLs require, AND the Python-package-runner runtime type. node:22 already
// ships git and is deliberately left alone, so a node/go/ruby server never gets
// the substitution — claiming otherwise promises a fix that never runs and
// points at a `default_images` key with no effect on that server.
func hintsHaveGitDependency(hints ClassifierHints) bool {
	if hints.DockerCommand == "" || !gitCapableRuntimeTypes[detectDockerRuntimeType(hints.DockerCommand)] {
		return false
	}
	for _, arg := range hints.DockerArgs {
		if strings.Contains(strings.ToLower(arg), "git+") {
			return true
		}
	}
	return false
}
