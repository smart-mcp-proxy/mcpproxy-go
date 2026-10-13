//go:build !server

package runtime

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 107 FR-039 part 2 / FR-040 (T055, personal twin): the personal build
// carries `server_edition` as an opaque canonical-JSON block, so the detector
// cannot tell `admin_emails` (live under the server build) from the keys that
// pin a restart there. It compares the raw bytes and reports ANY difference as
// `server_edition`, restart-pinned — never a partial, never "no changes".
func TestDetectConfigChanges_PersonalServerEditionCarrierIsRestartPinned(t *testing.T) {
	const doc = `{
  "listen": "127.0.0.1:18109",
  "data_dir": "/d",
  "server_edition": {
    "enabled": true,
    "admin_emails": ["admin@example.com"],
    "oauth": {"provider": "oidc", "client_id": "client-one", "client_secret": "secret-one", "issuer_url": "https://idp.example.com"},
    "public_url": "https://mcp.example.com"
  }
}`
	load := func(t *testing.T, raw string) *config.Config {
		t.Helper()
		var cfg config.Config
		require.NoError(t, json.Unmarshal([]byte(raw), &cfg))
		require.NotNil(t, cfg.ServerEdition)
		return &cfg
	}

	t.Run("any byte change is server_edition, restart-pinned", func(t *testing.T) {
		oldCfg := load(t, doc)
		// admin_emails is live under the server build; the personal build has
		// no way to know that and must not pretend to.
		newCfg := load(t, `{"listen":"127.0.0.1:18109","data_dir":"/d","server_edition":{"enabled":true,"admin_emails":["admin@example.com","second@example.com"],"oauth":{"provider":"oidc","client_id":"client-one","client_secret":"secret-one","issuer_url":"https://idp.example.com"},"public_url":"https://mcp.example.com"}}`)

		result := DetectConfigChanges(oldCfg, newCfg)
		require.True(t, result.Success)
		assert.Equal(t, []string{"server_edition"}, result.ChangedFields)
		assert.NotContains(t, result.ChangedFields, "server_edition.admin_emails")
		assert.True(t, result.RequiresRestart, "the personal build cannot interpret the block, so every edit is restart-pinned")
		assert.False(t, result.AppliedImmediately)
		assert.Contains(t, result.RestartReason, "server_edition")
		assert.NotContains(t, result.RestartReason, "secret-one")
	})

	t.Run("key-order-only rewrite (PATCH round-trip) is not a change", func(t *testing.T) {
		oldCfg := load(t, doc)
		baseBytes, err := json.Marshal(oldCfg)
		require.NoError(t, err)
		var baseMap map[string]interface{}
		require.NoError(t, json.Unmarshal(baseBytes, &baseMap))
		baseMap["tools_limit"] = 16
		mergedBytes, err := json.Marshal(baseMap)
		require.NoError(t, err)
		var newCfg config.Config
		require.NoError(t, json.Unmarshal(mergedBytes, &newCfg))

		result := DetectConfigChanges(oldCfg, &newCfg)
		assert.Contains(t, result.ChangedFields, "tools_limit")
		assert.NotContains(t, result.ChangedFields, "server_edition")
		assert.False(t, result.RequiresRestart)
	})

	t.Run("block added or removed is server_edition, restart-pinned", func(t *testing.T) {
		withBlock := load(t, doc)
		var without config.Config
		require.NoError(t, json.Unmarshal([]byte(`{"listen":"127.0.0.1:18109","data_dir":"/d"}`), &without))
		require.Nil(t, without.ServerEdition)

		added := DetectConfigChanges(&without, withBlock)
		assert.Equal(t, []string{"server_edition"}, added.ChangedFields)
		assert.True(t, added.RequiresRestart)

		removed := DetectConfigChanges(withBlock, &without)
		assert.Equal(t, []string{"server_edition"}, removed.ChangedFields)
		assert.True(t, removed.RequiresRestart)
	})
}
