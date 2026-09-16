//go:build !server

package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 107 FR-040 (T020, personal build): the opaque `server_edition` and
// `auth_broker` carriers are json.RawMessage holders. These tests pin the
// carrier contract itself — omission belongs to the parent pointer, an empty
// carrier is `{}`, and Clone() never aliases the source's backing array (the
// value copy CopyServerConfig used to make would have).

const carrierProbe = `{"mode":"oauth_connect","n":9007199254740993,"d":0.1000000000000000055511151231257827}`

func TestPersonalCarriers_CopyServerConfigDoesNotAliasAuthBroker(t *testing.T) {
	var src ServerConfig
	require.NoError(t, json.Unmarshal([]byte(`{"name":"s","url":"https://x/mcp","protocol":"http","auth_broker":`+carrierProbe+`}`), &src))
	require.NotNil(t, src.AuthBroker)

	dst := CopyServerConfig(&src)
	require.NotNil(t, dst.AuthBroker)
	require.NotSame(t, src.AuthBroker, dst.AuthBroker, "the copy must own its carrier")

	before, err := json.Marshal(dst.AuthBroker)
	require.NoError(t, err)
	assert.Equal(t, carrierProbe, string(before))

	// Mutate the SOURCE carrier's bytes in place; an aliased backing array
	// would leak the change into the copy.
	src.AuthBroker.raw[2] = 'X'
	after, err := json.Marshal(dst.AuthBroker)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "mutating the source carrier must leave the copy unchanged")
}

func TestPersonalCarriers_ServerEditionCloneDoesNotAlias(t *testing.T) {
	var src Config
	require.NoError(t, json.Unmarshal([]byte(`{"server_edition":`+carrierProbe+`}`), &src))
	require.NotNil(t, src.ServerEdition)

	dst := src.ServerEdition.Clone()
	require.NotNil(t, dst)
	require.NotSame(t, src.ServerEdition, dst)

	before, err := json.Marshal(dst)
	require.NoError(t, err)
	assert.Equal(t, carrierProbe, string(before))

	src.ServerEdition.raw[2] = 'X'
	after, err := json.Marshal(dst)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))

	assert.Nil(t, (*ServerEditionConfig)(nil).Clone())
	assert.Nil(t, (*AuthBrokerConfig)(nil).Clone())
}

// A carrier only Go code can construct (non-nil pointer, no raw bytes) must
// still emit valid JSON; a JSON null must leave the parent pointer nil so the
// key is omitted on write.
func TestPersonalCarriers_EmptyAndNull(t *testing.T) {
	out, err := json.Marshal(&Config{ServerEdition: &ServerEditionConfig{}})
	require.NoError(t, err)
	assert.Contains(t, string(out), `"server_edition":{}`)

	out, err = json.Marshal(&ServerConfig{Name: "s", AuthBroker: &AuthBrokerConfig{}})
	require.NoError(t, err)
	assert.Contains(t, string(out), `"auth_broker":{}`)

	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(`{"server_edition":null,"mcpServers":[{"name":"s","auth_broker":null}]}`), &cfg))
	assert.Nil(t, cfg.ServerEdition)
	require.Len(t, cfg.Servers, 1)
	assert.Nil(t, cfg.Servers[0].AuthBroker)

	out, err = json.Marshal(&cfg)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "server_edition")
	assert.NotContains(t, string(out), "auth_broker")
}
