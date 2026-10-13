package config

import (
	"encoding/json"
	"os"
	"path/filepath"
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
			// Review finding: `null` is the codebase's "unset" marker elsewhere
			// (RFC 7396 merge-patch semantics on env_json, mcp.go). Counting it
			// as an operator statement let a templating tool or a serializer
			// that emits null for an unset field silently admit a first-seen
			// server unquarantined.
			name:         "explicit null is NOT a statement",
			raw:          `{"name":"handwritten","command":"./poison","quarantined":null}`,
			wantExplicit: false,
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

// Review finding (P1) — mcpproxy's OWN config save must not fabricate an
// operator statement.
//
// `Quarantined` carries `json:"quarantined"` with no omitempty and SaveConfig is
// a plain json.MarshalIndent, so the moment mcpproxy wrote the config file every
// server gained a literal `"quarantined": false`. Reading that back set
// quarantineExplicitlySet=true, and the admission gate skipped the server
// forever — including servers the gate had itself just quarantined, whose
// config.db record was then overwritten with false on the next start.
//
// A save must round-trip the PRESENCE bit, not invent one.
func TestSaveConfig_DoesNotFabricateQuarantineStatement(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp_config.json")

	raw := `{
		"listen": "127.0.0.1:0",
		"api_key": "k",
		"mcpServers": [
			{"name":"unstated","command":"a","protocol":"stdio","enabled":true},
			{"name":"stated-false","command":"b","protocol":"stdio","enabled":true,"quarantined":false},
			{"name":"stated-true","command":"c","protocol":"stdio","enabled":true,"quarantined":true}
		]
	}`
	require.NoError(t, os.WriteFile(p, []byte(raw), 0600))

	cfg, err := LoadFromFile(p)
	require.NoError(t, err)
	require.NoError(t, SaveConfig(cfg, p))

	// The saved document itself must not carry a key the operator never wrote.
	saved, err := os.ReadFile(p)
	require.NoError(t, err)
	var doc struct {
		Servers []map[string]json.RawMessage `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(saved, &doc))
	require.Len(t, doc.Servers, 3)
	_, present := doc.Servers[0]["quarantined"]
	assert.False(t, present,
		"mcpproxy must not write a quarantined key for a server that never stated one")
	_, present = doc.Servers[1]["quarantined"]
	assert.True(t, present, "an explicit quarantined:false must survive the save")
	_, present = doc.Servers[2]["quarantined"]
	assert.True(t, present, "quarantined:true must always be written")

	// And the reload must agree, since that is what the admission gate reads.
	reloaded, err := LoadFromFile(p)
	require.NoError(t, err)
	require.Len(t, reloaded.Servers, 3)
	assert.False(t, reloaded.Servers[0].QuarantineExplicitlySet(),
		"a save/reload round-trip must not turn an unstated server into a stated one")
	assert.True(t, reloaded.Servers[1].QuarantineExplicitlySet())
	assert.True(t, reloaded.Servers[2].QuarantineExplicitlySet())
	assert.True(t, reloaded.Servers[2].Quarantined)
}

// A quarantined server must always serialize the flag even when the config file
// it came from never mentioned it — otherwise a gate decision written to disk
// would evaporate on the next load.
func TestServerConfig_MarshalJSON_AlwaysWritesTrue(t *testing.T) {
	var sc ServerConfig
	require.NoError(t, json.Unmarshal([]byte(`{"name":"s","command":"c"}`), &sc))
	require.False(t, sc.QuarantineExplicitlySet())

	sc.Quarantined = true
	encoded, err := json.Marshal(&sc)
	require.NoError(t, err)

	var probe map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &probe))
	assert.Equal(t, "true", string(probe["quarantined"]))
}
