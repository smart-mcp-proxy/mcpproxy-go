package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 126 FR-035 / FR-036 (T079): the per-window completion counters.

// The three counts are bucketed exactly like upstream_tool_call_count_bucket.
func TestUpstreamOutcomes_BucketsMatchDispatchBucket(t *testing.T) {
	for _, n := range []int{0, 1, 10, 11, 100, 101, 1000, 1001} {
		r := NewCounterRegistry()
		for i := 0; i < n; i++ {
			r.RecordUpstreamOutcome(true, false)
			r.RecordUpstreamOutcome(false, false)
		}
		got := r.Snapshot().UpstreamOutcomes
		want := bucketUpstream(int64(n))
		if got.Succeeded != want || got.Unusable != want || got.SucceededNonStarter != want {
			t.Fatalf("n=%d: got %+v, want every count %q", n, got, want)
		}
		if got.Coverage != OutcomeCoverageFull {
			t.Fatalf("n=%d: coverage = %q, want full", n, got.Coverage)
		}
	}
}

// A success on the starter counts in succeeded but not succeeded_non_starter;
// an unusable result never counts as a success, starter or not.
func TestUpstreamOutcomes_StarterSuccessExcludedFromNonStarter(t *testing.T) {
	r := NewCounterRegistry()
	r.RecordUpstreamOutcome(true, true)
	r.RecordUpstreamOutcome(true, true)
	r.RecordUpstreamOutcome(false, true)
	got := r.Snapshot().UpstreamOutcomes
	if got.Succeeded != "1-10" || got.SucceededNonStarter != "0" || got.Unusable != "1-10" {
		t.Fatalf("starter-only window: got %+v", got)
	}

	r.RecordUpstreamOutcome(true, false)
	if got := r.Snapshot().UpstreamOutcomes; got.SucceededNonStarter != "1-10" {
		t.Fatalf("a non-starter success must count: got %+v", got)
	}
}

// RecordUpstreamOutcomeOn is nil-safe.
func TestUpstreamOutcomes_NilRegistry(t *testing.T) {
	RecordUpstreamOutcomeOn(nil, true, false)
	var r *CounterRegistry
	if r.HasPendingCounters() {
		t.Fatal("nil registry has no pending counters")
	}
}

// An outcome alone is pending usage data for the shutdown flush.
func TestUpstreamOutcomes_ArePendingCounters(t *testing.T) {
	r := NewCounterRegistry()
	if r.HasPendingCounters() {
		t.Fatal("fresh registry has nothing pending")
	}
	r.RecordUpstreamOutcome(false, false)
	if !r.HasPendingCounters() {
		t.Fatal("an unusable outcome is pending until an accepted send")
	}
	r.Reset()
	if r.HasPendingCounters() {
		t.Fatal("Reset zeroes the outcome counts")
	}
}

// Coverage is "full" for a fresh registry (a fresh install's first window),
// "partial" for the first window of a process over an existing store, and
// "full" again from the window after the first accepted send. Drain carries
// the flag into the detached window, so the next window starts full.
func TestUpstreamOutcomes_CoverageFlag(t *testing.T) {
	fresh := NewCounterRegistry()
	if got := fresh.Drain().UpstreamOutcomes.Coverage; got != OutcomeCoverageFull {
		t.Fatalf("fresh install first window: coverage = %q, want full", got)
	}

	existing := NewCounterRegistry()
	existing.MarkOutcomeWindowPartial()
	if got := existing.Snapshot().UpstreamOutcomes.Coverage; got != OutcomeCoveragePartial {
		t.Fatalf("snapshot of the first window over an existing store: coverage = %q, want partial", got)
	}
	first := existing.Drain()
	if first.UpstreamOutcomes.Coverage != OutcomeCoveragePartial {
		t.Fatalf("first window over an existing store: coverage = %q, want partial", first.UpstreamOutcomes.Coverage)
	}
	if got := existing.Drain().UpstreamOutcomes.Coverage; got != OutcomeCoverageFull {
		t.Fatalf("the window after the first: coverage = %q, want full", got)
	}
}

type capturedHeartbeat struct {
	raw     map[string]json.RawMessage
	payload HeartbeatPayload
}

