package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
)

func doctorClient(t *testing.T, routes map[string]cannedResponse) (*cliclient.Client, *restRecorder) {
	t.Helper()
	rec := newRESTRecorder(t, routes)
	sess, err := newRESTSession("doctor")
	require.NoError(t, err)
	return sess.client, rec
}

func clientsWithWarnings(rows, warnings string) map[string]cannedResponse {
	return map[string]cannedResponse{"GET /api/v1/clients": okResp(`{"clients":[` + rows + `],"routing":{},"warnings":[` + warnings + `]}`)}
}

func findCheck(t *testing.T, checks []profileCheck, id string) profileCheck {
	t.Helper()
	for _, c := range checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %s in %+v", id, checks)
	return profileCheck{}
}

const bypassWarning = `{"code":"anonymous_denied_by_binding_guard","severity":"warn","message":"anonymous callers are denied while cursor could be bypassed without auth","action":{"kind":"change_setting","target":"require_mcp_auth"},"bindings":[{"client_id":"cursor","token_name":"client-cursor","profile":"work-readonly","mode":"locked"}],"fixes":[{"kind":"require_mcp_auth"},{"kind":"set_anonymous_profile","target":"work-readonly"}]}`

func TestDoctorProfileChecks_BindingBypassFromWarning(t *testing.T) {
	client, rec := doctorClient(t, clientsWithWarnings(cursorRow, bypassWarning))
	checks := collectProfileChecks(client)
	require.Len(t, rec.all(), 1, "doctor reads GET /clients once and nothing else")
	c := findCheck(t, checks, "profiles.binding_bypass")
	require.Equal(t, "warn", c.Status)
	require.Len(t, c.Bindings, 1)
	require.Equal(t, "cursor", c.Bindings[0].ClientID)
	require.Len(t, c.Fixes, 2)

	out := captureOutput(func() { printProfileChecksSection(checks) })
	require.Contains(t, out, "⚠ profiles.binding_bypass")
	require.Contains(t, out, "- cursor → work-readonly (locked)")
	require.Contains(t, out, "Fix: set require_mcp_auth: true (config or Settings → Security)")
	require.Contains(t, out, "Fix: mcpproxy profile anonymous work-readonly")

	// No warning: the check is ok.
	client, _ = doctorClient(t, clientsWithWarnings(cursorRow, ""))
	require.Equal(t, "ok", findCheck(t, collectProfileChecks(client), "profiles.binding_bypass").Status)
}

func TestDoctorProfileChecks_AdminKeyTwoSteps(t *testing.T) {
	client, _ := doctorClient(t, clientsWithWarnings(codexRow, `{"code":"client_holds_admin_key","severity":"warn","client_id":"codex","message":"codex holds the admin API key; upgrade it to a client credential","action":{"kind":"upgrade_admin_key_holders"}}`))
	checks := collectProfileChecks(client)
	c := findCheck(t, checks, "connect.admin_key_in_client_config")
	require.Equal(t, "warn", c.Status)
	require.Contains(t, c.Message, "codex")

	out := captureOutput(func() { printProfileChecksSection(checks) })
	require.Contains(t, out, "⚠ connect.admin_key_in_client_config")
	require.Contains(t, out, "Fix: 1. mcpproxy client upgrade-admin-key-holders")
	require.Contains(t, out, "Fix: 2. rotate the admin API key (set a new api_key and restart): https://docs.mcpproxy.app/configuration/config-file")

	client, _ = doctorClient(t, clientsWithWarnings(cursorRow, ""))
	require.Equal(t, "ok", findCheck(t, collectProfileChecks(client), "connect.admin_key_in_client_config").Status)
}

func TestDoctorProfileChecks_UnknownCredentialInfo(t *testing.T) {
	unknown := `{"id":"zed","display_name":"Zed","kind":"supported","state":"connected_seen","connected":true,"credential_state":"unknown"}`
	client, rec := doctorClient(t, clientsWithWarnings(unknown+","+cursorRow+","+codexRow, ""))
	checks := collectProfileChecks(client)
	c := findCheck(t, checks, "connect.admin_key_unchecked")
	require.Equal(t, "info", c.Status)
	require.Equal(t, "1 connected client was never checked for the admin key; run mcpproxy client upgrade-admin-key-holders (preview only) to check", c.Message)
	for _, r := range rec.all() {
		require.Equal(t, http.MethodGet, r.Method, "doctor never writes and never reads a client config")
	}
}

func TestDoctorProfileChecks_ServerEdition404Skipped(t *testing.T) {
	client, _ := doctorClient(t, map[string]cannedResponse{"GET /api/v1/clients": refuse(http.StatusNotFound, `{"success":false,"error":"not found"}`)})
	checks := collectProfileChecks(client)
	require.Len(t, checks, 2)
	for _, c := range checks {
		require.Equal(t, "skipped", c.Status)
		require.Contains(t, c.Message, "404")
	}
	out := captureOutput(func() { printProfileChecksSection(checks) })
	require.Contains(t, out, "– profiles.binding_bypass")
}

func TestDoctor_JSONHasProfileChecks(t *testing.T) {
	prev := doctorOutput
	doctorOutput = "json"
	t.Cleanup(func() { doctorOutput = prev })
	checks := []profileCheck{{ID: "profiles.binding_bypass", Status: "ok", Message: "fine"}}
	out := captureOutput(func() {
		require.NoError(t, outputDiagnosticsWithProfileChecks(map[string]interface{}{"total_issues": 0}, nil, nil, "", nil, checks))
	})
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	got, ok := parsed["profile_checks"].([]any)
	require.True(t, ok, "additive top-level key profile_checks")
	require.Len(t, got, 1)
	require.Equal(t, "profiles.binding_bypass", got[0].(map[string]any)["id"])
	require.Contains(t, parsed, "diagnostics", "existing keys are unchanged")

	// Not collected (older run shape): the key is absent, as before.
	out = captureOutput(func() {
		require.NoError(t, outputDiagnostics(map[string]interface{}{"total_issues": 0}, nil, nil, "", nil))
	})
	require.NotContains(t, out, "profile_checks")
}

func TestDoctor_PrettyWarnBlocksAllClear(t *testing.T) {
	prev := doctorOutput
	doctorOutput = "pretty"
	t.Cleanup(func() { doctorOutput = prev })
	warn := []profileCheck{{ID: "profiles.binding_bypass", Status: "warn", Message: "anonymous callers are denied"}}
	out := captureOutput(func() {
		_ = outputDiagnosticsWithProfileChecks(map[string]interface{}{"total_issues": 0}, nil, nil, "", nil, warn)
	})
	require.Contains(t, out, "Profiles & clients")
	require.NotContains(t, out, "All systems operational")

	ok := []profileCheck{{ID: "profiles.binding_bypass", Status: "ok", Message: "fine"}}
	out = captureOutput(func() {
		_ = outputDiagnosticsWithProfileChecks(map[string]interface{}{"total_issues": 0}, nil, nil, "", nil, ok)
	})
	require.Contains(t, out, "All systems operational")
}
