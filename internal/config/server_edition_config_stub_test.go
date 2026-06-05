//go:build !server

package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerEditionKeyAlias_PersonalEdition verifies the legacy "teams" -> new
// "server_edition" key alias (MCP-1085) is honored in the default (personal)
// build too: the key is accepted without error and the config always serializes
// with the canonical key. The stub ServerEditionConfig has no fields, so we only
// assert key-level behavior here.
func TestServerEditionKeyAlias_PersonalEdition(t *testing.T) {
	// Legacy key is accepted (no error) and maps onto ServerEdition.
	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(`{"teams": {}}`), &cfg))
	require.NotNil(t, cfg.ServerEdition, "legacy teams key should populate ServerEdition in personal edition")

	// Output always uses the canonical key, never the legacy one.
	data, err := json.Marshal(&cfg)
	require.NoError(t, err)
	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))
	_, hasLegacy := raw["teams"]
	assert.False(t, hasLegacy, "legacy teams key must never be written")
}
