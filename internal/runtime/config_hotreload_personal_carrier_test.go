//go:build !server

package runtime

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 107 FR-040 (codex round 1 on PR-A): in the personal build the
// `auth_broker` block is an opaque carrier. The boot config holds it as the
// FILE spelled it (server-edition struct order: mode, token_endpoint,
// authorization_endpoint, …) while a PATCH /api/v1/config re-emits it from a
// generic map (lexical key order). DetectConfigChanges compares mcpServers
// with jsonEqual — the bytes of json.Marshal — so a carrier that kept the
// source bytes verbatim reported "mcpServers" changed on the first unrelated
// PATCH after boot and Runtime scheduled a full LoadConfiguredServers
// (reconnecting every upstream). The carrier canonicalises instead; this pins
// the runtime-level consequence.
func TestDetectConfigChanges_PersonalOpaqueAuthBrokerIsKeyOrderBlind(t *testing.T) {
	const fileOrder = `{
  "listen": "127.0.0.1:18108",
  "tools_limit": 15,
  "mcpServers": [{
    "name": "gh",
    "url": "https://gh.example.com/mcp",
    "protocol": "http",
    "enabled": true,
    "auth_broker": {
      "mode": "oauth_connect",
      "token_endpoint": "https://idp.example.com/token",
      "authorization_endpoint": "https://idp.example.com/authorize",
      "scopes": ["repo", "read:org"],
      "resource": "https://gh.example.com"
    }
  }]
}`

	var oldCfg config.Config
	require.NoError(t, json.Unmarshal([]byte(fileOrder), &oldCfg))
	require.Len(t, oldCfg.Servers, 1)
	require.NotNil(t, oldCfg.Servers[0].AuthBroker)

	// The PATCH path: marshal the live config, decode to a generic map with
	// UseNumber, merge an unrelated key, marshal (sorted keys), decode back.
	baseBytes, err := json.Marshal(&oldCfg)
	require.NoError(t, err)
	var baseMap map[string]interface{}
	require.NoError(t, json.Unmarshal(baseBytes, &baseMap))
	baseMap["tools_limit"] = 16
	mergedBytes, err := json.Marshal(baseMap)
	require.NoError(t, err)
	var newCfg config.Config
	require.NoError(t, json.Unmarshal(mergedBytes, &newCfg))

	result := DetectConfigChanges(&oldCfg, &newCfg)
	assert.Contains(t, result.ChangedFields, "tools_limit")
	assert.NotContains(t, result.ChangedFields, "mcpServers",
		"an unrelated PATCH must not report the opaque auth_broker carrier as a server change (it would reconnect every upstream)")
}
