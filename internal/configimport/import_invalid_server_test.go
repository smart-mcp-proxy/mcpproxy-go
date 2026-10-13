package configimport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RC4-IMPORT-001: a URL-only entry forced through the claude-desktop parser
// maps to a commandless stdio server. config.Load rejects that on the next
// boot (exit 4), so Import must report it as failed and never offer it for
// persistence, while valid entries in the same payload still import.
func TestImport_RejectsServerThatFailsConfigValidation(t *testing.T) {
	content := []byte(`{"mcpServers": {
		"broken": {"url": "https://example.com/mcp"},
		"good": {"command": "npx", "args": ["-y", "some-server"]}
	}}`)

	for _, skipQuarantine := range []bool{false, true} {
		result, err := Import(content, &ImportOptions{FormatHint: FormatClaudeDesktop, SkipQuarantine: skipQuarantine})
		require.NoError(t, err)

		require.Len(t, result.Imported, 1, "only the valid server may be importable")
		assert.Equal(t, "good", result.Imported[0].Server.Name)

		require.Len(t, result.Failed, 1)
		assert.Equal(t, "broken", result.Failed[0].Name)
		assert.Equal(t, "invalid_server", result.Failed[0].Error)
		assert.Contains(t, result.Failed[0].Details, "command is required")
		assert.Equal(t, 1, result.Summary.Failed)
		assert.Equal(t, 1, result.Summary.Imported)
	}
}
