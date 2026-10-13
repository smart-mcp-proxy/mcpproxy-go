package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// deliveryConfigController serves only the config the delivery helpers read.
type deliveryConfigController struct {
	ServerController
	cfg *config.Config
}

func (c *deliveryConfigController) GetConfig() (*config.Config, error) { return c.cfg, nil }

// Review code-r1: the delivered endpoint, snippet URL and absolute UI links
// follow the listener's scheme, so a worker pasting the snippet on a TLS
// listener speaks HTTPS and never sends its credential header in plaintext.
func TestCredentialDelivery_SchemeFollowsTLS(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tls    *config.TLSConfig
		listen string
		want   string
	}{
		{"plain", nil, "127.0.0.1:18921", "http://127.0.0.1:18921"},
		{"tls disabled", &config.TLSConfig{Enabled: false}, "127.0.0.1:18921", "http://127.0.0.1:18921"},
		{"tls enabled", &config.TLSConfig{Enabled: true}, "127.0.0.1:18921", "https://127.0.0.1:18921"},
		{"tls enabled, port only", &config.TLSConfig{Enabled: true}, ":18921", "https://127.0.0.1:18921"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{controller: &deliveryConfigController{cfg: &config.Config{Listen: tc.listen, TLS: tc.tls}}}
			assert.Equal(t, tc.want+"/mcp", s.MCPEndpoint())

			var doc struct {
				MCPServers map[string]struct {
					URL string `json:"url"`
				} `json:"mcpServers"`
			}
			require.NoError(t, json.Unmarshal([]byte(s.CredentialSnippet("mcp_agt_x").GenericHTTP), &doc))
			assert.Equal(t, tc.want+"/mcp", doc.MCPServers["mcpproxy"].URL)

			links := s.CredentialUILinks("", "task-1", "ro")
			for _, l := range []string{links.Identity, links.Activity, links.Profile, links.EffectiveTools} {
				assert.True(t, strings.HasPrefix(l, tc.want+"/ui/"), l)
			}
		})
	}
}
