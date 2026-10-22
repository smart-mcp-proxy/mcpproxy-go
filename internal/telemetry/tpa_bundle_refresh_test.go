package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestSchemaVersionIs16 pins the Spec 087 / Spec 101 US3 bump: the five refresh
// counters ride schema 16.
func TestSchemaVersionIs16(t *testing.T) {
	if SchemaVersion != 16 {
		t.Fatalf("SchemaVersion = %d, want 16 (v16 TPA bundle refresh counters)", SchemaVersion)
	}
}

// TestRecordTPABundleRefresh_ClassMapping: every cycle bumps bundle_refresh_cycles
// and exactly the one class counter that matches; an unchanged cycle, or a
// class this build does not know, bumps the cycle counter alone.
func TestRecordTPABundleRefresh_ClassMapping(t *testing.T) {
	cases := []struct {
		class                    TPABundleRefreshClass
		act, rej, unavail, cycle int64
	}{
		{TPABundleRefreshActivated, 1, 0, 0, 1},
		{TPABundleRefreshRejected, 0, 1, 0, 1},
		{TPABundleRefreshUnavailable, 0, 0, 1, 1},
		{TPABundleRefreshUnchanged, 0, 0, 0, 1},
		{TPABundleRefreshClass("something-else"), 0, 0, 0, 1},
		{TPABundleRefreshClass(""), 0, 0, 0, 1},
	}
	for _, tc := range cases {
		t.Run(string(tc.class), func(t *testing.T) {
			r := NewCounterRegistry()
			RecordTPABundleRefreshOn(r, tc.class)
			s := r.Snapshot()
			if s.TPABundleRefreshCycles != tc.cycle || s.TPABundleRefreshActivations != tc.act ||
				s.TPABundleRefreshRejections != tc.rej || s.TPABundleRefreshUnavailable != tc.unavail ||
				s.TPABundleReprotectHits != 0 {
				t.Errorf("snapshot = cycles %d act %d rej %d unavail %d hits %d, want %d/%d/%d/%d/0",
					s.TPABundleRefreshCycles, s.TPABundleRefreshActivations, s.TPABundleRefreshRejections,
					s.TPABundleRefreshUnavailable, s.TPABundleReprotectHits,
					tc.cycle, tc.act, tc.rej, tc.unavail)
			}
		})
	}
}

// TestRecordTPABundleRefresh_Accumulates also covers the nil-registry guard.
func TestRecordTPABundleRefresh_Accumulates(t *testing.T) {
	r := NewCounterRegistry()
	for i := 0; i < 3; i++ {
		RecordTPABundleRefreshOn(r, TPABundleRefreshActivated)
	}
	RecordTPABundleRefreshOn(r, TPABundleRefreshRejected)
	RecordTPABundleRefreshOn(r, TPABundleRefreshUnavailable)
	RecordTPABundleRefreshOn(r, TPABundleRefreshUnavailable)
	RecordTPABundleRefreshOn(r, TPABundleRefreshUnchanged)
	RecordTPABundleReprotectHitOn(r)
	RecordTPABundleReprotectHitOn(r)

	s := r.Snapshot()
	if s.TPABundleRefreshCycles != 7 || s.TPABundleRefreshActivations != 3 || s.TPABundleRefreshRejections != 1 ||
		s.TPABundleRefreshUnavailable != 2 || s.TPABundleReprotectHits != 2 {
		t.Errorf("snapshot = %+v", s)
	}

	// A nil registry is a no-op, not a panic.
	RecordTPABundleRefreshOn(nil, TPABundleRefreshActivated)
	RecordTPABundleReprotectHitOn(nil)
}

// TestRecordTPABundleRefresh_Concurrent: recorders are safe from the refresher
// goroutine and the activation observer at once.
func TestRecordTPABundleRefresh_Concurrent(t *testing.T) {
	r := NewCounterRegistry()
	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); RecordTPABundleRefreshOn(r, TPABundleRefreshActivated) }()
		go func() { defer wg.Done(); RecordTPABundleReprotectHitOn(r) }()
	}
	wg.Wait()
	s := r.Snapshot()
	if s.TPABundleRefreshCycles != n || s.TPABundleRefreshActivations != n || s.TPABundleReprotectHits != n {
		t.Errorf("lost updates: %+v", s)
	}
}

