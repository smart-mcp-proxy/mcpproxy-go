package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Spec 108-l (SC-006, L8, T122): the CLI half of the cross-surface record-set test.
// internal/httpapi/testdata/sc006_activity_recordsets.json holds, for one seeded
// dataset, the REST query and the ids it returns for every (profile, client,
// token, status) combination (generated and verified against an independent
// reference filter by TestSC006ActivityRecordSets). For each combination a fake
// daemon answers the golden's ids for the query it receives; the CLI must send
// exactly the combination's parameters and print exactly its ids, in order.

type p108SC006Set struct {
	URLQuery  string   `json:"url_query"`
	RESTQuery string   `json:"rest_query"`
	IDs       []string `json:"ids"`
}

type p108SC006Golden struct {
	RecordSets []p108SC006Set `json:"recordsets"`
	Refused    []struct {
		RESTQuery string `json:"rest_query"`
		Contains  string `json:"contains"`
	} `json:"refused"`
}

func p108LoadSC006(t *testing.T) p108SC006Golden {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "httpapi", "testdata", "sc006_activity_recordsets.json"))
	require.NoError(t, err, "the SC-006 golden is missing (internal/httpapi TestSC006ActivityRecordSets writes it)")
	var g p108SC006Golden
	require.NoError(t, json.Unmarshal(b, &g))
	return g
}

// p108SC006Params keeps the four SC-006 parameters of a query, nothing else.
func p108SC006Params(q url.Values) url.Values {
	out := url.Values{}
	for _, key := range []string{"profile", "client", "token", "status"} {
		if v := q.Get(key); v != "" {
			out.Set(key, v)
		}
	}
	return out
}

func TestSC006CLIRecordSets(t *testing.T) {
	golden := p108LoadSC006(t)
	require.Len(t, golden.RecordSets, 64)

	byQuery := map[string][]string{}
	for _, s := range golden.RecordSets {
		byQuery[s.RESTQuery] = s.IDs
	}
	daemon := p108StartActivityDaemon(t, func(q url.Values) []map[string]any {
		records := []map[string]any{}
		for _, id := range byQuery[p108SC006Params(q).Encode()] {
			records = append(records, map[string]any{
				"id": id, "type": "tool_call", "server_name": "srv", "tool_name": "tool_" + id,
				"status": "success", "timestamp": "2026-10-01T09:00:00Z",
			})
		}
		return records
	})

	for _, s := range golden.RecordSets {
		want, err := url.ParseQuery(s.RESTQuery)
		require.NoError(t, err)
		var args []string
		for _, key := range []string{"profile", "client", "token", "status"} {
			if v := want.Get(key); v != "" {
				args = append(args, "--"+key, v)
			}
		}
		ids, runErr := p108RunActivityList(t, args...)
		require.NoError(t, runErr, s.RESTQuery)
		require.Equal(t, want, p108SC006Params(daemon.last(t)), "the CLI must send exactly the combination %q", s.RESTQuery)
		if len(s.IDs) == 0 {
			require.Empty(t, ids, s.RESTQuery)
		} else {
			require.Equal(t, s.IDs, ids, "the CLI must print exactly the combination's ids for %q", s.RESTQuery)
		}
	}

	t.Run("a combination the REST refuses is refused by the CLI with the same words", func(t *testing.T) {
		require.NotEmpty(t, golden.Refused)
		_, err := p108RunActivityList(t, "--token", "a", "--agent", "b")
		require.Error(t, err)
		require.Contains(t, err.Error(), golden.Refused[0].Contains)
	})

	t.Run("mutation: a CLI that dropped a filter would be caught", func(t *testing.T) {
		ids, err := p108RunActivityList(t, "--client", "cursor") // status omitted on purpose
		require.NoError(t, err)
		blocked := byQuery["client=cursor&status=blocked"]
		require.NotEqual(t, blocked, ids, "the golden tells the two combinations apart")
	})
}
