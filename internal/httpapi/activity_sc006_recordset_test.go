package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 108-l (SC-006, L8, T122): for ONE seeded dataset, the Activity record set of
// every (profile, client, token, status) filter combination is identical on REST,
// the CLI, the Web UI and macOS. The server computes the set once; this test
// derives each expected set INDEPENDENTLY from the seed (a reference filter that
// shares no code with storage.ActivityFilter), asserts REST returns exactly it,
// and writes it to internal/httpapi/testdata/sc006_activity_recordsets.json. The
// other three surfaces replay that golden: the CLI (TestSC006CLIRecordSets,
// cmd/mcpproxy), the Web UI (sc006-activity-recordset.spec.ts) and macOS
// (SC006RecordSetTests.swift) must each SEND the combination's query and RENDER
// exactly its ids.

const sc006GoldenPath = "internal/httpapi/testdata/sc006_activity_recordsets.json"

// p108SC006Row is one seeded record with the attribution it must be listed under
// (stated explicitly, not read back from the storage helpers).
type p108SC006Row struct {
	id                   string
	profile, client, tok string // "" = unattributed on that axis
	status               string
	build                func(*storage.ActivityRecord)
}

// p108SC006Seed: Cursor was reassigned from work-readonly to work-full in the
// middle (c1, c2 precede it, c3 follows), Codex is switchable on work-full, the
// token ro-bot is pinned to work-readonly, and two pre-upgrade records carry no
// attribution fields at all (u1) or only the legacy metadata shape (u2).
func p108SC006Seed() []p108SC006Row {
	attr := func(profile, source, client, token string) func(*storage.ActivityRecord) {
		return func(r *storage.ActivityRecord) {
			r.Profile, r.ProfileSource, r.ClientID, r.TokenName = profile, source, client, token
		}
	}
	return []p108SC006Row{
		{id: "c1", profile: "work-readonly", client: "cursor", tok: "client-cursor", status: "success", build: attr("work-readonly", "pin", "cursor", "client-cursor")},
		{id: "c2", profile: "work-readonly", client: "cursor", tok: "client-cursor", status: "blocked", build: attr("work-readonly", "pin", "cursor", "client-cursor")},
		{id: "c3", profile: "work-full", client: "cursor", tok: "client-cursor", status: "success", build: attr("work-full", "pin", "cursor", "client-cursor")},
		{id: "x1", profile: "work-full", client: "codex", tok: "client-codex", status: "success", build: attr("work-full", "binding", "codex", "client-codex")},
		{id: "x2", profile: "work-full", client: "codex", tok: "client-codex", status: "blocked", build: attr("work-full", "binding", "codex", "client-codex")},
		{id: "b1", profile: "work-readonly", tok: "ro-bot", status: "success", build: attr("work-readonly", "pin", "", "ro-bot")},
		{id: "b2", profile: "work-readonly", tok: "ro-bot", status: "blocked", build: attr("work-readonly", "pin", "", "ro-bot")},
		{id: "u1", status: "success"},
		{
			// legacy: only metadata.profile and the auth agent name; the filters must
			// still see work-readonly / ro-bot (US3-4).
			id: "u2", profile: "work-readonly", tok: "ro-bot", status: "success",
			build: func(r *storage.ActivityRecord) {
				r.Metadata = map[string]interface{}{"profile": "work-readonly"}
				r.Arguments = map[string]interface{}{"_auth_agent_name": "ro-bot", "_auth_auth_type": "agent"}
			},
		},
	}
}

// p108SC006Records builds storage records, newest first as the API lists them.
func p108SC006Records(rows []p108SC006Row) []*storage.ActivityRecord {
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	recs := make([]*storage.ActivityRecord, len(rows))
	for i, row := range rows {
		rec := &storage.ActivityRecord{
			ID: row.id, Type: storage.ActivityTypeToolCall, ServerName: "srv", ToolName: "tool_" + row.id,
			Status: row.status, Timestamp: base.Add(time.Duration(i) * time.Minute),
		}
		if row.build != nil {
			row.build(rec)
		}
		recs[i] = rec
	}
	// newest first
	for i, j := 0, len(recs)-1; i < j; i, j = i+1, j-1 {
		recs[i], recs[j] = recs[j], recs[i]
	}
	return recs
}

// p108SC006Matches is the reference filter: an axis value of "-" selects the
// unattributed, "" does not filter.
func p108SC006Matches(row p108SC006Row, profile, client, token, status string) bool {
	axis := func(want, got string) bool {
		switch want {
		case "":
			return true
		case "-":
			return got == ""
		}
		return want == got
	}
	return axis(profile, row.profile) && axis(client, row.client) && axis(token, row.tok) &&
		(status == "" || status == row.status)
}

type p108SC006Set struct {
	URLQuery  string   `json:"url_query"`
	RESTQuery string   `json:"rest_query"`
	IDs       []string `json:"ids"`
}

