package httpapi

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// scopeParamsController serves one record set through the list, stream and
// detail paths with the REAL storage.ActivityFilter.Matches, and records the
// last filter it was given so a test can see what a param turned into.
type scopeParamsController struct {
	baseController
	records    []*storage.ActivityRecord
	lastFilter storage.ActivityFilter
	streamed   int
	lifetime   *internalRuntime.UsageAggregate
}

func (c *scopeParamsController) GetCurrentConfig() *config.Config {
	return &config.Config{APIKey: scopeAdminAPIKey}
}

func (c *scopeParamsController) ListActivities(f storage.ActivityFilter) ([]*storage.ActivityRecord, int, error) {
	c.lastFilter = f
	f.Validate()
	var out []*storage.ActivityRecord
	for _, r := range c.records {
		if f.Matches(r) {
			out = append(out, r)
		}
	}
	total := len(out)
	if len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, total, nil
}

func (c *scopeParamsController) StreamActivities(f storage.ActivityFilter) <-chan *storage.ActivityRecord {
	c.lastFilter = f
	ch := make(chan *storage.ActivityRecord, len(c.records))
	for _, r := range c.records {
		if f.Matches(r) {
			ch <- r
			c.streamed++
		}
	}
	close(ch)
	return ch
}

func (c *scopeParamsController) GetActivity(id string) (*storage.ActivityRecord, error) {
	for _, r := range c.records {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, nil
}

func (c *scopeParamsController) UsageSnapshot() *internalRuntime.UsageAggregate {
	if c.lifetime != nil {
		return c.lifetime
	}
	return internalRuntime.NewUsageAggregate()
}

func (c *scopeParamsController) GetTokenSavings() (*contracts.ServerTokenMetrics, error) {
	return &contracts.ServerTokenMetrics{SavedTokens: 900, SavedTokensPercentage: 90}, nil
}

func attribRecord(id, server, tool string, mut func(*storage.ActivityRecord)) *storage.ActivityRecord {
	r := &storage.ActivityRecord{
		ID: id, Type: storage.ActivityTypeToolCall, ServerName: server, ToolName: tool,
		Status: storage.ActivityStatusSuccess, Timestamp: time.Now().UTC().Add(-time.Minute),
	}
	if mut != nil {
		mut(r)
	}
	return r
}

func scopeParamsRecords() []*storage.ActivityRecord {
	return []*storage.ActivityRecord{
		attribRecord("cursor-1", "alpha", "alpha_tool", func(r *storage.ActivityRecord) {
			r.Profile, r.ProfileSource, r.ClientID, r.ClientName, r.TokenName = "work-readonly", "pin", "cursor", "Cursor", "client-cursor"
			r.Arguments = map[string]interface{}{"_auth_token_prefix": "mcp_cli_aaaa", "_auth_agent_name": "client-cursor", "_auth_auth_type": "agent"}
		}),
		attribRecord("zed-1", "alpha", "alpha_tool", func(r *storage.ActivityRecord) {
			r.Profile, r.ProfileSource, r.ClientID, r.ClientName, r.TokenName = "work-full", "binding", "zed", "Zed", "client-zed"
			r.Arguments = map[string]interface{}{"_auth_token_prefix": "mcp_cli_bbbb", "_auth_agent_name": "client-zed", "_auth_auth_type": "agent"}
		}),
		attribRecord("legacy-1", "alpha", "alpha_tool", func(r *storage.ActivityRecord) {
			r.Metadata = map[string]interface{}{"profile": "work-readonly", "client_name": "Claude Code"}
			r.Arguments = map[string]interface{}{"_auth_agent_name": "ci-bot", "_auth_auth_type": "agent"}
		}),
		attribRecord("api-1", "alpha", "alpha_tool", nil),
		attribRecord("beta-1", "beta", "beta_tool", func(r *storage.ActivityRecord) {
			r.Profile, r.ClientID, r.TokenName = "work-readonly", "cursor", "client-cursor"
		}),
	}
}

func scopeParamsAdminServer(t *testing.T, ctrl *scopeParamsController) *Server {
	t.Helper()
	return NewServer(ctrl, zap.NewNop().Sugar(), nil)
}

func scopeGetJSON(t *testing.T, srv *Server, path, key string, into interface{}) *httptest.ResponseRecorder {
	t.Helper()
	rec := scopeGet(t, srv, path, key)
	if into != nil && rec.Code == http.StatusOK {
		// json.Unmarshal into a reused struct keeps stale values for omitted
		// fields; every call decodes into a zero value.
		reflect.ValueOf(into).Elem().Set(reflect.Zero(reflect.TypeOf(into).Elem()))
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), into), rec.Body.String())
	}
	return rec
}

