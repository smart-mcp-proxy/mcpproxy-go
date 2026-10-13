package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

func TestRestCall_DecodesRefusalFields(t *testing.T) {
	body := `{"success":false,"error":"profile in use","code":"profile_in_use","field":"name","action":"x",
"used_by":{"clients":[{"id":"cursor","mode":"locked"}],"tokens":["ci"],"anonymous_profile":true},
"bindings":[{"client_id":"cursor","token_name":"client-cursor","profile":"p","mode":"locked"}],
"fixes":[{"kind":"set_anonymous_profile","target":"p"}],
"conflicting_token":"client-cursor","remediation":"revoke it","skipped":[{"client_id":"a","code":"c"}],"request_id":"req-1"}`
	data, ref, err := decodeRESTResponse(http.StatusConflict, []byte(body))
	require.NoError(t, err)
	require.Nil(t, data)
	require.NotNil(t, ref)
	require.Equal(t, http.StatusConflict, ref.Status)
	require.Equal(t, "profile in use", ref.Error)
	require.Equal(t, "profile_in_use", ref.Code)
	require.Equal(t, "name", ref.Field)
	require.Equal(t, "x", ref.Action)
	require.Equal(t, []string{"ci"}, ref.UsedBy.Tokens)
	require.True(t, ref.UsedBy.AnonymousProfile)
	require.Equal(t, "cursor", ref.Bindings[0].ClientID)
	require.Equal(t, "p", ref.Fixes[0].Target)
	require.Equal(t, "client-cursor", ref.ConflictingToken)
	require.Equal(t, "revoke it", ref.Remediation)
	require.JSONEq(t, `[{"client_id":"a","code":"c"}]`, string(ref.Skipped))
	require.Equal(t, "req-1", ref.RequestID)
}

func TestRestCall_SuccessReturnsDataAndAPlainHTTPErrorIsARefusal(t *testing.T) {
	data, ref, err := decodeRESTResponse(http.StatusOK, []byte(`{"success":true,"data":{"a":1}}`))
	require.NoError(t, err)
	require.Nil(t, ref)
	require.JSONEq(t, `{"a":1}`, string(data))

	_, ref, err = decodeRESTResponse(http.StatusBadGateway, []byte(`{"success":false}`))
	require.NoError(t, err)
	require.Equal(t, "HTTP 502", ref.Error)

	_, _, err = decodeRESTResponse(http.StatusOK, []byte(`<html>`))
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
}

func TestRestCall_ConnectConflictDiscriminatesOnAction(t *testing.T) {
	_, ref, err := decodeRESTResponse(http.StatusConflict, []byte(`{"success":false,"data":{},"error":"stale","action":"precondition_failed"}`))
	require.NoError(t, err)
	require.Equal(t, "precondition_failed", ref.Code)
}

// R4: every refusal exits 1 even when its text mentions "config" (the string
// heuristics in classifyError would map that to exit 4).
func TestRefusalErrors_AlwaysExit1(t *testing.T) {
	for _, body := range []string{
		`{"success":false,"error":"invalid config: require_mcp_auth and anonymous_profile conflict"}`,
		`{"success":false,"error":"permission denied reading config file"}`,
		`{"success":false,"error":"a client bound to profile p could escape it while require_mcp_auth is off","code":"binding_bypassable_without_auth","bindings":[],"fixes":[{"kind":"require_mcp_auth"}]}`,
	} {
		_, ref, err := decodeRESTResponse(http.StatusBadRequest, []byte(body))
		require.NoError(t, err)
		require.Equal(t, ExitCodeGeneralError, classifyError(ref.asError()), body)
	}
}

func TestPrintData_YAMLAndJSONKeepUnknownFields(t *testing.T) {
	raw := json.RawMessage(`{"known":1,"future_field":{"nested":[1,2]}}`)
	setOutputGlobals(t, "json", false)
	out := captureOutput(func() { require.NoError(t, printData(raw)) })
	require.Contains(t, out, `"future_field"`)
	setOutputGlobals(t, "yaml", false)
	out = captureOutput(func() { require.NoError(t, printData(raw)) })
	require.Contains(t, out, "future_field:")
	require.Contains(t, out, "nested:")
}

func TestGuardFixLine_OneWording(t *testing.T) {
	require.Equal(t, "set require_mcp_auth: true (config or Settings → Security)", guardFixLine(runtime.GuardFix{Kind: "require_mcp_auth"}, "p"))
	require.Equal(t, "mcpproxy profile anonymous ro", guardFixLine(runtime.GuardFix{Kind: "set_anonymous_profile", Target: "ro"}, "p"))
	require.Equal(t, "mcpproxy profile anonymous <p> with a profile not wider than p", guardFixLine(runtime.GuardFix{Kind: "set_anonymous_profile"}, "p"))
}
