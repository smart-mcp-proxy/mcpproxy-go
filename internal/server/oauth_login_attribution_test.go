package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

func browserLoginContext(grant string) context.Context {
	return auth.WithAuthContext(context.Background(), auth.ClientAuthContext(auth.ClientIdentity{
		ClientID: "cursor", ProfilePin: "dev", ProfileMode: auth.ProfileModeLocked,
		CredentialKind: auth.CredentialKindOAuth, GrantID: grant, TokenPrefix: "mcp_oat_" + grant[:4], TokenGeneration: grant,
	}))
}

// Spec 132 R3: two logins of one client are two work sessions (the grant id is
// the credential generation), and a revoked login never merges with a new one.
func TestPrincipalFromContext_BrowserLoginsOfOneClientAreSeparate(t *testing.T) {
	a, b := browserLoginContext("grantAAAAAAAAAAAAAAAAAA"), browserLoginContext("grantBBBBBBBBBBBBBBBBBB")
	assert.NotEqual(t, principalFromContext(a), principalFromContext(b))
	assert.Contains(t, principalFromContext(a), "agent:client-cursor:")
}

// Session-backed and request-backed activity both carry the grant id.
func TestActivityAttribution_BrowserLoginCarriesGrantID(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	ctx := browserLoginContext("grantAAAAAAAAAAAAAAAAAA")
	attr := f.proxy.activityAttribution(ctx, "")
	assert.Equal(t, "grantAAAAAAAAAAAAAAAAAA", attr.GrantID)
	assert.Equal(t, "cursor", attr.ClientID, "attributed to the client exactly like its mcp_cli_ credential")
	assert.Equal(t, "client-cursor", attr.TokenName)

	// A ctx-less internal emitter reads the grant back from the session.
	f.proxy.sessionStore.SetSession("s-oauth", "Cursor", "1.0", false, false, nil)
	f.proxy.sessionStore.SetSessionIdentity("s-oauth", "client-cursor", "cursor")
	f.proxy.sessionStore.SetSessionGrantID("s-oauth", "grantAAAAAAAAAAAAAAAAAA")
	attr = f.proxy.activityAttribution(context.Background(), "s-oauth")
	assert.Equal(t, "grantAAAAAAAAAAAAAAAAAA", attr.GrantID)
}
