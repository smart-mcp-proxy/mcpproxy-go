package server

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// boolPtr is declared in mcp_annotations_test.go (same package).

// --- Pure derivation: stdio install command ----------------------------------

func TestAddFromRegistry_BuildStdioFromInstallCmd(t *testing.T) {
	entry := &registries.ServerEntry{
		ID:         "everything",
		Name:       "everything",
		InstallCmd: "npx -y @modelcontextprotocol/server-everything",
	}

	cfg, err := buildServerConfigFromEntry(entry, &AddFromRegistryRequest{
		RegistryID: "pulse",
		ServerID:   "everything",
	}, true)

	require.NoError(t, err)
	assert.Equal(t, "everything", cfg.Name)
	assert.Equal(t, "stdio", cfg.Protocol)
	assert.Equal(t, "npx", cfg.Command)
	assert.Equal(t, []string{"-y", "@modelcontextprotocol/server-everything"}, cfg.Args)
	assert.Empty(t, cfg.URL)
}

// --- Pure derivation: http/remote URL ----------------------------------------

func TestAddFromRegistry_BuildHTTPFromURL(t *testing.T) {
	entry := &registries.ServerEntry{
		ID:   "context7",
		Name: "context7",
		URL:  "https://mcp.context7.com/mcp",
	}

	cfg, err := buildServerConfigFromEntry(entry, &AddFromRegistryRequest{
		RegistryID: "pulse",
		ServerID:   "context7",
	}, true)

	require.NoError(t, err)
	assert.Equal(t, "http", cfg.Protocol)
	assert.Equal(t, "https://mcp.context7.com/mcp", cfg.URL)
	assert.Empty(t, cfg.Command)
}

// --- Quarantine-by-default (CN-002): client cannot opt out -------------------

func TestAddFromRegistry_QuarantineFollowsGlobalDefault(t *testing.T) {
	entry := &registries.ServerEntry{ID: "x", Name: "x", InstallCmd: "npx x"}

	// Global quarantine ON → derived server is quarantined.
	on, err := buildServerConfigFromEntry(entry, &AddFromRegistryRequest{}, true)
	require.NoError(t, err)
	assert.True(t, on.Quarantined, "must quarantine when global default is on")

	// Global quarantine OFF → respects the global default (there is no request
	// field to force quarantine false on this path, so it always mirrors the
	// global setting).
	off, err := buildServerConfigFromEntry(entry, &AddFromRegistryRequest{}, false)
	require.NoError(t, err)
	assert.False(t, off.Quarantined)
}

// --- Refusal: nothing runnable -----------------------------------------------

func TestAddFromRegistry_NoInstallInfo(t *testing.T) {
	entry := &registries.ServerEntry{ID: "broken", Name: "broken"} // no cmd, no url

	cfg, err := buildServerConfigFromEntry(entry, &AddFromRegistryRequest{}, true)

	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.True(t, errors.Is(err, ErrNoInstallInfo))
	assert.Equal(t, "no_install_info", RegistryAddErrorCode(err))
}

// --- Refusal: required input missing, then satisfied -------------------------

func TestAddFromRegistry_MissingRequiredInput(t *testing.T) {
	entry := &registries.ServerEntry{
		ID:         "gh",
		Name:       "gh",
		InstallCmd: "npx github-mcp --token ${GITHUB_TOKEN}",
	}

	// Missing → refusal that names the variable.
	_, err := buildServerConfigFromEntry(entry, &AddFromRegistryRequest{}, true)
	require.Error(t, err)
	assert.Equal(t, "missing_required_input", RegistryAddErrorCode(err))
	var missing *MissingRequiredInputError
	require.True(t, errors.As(err, &missing))
	assert.Equal(t, []string{"GITHUB_TOKEN"}, missing.Names)

	// Supplied via env → accepted, env carried onto the config.
	cfg, err := buildServerConfigFromEntry(entry, &AddFromRegistryRequest{
		Env: map[string]string{"GITHUB_TOKEN": "ghp_x"},
	}, true)
	require.NoError(t, err)
	assert.Equal(t, "ghp_x", cfg.Env["GITHUB_TOKEN"])
}

// --- Name override + enabled default -----------------------------------------

func TestAddFromRegistry_NameOverrideAndEnabledDefault(t *testing.T) {
	entry := &registries.ServerEntry{ID: "id1", Name: "proposed", InstallCmd: "npx z"}

	// Default name = entry.Name, Enabled defaults to true.
	def, err := buildServerConfigFromEntry(entry, &AddFromRegistryRequest{}, true)
	require.NoError(t, err)
	assert.Equal(t, "proposed", def.Name)
	assert.True(t, def.Enabled)

	// Override name + explicit disable.
	ov, err := buildServerConfigFromEntry(entry, &AddFromRegistryRequest{
		Name:    "myname",
		Enabled: boolPtr(false),
	}, true)
	require.NoError(t, err)
	assert.Equal(t, "myname", ov.Name)
	assert.False(t, ov.Enabled)
}

// --- Orchestrator refusal: registry not found (no network) -------------------

func TestAddFromRegistry_RegistryNotFound(t *testing.T) {
	s := &Server{}
	cfg, err := s.AddServerFromRegistry(context.Background(), &AddFromRegistryRequest{
		RegistryID: "does-not-exist-zzz",
		ServerID:   "whatever",
	})
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Equal(t, "registry_not_found", RegistryAddErrorCode(err))
}

// --- Error-code mapper -------------------------------------------------------

func TestRegistryAddErrorCode(t *testing.T) {
	assert.Equal(t, "", RegistryAddErrorCode(nil))
	assert.Equal(t, "registry_not_found", RegistryAddErrorCode(registries.ErrRegistryNotFound))
	assert.Equal(t, "server_not_found", RegistryAddErrorCode(registries.ErrServerNotFound))
	assert.Equal(t, "no_install_info", RegistryAddErrorCode(ErrNoInstallInfo))
	assert.Equal(t, "duplicate_name", RegistryAddErrorCode(ErrDuplicateName))
	assert.Equal(t, "missing_required_input", RegistryAddErrorCode(&MissingRequiredInputError{Names: []string{"K"}}))
	assert.Equal(t, "", RegistryAddErrorCode(errors.New("some other error")))
}
