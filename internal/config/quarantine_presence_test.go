package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #937: `Quarantined` is a plain bool, so an ABSENT "quarantined" key is
// indistinguishable from an explicit `false`. That is what let a hand-written
// mcp_config.json entry be admitted unquarantined while the identical server
// added via `upstream add` was correctly gated: config load read "not
// quarantined" for a server that had simply never said anything.
//
// The presence bit is what the admission gate keys off, so it has to survive
// JSON decoding.
func TestServerConfig_QuarantineExplicitlySet(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantExplicit bool
		wantValue    bool
	}{
		{
			name:         "absent key is not explicit",
			raw:          `{"name":"handwritten","command":"./poison"}`,
			wantExplicit: false,
			wantValue:    false,
		},
		{
			name:         "explicit false is explicit",
			raw:          `{"name":"handwritten","command":"./poison","quarantined":false}`,
			wantExplicit: true,
			wantValue:    false,
		},
		{
			name:         "explicit true is explicit",
			raw:          `{"name":"handwritten","command":"./poison","quarantined":true}`,
			wantExplicit: true,
			wantValue:    true,
		},
		{
			name:         "explicit null counts as stated",
			raw:          `{"name":"handwritten","command":"./poison","quarantined":null}`,
			wantExplicit: true,
			wantValue:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sc ServerConfig
			require.NoError(t, json.Unmarshal([]byte(tt.raw), &sc))
			assert.Equal(t, tt.wantExplicit, sc.QuarantineExplicitlySet())
			assert.Equal(t, tt.wantValue, sc.Quarantined)
			// The rest of the struct must still decode normally — the custom
			// unmarshaler must not shadow any other field.
			assert.Equal(t, "handwritten", sc.Name)
			assert.Equal(t, "./poison", sc.Command)
		})
	}
}

// The presence bit is unexported, so it is invisible to reflection-based copies.
// CopyServerConfig is on the config-apply path; losing the bit there would make
// an explicitly-opted-out server look un-stated and get gated on the next sync.
func TestCopyServerConfig_PreservesQuarantinePresence(t *testing.T) {
	var sc ServerConfig
	require.NoError(t, json.Unmarshal([]byte(`{"name":"s","quarantined":false}`), &sc))
	require.True(t, sc.QuarantineExplicitlySet())

	assert.True(t, CopyServerConfig(&sc).QuarantineExplicitlySet())
}

// A server list parsed from a whole config file must carry the bit too — the
// admission gate runs on Config.Servers, not on individually-decoded structs.
func TestConfigLoad_ServersCarryQuarantinePresence(t *testing.T) {
	raw := `{
		"listen": "127.0.0.1:0",
		"mcpServers": [
			{"name":"stated","command":"a","quarantined":false},
			{"name":"unstated","command":"b"}
		]
	}`

	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(raw), &cfg))
	require.Len(t, cfg.Servers, 2)

	assert.True(t, cfg.Servers[0].QuarantineExplicitlySet(), "stated")
	assert.False(t, cfg.Servers[1].QuarantineExplicitlySet(), "unstated")
}