// TestTPABundleRefreshCounters_DrainResetPending: the counters are
// reset-on-accept like every other counter: Drain detaches them, Reset zeroes
// them, and HasPendingCounters sees each of them on its own.
func TestTPABundleRefreshCounters_DrainResetPending(t *testing.T) {
	records := map[string]func(*CounterRegistry){
		"cycles":       func(r *CounterRegistry) { RecordTPABundleRefreshOn(r, TPABundleRefreshUnchanged) },
		"activations":  func(r *CounterRegistry) { RecordTPABundleRefreshOn(r, TPABundleRefreshActivated) },
		"rejections":   func(r *CounterRegistry) { RecordTPABundleRefreshOn(r, TPABundleRefreshRejected) },
		"unavailable":  func(r *CounterRegistry) { RecordTPABundleRefreshOn(r, TPABundleRefreshUnavailable) },
		"reprotecthit": func(r *CounterRegistry) { RecordTPABundleReprotectHitOn(r) },
	}
	for name, rec := range records {
		t.Run(name, func(t *testing.T) {
			r := NewCounterRegistry()
			if r.HasPendingCounters() {
				t.Fatal("fresh registry reports pending counters")
			}
			rec(r)
			if !r.HasPendingCounters() {
				t.Error("HasPendingCounters = false after recording")
			}

			drained := r.Drain()
			if drained.TPAScannerStats() == nil {
				t.Error("drained snapshot lost the counter")
			}
			if after := r.Snapshot(); after.TPAScannerStats() != nil || r.HasPendingCounters() {
				t.Errorf("Drain left counters behind: %+v", after)
			}

			rec(r)
			r.Reset()
			if after := r.Snapshot(); after.TPAScannerStats() != nil || r.HasPendingCounters() {
				t.Errorf("Reset left counters behind: %+v", after)
			}
		})
	}
}

// TestTPABundleRefreshCounters_AloneEmitTPAScanner: the isZero gap. An install
// whose only TPA signal is a refresh cycle must still emit tpa_scanner.
func TestTPABundleRefreshCounters_AloneEmitTPAScanner(t *testing.T) {
	r := NewCounterRegistry()
	if r.Snapshot().TPAScannerStats() != nil {
		t.Fatal("fresh registry projects a tpa_scanner object")
	}
	RecordTPABundleReprotectHitOn(r)
	stats := r.Snapshot().TPAScannerStats()
	if stats == nil {
		t.Fatal("TPAScannerStats() = nil with only bundle_reprotect_hits recorded")
	}
	if stats.BundleReprotectHits != 1 {
		t.Errorf("BundleReprotectHits = %d, want 1", stats.BundleReprotectHits)
	}

	svc := newTPAPayloadTestService(t, false)
	RecordTPABundleRefreshOn(svc.Registry(), TPABundleRefreshActivated)
	RecordTPABundleRefreshOn(svc.Registry(), TPABundleRefreshUnavailable)
	RecordTPABundleRefreshOn(svc.Registry(), TPABundleRefreshRejected)
	RecordTPABundleRefreshOn(svc.Registry(), TPABundleRefreshUnchanged)
	RecordTPABundleReprotectHitOn(svc.Registry())

	payload := svc.BuildPayload()
	if payload.TPAScanner == nil {
		t.Fatal("payload.tpa_scanner = nil with only refresh counters recorded")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	js := string(data)
	for _, want := range []string{
		`"schema_version":16`,
		`"tpa_scanner":{`,
		`"bundle_refresh_cycles":4`,
		`"bundle_refresh_activations":1`,
		`"bundle_refresh_rejections":1`,
		`"bundle_refresh_unavailable":1`,
		`"bundle_reprotect_hits":1`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("payload missing %s:\n%s", want, js)
		}
	}
	withoutBlockedValues(t)
	if err := ScanForPII(data); err != nil {
		t.Fatalf("payload must pass the backstop: %v\n%s", err, js)
	}
}

// TestTPABundleRefreshCounters_MergeWithBundleStateAndPending: the counters
// survive the pending-snapshot path and sit beside the provenance tuple, and a
// heartbeat without refresh activity carries none of the new keys.
func TestTPABundleRefreshCounters_MergeWithBundleStateAndPending(t *testing.T) {
	svc := newBundleTestService(t)
	svc.SetTPABundleProvider(func() *TPABundleSnapshot {
		return &TPABundleSnapshot{Source: "fetch", SignatureVerified: true, BundleVersion: "other"}
	})

	data, _ := json.Marshal(svc.BuildPayload())
	if strings.Contains(string(data), "bundle_refresh_") || strings.Contains(string(data), "bundle_reprotect_") {
		t.Errorf("refresh counters present without any refresh activity:\n%s", data)
	}

	RecordTPABundleRefreshOn(svc.Registry(), TPABundleRefreshActivated)
	snap := svc.Registry().Drain()
	svc.pendingCounterSnapshot = &snap
	payload := svc.BuildPayload()
	if payload.TPAScanner == nil || payload.TPAScanner.BundleRefreshActivations != 1 || payload.TPAScanner.BundleRefreshCycles != 1 {
		t.Fatalf("counters lost on the pending path: %+v", payload.TPAScanner)
	}
	if payload.TPAScanner.BundleSource != "fetch" {
		t.Errorf("bundle tuple lost beside the counters: %+v", payload.TPAScanner)
	}
	data, _ = json.Marshal(payload)
	withoutBlockedValues(t)
	if err := ScanForPII(data); err != nil {
		t.Fatalf("backstop: %v\n%s", err, data)
	}
}

