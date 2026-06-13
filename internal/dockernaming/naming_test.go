package dockernaming

import (
	"strings"
	"testing"
)

func TestSanitizeServerName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Regression cases for MCP-2123: official-registry servers carry a
		// dotted namespace AND a slash (namespace/name). The dot MUST be
		// preserved (Docker allows it) while the slash becomes a hyphen, so the
		// scanner's container-name prefix matches what the launcher actually
		// named the container (mcpproxy-com.pulsemcp-google-flights-<suffix>).
		{name: "official registry namespaced", input: "com.pulsemcp/google-flights", expected: "com.pulsemcp-google-flights"},
		{name: "github official namespaced", input: "io.github.owner/repo", expected: "io.github.owner-repo"},

		// Parity with the launcher's existing sanitizer
		// (internal/upstream/core sanitizeServerNameForContainer).
		{name: "simple name", input: "github-server", expected: "github-server"},
		{name: "name with spaces", input: "my server name", expected: "my-server-name"},
		{name: "name with special characters", input: "server@#$%^&*()", expected: "server"},
		{name: "name starting with invalid character", input: "-invalid-start", expected: "server-invalid-start"},
		{name: "name with dots", input: "server.name.test", expected: "server.name.test"},
		{name: "name with underscores", input: "server_name_test", expected: "server_name_test"},
		{name: "empty name", input: "", expected: "server"},
		{name: "name with multiple consecutive special chars", input: "server!!!@@@name", expected: "server-name"},
		{name: "name ending with hyphens and dots", input: "server-name-..", expected: "server-name"},
		{name: "very long name", input: strings.Repeat("a", 250), expected: strings.Repeat("a", 200)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeServerName(tt.input); got != tt.expected {
				t.Errorf("SanitizeServerName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
