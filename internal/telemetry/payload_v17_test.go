package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 126 T012: the schema-17 payload. The key sets below are the schema-17
// part of specs/126-day-two-activation/contracts/telemetry-day-two.schema.json.

var (
	v17UpstreamOutcomesKeys      = []string{"coverage", "succeeded", "succeeded_non_starter", "unusable"}
	v17UpstreamOutcomesKeysFresh = []string{"coverage", "succeeded", "succeeded_non_starter", "unusable", "window_age"}
	v17DayTwoKeysFresh           = []string{"install_age", "install_origin", "success_scope_ever"}
	v17DayTwoKeysExisting        = []string{"install_origin", "success_scope_ever"}
)

func v17Keys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		t.Fatalf("not an object: %s (%v)", raw, err)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func v17EqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPayloadV17(t *testing.T) {
	if SchemaVersion != 17 {
		t.Fatalf("SchemaVersion = %d, want 17 (Spec 126 PR-A)", SchemaVersion)
	}

	cases := []struct {
		name        string
		edition     string
		hasAnchor   bool
		wantDayTwo  []string // nil: day_two must be absent
		wantOutKeys []string
		wantOrigin  string
		wantPartial bool
	}{
		{name: "personal fresh install", edition: "personal", hasAnchor: true, wantDayTwo: v17DayTwoKeysFresh, wantOutKeys: v17UpstreamOutcomesKeysFresh, wantOrigin: "fresh"},
		{name: "personal existing install", edition: "personal", hasAnchor: false, wantDayTwo: v17DayTwoKeysExisting, wantOutKeys: v17UpstreamOutcomesKeys, wantOrigin: "existing", wantPartial: true},
		{name: "server edition", edition: "server", hasAnchor: true, wantDayTwo: nil, wantOutKeys: v17UpstreamOutcomesKeys},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, cleanup := newTestActivationDB(t)
			defer cleanup()
			srv, received, setStatus := newOutcomeCaptureServer(t)
			setStatus(http.StatusNoContent)
			svc := newOutcomeService(t, srv.URL, c.edition)
			svc.SetActivationStore(NewActivationStore(), db)
			anchor := time.Now().UTC().Add(-2 * time.Hour)
			svc.SetInstallAnchorProvider(func() (time.Time, bool, error) {
				if !c.hasAnchor {
					return time.Time{}, false, nil
				}
				return anchor, true, nil
			})
			// A fresh install's store was empty at open; the existing install
			// starts this build for the first time (no marker yet: partial).
			svc.InitOutcomeWindow(c.hasAnchor)
			svc.Registry().RecordUpstreamOutcome(true, false)
			if err := RecordSuccessScope(db, false); err != nil {
				t.Fatal(err)
			}

			svc.sendHeartbeat(context.Background())
			got := received()
			if len(got) != 1 {
				t.Fatalf("received %d heartbeats", len(got))
			}
			raw := got[0].raw
			if string(raw["schema_version"]) != "17" {
				t.Fatalf("schema_version = %s, want 17", raw["schema_version"])
			}
			if keys := v17Keys(t, raw["upstream_outcomes"]); !v17EqualStrings(keys, c.wantOutKeys) {
				t.Fatalf("upstream_outcomes keys = %v, want %v", keys, c.wantOutKeys)
			}
			wantCoverage := OutcomeCoverageFull
			if c.wantPartial {
				wantCoverage = OutcomeCoveragePartial
			}
			if o := got[0].payload.UpstreamOutcomes; o.Succeeded != "1-10" || o.SucceededNonStarter != "1-10" || o.Unusable != "0" || o.Coverage != wantCoverage {
				t.Fatalf("upstream_outcomes = %+v", o)
			}

			rawDayTwo, has := raw["day_two"]
			if c.wantDayTwo == nil {
				if has {
					t.Fatalf("day_two must be absent: %s", rawDayTwo)
				}
				return
			}
			if !has {
				t.Fatal("day_two missing")
			}
			if keys := v17Keys(t, rawDayTwo); !v17EqualStrings(keys, c.wantDayTwo) {
				t.Fatalf("day_two keys = %v, want %v", keys, c.wantDayTwo)
			}
			dt := got[0].payload.DayTwo
			if dt.InstallOrigin != c.wantOrigin || dt.SuccessScopeEver != "non_starter" {
				t.Fatalf("day_two = %+v", dt)
			}
			if c.hasAnchor && dt.InstallAge != "d0_6" {
				t.Fatalf("install_age = %q, want d0_6", dt.InstallAge)
			}

			// Every built payload must pass the anonymity scanner.
			body, err := json.Marshal(got[0].payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := ScanForPII(body); err != nil {
				t.Fatalf("schema-17 payload rejected by ScanForPII: %v", err)
			}
			// quickstart L4: one sample payload.
			t.Logf("schema-17 sample (%s): upstream_outcomes=%s day_two=%s", c.name, raw["upstream_outcomes"], rawDayTwo)
		})
	}
}

// Opt-out sends nothing: no heartbeat, and the day_two provider is never
// consulted.
func TestPayloadV17_OptOutSendsNothing(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	cfg := &config.Config{Telemetry: &config.TelemetryConfig{
		Enabled: boolPtr(true), AnonymousID: "anon-xyz", Endpoint: server.URL,
	}}
	svc := New(cfg, "", "v1.2.3", "personal", zap.NewNop())
	var calls atomic.Int32
	svc.SetInstallAnchorProvider(func() (time.Time, bool, error) {
		calls.Add(1)
		return time.Now(), true, nil
	})
	svc.Registry().RecordUpstreamOutcome(true, false)
	svc.optedOut.Store(true)

	svc.sendHeartbeat(context.Background())

	if hits.Load() != 0 {
		t.Fatalf("heartbeat transmitted after opt-out: %d", hits.Load())
	}
	if calls.Load() != 0 {
		t.Fatalf("install-anchor provider consulted %d times after opt-out", calls.Load())
	}
}

// An install with origin "existing" sends no window_age (the receiver rejects
// that combination), even after a window opened at an accepted send.
func TestPayloadV17_ExistingInstallSendsNoWindowAge(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()
	srv, captured, setStatus := newOutcomeCaptureServer(t)
	setStatus(http.StatusNoContent)
	svc := newOutcomeService(t, srv.URL, "personal")
	svc.SetActivationStore(NewActivationStore(), db)
	svc.SetInstallAnchorProvider(func() (time.Time, bool, error) { return time.Time{}, false, nil })
	svc.InitOutcomeWindow(false)
	svc.Registry().RecordUpstreamOutcome(true, false)
	svc.sendHeartbeat(context.Background()) // the first window
	svc.Registry().RecordUpstreamOutcome(true, false)
	svc.sendHeartbeat(context.Background()) // a window opened at an accepted send

	got := captured()
	if len(got) != 2 {
		t.Fatalf("received %d heartbeats", len(got))
	}
	for i, hb := range got {
		if hb.payload.DayTwo == nil || hb.payload.DayTwo.InstallOrigin != InstallOriginExisting {
			t.Fatalf("heartbeat %d: day_two = %+v, want existing", i, hb.payload.DayTwo)
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(hb.raw["upstream_outcomes"], &obj); err != nil {
			t.Fatal(err)
		}
		if _, has := obj["window_age"]; has {
			t.Fatalf("heartbeat %d: an existing install sent window_age: %s", i, hb.raw["upstream_outcomes"])
		}
	}
}
