//go:build server

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// A discovery document whose issuer does not byte-for-byte equal the
// configured issuer_url must refuse the login as discovery_failed AND name
// both values in the operator-facing check text — spec.md's Edge Cases
// section promises "the log line names both values (they are not secrets)
// with the fix", and docs/development/server-edition-multiuser-auth.md and
// docs/configuration/config-file.md both describe the same behaviour, but the
// code only ever said "discovery issuer does not equal issuer_url" with
// neither value (cross-review round 7, chunk 3 P3).
func TestOIDCDiscovery_IssuerMismatchNamesBothValues(t *testing.T) {
	var issuerURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"issuer": "` + issuerURL + `/wrong-path",
			"authorization_endpoint": "` + issuerURL + `/authorize",
			"token_endpoint": "` + issuerURL + `/token",
			"jwks_uri": "` + issuerURL + `/jwks",
			"token_endpoint_auth_methods_supported": ["client_secret_basic"],
			"id_token_signing_alg_values_supported": ["RS256"]
		}`))
	}))
	defer srv.Close()
	issuerURL = srv.URL

	prov := newOIDCProvider(&config.ServerEditionOAuthConfig{
		IssuerURL:           issuerURL,
		AllowInsecureIssuer: true,
	})

	_, err := prov.discover(context.Background())
	require.Error(t, err)

	var oe *oidcError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, LoginDiscoveryFailed, oe.reason)
	assert.Contains(t, oe.check, issuerURL+"/wrong-path", "names the discovered issuer")
	assert.Contains(t, oe.check, issuerURL, "names the configured issuer_url")
}
