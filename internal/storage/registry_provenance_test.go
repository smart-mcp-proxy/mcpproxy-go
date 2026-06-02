package storage

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// MCP-866: a server's registry origin + provenance must survive a save→reload
// so the approval/quarantine view and the custom-origin skip_quarantine guard
// keep working after a restart.
func TestUpstreamServer_ProvenanceRoundTrips(t *testing.T) {
	m, err := NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	defer m.Close()

	require.NoError(t, m.SaveUpstreamServer(&config.ServerConfig{
		Name:                     "acme-widget",
		Protocol:                 "stdio",
		Command:                  "npx",
		Enabled:                  true,
		Quarantined:              true,
		SourceRegistryID:         "acme",
		SourceRegistryProvenance: config.RegistryProvenanceCustom,
	}))

	got, err := m.GetUpstreamServer("acme-widget")
	require.NoError(t, err)
	assert.Equal(t, "acme", got.SourceRegistryID)
	assert.Equal(t, config.RegistryProvenanceCustom, got.SourceRegistryProvenance)

	// And it surfaces through the quarantine listing (the approval view).
	q, err := m.ListQuarantinedUpstreamServers()
	require.NoError(t, err)
	require.Len(t, q, 1)
	assert.Equal(t, "acme", q[0].SourceRegistryID)
	assert.Equal(t, config.RegistryProvenanceCustom, q[0].SourceRegistryProvenance)
}