func activityIDs(t *testing.T, srv *Server, path, key string) []string {
	t.Helper()
	var resp struct {
		Data contracts.ActivityListResponse `json:"data"`
	}
	rec := scopeGetJSON(t, srv, path, key, &resp)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	ids := make([]string, 0, len(resp.Data.Activities))
	for _, a := range resp.Data.Activities {
		ids = append(ids, a.ID)
	}
	return ids
}

func TestActivityScopeParams_ListFiltersAndAliases(t *testing.T) {
	ctrl := &scopeParamsController{records: scopeParamsRecords()}
	srv := scopeParamsAdminServer(t, ctrl)

	assert.ElementsMatch(t, []string{"cursor-1", "beta-1"}, activityIDs(t, srv, "/api/v1/activity?client=cursor", scopeAdminAPIKey))
	assert.ElementsMatch(t, []string{"cursor-1", "legacy-1", "beta-1"}, activityIDs(t, srv, "/api/v1/activity?profile=work-readonly", scopeAdminAPIKey),
		"legacy metadata.profile still matches")
	assert.ElementsMatch(t, []string{"cursor-1"}, activityIDs(t, srv, "/api/v1/activity?token=client-cursor&server=alpha", scopeAdminAPIKey))
	assert.ElementsMatch(t, []string{"legacy-1"}, activityIDs(t, srv, "/api/v1/activity?token=ci-bot", scopeAdminAPIKey),
		"legacy _auth_agent_name matches the token filter")
	assert.ElementsMatch(t, []string{"legacy-1"}, activityIDs(t, srv, "/api/v1/activity?client_name=Claude+Code", scopeAdminAPIKey))
	assert.ElementsMatch(t, []string{"api-1"}, activityIDs(t, srv, "/api/v1/activity?client=-&profile=-&token=-", scopeAdminAPIKey)[:1],
		"the unattributed sentinel selects the record with nothing")

	// agent is an alias of token, and the two must agree.
	assert.Equal(t, activityIDs(t, srv, "/api/v1/activity?token=client-zed", scopeAdminAPIKey),
		activityIDs(t, srv, "/api/v1/activity?agent=client-zed", scopeAdminAPIKey))
	assert.Equal(t, "client-zed", ctrl.lastFilter.TokenName, "agent maps onto TokenName, not the legacy AgentName")
	assert.Empty(t, ctrl.lastFilter.AgentName)
	assert.ElementsMatch(t, []string{"zed-1"}, activityIDs(t, srv, "/api/v1/activity?token=client-zed&agent=client-zed", scopeAdminAPIKey))

	rec := scopeGet(t, srv, "/api/v1/activity?token=a&agent=b", scopeAdminAPIKey)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "token and agent must name the same token (agent is an alias of token)")
}

func TestActivityScopeParams_RecordsCarryAttributionFields(t *testing.T) {
	srv := scopeParamsAdminServer(t, &scopeParamsController{records: scopeParamsRecords()})
	var resp struct {
		Data contracts.ActivityListResponse `json:"data"`
	}
	rec := scopeGetJSON(t, srv, "/api/v1/activity?client=cursor&server=alpha", scopeAdminAPIKey, &resp)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, resp.Data.Activities, 1)
	a := resp.Data.Activities[0]
	assert.Equal(t, "work-readonly", a.Profile)
	assert.Equal(t, "pin", a.ProfileSource)
	assert.Equal(t, "cursor", a.ClientID)
	assert.Equal(t, "Cursor", a.ClientName)
	assert.Equal(t, "client-cursor", a.TokenName)
}

