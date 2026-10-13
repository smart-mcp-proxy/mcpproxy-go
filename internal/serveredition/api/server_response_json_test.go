//go:build server

package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Regression for the #937 embedding landmine.
//
// config.ServerConfig grew custom MarshalJSON/UnmarshalJSON to record whether a
// parsed document carried a "quarantined" key. Go promotes those methods to any
// struct that EMBEDS it, so ServerResponse — which embeds *config.ServerConfig
// and adds `ownership` / `user_enabled` — was handed wholesale to the embedded
// config by encoding/json:
//
//   - encode silently dropped `ownership` and `user_enabled` from every
//     server-edition API response (a silent API regression, no test caught it);
//   - decode failed with "json: Unmarshal(nil *config.Alias)" because the
//     embedded pointer is nil before decoding starts (this one turned every test
//     in this package red).
//
// This test fails if the promotion ever comes back — e.g. if ServerResponse's
// explicit JSON methods are removed, or if new wrapper fields are added to the
// struct without being added to the methods.
func TestServerResponse_JSONRoundTrip(t *testing.T) {
	userEnabled := true
	resp := &ServerResponse{
		ServerConfig: &config.ServerConfig{
			Name:     "shared-srv",
			URL:      "https://example.test/mcp",
			Protocol: "http",
			Enabled:  true,
			Shared:   true,
		},
		Ownership:   "shared",
		UserEnabled: &userEnabled,
	}

	encoded, err := json.Marshal(resp)
	require.NoError(t, err)

	// The wire form must be FLAT and must carry all three groups of fields.
	var wire map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &wire))
	assert.Contains(t, wire, "name", "embedded server fields must survive")
	assert.Contains(t, wire, "url")
	assert.Contains(t, wire, "enabled")
	assert.Contains(t, wire, "ownership", "wrapper field must not be dropped by method promotion")
	assert.Contains(t, wire, "user_enabled", "wrapper field must not be dropped by method promotion")

	// And it must decode back into a fully populated value.
	var decoded ServerResponse
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.NotNil(t, decoded.ServerConfig, "embedded config must be allocated by UnmarshalJSON")
	assert.Equal(t, "shared-srv", decoded.Name)
	assert.Equal(t, "https://example.test/mcp", decoded.URL)
	assert.Equal(t, "http", decoded.Protocol)
	assert.True(t, decoded.Enabled)
	assert.True(t, decoded.Shared)
	assert.Equal(t, "shared", decoded.Ownership)
	require.NotNil(t, decoded.UserEnabled)
	assert.True(t, *decoded.UserEnabled)
}

// A nil embedded config must not panic or lose the wrapper fields, and
// `user_enabled` must stay omitted when there is no per-user preference.
func TestServerResponse_MarshalJSON_NilConfigAndOmittedPreference(t *testing.T) {
	encoded, err := json.Marshal(&ServerResponse{Ownership: "personal"})
	require.NoError(t, err)

	var wire map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &wire))
	assert.Equal(t, `"personal"`, string(wire["ownership"]))
	assert.NotContains(t, wire, "user_enabled", "omitempty must still apply")
}

// The #937 presence semantics of the embedded config must survive the wrapper:
// an unstated `quarantined` stays absent, an explicit one is written through.
func TestServerResponse_PreservesQuarantinePresenceSemantics(t *testing.T) {
	t.Run("unstated quarantined:false is omitted", func(t *testing.T) {
		encoded, err := json.Marshal(&ServerResponse{
			ServerConfig: &config.ServerConfig{Name: "s", Command: "c"},
			Ownership:    "personal",
		})
		require.NoError(t, err)

		var wire map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(encoded, &wire))
		assert.NotContains(t, wire, "quarantined",
			"the wrapper must not fabricate an operator statement")
	})

	t.Run("quarantined:true is always written", func(t *testing.T) {
		encoded, err := json.Marshal(&ServerResponse{
			ServerConfig: &config.ServerConfig{Name: "s", Command: "c", Quarantined: true},
			Ownership:    "personal",
		})
		require.NoError(t, err)

		var wire map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(encoded, &wire))
		assert.Equal(t, "true", string(wire["quarantined"]))
	})

	t.Run("decode records the presence bit on the embedded config", func(t *testing.T) {
		var stated ServerResponse
		require.NoError(t, json.Unmarshal(
			[]byte(`{"name":"s","command":"c","quarantined":false,"ownership":"personal"}`), &stated))
		require.NotNil(t, stated.ServerConfig)
		assert.True(t, stated.QuarantineExplicitlySet())

		var unstated ServerResponse
		require.NoError(t, json.Unmarshal(
			[]byte(`{"name":"s","command":"c","ownership":"personal"}`), &unstated))
		require.NotNil(t, unstated.ServerConfig)
		assert.False(t, unstated.QuarantineExplicitlySet())
	})
}

// Lists of responses (the actual endpoint payload) must round-trip too — this is
// the shape that was returning ownership-less objects to the Web UI.
func TestServerListResponse_RoundTrip(t *testing.T) {
	userEnabled := false
	payload := ServerListResponse{
		Personal: []*ServerResponse{{
			ServerConfig: &config.ServerConfig{Name: "p1", Command: "c", Enabled: true},
			Ownership:    "personal",
		}},
		Shared: []*ServerResponse{{
			ServerConfig: &config.ServerConfig{Name: "s1", URL: "https://x.test", Shared: true},
			Ownership:    "shared",
			UserEnabled:  &userEnabled,
		}},
	}

	encoded, err := json.Marshal(payload)
	require.NoError(t, err)

	var decoded ServerListResponse
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Len(t, decoded.Personal, 1)
	require.Len(t, decoded.Shared, 1)
	assert.Equal(t, "p1", decoded.Personal[0].Name)
	assert.Equal(t, "personal", decoded.Personal[0].Ownership)
	assert.Nil(t, decoded.Personal[0].UserEnabled)
	assert.Equal(t, "s1", decoded.Shared[0].Name)
	assert.Equal(t, "shared", decoded.Shared[0].Ownership)
	require.NotNil(t, decoded.Shared[0].UserEnabled)
	assert.False(t, *decoded.Shared[0].UserEnabled)
}
