//go:build server

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// legacyServerEditionFixture is the SC-005 fixture: removed server_edition
// keys, a retired auth_broker mode, a removed auth_broker leaf and
// store_idp_tokens: true — five load diagnostics under the server build.
func legacyServerEditionFixture(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "internal", "config", "testdata", "legacy_server_edition.json"))
	require.NoError(t, err)
	return path
}

// TestLoadCLIConfig_EmitsLoadDiagnostics pins Spec 107 FR-032 on the CLI
// door (codex round 2 on PR-A): `mcpproxy upstream add` in standalone mode,
// `telemetry enable|disable` and the API-key reset all load through
// loadCLIConfig and then SaveConfig the result, so under the server build the
// normaliser's drops would be written back with no operator-visible warning.
// The diagnostics must reach stderr — one line per diagnostic, the contract
// message verbatim, and never stdout (`-o json` consumers parse stdout).
func TestLoadCLIConfig_EmitsLoadDiagnostics(t *testing.T) {
	var stderr bytes.Buffer
	prev := cliDiagnosticsWriter
	cliDiagnosticsWriter = &stderr
	t.Cleanup(func() { cliDiagnosticsWriter = prev })

	cfg, err := loadCLIConfig(legacyServerEditionFixture(t))
	require.NoError(t, err)
	diags := cfg.LoadDiagnostics()
	require.Len(t, diags, 5, "fixture must still carry five diagnostics: %+v", diags)

	lines := strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n")
	require.Len(t, lines, len(diags), "exactly one stderr line per diagnostic; got:\n%s", stderr.String())
	for i, d := range diags {
		assert.Contains(t, lines[i], d.Message, "line %d carries the contract message", i)
		assert.Contains(t, lines[i], d.Key, "line %d names the key", i)
		assert.True(t, strings.HasPrefix(lines[i], "warning: "), "line %d is marked as a warning: %q", i, lines[i])
	}
}

// TestLoadCLIConfig_CleanFileEmitsNothing: a file with nothing to drop must
// not print anything (the common case stays byte-for-byte quiet).
func TestLoadCLIConfig_CleanFileEmitsNothing(t *testing.T) {
	var stderr bytes.Buffer
	prev := cliDiagnosticsWriter
	cliDiagnosticsWriter = &stderr
	t.Cleanup(func() { cliDiagnosticsWriter = prev })

	path := filepath.Join(t.TempDir(), "clean.json")
	cfg := config.DefaultConfig()
	cfg.APIKey = "clean-key"
	require.NoError(t, config.SaveConfig(cfg, path))

	loaded, err := loadCLIConfig(path)
	require.NoError(t, err)
	assert.Empty(t, loaded.LoadDiagnostics())
	assert.Empty(t, stderr.String())
}