func TestActivityScopeParams_ClientNameOnlyOnActivityAndExport(t *testing.T) {
	srv := scopeParamsAdminServer(t, &scopeParamsController{records: scopeParamsRecords()})
	for _, path := range []string{"/api/v1/activity", "/api/v1/activity/export"} {
		rec := scopeGet(t, srv, path+"?client_name=Cursor", scopeAdminAPIKey)
		assert.Equal(t, http.StatusOK, rec.Code, path)
	}
	for _, path := range []string{"/api/v1/activity/summary", "/api/v1/activity/usage", "/api/v1/sessions"} {
		rec := scopeGet(t, srv, path+"?client_name=Cursor", scopeAdminAPIKey)
		require.Equal(t, http.StatusBadRequest, rec.Code, path)
		assert.Contains(t, rec.Body.String(), "client_name is not supported on this endpoint; filter by client", path)
	}
}

func TestActivityScopeParams_SummaryCountsOnlyTheFilteredRecords(t *testing.T) {
	ctrl := &scopeParamsController{records: scopeParamsRecords()}
	srv := scopeParamsAdminServer(t, ctrl)

	var resp struct {
		Data contracts.ActivitySummaryResponse `json:"data"`
	}
	rec := scopeGetJSON(t, srv, "/api/v1/activity/summary?client=cursor", scopeAdminAPIKey, &resp)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 2, resp.Data.TotalCount)
	assert.Equal(t, "cursor", ctrl.lastFilter.ClientID, "the streamed filter carries the scope param")

	rec = scopeGetJSON(t, srv, "/api/v1/activity/summary?agent=client-zed", scopeAdminAPIKey, &resp)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, resp.Data.TotalCount)
}

func TestActivityScopeParams_UsageScanPathOnlyWhenFiltered(t *testing.T) {
	lifetime := internalRuntime.NewUsageAggregate()
	lifetime.Apply(attribRecord("lt", "alpha", "lifetime_only_tool", nil))
	ctrl := &scopeParamsController{records: scopeParamsRecords(), lifetime: lifetime}
	srv := scopeParamsAdminServer(t, ctrl)

	type usage struct {
		Data contracts.UsageAggregateResponse `json:"data"`
	}
	var unfiltered usage
	rec := scopeGetJSON(t, srv, "/api/v1/activity/usage?window=24h", scopeAdminAPIKey, &unfiltered)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, unfiltered.Data.Tools, 1)
	assert.Equal(t, "lifetime_only_tool", unfiltered.Data.Tools[0].Tool, "the unfiltered path serves the persisted aggregate")
	assert.Zero(t, ctrl.streamed, "and scans nothing")

	var filtered usage
	rec = scopeGetJSON(t, srv, "/api/v1/activity/usage?window=24h&profile=work-readonly", scopeAdminAPIKey, &filtered)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 3, ctrl.streamed, "the filtered path streams the matching records")
	got := map[string]int64{}
	for _, tl := range filtered.Data.Tools {
		got[tl.Server+":"+tl.Tool] = tl.Calls
	}
	assert.Equal(t, map[string]int64{"alpha:alpha_tool": 2, "beta:beta_tool": 1}, got, "only work-readonly's records, not the lifetime-only tool")
	assert.Zero(t, filtered.Data.TokensSaved, "the global tokens-saved headline is omitted under a scope filter")
	assert.Equal(t, "work-readonly", ctrl.lastFilter.Profile)
	assert.False(t, ctrl.lastFilter.StartTime.IsZero(), "24h bounds the scan")

	rec = scopeGetJSON(t, srv, "/api/v1/activity/usage?window=all&client=cursor", scopeAdminAPIKey, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, ctrl.lastFilter.StartTime.IsZero(), "window=all has no lower bound")

	rec = scopeGet(t, srv, "/api/v1/activity/usage?token=a&agent=b", scopeAdminAPIKey)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "token and agent must name the same token")
}

