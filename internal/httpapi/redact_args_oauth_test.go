package httpapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// Issue #1148, round 4 finding 3: the REST twin of the MCP surface. The
// `upstream_servers list` payload masks credential-shaped argv tokens and
// oauth extra_params; GET /api/v1/servers returned both in the clear.
//
// Passing a credential as an argv token (`uvx mcp-foo --api-key sk-…`) is one
// of the commonest MCP server config shapes, and an RFC 8707 resource
// indicator routinely carries a signed URL.
func TestRedactServerSecretFields_MasksArgsAndOAuthExtraParams(t *testing.T) {
	srv := &contracts.Server{
		Name:    "alpha",
		Command: "uvx",
		Args: []string{
			"mcp-foo",
			"--api-key", "sk-argv-secret-777",
			"--endpoint=ghp_16Cabcdefghijklmnopqrstuvwxyz0123456789",
			"--port", "8080",
		},
		OAuth: &contracts.OAuthConfig{
			ClientID: "client-abc",
			ExtraParams: map[string]string{
				"resource": "https://api.example.com/mcp?sig=urlsecret999&region=eu",
				"audience": "tenant-a",
			},
		},
	}

	redactServerSecretFields(srv)

	joined := srv.Args
	assert.NotContains(t, joined, "sk-argv-secret-777", "a flag-named argv credential must be masked")
	for _, a := range joined {
		assert.NotContains(t, a, "ghp_16Cabcdefghijklmnopqrstuvwxyz0123456789",
			"a value-shaped argv credential must be masked")
	}
	assert.Contains(t, joined, "mcp-foo", "ordinary arguments must stay readable")
	assert.Contains(t, joined, "--api-key", "the flag is the audit signal and must survive")
	assert.Contains(t, joined, "8080")

	assert.NotContains(t, srv.OAuth.ExtraParams["resource"], "urlsecret999",
		"a signed resource URL in extra_params must be masked")
	assert.Equal(t, "tenant-a", srv.OAuth.ExtraParams["audience"], "non-secret params round-trip verbatim")
}

// The write half of the same contract. REST PATCH REPLACES `args` outright and
// the MCP surface established that argv masks are REFUSED, not reverted (an
// argv slot has no key to bind a stored secret to). Masking args on the REST
// read without refusing them on the REST write would reintroduce exactly the
// read-modify-write corruption the MCP side closed.
func TestRESTArgs_RefuseEchoedMask(t *testing.T) {
	stored := []string{"mcp-foo", "--api-key", "sk-argv-secret-777"}
	masked := oauth.LiveRedaction.Argv(stored)
	require.NotEqual(t, stored[2], masked[2], "precondition: the read path masks the credential")

	assert.Error(t, oauth.CheckArgvMaskEcho("args", masked, stored),
		"an echoed mask must be refused, never persisted over the credential")
	assert.Error(t, oauth.CheckArgvMaskEcho("args", masked, nil),
		"a mask sent on create has nothing to bind to either")
	assert.NoError(t, oauth.CheckArgvMaskEcho("args", stored, stored))
	assert.NoError(t, oauth.CheckArgvMaskEcho("args", nil, stored))
}
