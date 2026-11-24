package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Spec 132 PR-2b (T059): `client show` lists credentials and
// `client revoke-login` revokes one browser login.

const cursorWithLogins = `{"id":"cursor","display_name":"Cursor","kind":"supported","state":"connected_seen","connected":true,"config_path":"/c","display_path":"~/c","active_sessions":0,"calls_24h":0,"blocked_24h":0,"credential_state":"client","token_name":"client-cursor","profile":"dev","profile_mode":"locked","sessions":[],` +
	`"credentials":[` +
	`{"kind":"api_key","prefix":"mcp_cli_ab12","state":"active","expires_at":"2027-01-01T00:00:00Z","last_used_at":"2026-11-22T10:00:00Z"},` +
	`{"kind":"browser_login","grant_id":"grantAAAA","state":"active","approved_at":"2026-11-20T10:00:00Z","last_used_at":"2026-11-22T11:00:00Z","reported_name":"Cursor IDE","redirect_host":"127.0.0.1","observed_name":"cursor-vscode","name_mismatch":true},` +
	`{"kind":"browser_login","grant_id":"grantBBBB","state":"lapsed","approved_at":"2026-10-01T10:00:00Z","reported_name":"\u001b[31mEvil","redirect_host":"localhost","name_mismatch":false}` +
	`],"browser_login_disabled":true}`

func TestClientShow_ListsCredentials(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/clients/cursor": okResp(cursorWithLogins)})
	out, _, err := runCLI(t, GetClientCommand, "table", "show", "cursor")
	require.NoError(t, err)
	require.Contains(t, out, "Credentials:")
	require.Contains(t, out, "api_key")
	require.Contains(t, out, "mcp_cli_ab12")
	require.Contains(t, out, "grantAAAA")
	require.Contains(t, out, "lapsed")
	require.Contains(t, out, "127.0.0.1")
	require.Contains(t, out, "Cursor IDE")
	require.Contains(t, out, "Browser login: disabled")
	require.Contains(t, out, "cursor-vscode", "the mismatch is spelled out")
	require.NotContains(t, out, "\x1b", "an untrusted name never reaches the terminal raw")
}

func TestClientShow_NoCredentialsFieldPrintsNothingNew(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/clients/cursor": okResp(cursorRow)})
	out, _, err := runCLI(t, GetClientCommand, "table", "show", "cursor")
	require.NoError(t, err)
	require.NotContains(t, out, "Credentials:")
	require.NotContains(t, out, "Browser login")
	assertGolden108(t, "client-show.golden", out)
}

func TestClientRevokeLogin_DeletesOneLogin(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"DELETE /api/v1/clients/cursor/logins/grantAAAA": okResp(`{"changed":true}`)})
	out, _, err := runCLI(t, GetClientCommand, "table", "revoke-login", "cursor", "grantAAAA")
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, rec.only(t).Method)
	require.Contains(t, out, "Revoked login grantAAAA")

	newRESTRecorder(t, map[string]cannedResponse{"DELETE /api/v1/clients/cursor/logins/grantAAAA": okResp(`{"changed":false}`)})
	out, _, err = runCLI(t, GetClientCommand, "table", "revoke-login", "cursor", "grantAAAA")
	require.NoError(t, err)
	require.Contains(t, out, "already revoked")
}

func TestClientRevokeLogin_UnknownLoginExitsNonZero(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"DELETE /api/v1/clients/cursor/logins/nope": refuse(http.StatusNotFound, `{"success":false,"error":"login not found"}`)})
	_, _, err := runCLI(t, GetClientCommand, "table", "revoke-login", "cursor", "nope")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
}

func TestClientRevokeLogin_NeedsDaemonAndTwoArgs(t *testing.T) {
	_, _, err := runCLI(t, GetClientCommand, "table", "revoke-login", "cursor")
	require.Error(t, err)
	withNoDaemon(t)
	_, _, err = runCLI(t, GetClientCommand, "table", "revoke-login", "cursor", "g")
	require.Error(t, err)
	require.Contains(t, err.Error(), "client requires running daemon")
}