func TestActivityScopeParams_UsageCacheKeyDiffersPerScopeParam(t *testing.T) {
	base := usageParams{window: "24h", top: 20, sort: "calls"}
	keys := map[string]string{}
	for name, p := range map[string]usageParams{
		"none":    base,
		"profile": {window: "24h", top: 20, sort: "calls", profile: "x"},
		"client":  {window: "24h", top: 20, sort: "calls", client: "x"},
		"token":   {window: "24h", top: 20, sort: "calls", token: "x"},
	} {
		key := p.cacheKey()
		for other, k := range keys {
			assert.NotEqual(t, k, key, "%s vs %s", name, other)
		}
		keys[name] = key
	}
	// Length-prefixed: a value containing the separator cannot collide.
	a := usageParams{window: "24h", top: 20, sort: "calls", profile: "a|b"}.cacheKey()
	b := usageParams{window: "24h", top: 20, sort: "calls", profile: "a", client: "b"}.cacheKey()
	assert.NotEqual(t, a, b)

	// A scoped caller's filtered view depends on WHICH token it is.
	o1 := usageParams{window: "24h", top: 20, sort: "calls", scoped: true, allowed: []string{"alpha"}, token: "x", owner: &storage.ActivityIdentityOwner{TokenName: "t1", TokenPrefix: "p1"}}
	o2 := usageParams{window: "24h", top: 20, sort: "calls", scoped: true, allowed: []string{"alpha"}, token: "x", owner: &storage.ActivityIdentityOwner{TokenName: "t2", TokenPrefix: "p2"}}
	assert.NotEqual(t, o1.cacheKey(), o2.cacheKey())
}

func TestActivityScopeParams_CSVHasSixTrailingColumns(t *testing.T) {
	srv := scopeParamsAdminServer(t, &scopeParamsController{records: scopeParamsRecords()})
	rec := scopeGet(t, srv, "/api/v1/activity/export?format=csv&client=cursor&server=alpha", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code)
	rows, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	header := rows[0]
	require.GreaterOrEqual(t, len(header), 19)
	assert.Equal(t, []string{"parent_id", "profile", "profile_source", "client_id", "client_name", "token_name", "block_reason"}, header[12:19],
		"the six new columns are APPENDED after parent_id")
	assert.Equal(t, []string{"work-readonly", "pin", "cursor", "Cursor", "client-cursor", ""}, rows[1][13:19])
}

// A scoped caller filtering on another token's name must learn nothing: the
// filter evaluates the view in which foreign rows carry no attribution.
func TestActivityScopeParams_ScopedCallerCannotProbeForeignAttribution(t *testing.T) {
	records := []*storage.ActivityRecord{
		attribRecord("own", "alpha", "alpha_tool", func(r *storage.ActivityRecord) {
			r.Profile, r.ProfileSource, r.ClientID, r.TokenName = "work-readonly", "pin", "ci-client", "scoped-ci"
			r.ClientName = "CI"
			r.Arguments = map[string]interface{}{"_auth_token_prefix": "PLACEHOLDER", "_auth_agent_name": "scoped-ci", "_auth_auth_type": "agent"}
		}),
		attribRecord("foreign", "alpha", "alpha_tool", func(r *storage.ActivityRecord) {
			r.Profile, r.ProfileSource, r.ClientID, r.TokenName = "work-full", "binding", "zed", "client-zed"
			r.ClientName = "Zed"
			r.Arguments = map[string]interface{}{"_auth_token_prefix": "mcp_cli_bbbb", "_auth_agent_name": "client-zed", "_auth_auth_type": "agent"}
		}),
	}
	ctrl := &scopeParamsController{records: records}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})
	// The scoped token's prefix is derived from its raw secret.
	prefix := auth.TokenPrefix(token)
	records[0].Arguments["_auth_token_prefix"] = prefix

	// Probing a foreign token name matches nothing, total included.
	var resp struct {
		Data contracts.ActivityListResponse `json:"data"`
	}
	rec := scopeGetJSON(t, srv, "/api/v1/activity?token=client-zed", token, &resp)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 0, resp.Data.Total)
	scopeGetJSON(t, srv, "/api/v1/activity?client=zed", token, &resp)
	assert.Equal(t, 0, resp.Data.Total)
	scopeGetJSON(t, srv, "/api/v1/activity?profile=work-full", token, &resp)
	assert.Equal(t, 0, resp.Data.Total)

	// Its own rows filter normally.
	rec = scopeGetJSON(t, srv, "/api/v1/activity?token=scoped-ci", token, &resp)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, resp.Data.Total)
	assert.Equal(t, "own", resp.Data.Activities[0].ID)
	assert.Equal(t, "ci-client", resp.Data.Activities[0].ClientID)

	// The foreign row reads as unattributed, and its attribution is blanked.
	rec = scopeGetJSON(t, srv, "/api/v1/activity?token=-", token, &resp)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, resp.Data.Total)
	f := resp.Data.Activities[0]
	assert.Equal(t, "foreign", f.ID)
	assert.Empty(t, f.Profile)
	assert.Empty(t, f.ProfileSource)
	assert.Empty(t, f.ClientID)
	assert.Empty(t, f.TokenName)
	assert.Equal(t, "Zed", f.ClientName, "client_name is self-reported and stays")

	// Detail is redacted the same way.
	var detail struct {
		Data contracts.ActivityDetailResponse `json:"data"`
	}
	rec = scopeGetJSON(t, srv, "/api/v1/activity/foreign", token, &detail)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, detail.Data.Activity.Profile)
	assert.Empty(t, detail.Data.Activity.ClientID)
	assert.Empty(t, detail.Data.Activity.TokenName)

	// CSV and JSON export blank the foreign row too.
	rec = scopeGet(t, srv, "/api/v1/activity/export?format=csv", token)
	require.Equal(t, http.StatusOK, rec.Code)
	rows, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	require.NoError(t, err)
	byID := map[string][]string{}
	for _, row := range rows[1:] {
		byID[row[0]] = row
	}
	assert.Equal(t, []string{"work-readonly", "pin", "ci-client", "CI", "scoped-ci", ""}, byID["own"][13:19])
	assert.Equal(t, []string{"", "", "", "Zed", "", ""}, byID["foreign"][13:19])
	assert.NotContains(t, rec.Body.String(), "client-zed")

	rec = scopeGet(t, srv, "/api/v1/activity/export", token)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "client-zed")
	assert.NotContains(t, rec.Body.String(), "work-full")
}

