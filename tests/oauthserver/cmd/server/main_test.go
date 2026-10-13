package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// Spec 107 T034a — parseFlags maps the standalone CLI onto oauthserver.Options.

func TestParseFlags_Defaults(t *testing.T) {
	opts, err := parseFlags(nil)
	require.NoError(t, err)
	assert.False(t, opts.OIDC)
	assert.True(t, opts.EnableAuthCode)
	assert.True(t, opts.EnableDeviceCode)
	assert.True(t, opts.EnableDCR)
	assert.True(t, opts.RequirePKCE)
	assert.Equal(t, oauthserver.Both, opts.DetectionMode)
	assert.Equal(t, oauthserver.ErrorMode{}, opts.ErrorMode)
	assert.Empty(t, opts.ClientRedirectURIs)
	assert.Empty(t, opts.UserClaims)
	require.Len(t, opts.Clients, 1, "the Playwright test-client stays pre-registered")
	assert.Equal(t, "test-client", opts.Clients[0].ClientID)
}

func TestParseFlags_OIDC(t *testing.T) {
	opts, err := parseFlags([]string{
		"-oidc",
		"-user", "alice@example.com:pass:eng,sre",
		"-user", "bob@example.com:pw:",
		"-user", "carol@example.com:pw2",
		"-redirect-uri", "http://127.0.0.1:18080/api/v1/auth/callback",
		"-redirect-uri", "http://localhost:18080/api/v1/auth/callback",
		"-groups-claim", "roles",
	})
	require.NoError(t, err)

	assert.True(t, opts.OIDC)
	assert.Equal(t, "roles", opts.GroupsClaim)
	assert.Equal(t, []string{
		"http://127.0.0.1:18080/api/v1/auth/callback",
		"http://localhost:18080/api/v1/auth/callback",
	}, opts.ClientRedirectURIs)

	assert.Equal(t, map[string]string{
		"alice@example.com": "pass",
		"bob@example.com":   "pw",
		"carol@example.com": "pw2",
	}, opts.ValidUsers)

	require.Contains(t, opts.UserClaims, "alice@example.com")
	alice := opts.UserClaims["alice@example.com"]
	assert.Equal(t, "alice@example.com", alice["email"])
	assert.Equal(t, []any{"eng", "sre"}, alice["roles"], "groups land under the configured claim name")
	assert.Equal(t, true, alice["email_verified"])
	assert.NotEmpty(t, alice["sub"])
	assert.NotEmpty(t, alice["name"])

	assert.Equal(t, []any{}, opts.UserClaims["bob@example.com"]["roles"], "trailing ':' means no groups")
	assert.Equal(t, []any{}, opts.UserClaims["carol@example.com"]["roles"], "omitted groups field means no groups")

	// -redirect-uri also reaches the Playwright test-client.
	require.Len(t, opts.Clients, 1)
	assert.Contains(t, opts.Clients[0].RedirectURIs, "http://127.0.0.1:18080/api/v1/auth/callback")
}

func TestParseFlags_OIDCDefaultsGroupsClaim(t *testing.T) {
	opts, err := parseFlags([]string{"-oidc", "-user", "alice@example.com:pass:eng"})
	require.NoError(t, err)
	assert.Equal(t, "groups", opts.GroupsClaim)
	assert.Equal(t, []any{"eng"}, opts.UserClaims["alice@example.com"]["groups"])
}

