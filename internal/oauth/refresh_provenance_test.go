package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 135 FR-019 (research R1, "neutralised"): mcp-go >= v1.3 refuses to
// refresh a token without issuer provenance, and v1.6's RefreshToken reads
// the token store first. mcpproxy's stores persist no provenance yet (that
// is PR-2), so mcpproxy's own refresh hands mcp-go the provenance through
// ctx and the store answers from it without a store read.
func TestRefreshProvenance_StoresAnswerFromContext(t *testing.T) {
	ctx := WithRefreshProvenance(context.Background(), "rt-1", "https://as.example")

	mem := newCoordinatedMemoryTokenStore("prov-mem")
	tok, err := mem.GetToken(ctx)
	require.NoError(t, err, "no token is stored: the answer comes from ctx")
	assert.Equal(t, "rt-1", tok.RefreshToken)
	assert.Equal(t, "https://as.example", tok.Issuer)

	p := newPersistentTokenStore("prov-db", "https://mcp.example/mcp", nil, commitview.CredentialBinding{})
	tok, err = p.GetToken(ctx)
	require.NoError(t, err, "the bbolt store is not read either")
	assert.Equal(t, "rt-1", tok.RefreshToken)
	assert.Equal(t, "https://as.example", tok.Issuer)

	_, err = mem.GetToken(context.Background())
	assert.True(t, errors.Is(err, transport.ErrNoToken), "without the mark the store answers as before")
}

func TestRefreshProvenance_MCPGoRefreshSucceedsOnlyWithIt(t *testing.T) {
	var tokenCalls atomic.Int32
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_, _ = w.Write([]byte(`{"issuer":"` + srvURL + `","authorization_endpoint":"` + srvURL + `/authorize","token_endpoint":"` + srvURL + `/token","response_types_supported":["code"]}`))
		case "/token":
			tokenCalls.Add(1)
			_, _ = w.Write([]byte(`{"access_token":"new-at","token_type":"Bearer","refresh_token":"rt-2","expires_in":3600}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	store := newCoordinatedMemoryTokenStore("prov-e2e")
	h := transport.NewOAuthHandler(transport.OAuthConfig{
		ClientID: "c1", TokenStore: store, PKCEEnabled: true,
		AuthServerMetadataURL: srv.URL + "/.well-known/oauth-authorization-server",
	})
	ctx := context.Background()

	// A legacy token (no provenance), as every stored row is today.
	require.NoError(t, store.inner.SaveToken(ctx, &transport.Token{AccessToken: "old", RefreshToken: "rt-1", TokenType: "Bearer"}))

	_, err := h.RefreshToken(ctx, "rt-1")
	require.Error(t, err, "upstream change: mcp-go refuses a refresh without provenance")
	assert.Zero(t, tokenCalls.Load(), "and sends nothing")

	md, err := h.GetServerMetadata(ctx)
	require.NoError(t, err)
	tok, err := h.RefreshToken(WithRefreshProvenance(ctx, "rt-1", md.Issuer), "rt-1")
	require.NoError(t, err, "mcpproxy's refresh supplies the legacy provenance")
	assert.Equal(t, "new-at", tok.AccessToken)
	assert.Equal(t, int32(1), tokenCalls.Load())
}
