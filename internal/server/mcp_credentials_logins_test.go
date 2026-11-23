package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 132 PR-2b (T058): the `credentials` tool lists, inspects and revokes
// browser logins, and never creates one.

func (f *credentialsToolFixture) seedLogin(client, grant, reg string) {
	f.t.Helper()
	sm := f.rt.StorageManager()
	now := time.Now()
	require.NoError(f.t, sm.CreateOAuthRegistration(storage.OAuthRegistration{
		ClientID: reg, ReportedName: "Cursor IDE", RedirectURIs: []string{"http://127.0.0.1:9/cb"},
		TokenEndpointAuthMethod: "none", GrantTypes: []string{"authorization_code", "refresh_token"}, CreatedAt: now,
	}))
	_, err := sm.CommitOAuthLogin(storage.CommitLoginInput{
		Grant: storage.OAuthGrant{
			GrantID: grant, RegistrationID: reg, Client: client,
			RedirectURI: "http://127.0.0.1:9/cb", Resource: "http://127.0.0.1:8080/mcp/oauth/" + client, Issuer: "http://127.0.0.1:8080",
			RefreshHash: "rh-" + grant, AccessHash: "ah-" + grant, AccessExpiresAt: now.Add(time.Hour), RefreshExpiresAt: now.Add(24 * time.Hour),
			ApprovedAt: now, DisplayPrefix: "mcp_oat_abcd", ObservedClientName: "cursor-vscode",
		},
		Client: client, NewClient: true, Pin: "work-readonly", Mode: auth.ProfileModeLocked,
		ExpectedBindingRevision: storage.OAuthBindingAbsent, Now: now,
	})
	require.NoError(f.t, err)
}

func loginsFixture(t *testing.T) *credentialsToolFixture {
	if !clientsEdition {
		t.Skip("browser login is a personal-edition feature")
	}
	f := newCredentialsToolFixture(t, nil)
	f.seedLogin("cursor", "grantAAAAAAAAAAAAAAAAAA", "reg1")
	return f
}

func loginRows(out map[string]any) []map[string]any {
	var rows []map[string]any
	for _, r := range out["credentials"].([]any) {
		if row := r.(map[string]any); row["kind"] == "browser_login" {
			rows = append(rows, row)
		}
	}
	return rows
}

func TestCredentialsTool_ListShowsBrowserLogins(t *testing.T) {
	f := loginsFixture(t)
	for _, args := range []map[string]any{
		{"operation": "list"},
		{"operation": "list", "kind": "browser_login"},
	} {
		logins := loginRows(f.cok(args))
		require.Len(t, logins, 1, "%v", args)
		assert.Equal(t, "grantAAAAAAAAAAAAAAAAAA", logins[0]["id"])
		assert.Equal(t, "cursor", logins[0]["client"])
		assert.Equal(t, "active", logins[0]["state"])
		assert.Equal(t, "Cursor IDE", logins[0]["reported_name"])
		assert.Equal(t, "127.0.0.1", logins[0]["redirect_host"])
	}
	assert.Empty(t, loginRows(f.cok(map[string]any{"operation": "list", "kind": "client"})))
	// A lapsed filter is accepted.
	f.cok(map[string]any{"operation": "list", "state": "lapsed"})
}

func TestCredentialsTool_GetClientIncludesLogins(t *testing.T) {
	f := loginsFixture(t)
	out := f.cok(map[string]any{"operation": "get", "client": "cursor"})
	logins, ok := out["logins"].([]any)
	require.True(t, ok, "get client carries its logins: %v", out)
	require.Len(t, logins, 1)
	assert.Equal(t, "grantAAAAAAAAAAAAAAAAAA", logins[0].(map[string]any)["id"])
}

