//go:build !server

package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadCLIConfig_PersonalBuildEmitsNoDiagnostics: the personal build
// carries server_edition and auth_broker as opaque blocks (FR-040) and
// records no diagnostics, so the CLI door prints nothing for the legacy
// fixture.
func TestLoadCLIConfig_PersonalBuildEmitsNoDiagnostics(t *testing.T) {
	var stderr bytes.Buffer
	prev := cliDiagnosticsWriter
	cliDiagnosticsWriter = &stderr
	t.Cleanup(func() { cliDiagnosticsWriter = prev })

	path, err := filepath.Abs(filepath.Join("..", "..", "internal", "config", "testdata", "legacy_server_edition.json"))
	require.NoError(t, err)
	cfg, err := loadCLIConfig(path)
	require.NoError(t, err)
	assert.Empty(t, cfg.LoadDiagnostics())
	assert.Empty(t, stderr.String())
}