func newOutcomeCaptureServer(t *testing.T) (*httptest.Server, func() []capturedHeartbeat, func(int)) {
	t.Helper()
	var (
		mu     sync.Mutex
		got    []capturedHeartbeat
		status = http.StatusInternalServerError
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var c capturedHeartbeat
		if err := json.Unmarshal(body, &c.raw); err != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		_ = json.Unmarshal(body, &c.payload)
		mu.Lock()
		got = append(got, c)
		st := status
		mu.Unlock()
		w.WriteHeader(st)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []capturedHeartbeat {
			mu.Lock()
			defer mu.Unlock()
			return append([]capturedHeartbeat(nil), got...)
		}, func(s int) {
			mu.Lock()
			status = s
			mu.Unlock()
		}
}

func newOutcomeService(t *testing.T, endpoint, edition string) *Service {
	t.Helper()
	t.Setenv("CI", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")
	svc := New(&config.Config{
		Telemetry: &config.TelemetryConfig{
			AnonymousID: "550e8400-e29b-41d4-a716-446655440000",
			Endpoint:    endpoint,
		},
		RoutingMode: "retrieve_tools",
	}, "", "v1.0.0", edition, zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{})
	return svc
}

// The counts drain only on an accepted send: a rejected send keeps the
// window, and its retry re-sends byte-identical upstream_outcomes (coverage
// included) under the same heartbeat_id, even when calls happened in between.
// Those calls go to the next window. The receiver stores one immutable
// heartbeat_events row per heartbeat_id and answers 409 to a retry whose
// counter payload differs, so this is a wire contract.
func TestUpstreamOutcomes_DrainOnlyOnAcceptedSend_RetryIsByteIdentical(t *testing.T) {
	srv, received, setStatus := newOutcomeCaptureServer(t)
	svc := newOutcomeService(t, srv.URL, "personal")
	reg := svc.Registry()
	reg.MarkOutcomeWindowPartial() // first window of a process over an existing store

	for i := 0; i < 3; i++ {
		reg.RecordUpstreamOutcome(true, false)
	}
	reg.RecordUpstreamOutcome(false, false)

	svc.sendHeartbeat(context.Background()) // 500: the window stays pending

	// Calls between the rejected send and its retry belong to the next window.
	for i := 0; i < 20; i++ {
		reg.RecordUpstreamOutcome(false, false)
	}
	reg.RecordUpstreamOutcome(true, true)

	setStatus(http.StatusNoContent)
	svc.sendHeartbeat(context.Background()) // 204: the retry is accepted
	svc.sendHeartbeat(context.Background()) // the next window

	got := received()
	if len(got) != 3 {
		t.Fatalf("received %d heartbeats, want 3", len(got))
	}
	first, retry, next := got[0], got[1], got[2]
	if first.payload.HeartbeatID != retry.payload.HeartbeatID {
		t.Fatalf("retry changed heartbeat_id")
	}
	if !bytes.Equal(first.raw["upstream_outcomes"], retry.raw["upstream_outcomes"]) {
		t.Fatalf("retry upstream_outcomes differ:\nfirst %s\nretry %s", first.raw["upstream_outcomes"], retry.raw["upstream_outcomes"])
	}
	want := UpstreamOutcomes{Succeeded: "1-10", Unusable: "1-10", SucceededNonStarter: "1-10", Coverage: OutcomeCoveragePartial}
	if first.payload.UpstreamOutcomes == nil || *first.payload.UpstreamOutcomes != want {
		t.Fatalf("first window = %+v, want %+v", first.payload.UpstreamOutcomes, want)
	}
	wantNext := UpstreamOutcomes{Succeeded: "1-10", Unusable: "11-100", SucceededNonStarter: "0", Coverage: OutcomeCoverageFull}
	if next.payload.UpstreamOutcomes == nil || *next.payload.UpstreamOutcomes != wantNext {
		t.Fatalf("next window = %+v, want %+v (the in-between calls, full coverage after the accepted send)", next.payload.UpstreamOutcomes, wantNext)
	}
	if next.payload.HeartbeatID == retry.payload.HeartbeatID {
		t.Fatal("accepted window did not rotate heartbeat_id")
	}
}

// Both editions send upstream_outcomes, and an idle window still carries it
// (all counts "0"), so absence always means "not measured".
func TestUpstreamOutcomes_SentByBothEditions(t *testing.T) {
	for _, edition := range []string{"personal", "server"} {
		t.Run(edition, func(t *testing.T) {
			srv, received, setStatus := newOutcomeCaptureServer(t)
			setStatus(http.StatusNoContent)
			svc := newOutcomeService(t, srv.URL, edition)
			svc.sendHeartbeat(context.Background())
			got := received()
			if len(got) != 1 {
				t.Fatalf("received %d heartbeats, want 1", len(got))
			}
			raw, ok := got[0].raw["upstream_outcomes"]
			if !ok {
				t.Fatalf("%s edition heartbeat has no upstream_outcomes", edition)
			}
			var o UpstreamOutcomes
			if err := json.Unmarshal(raw, &o); err != nil {
				t.Fatal(err)
			}
			want := UpstreamOutcomes{Succeeded: "0", Unusable: "0", SucceededNonStarter: "0", Coverage: OutcomeCoverageFull}
			if o != want {
				t.Fatalf("idle window = %+v, want %+v", o, want)
			}
		})
	}
}