func TestCredentialsTool_RevokeOneLogin(t *testing.T) {
	f := loginsFixture(t)
	f.seedLogin("windsurf", "grantBBBBBBBBBBBBBBBBBB", "reg2")
	out := f.cok(map[string]any{"operation": "revoke", "client": "cursor", "grant": "grantAAAAAAAAAAAAAAAAAA"})
	assert.Equal(t, true, out["changed"])
	cred := out["credential"].(map[string]any)
	assert.Equal(t, "browser_login", cred["kind"])
	assert.Equal(t, "revoked", cred["state"])

	states := map[string]string{}
	for _, row := range loginRows(f.cok(map[string]any{"operation": "list", "kind": "browser_login"})) {
		states[row["id"].(string)] = row["state"].(string)
	}
	assert.Equal(t, map[string]string{"grantAAAAAAAAAAAAAAAAAA": "revoked", "grantBBBBBBBBBBBBBBBBBB": "active"}, states)

	// Revoking it again is not an error and changes nothing.
	again := f.cok(map[string]any{"operation": "revoke", "client": "cursor", "grant": "grantAAAAAAAAAAAAAAAAAA"})
	assert.Equal(t, false, again["changed"])
}

func TestCredentialsTool_RevokeLoginRefusals(t *testing.T) {
	f := loginsFixture(t)
	out := f.crefused(map[string]any{"operation": "revoke", "client": "cursor", "grant": "nope"})
	assert.Equal(t, "identity_not_found", out["code"])
	assert.Equal(t, "grant", out["field"])

	// A grant of another client is not found under this one.
	out = f.crefused(map[string]any{"operation": "revoke", "client": "other", "grant": "grantAAAAAAAAAAAAAAAAAA"})
	assert.Equal(t, "identity_not_found", out["code"])

	// A grant needs a client; get and the create operations do not take one.
	out = f.crefused(map[string]any{"operation": "revoke", "token": "t1", "grant": "g"})
	assert.Equal(t, "invalid_argument", out["code"])
	out = f.crefused(map[string]any{"operation": "get", "client": "cursor", "grant": "g"})
	assert.Equal(t, "invalid_argument", out["code"])
	out = f.crefused(map[string]any{"operation": "create_client", "client": "x", "grant": "g"})
	assert.Equal(t, "invalid_argument", out["code"])

	// The login is untouched by every refusal.
	rows := loginRows(f.cok(map[string]any{"operation": "list", "kind": "browser_login"}))
	require.Len(t, rows, 1)
	assert.Equal(t, "active", rows[0]["state"])
}

// An empty or blank grant is a refusal, never the client-wide revoke that
// omitting grant means.
func TestCredentialsTool_BlankGrantIsRefusedNotClientWide(t *testing.T) {
	f := loginsFixture(t)
	for _, blank := range []string{"", " ", "\t", "  \n"} {
		out := f.crefused(map[string]any{"operation": "revoke", "client": "cursor", "grant": blank})
		assert.Equal(t, "invalid_argument", out["code"], "%q", blank)
		assert.Equal(t, "grant", out["field"], "%q", blank)
	}
	rows := loginRows(f.cok(map[string]any{"operation": "list", "kind": "browser_login"}))
	require.Len(t, rows, 1)
	assert.Equal(t, "active", rows[0]["state"], "nothing was revoked")
}

func TestCredentialsTool_NoOperationCreatesALogin(t *testing.T) {
	for _, op := range credentialsOperations {
		assert.NotContains(t, op, "login")
	}
	assert.False(t, credentialsOpArgs["create_client"]["grant"])
	assert.False(t, credentialsOpArgs["create_token"]["grant"])
}

func TestCredentialsTool_LoginRevokeHonoursWriteGates(t *testing.T) {
	f := loginsFixture(t)
	f.proxy.currentConfig().ReadOnlyMode = true
	out := f.crefused(map[string]any{"operation": "revoke", "client": "cursor", "grant": "grantAAAAAAAAAAAAAAAAAA"})
	assert.Equal(t, "read_only_mode", out["code"])
	rows := loginRows(f.cok(map[string]any{"operation": "list", "kind": "browser_login"}))
	require.Len(t, rows, 1)
	assert.Equal(t, "active", rows[0]["state"])
}
