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
// — the diagnostics package must not import upstream/core, and (like
// supervisor.usesDockerIsolation mirrors ShouldIsolate) faithfulness for the
// display path matters more than sharing the implementation. Unknown commands
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
