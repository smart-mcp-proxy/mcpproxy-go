package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Spec 108 FR-032 (T059): GET /api/v1/tools?client= / ?profile= through the
// real router, with real stored tokens, against the enforcement-matrix fixture.

// newViewAsFixture is the REST fixture with the pieces the listing endpoints
// touch that the call-path fixture leaves unset (the Server's logger).
func newViewAsFixture(t *testing.T) *restV3Fixture {
	t.Helper()
	f := newProfilesV3RESTFixture(t, nil)
	f.srv.logger = zap.NewNop()
	return f
}

func decodeGlobalTools(t *testing.T, body string) contracts.GlobalToolsResponse {
	t.Helper()
	var envelope struct {
		Success bool                          `json:"success"`
		Data    contracts.GlobalToolsResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &envelope), body)
	require.True(t, envelope.Success, body)
	return envelope.Data
}

func toolRow(t *testing.T, resp contracts.GlobalToolsResponse, server, tool string) contracts.Tool {
	t.Helper()
	for _, row := range resp.Tools {
		if row.ServerName == server && row.Name == tool {
			return row
		}
	}
	t.Fatalf("no row %s:%s in %d rows", server, tool, len(resp.Tools))
	return contracts.Tool{}
}

func TestToolsViewAs_AdminClientHasVerdictPerRow(t *testing.T) {
	f := newViewAsFixture(t)
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)

	plain := f.do(http.MethodGet, "/api/v1/tools", restV3AdminKey, nil, "")
	require.Equal(t, http.StatusOK, plain.Code, plain.Body.String())
	base := decodeGlobalTools(t, plain.Body.String())

	rec := f.do(http.MethodGet, "/api/v1/tools?client=cursor", restV3AdminKey, nil, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeGlobalTools(t, rec.Body.String())
	assert.Nil(t, resp.Counts, "an administrator gets every row, no counts")
	require.Len(t, resp.Tools, len(base.Tools), "an administrator gets every row")

	for _, row := range resp.Tools {
		require.NotNil(t, row.Access, "%s:%s", row.ServerName, row.Name)
		assert.NotEmpty(t, row.ProfileTier, "%s:%s", row.ServerName, row.Name)
		// The intrinsic tier keeps its existing field.
		assert.Equal(t, toolRow(t, base, row.ServerName, row.Name).Tier, row.Tier)
	}

	create := toolRow(t, resp, "github", "create_issue")
	assert.False(t, create.Access.Visible)
	assert.False(t, create.Access.Callable)
	assert.Equal(t, "above_tier_cap", create.Access.Reason)
	assert.Equal(t, contracts.TierWrite, create.ProfileTier)

	search := toolRow(t, resp, "github", "search_code")
	assert.Equal(t, "unannotated_hidden", search.Access.Reason)
	fs := toolRow(t, resp, "filesystem", "read_text_file")
	assert.Equal(t, "server_not_in_profile", fs.Access.Reason)
	list := toolRow(t, resp, "github", "list_issues")
	assert.True(t, list.Access.Visible)
	assert.True(t, list.Access.Callable)
	assert.Empty(t, list.Access.Reason)

	// stats are recomputed over the returned rows (all of them for an admin).
	assert.Equal(t, len(resp.Tools), resp.Stats.Total)
}

func TestToolsViewAs_ClientRequiresAdministrator(t *testing.T) {
	f := newViewAsFixture(t)
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	scoped := f.mint("ci-bot", "")

	rec := f.do(http.MethodGet, "/api/v1/tools?client=cursor", scoped, nil, "")
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "operation requires admin access")
}