// TestTPABundleRefreshPayload_NeverCarriesAgeURLHostOrReason: whatever is
// recorded, the serialized heartbeat has no freshness, URL, host or reason
// key, and an unknown class string never reaches the wire.
func TestTPABundleRefreshPayload_NeverCarriesAgeURLHostOrReason(t *testing.T) {
	svc := newBundleTestService(t)
	svc.SetTPABundleProvider(func() *TPABundleSnapshot {
		return &TPABundleSnapshot{Source: "fetch", SignatureVerified: true, BundleVersion: "other"}
	})
	for _, c := range []TPABundleRefreshClass{
		TPABundleRefreshActivated, TPABundleRefreshRejected, TPABundleRefreshUnavailable, TPABundleRefreshUnchanged,
		TPABundleRefreshClass("rate_limited https://example.test/s3cr3t"),
	} {
		RecordTPABundleRefreshOn(svc.Registry(), c)
	}
	RecordTPABundleReprotectHitOn(svc.Registry())

	data, err := json.Marshal(svc.BuildPayload())
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	var tpa map[string]json.RawMessage
	if err := json.Unmarshal(top["tpa_scanner"], &tpa); err != nil {
		t.Fatalf("tpa_scanner: %v", err)
	}
	for k := range tpa {
		for _, bad := range []string{"age", "url", "host", "reason", "generated", "checked", "activated_at", "next_check", "digest", "stale"} {
			if strings.Contains(k, bad) {
				t.Errorf("tpa_scanner carries forbidden key %q", k)
			}
		}
	}
	if strings.Contains(string(data), "example.test") || strings.Contains(string(data), "s3cr3t") {
		t.Errorf("payload leaked an unknown class string:\n%s", data)
	}
	withoutBlockedValues(t)
	if err := ScanForPII(data); err != nil {
		t.Fatalf("backstop: %v\n%s", err, data)
	}
}

// TestTPABundleRefreshCounters_NothingSentWhenTelemetryDisabled: with the
// opt-out latched, no heartbeat (and therefore no counter or tpa_scanner key)
// reaches the endpoint; the control proves the counters do ride an enabled
// heartbeat, so the zero-hit assertion cannot pass vacuously.
func TestTPABundleRefreshCounters_NothingSentWhenTelemetryDisabled(t *testing.T) {
	clearTelemetryEnv(t)

	run := func(t *testing.T, optedOut bool) (hits int32, body string) {
		t.Helper()
		var n atomic.Int32
		var mu sync.Mutex
		var captured string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			captured = string(b)
			mu.Unlock()
			n.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		cfg := &config.Config{Telemetry: &config.TelemetryConfig{
			Enabled: boolPtr(true), AnonymousID: "anon-xyz", Endpoint: server.URL,
		}}
		svc := New(cfg, "", "v1.2.3", "personal", zap.NewNop())
		RecordTPABundleRefreshOn(svc.Registry(), TPABundleRefreshActivated)
		RecordTPABundleReprotectHitOn(svc.Registry())
		svc.optedOut.Store(optedOut)
		svc.sendHeartbeat(context.Background())
		mu.Lock()
		defer mu.Unlock()
		return n.Load(), captured
	}

	hits, body := run(t, true)
	if hits != 0 || strings.Contains(body, "bundle_refresh_") || strings.Contains(body, "tpa_scanner") {
		t.Errorf("opted-out install transmitted %d request(s): %s", hits, body)
	}

	hits, body = run(t, false)
	if hits != 1 {
		t.Fatalf("control: %d requests, want 1", hits)
	}
	for _, want := range []string{`"bundle_refresh_cycles":1`, `"bundle_refresh_activations":1`, `"bundle_reprotect_hits":1`} {
		if !strings.Contains(body, want) {
			t.Errorf("control payload missing %s:\n%s", want, body)
		}
	}
}