func TestParseFlags_UserWithoutPasswordIsAnError(t *testing.T) {
	_, err := parseFlags([]string{"-oidc", "-user", "alice@example.com"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "-user")
}

func TestParseFlags_TamperFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want oauthserver.ErrorMode
	}{
		// existing knobs keep working
		{"token invalid_client", []string{"-token-error", "invalid_client"}, oauthserver.ErrorMode{TokenInvalidClient: true}},
		{"token invalid_grant", []string{"-token-error", "invalid_grant"}, oauthserver.ErrorMode{TokenInvalidGrant: true}},
		{"token invalid_scope", []string{"-token-error", "invalid_scope"}, oauthserver.ErrorMode{TokenInvalidScope: true}},
		{"token server_error", []string{"-token-error", "server_error"}, oauthserver.ErrorMode{TokenServerError: true}},
		{"auth access_denied", []string{"-auth-error", "access_denied"}, oauthserver.ErrorMode{AuthAccessDenied: true}},
		{"auth invalid_request", []string{"-auth-error", "invalid_request"}, oauthserver.ErrorMode{AuthInvalidRequest: true}},

		// id_token knobs (research D2 / quickstart §1)
		{"bad-signature", []string{"-oidc", "-token-error", "bad-signature"}, oauthserver.ErrorMode{IDTokenBadSignature: true}},
		{"wrong-iss", []string{"-oidc", "-token-error", "wrong-iss"}, oauthserver.ErrorMode{IDTokenWrongIssuer: true}},
		{"wrong-aud", []string{"-oidc", "-token-error", "wrong-aud"}, oauthserver.ErrorMode{IDTokenWrongAudience: true}},
		{"multi-aud-no-azp", []string{"-oidc", "-token-error", "multi-aud-no-azp"}, oauthserver.ErrorMode{IDTokenMultiAudNoAzp: true}},
		{"expired", []string{"-oidc", "-token-error", "expired"}, oauthserver.ErrorMode{IDTokenExpired: true}},
		{"nbf-future", []string{"-oidc", "-token-error", "nbf-future"}, oauthserver.ErrorMode{IDTokenNbfFuture: true}},
		{"no-nonce", []string{"-oidc", "-token-error", "no-nonce"}, oauthserver.ErrorMode{IDTokenNoNonce: true}},
		{"alg-none", []string{"-oidc", "-token-error", "alg-none"}, oauthserver.ErrorMode{IDTokenAlgNone: true}},
		{"hs256", []string{"-oidc", "-token-error", "hs256"}, oauthserver.ErrorMode{IDTokenHS256: true}},
		{"unknown-kid", []string{"-oidc", "-token-error", "unknown-kid"}, oauthserver.ErrorMode{IDTokenUnknownKid: true}},
		{"key-alg-mismatch", []string{"-oidc", "-token-error", "key-alg-mismatch"}, oauthserver.ErrorMode{IDTokenKeyAlgMismatch: true}},

		// claims knobs
		{"email-verified=false", []string{"-oidc", "-email-verified=false"}, oauthserver.ErrorMode{EmailVerifiedFalse: true}},
		{"email-verified default", []string{"-oidc"}, oauthserver.ErrorMode{}},
		{"groups non-array", []string{"-oidc", "-groups-error", "non-array"}, oauthserver.ErrorMode{GroupsNonArray: true}},
		{"groups overage", []string{"-oidc", "-groups-error", "overage"}, oauthserver.ErrorMode{GroupsOverageMarker: true}},
		{"groups absent", []string{"-oidc", "-groups-error", "absent"}, oauthserver.ErrorMode{GroupsAbsentEverywhere: true}},

		// userinfo knobs
		{"userinfo-sub-mismatch (quickstart alias)", []string{"-oidc", "-userinfo-sub-mismatch"}, oauthserver.ErrorMode{UserinfoSubMismatch: true}},
		{"userinfo sub-mismatch", []string{"-oidc", "-userinfo-error", "sub-mismatch"}, oauthserver.ErrorMode{UserinfoSubMismatch: true}},
		{"userinfo redirect", []string{"-oidc", "-userinfo-error", "redirect"}, oauthserver.ErrorMode{UserinfoRedirect: true}},
		{"userinfo non-json", []string{"-oidc", "-userinfo-error", "non-json"}, oauthserver.ErrorMode{UserinfoNonJSON: true}},
		{"userinfo unavailable", []string{"-oidc", "-userinfo-error", "unavailable"}, oauthserver.ErrorMode{UserinfoUnavailable: true}},

		// hostile metadata / token endpoint
		{"discovery-http-token-endpoint", []string{"-oidc", "-discovery-http-token-endpoint"}, oauthserver.ErrorMode{DiscoveryHTTPTokenEndpoint: true}},
		{"token-endpoint-redirect", []string{"-oidc", "-token-endpoint-redirect"}, oauthserver.ErrorMode{TokenEndpointRedirect: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseFlags(tc.args)
			require.NoError(t, err)
			assert.Equal(t, tc.want, opts.ErrorMode)
		})
	}
}

func TestParseFlags_UnknownTamperNameIsAnError(t *testing.T) {
	for _, args := range [][]string{
		{"-token-error", "bogus"},
		{"-auth-error", "bogus"},
		{"-oidc", "-groups-error", "bogus"},
		{"-oidc", "-userinfo-error", "bogus"},
		{"-detection", "bogus"},
		{"-no-such-flag"},
	} {
		_, err := parseFlags(args)
		assert.Error(t, err, "args=%v", args)
	}
}

func TestParseFlags_IDTokenTamperRequiresOIDC(t *testing.T) {
	_, err := parseFlags([]string{"-token-error", "bad-signature"})
	require.Error(t, err, "an id_token knob without -oidc would silently do nothing")
	assert.Contains(t, err.Error(), "-oidc")
}
