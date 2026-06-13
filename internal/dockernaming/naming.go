// Package dockernaming centralises the rule for turning an MCPProxy server name
// into the sanitized token used inside a Docker container name
// (mcpproxy-<sanitized>-<suffix>).
//
// It exists so the component that NAMES a container at launch
// (internal/upstream/core) and the component that LOOKS UP a running container
// by name (internal/security/scanner) share one source of truth. They used to
// carry independent sanitizers that disagreed on '.': the launcher preserved it
// (Docker allows '.') while the scanner mapped it to '-'. For official-registry
// servers — whose names are a dotted namespace plus a slash, e.g.
// "com.pulsemcp/google-flights" — that mismatch meant the scanner's
// `docker ps --filter name=mcpproxy-<sanitized>-` prefix never matched the real
// container, so source extraction silently fell through to "No Source Available"
// (MCP-2123). Keeping the rule in one leaf package makes that drift impossible.
package dockernaming

import (
	"regexp"
	"strings"
)

var (
	invalidContainerChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)
	leadingAlphanumeric   = regexp.MustCompile(`^[a-zA-Z0-9]`)
)

// maxSanitizedLen bounds the sanitized token so the full container name
// (mcpproxy- prefix + token + - + 4-char suffix) stays well under Docker's
// 253-char limit.
const maxSanitizedLen = 200

// SanitizeServerName converts a server name into a valid Docker container-name
// token. Docker container names may contain [a-zA-Z0-9][a-zA-Z0-9_.-]*, so this
// preserves letters, digits, '_', '.', and '-' and replaces any other run of
// characters with a single '-'. It then collapses consecutive hyphens, ensures
// the result starts with an alphanumeric character, trims trailing '-'/'.', and
// truncates to a safe length.
func SanitizeServerName(name string) string {
	sanitized := invalidContainerChars.ReplaceAllString(name, "-")

	for strings.Contains(sanitized, "--") {
		sanitized = strings.ReplaceAll(sanitized, "--", "-")
	}

	if sanitized != "" && !leadingAlphanumeric.MatchString(sanitized) {
		sanitized = "server-" + sanitized
		for strings.Contains(sanitized, "--") {
			sanitized = strings.ReplaceAll(sanitized, "--", "-")
		}
	}

	sanitized = strings.TrimRight(sanitized, "-.")

	if sanitized == "" {
		sanitized = "server"
	}

	if len(sanitized) > maxSanitizedLen {
		sanitized = sanitized[:maxSanitizedLen]
		sanitized = strings.TrimRight(sanitized, "-.")
	}

	return sanitized
}