type p108SC006Refusal struct {
	RESTQuery string `json:"rest_query"`
	Status    int    `json:"status"`
	Contains  string `json:"contains"`
}

type p108SC006Golden struct {
	Comment    string             `json:"_comment"`
	RecordSets []p108SC006Set     `json:"recordsets"`
	Refused    []p108SC006Refusal `json:"refused"`
}

// p108SC006Query renders the combination's parameters in sorted order, the
// canonical spelling every surface compares (the URL and REST names are the same
// four by the Spec 109 url-filter contract).
func p108SC006Query(profile, client, token, status string) string {
	v := url.Values{}
	for k, val := range map[string]string{"profile": profile, "client": client, "token": token, "status": status} {
		if val != "" {
			v.Set(k, val)
		}
	}
	return v.Encode() // Encode sorts by key
}

func TestSC006ActivityRecordSets(t *testing.T) {
	rows := p108SC006Seed()
	ctrl := &scopeParamsController{records: p108SC006Records(rows)}
	srv := scopeParamsAdminServer(t, ctrl)

	// newest first, the order the API (and so every surface) lists in
	order := make([]string, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		order = append(order, rows[i].id)
	}

	var sets []p108SC006Set
	sawPreReassignment := false
	for _, profile := range []string{"work-readonly", "work-full", "-", ""} {
		for _, client := range []string{"cursor", "codex", "-", ""} {
			for _, token := range []string{"ro-bot", ""} {
				for _, status := range []string{"blocked", ""} {
					query := p108SC006Query(profile, client, token, status)
					want := []string{}
					for _, id := range order {
						for _, row := range rows {
							if row.id == id && p108SC006Matches(row, profile, client, token, status) {
								want = append(want, id)
							}
						}
					}
					path := "/api/v1/activity"
					if query != "" {
						path += "?" + query
					}
					got := activityIDs(t, srv, path, scopeAdminAPIKey)
					require.Equal(t, want, got, "REST disagrees with the reference filter for %q", query)
					sets = append(sets, p108SC006Set{URLQuery: query, RESTQuery: query, IDs: want})
					if profile == "work-readonly" && client == "cursor" && token == "" && status == "" {
						sawPreReassignment = true
						require.Equal(t, []string{"c2", "c1"}, got, "calls made before Cursor was reassigned still match the profile it had then (US3-5)")
					}
				}
			}
		}
	}
	require.True(t, sawPreReassignment)
	require.Len(t, sets, 64)
	sort.Slice(sets, func(i, j int) bool { return sets[i].URLQuery < sets[j].URLQuery })

	// The dataset is rich enough to tell the filters apart: at least a dozen
	// different answers, and some combinations are legitimately empty (Codex has
	// no work-readonly record), which every surface must render as empty too.
	distinct := map[string]bool{}
	empty := 0
	for _, s := range sets {
		distinct[strings.Join(s.IDs, ",")] = true
		if len(s.IDs) == 0 {
			empty++
		}
	}
	require.GreaterOrEqual(t, len(distinct), 12)
	require.NotZero(t, empty)

	// What REST refuses is the documented text.
	refused := []p108SC006Refusal{{RESTQuery: "agent=b&token=a", Status: http.StatusBadRequest, Contains: "must name the same token"}}
	for _, r := range refused {
		rec := scopeGet(t, srv, "/api/v1/activity?"+r.RESTQuery, scopeAdminAPIKey)
		require.Equal(t, r.Status, rec.Code)
		require.Contains(t, rec.Body.String(), r.Contains)
	}

	golden := p108SC006Golden{
		Comment:    "Spec 108-l SC-006: generated by TestSC006ActivityRecordSets (UPDATE_GOLDEN=1). Seed: c1,c2 = Cursor on work-readonly before it was reassigned, c3 = Cursor on work-full after; x1,x2 = Codex (switchable, work-full); b1,b2 = token ro-bot pinned to work-readonly; u1 = a pre-upgrade record with no attribution; u2 = a pre-upgrade record with only the legacy metadata shape. ids are newest first. url_query and rest_query are the same four parameters: the Spec 109 url-filter contract maps the URL names 1:1 onto the REST names.",
		RecordSets: sets,
		Refused:    refused,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // keep & readable in the query strings
	require.NoError(t, enc.Encode(golden))

	path := p108RepoRoot(t) + "/" + sc006GoldenPath
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(path[:strings.LastIndex(path, "/")], 0o755))
		require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "the SC-006 golden is missing: run UPDATE_GOLDEN=1 go test -run TestSC006ActivityRecordSets ./internal/httpapi/")
	require.Equal(t, string(want), buf.String(), "the record sets drifted from the golden (regenerate with UPDATE_GOLDEN=1, then check the CLI, Web and macOS replays)")
}