func TestToolsViewAs_NonAdminProfileGetsOnlyVisibleRowsAndCounts(t *testing.T) {
	f := newViewAsFixture(t)
	scoped := f.mint("ci-bot", "") // unpinned: sees every server

	rec := f.do(http.MethodGet, "/api/v1/tools?profile=work-readonly", scoped, nil, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeGlobalTools(t, rec.Body.String())

	got := map[string]bool{}
	for _, row := range resp.Tools {
		got[row.ServerName+":"+row.Name] = true
		require.NotNil(t, row.Access)
		assert.True(t, row.Access.Visible)
		assert.Empty(t, row.Access.Reason, "a non-administrator never sees a reason")
	}
	assert.Equal(t, map[string]bool{"github:list_issues": true, "notion:update_page": true}, got)

	require.NotNil(t, resp.Counts)
	assert.Equal(t, 2, resp.Counts.Visible)
	assert.Equal(t, 5, resp.Counts.Hidden)
	assert.Equal(t, 2, resp.Stats.Total, "stats cover only the returned rows")

	body := rec.Body.String()
	for _, excluded := range []string{"create_issue", "delete_repo", "search_code", "get_secret_scanning_alert", "read_text_file", "filesystem"} {
		assert.NotContains(t, body, excluded, "excluded rows are administrator-only")
	}
	for _, reason := range []string{"above_tier_cap", "denied_by_rule", "unannotated_hidden", "server_not_in_profile"} {
		assert.NotContains(t, body, reason)
	}
}

func TestToolsViewAs_UnreachableProfileIsIndistinguishableFromUnknown(t *testing.T) {
	f := newViewAsFixture(t)
	// A token scoped to notion alone cannot reach work-full's server set...
	// except through notion, so scope it to a server no profile mentions.
	scoped := f.mintWith("only-fs", "", []string{"unlisted"}, []string{auth.PermRead})

	unreachable := f.do(http.MethodGet, "/api/v1/tools?profile=work-readonly", scoped, nil, "req-1")
	unknown := f.do(http.MethodGet, "/api/v1/tools?profile=no-such-profile", scoped, nil, "req-1")
	require.Equal(t, http.StatusNotFound, unreachable.Code, unreachable.Body.String())
	assert.Equal(t, unknown.Code, unreachable.Code)
	assert.Equal(t, strings.ReplaceAll(unknown.Body.String(), "no-such-profile", "work-readonly"), unreachable.Body.String(),
		"an unreachable profile answers byte-equal to an unknown one")
	assert.Contains(t, unreachable.Body.String(), "profile not found")

	// For an administrator an unknown name is 404 too.
	rec := f.do(http.MethodGet, "/api/v1/tools?profile=no-such-profile", restV3AdminKey, nil, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = f.do(http.MethodGet, "/api/v1/tools?client=no-such-client", restV3AdminKey, nil, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "client not found")
}

func TestToolsViewAs_InvalidCombinations(t *testing.T) {
	f := newViewAsFixture(t)
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)

	rec := f.do(http.MethodGet, "/api/v1/tools?client=cursor&profile=work-readonly", restV3AdminKey, nil, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "use either client or profile, not both")

	for _, q := range []string{"client=-", "profile=-"} {
		rec = f.do(http.MethodGet, "/api/v1/tools?"+q, restV3AdminKey, nil, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, q)
		assert.Contains(t, rec.Body.String(), "'-' (unattributed) is only valid on activity and session filters")
	}

	rec = f.do(http.MethodGet, "/api/v1/tools?token=ci-bot", restV3AdminKey, nil, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "unsupported_scope_filter")
}

func TestToolsViewAs_QuarantinedServerRowsNeverAppear(t *testing.T) {
	f := newViewAsFixture(t)
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	p := &precedenceFixture{proxy: f.proxy, ups: f.upstreams, f: f}
	p.setGithubServerState(t, true, true)

	rec := f.do(http.MethodGet, "/api/v1/tools?client=cursor", restV3AdminKey, nil, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeGlobalTools(t, rec.Body.String())
	for _, row := range resp.Tools {
		assert.NotEqual(t, "github", row.ServerName, "rows of a quarantined server stay withheld (#1064)")
	}
	require.NotEmpty(t, resp.Tools)
}

// With no view-as parameter the response carries none of the new keys: an
// ordinary listing is unchanged.
func TestToolsViewAs_OrdinaryListingHasNoViewAsKeys(t *testing.T) {
	f := newViewAsFixture(t)
	rec := f.do(http.MethodGet, "/api/v1/tools", restV3AdminKey, nil, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	for _, key := range []string{`"access"`, `"profile_tier"`, `"counts"`} {
		assert.NotContains(t, rec.Body.String(), key)
	}
}

func TestServersProfileFilter_RowsAndToolCounts(t *testing.T) {
	f := newViewAsFixture(t)

	rec := f.do(http.MethodGet, "/api/v1/servers?profile=work-readonly", restV3AdminKey, nil, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var envelope struct {
		Data contracts.GetServersResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	counts := map[string]int{}
	for _, srv := range envelope.Data.Servers {
		counts[srv.Name] = srv.ToolCount
	}
	assert.Equal(t, map[string]int{"github": 1, "notion": 1}, counts,
		"only the profile's servers, each with the number of tools visible under it (list_issues; update_page by allow)")
	assert.Equal(t, 2, envelope.Data.Stats.TotalServers, "stats are recomputed over the returned rows")

	// A non-administrator gets the same rows only within its own entitlement.
	scoped := f.mintWith("gh-only", "", []string{"github"}, []string{auth.PermRead})
	rec = f.do(http.MethodGet, "/api/v1/servers?profile=work-readonly", scoped, nil, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data.Servers, 1)
	assert.Equal(t, "github", envelope.Data.Servers[0].Name)

	rec = f.do(http.MethodGet, "/api/v1/servers?profile=no-such-profile", restV3AdminKey, nil, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = f.do(http.MethodGet, "/api/v1/servers?profile=-", restV3AdminKey, nil, "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = f.do(http.MethodGet, "/api/v1/servers?client=cursor", restV3AdminKey, nil, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "unsupported_scope_filter")
}
