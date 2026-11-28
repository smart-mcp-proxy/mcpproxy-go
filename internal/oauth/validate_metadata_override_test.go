package oauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// A server whose MCP endpoint answers the empty POST with something other than
// 401 used to skip the sign-in metadata check. Since the well-known PRM is read
// first, the check now runs for it. When the server's oauth block sets
// auth_server_metadata_url, the sign-in uses that URL instead of discovery, so
// the check must not fail the sign-in because discovery finds no metadata.
func TestValidateOAuthMetadataForServer_HonoursAuthServerMetadataURL(t *testing.T) {
	var base string
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/.well-known/oauth-protected-resource/mcp":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"resource": %q, "authorization_servers": [%q]}`, base+"/mcp", base+"/issuer")
		case r.URL.Path == "/custom/as-metadata":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"issuer": %q, "authorization_endpoint": "%s/a", "token_endpoint": "%s/t"}`, base+"/issuer", base, base)
		case r.URL.Path == "/mcp" && r.Method == http.MethodPost:
			posts++
			w.WriteHeader(http.StatusBadRequest)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	base = srv.URL
	env := Env{Ctx: context.Background()}

	plain := &config.ServerConfig{Name: "plain", URL: base + "/mcp"}
	_, err := ValidateOAuthMetadataForServer(env, plain, 5*time.Second)
	require.Error(t, err, "without an override, missing AS metadata still fails early")

	pinned := &config.ServerConfig{Name: "pinned", URL: base + "/mcp",
		OAuth: &config.OAuthConfig{AuthServerMetadataURL: base + "/custom/as-metadata"}}
	res, err := ValidateOAuthMetadataForServer(env, pinned, 5*time.Second)
	require.NoError(t, err, "auth_server_metadata_url replaces the discovery this check performs")
	assert.Nil(t, res)
	assert.Zero(t, posts, "no unauthenticated POST")
}
