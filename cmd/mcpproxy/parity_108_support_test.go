package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

// Spec 108-l: shared support for the CLI parity tests that replay `activity
// list` against a fake daemon (the Check 2 acceptance test and the SC-006
// record-set replay). Every helper is prefixed p108.

// p108ActivityDaemon serves GET /api/v1/activity by calling answer with the
// query the CLI sent, and records every query. It also answers /api/v1/status,
// which the CLI's daemon detection probes.
type p108ActivityDaemon struct {
	mu      sync.Mutex
	queries []url.Values
}

func p108StartActivityDaemon(t *testing.T, answer func(url.Values) []map[string]any) *p108ActivityDaemon {
	t.Helper()
	d := &p108ActivityDaemon{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"running":true}}`))
		case "/api/v1/activity":
			d.mu.Lock()
			d.queries = append(d.queries, r.URL.Query())
			d.mu.Unlock()
			records := answer(r.URL.Query())
			if records == nil {
				records = []map[string]any{}
			}
			body, _ := json.Marshal(map[string]any{"success": true, "data": map[string]any{
				"activities": records, "total": len(records), "limit": 50, "offset": 0,
			}})
			_, _ = w.Write(body)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	withClientDaemon(t, srv.URL)
	return d
}

func (d *p108ActivityDaemon) last(t *testing.T) url.Values {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	require.NotEmpty(t, d.queries, "the CLI sent no activity request")
	return d.queries[len(d.queries)-1]
}

// p108ResetFlags returns every flag of cmd to its default, so a second run of the
// same package-level command does not inherit the first run's values (cobra
// keeps them on the command object between Execute calls).
func p108ResetFlags(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
}

// p108RunActivityList runs `mcpproxy activity list <args> -o json` and returns the
// ids the CLI printed, in order.
func p108RunActivityList(t *testing.T, args ...string) ([]string, error) {
	t.Helper()
	p108ResetFlags(activityListCmd)
	t.Cleanup(func() { p108ResetFlags(activityListCmd) })
	out, _, err := runCLI(t, GetActivityCommand, "json", append([]string{"list"}, args...)...)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Activities []struct {
			ID string `json:"id"`
		} `json:"activities"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &doc), out)
	ids := make([]string, 0, len(doc.Activities))
	for _, a := range doc.Activities {
		ids = append(ids, a.ID)
	}
	return ids, nil
}
