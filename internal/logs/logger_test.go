package logs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Official modelcontextprotocol/registry server names are namespace/name
// (e.g. "io.github.evidai/polymarket-guard"). The per-server log filename is
// derived from the server name, so an unsanitized "/" would turn
// "server-io.github.evidai/polymarket-guard.log" into a nested directory
// (server-io.github.evidai/) instead of a single flat log file (MCP-1111).
func TestServerLogFilename_SanitizesPathSeparators(t *testing.T) {
	cases := []struct {
		name     string
		server   string
		expected string
	}{
		{"plain", "github", "server-github.log"},
		{"namespaced slash", "io.github.evidai/polymarket-guard", "server-io.github.evidai_polymarket-guard.log"},
		{"windows backslash", "ns\\name", "server-ns_name.log"},
		{"colon", "host:1234", "server-host_1234.log"},
		{"spaces and slashes", "a b/c", "server-a_b_c.log"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := serverLogFilename(tc.server)
			assert.Equal(t, tc.expected, got)
			// A sanitized filename must never contain an OS path separator.
			assert.NotContains(t, got, "/")
			assert.NotContains(t, got, "\\")
			assert.Equal(t, filepath.Base(got), got, "sanitized filename must be a single path element")
		})
	}
}

// Regression: creating a logger for a namespaced (slash-bearing) server name
// must produce a single flat log file, not a nested directory, and the tail
// reader must round-trip the same raw name back to that file.
func TestCreateUpstreamServerLogger_NamespacedNameFlatFile(t *testing.T) {
	// Use os.MkdirTemp (not t.TempDir) with a best-effort cleanup: the lumberjack
	// writer keeps the log file handle open for the lifetime of the logger, and
	// Windows cannot remove an open file. t.TempDir's cleanup asserts RemoveAll
	// succeeds and would fail the test on Windows; a non-asserting cleanup mirrors
	// the existing TestE2E_LogRotation pattern.
	logDir, err := os.MkdirTemp("", "mcpproxy-logtest-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(logDir) })
	const serverName = "io.github.evidai/polymarket-guard"

	cfg := DefaultLogConfig()
	cfg.LogDir = logDir
	cfg.EnableFile = true
	cfg.EnableConsole = false

	logger, err := CreateUpstreamServerLogger(cfg, serverName)
	require.NoError(t, err)
	logger.Info("hello from polymarket-guard")
	_ = logger.Sync()

	// The flat file exists directly in logDir.
	flatPath := filepath.Join(logDir, "server-io.github.evidai_polymarket-guard.log")
	_, err = os.Stat(flatPath)
	require.NoError(t, err, "expected a single flat log file at %s", flatPath)

	// No nested directory was created from the "/" in the server name.
	nestedDir := filepath.Join(logDir, "server-io.github.evidai")
	_, err = os.Stat(nestedDir)
	assert.True(t, os.IsNotExist(err), "no nested directory should be created from a namespaced server name")

	// The tail reader resolves the same raw name back to the flat file.
	lines, err := ReadUpstreamServerLogTail(cfg, serverName, 10)
	require.NoError(t, err)
	require.NotEmpty(t, lines, "tail reader must round-trip the namespaced server name to its flat log file")
	assert.Contains(t, lines[len(lines)-1], "hello from polymarket-guard")
}
