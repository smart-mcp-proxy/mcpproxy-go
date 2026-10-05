package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
)

// Spec 108 FR-032 (T065a): `tools list --client/--profile` and
// `upstream list --profile` are thin mappings of the REST view-as parameters.

func resetToolsViewAs(t *testing.T) {
	t.Helper()
	prevC, prevP := toolsClientView, toolsProfileView
	toolsClientView, toolsProfileView = "", ""
	t.Cleanup(func() { toolsClientView, toolsProfileView = prevC, prevP })
}

func TestToolsViewAs_FlagsRegisteredAndQueryMapping(t *testing.T) {
	resetToolsViewAs(t)
	assert.NotNil(t, toolsListCmd.Flags().Lookup("client"))
	assert.NotNil(t, toolsListCmd.Flags().Lookup("profile"))
	// -t stays the timeout shorthand: no new shorthand was registered.
	assert.Equal(t, "timeout", toolsListCmd.Flags().ShorthandLookup("t").Name)

	q, err := toolsViewAsQuery()
	require.NoError(t, err)
	assert.Empty(t, q.Encode())

	toolsClientView = "cursor"
	q, err = toolsViewAsQuery()
	require.NoError(t, err)
	assert.Equal(t, "cursor", q.Get("client"))
	assert.False(t, q.Has("profile"))

	toolsClientView, toolsProfileView = "", "work-readonly"
	q, err = toolsViewAsQuery()
	require.NoError(t, err)
	assert.Equal(t, "work-readonly", q.Get("profile"))

	toolsClientView = "cursor"
	_, err = toolsViewAsQuery()
	require.Error(t, err, "client and profile are mutually exclusive")
	assert.Contains(t, err.Error(), "not both")
}

func TestToolsViewAs_GoldenColumns(t *testing.T) {
	plain := []map[string]interface{}{{"name": "list_issues", "server_name": "github", "tier": "read"}}
	headers, rows := globalToolRows(plain)
	assert.Equal(t, []string{"NAME", "SERVER", "STATE", "TIER", "APPROVAL", "HELD", "USAGE", "LAST USED", "DESCRIPTION"}, headers,
		"without view-as the columns are unchanged")
	require.Len(t, rows, 1)

	tools := []map[string]interface{}{
		{"name": "list_issues", "server_name": "github", "tier": "read", "profile_tier": "read",
			"access": map[string]interface{}{"visible": true, "callable": true}},
		{"name": "create_issue", "server_name": "github", "tier": "write", "profile_tier": "write",
			"access": map[string]interface{}{"visible": false, "callable": false, "reason": "above_tier_cap"}},
		{"name": "search_docs", "server_name": "notion", "tier": "read", "approval_status": "pending",
			"access": map[string]interface{}{"visible": true, "callable": false, "reason": "tool_approval"}},
	}
	headers, rows = globalToolRows(tools)
	assert.Equal(t, []string{"NAME", "SERVER", "STATE", "TIER", "ACCESS", "REASON", "APPROVAL", "HELD", "USAGE", "LAST USED", "DESCRIPTION"}, headers)
	require.Len(t, rows, 3)
	assert.Equal(t, []string{"callable", "-"}, []string{rows[0][4], rows[0][5]})
	assert.Equal(t, []string{"hidden", "above_tier_cap"}, []string{rows[1][4], rows[1][5]})
	assert.Equal(t, []string{"visible", "tool_approval"}, []string{rows[2][4], rows[2][5]})
	for _, row := range rows {
		assert.Len(t, row, len(headers))
	}
}

func TestToolsViewAs_QueryReachesTheDaemonAnd403TextPassesThrough(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		if r.URL.Query().Get("client") == "cursor" && r.Header.Get("X-API-Key") == "scoped" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"error":"operation requires admin access"}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"tools":[{"name":"list_issues","server_name":"github"}],"counts":{"visible":1,"hidden":4}}}`))
	}))
	defer srv.Close()

	admin := cliclient.NewClientWithAPIKey(srv.URL, "admin", zap.NewNop().Sugar())
	tools, counts, err := admin.GetGlobalToolsView(context.Background(), url.Values{"profile": {"work-readonly"}})
	require.NoError(t, err)
	assert.Equal(t, "work-readonly", gotQuery.Get("profile"))
	require.Len(t, tools, 1)
	assert.EqualValues(t, 4, counts["hidden"])

	scoped := cliclient.NewClientWithAPIKey(srv.URL, "scoped", zap.NewNop().Sugar())
	_, _, err = scoped.GetGlobalToolsView(context.Background(), url.Values{"client": {"cursor"}})
	require.Error(t, err)
	assert.Contains(t, cliError("failed to get global tools from daemon", err).Error(), "operation requires admin access")
}

func TestUpstreamListProfile_FlagAndQueryMapping(t *testing.T) {
	prev := upstreamListProfile
	t.Cleanup(func() { upstreamListProfile = prev })

	require.NotNil(t, upstreamListCmd.Flags().Lookup("profile"))
	upstreamListProfile = ""
	assert.Empty(t, upstreamListQuery().Encode())
	upstreamListProfile = "work-readonly"
	assert.Equal(t, "work-readonly", upstreamListQuery().Get("profile"))

	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_, _ = w.Write([]byte(`{"success":true,"data":{"servers":[{"name":"github","tool_count":1}]}}`))
	}))
	defer srv.Close()
	client := cliclient.NewClientWithAPIKey(srv.URL, "k", zap.NewNop().Sugar())
	servers, err := client.GetServersWithQuery(context.Background(), upstreamListQuery())
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.Equal(t, "work-readonly", gotQuery.Get("profile"))

	// The old entry point sends no query.
	_, err = client.GetServers(context.Background())
	require.NoError(t, err)
	assert.Empty(t, gotQuery.Encode())
}
