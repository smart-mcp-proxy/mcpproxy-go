package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 108-h (H1/H2): ProfilesErrorBody is the one mapping from a profiles- or
// clients-service error to its REST status and body. The REST writers add the
// envelope (`success`, `request_id`); the MCP `profiles` tool returns the body
// as its error text.

func profilesErrorCases() []struct {
	name   string
	err    error
	status int
	body   map[string]any
} {
	guard := &internalRuntime.BindingGuardError{
		Bindings: []internalRuntime.BindingRef{{ClientID: "cursor", TokenName: "client-cursor", Profile: "work-readonly", Mode: "locked"}},
		Fixes:    []internalRuntime.GuardFix{{Kind: "require_mcp_auth"}, {Kind: "set_anonymous_profile", Target: "work-readonly"}},
	}
	used := internalRuntime.UsedBy{Clients: []internalRuntime.UsedByClient{{ID: "cursor", Mode: "locked"}}, Tokens: []string{}}
	return []struct {
		name   string
		err    error
		status int
		body   map[string]any
	}{
		{"guard", guard, http.StatusConflict, map[string]any{
			"error": guard.Error(), "code": profile.ErrorCodeBindingBypassable,
			"bindings": []GuardBinding{{ClientID: "cursor", TokenName: "client-cursor", Profile: "work-readonly", Mode: "locked"}},
			"fixes":    []GuardFixOption{{Kind: "require_mcp_auth"}, {Kind: "set_anonymous_profile", Target: "work-readonly"}},
		}},
		{"not found", &internalRuntime.ProfileNotFoundError{Name: "x"}, http.StatusNotFound, map[string]any{"error": "profile not found"}},
		{"unknown profile sentinel", profile.ErrUnknownProfile, http.StatusNotFound, map[string]any{"error": "profile not found"}},
		{"exists", &internalRuntime.ProfileExistsError{Name: "x"}, http.StatusConflict, map[string]any{
			"error": `profile "x" already exists`, "code": profile.ErrorCodeProfileExists}},
		{"name mismatch", &internalRuntime.NameMismatchError{Path: "a", Body: "b"}, http.StatusConflict, map[string]any{
			"error": "name must equal the path; use POST /profiles/{name}/rename", "code": profile.ErrorCodeNameMismatch}},
		{"in use", &internalRuntime.ProfileInUseError{UsedBy: used}, http.StatusConflict, map[string]any{
			"error": "profile in use", "code": profile.ErrorCodeProfileInUse, "used_by": used}},
		{"is anonymous", &internalRuntime.ProfileIsAnonymousError{UsedBy: used}, http.StatusConflict, map[string]any{
			"error": (&internalRuntime.ProfileIsAnonymousError{}).Error(), "code": profile.ErrorCodeProfileIsAnonymous, "used_by": used}},
		{"validation", &internalRuntime.ValidationError{Field: "max_tier", Message: "invalid max_tier"}, http.StatusBadRequest, map[string]any{
			"error": "invalid max_tier", "field": "max_tier"}},
		{"no client credential", &internalRuntime.NoClientCredentialError{ClientID: "codex", State: profile.CredentialStateNone}, http.StatusConflict, map[string]any{
			"error": (&internalRuntime.NoClientCredentialError{ClientID: "codex"}).Error(), "code": profile.ErrorCodeNoClientCredential}},
		{"unknown client", profile.ErrUnknownClient, http.StatusNotFound, map[string]any{"error": "client not found"}},
		{"unknown token", profile.ErrUnknownToken, http.StatusNotFound, map[string]any{"error": "token not found"}},
		{"client credential token", profile.ErrClientCredentialToken, http.StatusBadRequest, map[string]any{"error": profile.ErrClientCredentialToken.Error()}},
		{"explain builtin", profile.ErrExplainBuiltinTool, http.StatusBadRequest, map[string]any{"error": profile.ErrExplainBuiltinTool.Error()}},
		{"request error", &requestError{http.StatusBadRequest, "bad"}, http.StatusBadRequest, map[string]any{"error": "bad"}},
		{"no minter", connect.ErrNoCredentialMinter, http.StatusServiceUnavailable, map[string]any{"error": connect.ErrNoCredentialMinter.Error()}},
		{"evaluator unavailable", internalRuntime.ErrEvaluatorUnavailable, http.StatusServiceUnavailable, map[string]any{"error": "profiles service unavailable"}},
		{"config unavailable", internalRuntime.ErrConfigUnavailable, http.StatusInternalServerError, map[string]any{"error": "Configuration unavailable"}},
		{"unknown error", errors.New("boom"), http.StatusInternalServerError, map[string]any{"error": "profiles operation failed"}},
	}
}

func TestProfilesErrorBody_EveryTypedError(t *testing.T) {
	for _, tc := range profilesErrorCases() {
		t.Run(tc.name, func(t *testing.T) {
			status, body := ProfilesErrorBody(tc.err)
			assert.Equal(t, tc.status, status)
			assert.NotContains(t, body, "success", "the body carries no REST envelope")
			assert.NotContains(t, body, "request_id", "the body carries no REST envelope")
			want, err := json.Marshal(tc.body)
			require.NoError(t, err)
			got, err := json.Marshal(body)
			require.NoError(t, err)
			assert.JSONEq(t, string(want), string(got))
		})
	}
}

// TestRESTWriters_UseProfilesErrorBody: every profile route's error body is the
// shared body plus the envelope, so REST and MCP cannot drift.
func TestRESTWriters_UseProfilesErrorBody(t *testing.T) {
	g := newProfilesRig(t)
	routes := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/profiles", `{"name":"tmp","servers":["alpha"]}`},
		{http.MethodPut, "/api/v1/profiles/tmp", `{"servers":["alpha"]}`},
		{http.MethodPost, "/api/v1/profiles/tmp/rename", `{"new_name":"tmp2"}`},
		{http.MethodDelete, "/api/v1/profiles/tmp", ``},
	}
	for _, tc := range profilesErrorCases() {
		if tc.name == "unknown error" {
			continue // the writers log it and answer their own generic 500
		}
		for _, route := range routes {
			t.Run(tc.name+" "+route.method+" "+route.path, func(t *testing.T) {
				g.fake.err = tc.err
				rec := g.do(scopeAdminAPIKey, route.method, route.path, route.body)
				status, body := ProfilesErrorBody(tc.err)
				require.Equal(t, status, rec.Code, rec.Body.String())
				var got map[string]any
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
				assert.Equal(t, false, got["success"])
				delete(got, "success")
				delete(got, "request_id")
				want, err := json.Marshal(body)
				require.NoError(t, err)
				gotRaw, err := json.Marshal(got)
				require.NoError(t, err)
				assert.JSONEq(t, string(want), string(gotRaw))
			})
		}
	}
}
