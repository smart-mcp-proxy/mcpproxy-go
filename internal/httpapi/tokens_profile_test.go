package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// Spec 108-f T074c (FR-031, contracts/rest-api.md Tokens): `profile` on create,
// the reserved client- prefix, the new row fields and the list filters.

func postToken(t *testing.T, srv *Server, body map[string]interface{}) *map[string]interface{} {
	t.Helper()
	w := doRequest(t, srv, http.MethodPost, "/api/v1/tokens", body)
	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	out["_status"] = float64(w.Code)
	return &out
}

func TestTokensProfile_ProfileSetsThePinWithDefaults(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServerWithProfiles(t, store, []string{"server1"}, []string{"work-readonly"})

	resp := postToken(t, srv, map[string]interface{}{"name": "pin2", "profile": "work-readonly"})
	require.Equal(t, float64(http.StatusCreated), (*resp)["_status"], *resp)

	stored, err := store.GetAgentTokenByName("pin2")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "work-readonly", stored.ProfilePin)
	assert.Equal(t, []string{"*"}, stored.AllowedServers, "scope comes from the profile: servers default to *")
	assert.ElementsMatch(t, []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}, stored.Permissions, "and all three permissions")
	assert.Empty(t, stored.ProfileMode, "a regular token has no mode")
	assert.Empty(t, stored.Kind == auth.KindClient)

	// An explicit permission list is kept.
	resp = postToken(t, srv, map[string]interface{}{"name": "pin3", "profile": "work-readonly", "permissions": []string{"read"}})
	require.Equal(t, float64(http.StatusCreated), (*resp)["_status"])
	stored, _ = store.GetAgentTokenByName("pin3")
	assert.Equal(t, []string{"read"}, stored.Permissions)

	// profile_pin is the same field: both naming the same profile is fine,
	// naming different ones is a 400 on `profile`.
	resp = postToken(t, srv, map[string]interface{}{"name": "pin4", "profile": "work-readonly", "profile_pin": "work-readonly"})
	assert.Equal(t, float64(http.StatusCreated), (*resp)["_status"])
	resp = postToken(t, srv, map[string]interface{}{"name": "pin5", "profile": "work-readonly", "profile_pin": "other"})
	assert.Equal(t, float64(http.StatusBadRequest), (*resp)["_status"])
	assert.Equal(t, "profile", (*resp)["field"])

	// An unknown profile keeps the existing 400.
	resp = postToken(t, srv, map[string]interface{}{"name": "pin6", "profile": "ghost"})
	assert.Equal(t, float64(http.StatusBadRequest), (*resp)["_status"])
}

func TestTokensProfile_ClientPrefixIsReservedWithAFieldNotA500(t *testing.T) {
	store := newMockTokenStore()
	srv := newTestTokenServer(t, store, []string{"server1"})
	resp := postToken(t, srv, map[string]interface{}{"name": "client-x"})
	assert.Equal(t, float64(http.StatusBadRequest), (*resp)["_status"])
	assert.Equal(t, "name", (*resp)["field"])
	assert.Equal(t, `token names starting with "client-" are reserved for client credentials`, (*resp)["error"])
	_, err := store.GetAgentTokenByName("client-x")
	require.NoError(t, err)
	assert.Empty(t, store.tokens, "checked before storage")
}

func seedTokenRows(store *mockTokenStore) {
	now := time.Now()
	store.tokens["ro-bot"] = auth.AgentToken{Name: "ro-bot", AllowedServers: []string{"github"}, Permissions: []string{"read"},
		ExpiresAt: now.Add(time.Hour), ProfilePin: "work-readonly", TokenPrefix: "mcp_agt_aaaa"}
	store.tokens["pin2"] = auth.AgentToken{Name: "pin2", AllowedServers: []string{"*"},
		Permissions: []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}, ExpiresAt: now.Add(time.Hour), ProfilePin: "work-readonly"}
	store.tokens["loose"] = auth.AgentToken{Name: "loose", AllowedServers: []string{"*"}, Permissions: []string{"read"}, ExpiresAt: now.Add(time.Hour)}
	store.tokens["client-cursor"] = auth.AgentToken{Name: "client-cursor", Kind: auth.KindClient, ClientID: "cursor", ProfileMode: auth.ProfileModeLocked,
		ProfilePin: "work-full", AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}, ExpiresAt: now.Add(time.Hour)}
}

func listTokens(t *testing.T, srv *Server, query string) map[string]map[string]interface{} {
	t.Helper()
	w := doRequest(t, srv, http.MethodGet, "/api/v1/tokens"+query, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var data struct {
		Tokens []map[string]interface{} `json:"tokens"`
	}
	decodeSuccess(t, w, &data)
	out := map[string]map[string]interface{}{}
	for _, row := range data.Tokens {
		out[row["name"].(string)] = row
	}
	return out
}

func TestTokensProfile_RowsCarryKindClientIDModeAndLegacyScope(t *testing.T) {
	store := newMockTokenStore()
	seedTokenRows(store)
	srv := newTestTokenServerWithProfiles(t, store, []string{"server1"}, []string{"work-readonly", "work-full"})

	rows := listTokens(t, srv, "")
	assert.Equal(t, "agent", rows["ro-bot"]["kind"], "an empty kind reads as agent")
	assert.Equal(t, true, rows["ro-bot"]["legacy_scope"], "allowed_servers=[github] is a legacy scope")
	assert.Equal(t, false, rows["pin2"]["legacy_scope"], "*/all permissions: scope comes from the profile")
	assert.Equal(t, true, rows["loose"]["legacy_scope"], "read-only permissions are a legacy scope")

	cursor := rows["client-cursor"]
	assert.Equal(t, "client", cursor["kind"])
	assert.Equal(t, "cursor", cursor["client_id"])
	assert.Equal(t, "locked", cursor["profile_mode"])
	assert.Equal(t, false, cursor["legacy_scope"], "a client credential has no legacy scope")
	assert.NotContains(t, rows["ro-bot"], "client_id")
}

func TestTokensProfile_ListFilters(t *testing.T) {
	store := newMockTokenStore()
	seedTokenRows(store)
	srv := newTestTokenServerWithProfiles(t, store, []string{"server1"}, []string{"work-readonly", "work-full"})

	names := func(query string) []string {
		var out []string
		for n := range listTokens(t, srv, query) {
			out = append(out, n)
		}
		return out
	}
	assert.ElementsMatch(t, []string{"ro-bot", "pin2"}, names("?profile=work-readonly"))
	assert.ElementsMatch(t, []string{"client-cursor"}, names("?profile=work-full"), "both kinds are matched by their CURRENT pin")
	assert.ElementsMatch(t, []string{"loose"}, names("?profile=-"), "- selects the unpinned")
	assert.ElementsMatch(t, []string{"pin2"}, names("?token=pin2"))
	assert.ElementsMatch(t, []string{"ro-bot"}, names("?profile=work-readonly&token=ro-bot"))
	assert.Empty(t, names("?profile=ghost"), "an unknown value is not an error: no rows")
	assert.Empty(t, names("?token=ghost"))

	// client= stays unsupported on this route; nothing is leaked before the gate.
	w := doRequest(t, srv, http.MethodGet, "/api/v1/tokens?client=cursor", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "unsupported_scope_filter")
}
