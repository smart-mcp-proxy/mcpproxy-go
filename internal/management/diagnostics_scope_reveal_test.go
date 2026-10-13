package management

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// oauthConfigDetail is the shape health.ExtractOAuthConfigError produces: it
// returns the wrapped upstream connect error VERBATIM whenever it matches an
// OAuth-config pattern, so the string carries the full URL — query credential
// and all — into contracts.OAuthIssue.Error.
const oauthConfigDetail = `failed to connect to https://provider.example.com/mcp?api_key=OAUTHISSUELEAK: ` +
	`oauth config validation failed: requires 'resource' parameter`

func doctorService(t *testing.T, cfg *config.Config, servers []map[string]interface{}) *service {
	t.Helper()
	rt := newMockRuntime()
	rt.servers = servers
	return NewService(rt, cfg, "", &mockEventEmitter{}, nil, zaptest.NewLogger(t).Sugar()).(*service)
}

// TestDoctor_OAuthIssueErrorIsScrubbedForNonAdmin closes the hole the
// adversarial pass found in the #1167 design: redactErr was applied to only TWO
// of the fields Doctor builds out of healthDetail. The health.ActionConfigure
// branch emitted the SAME variable RAW into contracts.OAuthIssue.Error, which
// both /api/v1/doctor (writeSuccess) and the MCP handleDoctor serialize
// verbatim — and neither door carries an admin gate of its own.
//
// Note this leaked with reveal_secret_headers OFF as well as on, so the flag is
// deliberately false for the non-admin cases below.
func TestDoctor_OAuthIssueErrorIsScrubbedForNonAdmin(t *testing.T) {
	servers := []map[string]interface{}{
		{
			"name":       "oauthy",
			"last_error": oauthConfigDetail,
			"health": map[string]interface{}{
				"level":       "unhealthy",
				"admin_state": "enabled",
				"summary":     "OAuth configuration error",
				"detail":      oauthConfigDetail,
				"action":      health.ActionConfigure,
			},
		},
	}

	cases := []struct {
		name string
		ctx  func() context.Context
	}{
		{"no auth context", func() context.Context { return context.Background() }},
		{"anonymous back-compat admin", func() context.Context {
			return auth.WithAuthContext(context.Background(), auth.AnonymousContext())
		}},
		{"scoped agent token", func() context.Context {
			return auth.WithAuthContext(context.Background(), &auth.AuthContext{
				Type: auth.AuthTypeAgent, AllowedServers: []string{"*"},
			})
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// reveal is OFF here on purpose: this field bypassed the gate
			// entirely, so it leaked at the default configuration.
			svc := doctorService(t, &config.Config{}, servers)
			diag, err := svc.Doctor(tc.ctx())
			require.NoError(t, err)
			require.Len(t, diag.OAuthIssues, 1, "precondition: the ActionConfigure branch must have fired")
			assert.Equal(t, oauth.ScrubUpstreamText(oauthConfigDetail), diag.OAuthIssues[0].Error,
				"oauth_issues[].error must take the one shared free-text rule")
			assert.NotContains(t, diag.OAuthIssues[0].Error, "OAUTHISSUELEAK")
			// The parameter name is not a credential and must survive, or the
			// scrub has broken the diagnosis it exists to deliver.
			assert.Equal(t, []string{"resource"}, diag.OAuthIssues[0].MissingParams,
				"the OAuth parameter name must still be extracted after scrubbing")
		})
	}

	t.Run("authenticated admin with the opt-in still gets raw", func(t *testing.T) {
		svc := doctorService(t, &config.Config{RevealSecretHeaders: true}, servers)
		diag, err := svc.Doctor(auth.WithAuthContext(context.Background(), auth.AdminContext()))
		require.NoError(t, err)
		require.Len(t, diag.OAuthIssues, 1)
		assert.Equal(t, oauthConfigDetail, diag.OAuthIssues[0].Error,
			"the operator opt-in must still work for an authenticated admin")
	})
}

// TestDoctor_RevealRequiresAuthenticatedAdmin pins the #1167 gate itself on the
// UpstreamErrors field. Equality against the shared scrub rule, not substring
// absence, so a nil/empty ErrorMessage cannot pass.
func TestDoctor_RevealRequiresAuthenticatedAdmin(t *testing.T) {
	const msg = `Post "https://api.example.com/mcp?token=DOCTORLEAK": no such host`
	servers := []map[string]interface{}{{"name": "leaky", "last_error": msg}}
	cfg := func() *config.Config { return &config.Config{RevealSecretHeaders: true} }

	scrubbed := []struct {
		name string
		ctx  context.Context
	}{
		{"no auth context", context.Background()},
		{"anonymous", auth.WithAuthContext(context.Background(), auth.AnonymousContext())},
		{"scoped agent token", auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type: auth.AuthTypeAgent, AllowedServers: []string{"*"},
		})},
	}
	for _, tc := range scrubbed {
		t.Run(tc.name, func(t *testing.T) {
			diag, err := doctorService(t, cfg(), servers).Doctor(tc.ctx)
			require.NoError(t, err)
			require.Len(t, diag.UpstreamErrors, 1)
			assert.Equal(t, oauth.ScrubUpstreamText(msg), diag.UpstreamErrors[0].ErrorMessage,
				"reveal_secret_headers alone must not hand a non-admin the raw error")
		})
	}

	t.Run("authenticated admin", func(t *testing.T) {
		diag, err := doctorService(t, cfg(), servers).
			Doctor(auth.WithAuthContext(context.Background(), auth.AdminContext()))
		require.NoError(t, err)
		require.Len(t, diag.UpstreamErrors, 1)
		assert.Equal(t, msg, diag.UpstreamErrors[0].ErrorMessage)
	})
}

// TestDoctor_ScopedTokenSeesOnlyAllowedServers pins #1166 on the doctor
// aggregator, which enumerates every server with its last_error.
func TestDoctor_ScopedTokenSeesOnlyAllowedServers(t *testing.T) {
	servers := []map[string]interface{}{
		{"name": "alpha", "last_error": "alpha is down"},
		{"name": "beta", "last_error": "beta is down"},
	}

	scoped := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"},
	})
	diag, err := doctorService(t, &config.Config{}, servers).Doctor(scoped)
	require.NoError(t, err)
	require.Len(t, diag.UpstreamErrors, 1,
		"a token scoped to alpha must be diagnosed only about alpha")
	assert.Equal(t, "alpha", diag.UpstreamErrors[0].ServerName)

	// Regression guard: an admin must still be diagnosed about everything.
	adminDiag, err := doctorService(t, &config.Config{}, servers).
		Doctor(auth.WithAuthContext(context.Background(), auth.AdminContext()))
	require.NoError(t, err)
	require.Len(t, adminDiag.UpstreamErrors, 2)

	// And so must a caller with no AuthContext (the in-process / bootstrap
	// path), or every existing caller loses its diagnosis.
	bareDiag, err := doctorService(t, &config.Config{}, servers).Doctor(context.Background())
	require.NoError(t, err)
	require.Len(t, bareDiag.UpstreamErrors, 2)
}
