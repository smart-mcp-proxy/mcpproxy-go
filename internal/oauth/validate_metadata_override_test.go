package oauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

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

// authorization_endpoint + token_endpoint overrides also stand in for
// discovery: the transport serves a document built from them when no metadata
// can be fetched, so the check must not fail the sign-in either.
func TestValidateOAuthMetadataForServer_HonoursEndpointOverrides(t *testing.T) {
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource/mcp" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"resource": %q, "authorization_servers": [%q]}`, base+"/mcp", base+"/issuer")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	base = srv.URL
	sc := &config.ServerConfig{Name: "endpoints", URL: base + "/mcp",
		OAuth: &config.OAuthConfig{AuthorizationEndpoint: base + "/a", TokenEndpoint: base + "/t"}}
	res, err := ValidateOAuthMetadataForServer(Env{Ctx: context.Background()}, sc, 5*time.Second)
	require.NoError(t, err)
	assert.Nil(t, res)
}

// A matching well-known PRM that lists no authorization_servers leaves the
// authorization server at the MCP URL. Discovery must then try the RFC 8414
// path-inserted URL of the full server URL, as the config build and main's
// 401-without-resource_metadata path do, not only the origin.
func TestValidateOAuthMetadata_WellKnownPRMWithoutASUsesServerPath(t *testing.T) {
	var base string
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/.well-known/oauth-protected-resource/mcp":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"resource": %q}`, base+"/mcp")
		case r.URL.Path == "/.well-known/oauth-authorization-server/mcp":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"issuer": %q, "authorization_endpoint": "%s/a", "token_endpoint": "%s/t"}`, base+"/mcp", base, base)
		case r.Method == http.MethodPost:
			posts++
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	base = srv.URL
	res, err := ValidateOAuthMetadataWithEnv(Env{Ctx: context.Background()}, base+"/mcp", "path-as", 5*time.Second)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Valid)
	assert.Equal(t, base+"/.well-known/oauth-authorization-server/mcp", res.AuthorizationServerMetadataURL)
	assert.Zero(t, posts)
}

// The well-known PRM is now fetched before any 401 advertised it, so its body
// is read with the same bound as the authorization server metadata.
func TestWellKnownPRM_OversizedDocumentIsNotUsed(t *testing.T) {
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"resource": %q, "pad": "%s"}`, base+"/mcp", strings.Repeat("x", maxASMetadataBytes+1))
	}))
	defer srv.Close()
	base = srv.URL
	InvalidateDiscovery(base + "/mcp")
	assert.Nil(t, wellKnownPRM(Env{Ctx: context.Background()}, base+"/mcp", discoveryOverrides{}, 5*time.Second, zap.NewNop()))
}

// One endpoint override is enough: the transport writes it over the fetched
// document, so the unmodified document this check reads can lack that field.
func TestValidateOAuthMetadataForServer_HonoursSingleEndpointOverride(t *testing.T) {
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"resource": %q, "authorization_servers": [%q]}`, base+"/mcp", base)
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"issuer": %q, "authorization_endpoint": "%s/a"}`, base, base)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	base = srv.URL
	sc := &config.ServerConfig{Name: "token-only", URL: base + "/mcp",
		OAuth: &config.OAuthConfig{TokenEndpoint: base + "/t"}}
	res, err := ValidateOAuthMetadataForServer(Env{Ctx: context.Background()}, sc, 5*time.Second)
	require.NoError(t, err)
	assert.Nil(t, res)
}
