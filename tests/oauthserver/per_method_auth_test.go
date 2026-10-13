package oauthserver

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postMCP sends one JSON-RPC request to the test server's /mcp endpoint and
// returns the HTTP status. bearer == "" sends the request anonymously.
func postMCP(t *testing.T, mcpURL, bearer, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, mcpURL, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

// clientCredentialsToken mints an access token the /mcp middleware accepts.
func clientCredentialsToken(t *testing.T, server *ServerResult) string {
	t.Helper()
	params := url.Values{}
	params.Set("grant_type", "client_credentials")
	params.Set("client_id", server.ClientID)
	params.Set("client_secret", server.ClientSecret)
	params.Set("scope", "read")
	resp, err := http.PostForm(server.TokenEndpoint, params)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var tok TokenResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&tok))
	require.NotEmpty(t, tok.AccessToken)
	return tok.AccessToken
}

const (
	initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}`
	toolsListBody  = `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	toolsCallBody  = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_time","arguments":{}}}`
)

// GH #1271: MCPPerMethodAuth reproduces Google's Gmail MCP endpoint, which
// answers initialize and tools/list anonymously and only 401s tools/call.
func TestMCPPerMethodAuth_AnonymousHandshakeBearerCalls(t *testing.T) {
	server := Start(t, Options{MCPPerMethodAuth: true})
	defer server.Shutdown()

	assert.Equal(t, http.StatusOK, postMCP(t, server.MCPURL, "", initializeBody), "anonymous initialize")
	assert.Equal(t, http.StatusOK, postMCP(t, server.MCPURL, "", toolsListBody), "anonymous tools/list")
	assert.Equal(t, http.StatusUnauthorized, postMCP(t, server.MCPURL, "", toolsCallBody), "anonymous tools/call")

	token := clientCredentialsToken(t, server)
	assert.Equal(t, http.StatusOK, postMCP(t, server.MCPURL, token, toolsCallBody), "bearer tools/call")
	assert.Equal(t, http.StatusUnauthorized, postMCP(t, server.MCPURL, "not-a-jwt", toolsCallBody), "bad bearer tools/call")
}

// The default mode is unchanged: every method, initialize included, needs a token.
func TestMCPPerMethodAuth_DefaultStillGatesHandshake(t *testing.T) {
	server := Start(t, Options{})
	defer server.Shutdown()

	assert.Equal(t, http.StatusUnauthorized, postMCP(t, server.MCPURL, "", initializeBody))
}
