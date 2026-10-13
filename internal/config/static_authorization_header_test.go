package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// GH #1172: the server-list projection must be able to tell "this server
// authenticates with a static Authorization header" apart from "this server
// may use OAuth via autodiscovery" without consulting stored tokens.
func TestServerConfig_HasStaticAuthorizationHeader(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{"nil headers", nil, false},
		{"empty headers", map[string]string{}, false},
		{"canonical Authorization", map[string]string{"Authorization": "Bearer abc"}, true},
		{"lower-case authorization", map[string]string{"authorization": "Bearer abc"}, true},
		{"mixed-case AUTHORIZATION", map[string]string{"AUTHORIZATION": "Basic xyz"}, true},
		{"empty Authorization value is not a credential", map[string]string{"Authorization": ""}, false},
		{"blank Authorization value is not a credential", map[string]string{"Authorization": "   "}, false},
		{"unrelated header only", map[string]string{"X-Client-Version": "1.0"}, false},
		{"api-key style header is not Authorization", map[string]string{"X-API-Key": "k"}, false},
		{"Proxy-Authorization is not the resource credential", map[string]string{"Proxy-Authorization": "Basic x"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := &ServerConfig{Name: "s", URL: "https://example.invalid/mcp", Headers: tc.headers}
			assert.Equal(t, tc.want, sc.HasStaticAuthorizationHeader())
		})
	}

	t.Run("nil receiver", func(t *testing.T) {
		var sc *ServerConfig
		assert.False(t, sc.HasStaticAuthorizationHeader())
	})
}