// Live QA failure 2: the legacy Spec 057 metadata.profile slug (the /mcp/p/<slug>
// a call arrived on) names a profile just like the first-class field does, so a
// scoped caller must not read it off another token's row either (FR-031/FR-032:
// binding disclosure is admin-only). Its own rows keep it.
func TestActivityScopeParams_ForeignMetadataProfileIsRedacted(t *testing.T) {
	records := []*storage.ActivityRecord{
		attribRecord("own", "alpha", "alpha_tool", func(r *storage.ActivityRecord) {
			r.Metadata = map[string]interface{}{"profile": "work-readonly", "client_name": "CI"}
			r.Arguments = map[string]interface{}{"_auth_token_prefix": "PLACEHOLDER", "_auth_agent_name": "scoped-ci", "_auth_auth_type": "agent"}
		}),
		attribRecord("foreign", "alpha", "alpha_tool", func(r *storage.ActivityRecord) {
			r.Metadata = map[string]interface{}{"profile": "work-full", "client_name": "Zed"}
			r.Arguments = map[string]interface{}{"_auth_token_prefix": "mcp_cli_bbbb", "_auth_agent_name": "client-zed", "_auth_auth_type": "agent"}
		}),
	}
	ctrl := &scopeParamsController{records: records}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})
	records[0].Arguments["_auth_token_prefix"] = auth.TokenPrefix(token)

	var list struct {
		Data contracts.ActivityListResponse `json:"data"`
	}
	rec := scopeGetJSON(t, srv, "/api/v1/activity", token, &list)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	byID := map[string]contracts.ActivityRecord{}
	for _, a := range list.Data.Activities {
		byID[a.ID] = a
	}
	require.Contains(t, byID, "own")
	require.Contains(t, byID, "foreign")
	assert.Equal(t, "work-readonly", byID["own"].Metadata["profile"], "its own rows keep the slug")
	assert.NotContains(t, byID["foreign"].Metadata, "profile", "another token's slug is withheld")
	assert.Equal(t, "Zed", byID["foreign"].Metadata["client_name"], "self-reported client_name stays")
	// The stored record is never edited in place.
	assert.Equal(t, "work-full", records[1].Metadata["profile"])

	var detail struct {
		Data contracts.ActivityDetailResponse `json:"data"`
	}
	rec = scopeGetJSON(t, srv, "/api/v1/activity/foreign", token, &detail)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, detail.Data.Activity.Metadata, "profile")
}
