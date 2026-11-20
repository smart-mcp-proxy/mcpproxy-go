package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

// Spec 126 FR-036: coverage comes from a persisted "undelivered window"
// marker, so a plain restart after a delivered window is "full". Each
// "process" below is a new Service over the same activation DB.

func openOutcomeDB(t *testing.T) (*bbolt.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "outcome.db")
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, path
}

// startOutcomeProcess starts one "process": storeWasEmpty is whether
// config.db held no data at open, fresh whether the store holds an install
// anchor (install_origin "fresh"). A fresh install stays fresh on every later
// start; only its first start has storeWasEmpty.
func startOutcomeProcess(t *testing.T, endpoint string, db *bbolt.DB, storeWasEmpty, fresh bool) *Service {
	t.Helper()
	svc := newOutcomeService(t, endpoint, "personal")
	svc.SetActivationStore(NewActivationStore(), db)
	anchor := time.Now().UTC().Add(-time.Hour)
	svc.SetInstallAnchorProvider(func() (time.Time, bool, error) {
		if !fresh {
			return time.Time{}, false, nil
		}
		return anchor, true, nil
	})
	svc.InitOutcomeWindow(storeWasEmpty)
	return svc
}

func coverageOf(svc *Service) string { return svc.Registry().Snapshot().UpstreamOutcomes.Coverage }

func markerOf(t *testing.T, db *bbolt.DB) (set, exists bool) {
	t.Helper()
	set, exists, err := loadOutcomeWindowMarker(db)
	if err != nil {
		t.Fatal(err)
	}
	return set, exists
}

func TestOutcomeWindow_FreshInstallIsFull(t *testing.T) {
	db, _ := openOutcomeDB(t)
	svc := startOutcomeProcess(t, "http://127.0.0.1:1", db, true, true)
	if got := coverageOf(svc); got != OutcomeCoverageFull {
		t.Fatalf("fresh install first window: %q, want full", got)
	}
	if set, exists := markerOf(t, db); !exists || set {
		t.Fatalf("marker after a fresh start: set=%v exists=%v, want written as cleared", set, exists)
	}
	// The fresh install restarts before any call: the store is no longer
	// empty at open, but the marker exists and is clear, so the second start
	// is full too.
	second := startOutcomeProcess(t, "http://127.0.0.1:1", db, false, true)
	if got := coverageOf(second); got != OutcomeCoverageFull {
		t.Fatalf("second start of a fresh install with no calls: %q, want full", got)
	}
}

// FR-036 (r4): an outcome recorded while a send is in flight does not wait
// for the network, and the accept that follows can never leave the marker
// false. The request handler blocks the 2xx until an outcome recorded from
// another goroutine has returned (bounded, so a tool call stuck behind the
// HTTP POST fails the test instead of hanging it): the outcome lands after
// the drain's swap, the accept then sees it and keeps the marker set.
func TestOutcomeWindow_InFlightOutcomeNeverBlocksNorLeavesMarkerFalse(t *testing.T) {
	db, _ := openOutcomeDB(t)
	var p *Service
	recorded := make(chan time.Duration, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		go func() {
			start := time.Now()
			p.Registry().RecordUpstreamOutcome(true, false) // a tool call during the POST
			recorded <- time.Since(start)
		}()
		select {
		case <-recorded:
		case <-time.After(5 * time.Second):
			t.Error("an outcome recorded during an in-flight send blocked on the send")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	p = startOutcomeProcess(t, srv.URL, db, true, true)
	p.Registry().RecordUpstreamOutcome(true, false)
	p.sendHeartbeat(context.Background()) // drain -> handler (outcome lands) -> 2xx -> accept
	if !p.Registry().HasPendingUpstreamOutcomes() {
		t.Fatal("precondition: the in-flight outcome is in the next window")
	}
	if set, _ := markerOf(t, db); !set {
		t.Fatal("the accept left the marker false while an outcome recorded after the drain is undelivered")
	}
}

// The first start of this build on an existing store has no marker yet: that
// window counts as partial, once.
func TestOutcomeWindow_FirstStartOnExistingStoreIsPartialOnce(t *testing.T) {
	db, _ := openOutcomeDB(t)
	first := startOutcomeProcess(t, "http://127.0.0.1:1", db, false, false)
	if got := coverageOf(first); got != OutcomeCoveragePartial {
		t.Fatalf("first start of this build on an existing store: %q, want partial", got)
	}
	// That process recorded nothing; the next start is full.
	second := startOutcomeProcess(t, "http://127.0.0.1:1", db, false, false)
	if got := coverageOf(second); got != OutcomeCoverageFull {
		t.Fatalf("second start: %q, want full (partial counts once)", got)
	}
}

// A restart after a delivered window is full; a restart after an undelivered
// one is partial.
func TestOutcomeWindow_RestartCoverageFollowsDelivery(t *testing.T) {
	srv, _, setStatus := newOutcomeCaptureServer(t)
	db, _ := openOutcomeDB(t)

	p1 := startOutcomeProcess(t, srv.URL, db, true, true)
	p1.Registry().RecordUpstreamOutcome(true, false)
	if set, _ := markerOf(t, db); !set {
		t.Fatal("the first outcome of a window must set the marker")
	}
	setStatus(http.StatusNoContent)
	p1.sendHeartbeat(context.Background())
	if set, _ := markerOf(t, db); set {
		t.Fatal("an accepted send must clear the marker")
	}
	p2 := startOutcomeProcess(t, srv.URL, db, false, true)
	if got := coverageOf(p2); got != OutcomeCoverageFull {
		t.Fatalf("restart after a delivered window: %q, want full", got)
	}

	p2.Registry().RecordUpstreamOutcome(false, false)
	setStatus(http.StatusInternalServerError)
	p2.sendHeartbeat(context.Background()) // rejected: the window stays undelivered
	p3 := startOutcomeProcess(t, srv.URL, db, false, true)
	if got := coverageOf(p3); got != OutcomeCoveragePartial {
		t.Fatalf("restart after an undelivered window: %q, want partial", got)
	}
}

// The send drains at its start, so an outcome recorded while it (or its
// retries) is in flight is undelivered when the retry is accepted: the send
// fails, outcomes keep arriving, the retry is accepted, the process is killed,
// and the next start must report the first window "partial".
func TestOutcomeWindow_AcceptKeepsMarkerForNextWindowOutcome(t *testing.T) {
	srv, _, setStatus := newOutcomeCaptureServer(t)
	db, _ := openOutcomeDB(t)
	p := startOutcomeProcess(t, srv.URL, db, true, true)
	p.Registry().RecordUpstreamOutcome(true, false)
	p.sendHeartbeat(context.Background()) // 500: drained, pending
	p.Registry().RecordUpstreamOutcome(true, false)
	setStatus(http.StatusNoContent)
	p.sendHeartbeat(context.Background()) // accepts the first window only
	if set, _ := markerOf(t, db); !set {
		t.Fatal("the next window holds an outcome: the marker must stay set")
	}
	next := startOutcomeProcess(t, srv.URL, db, false, true)
	if got := coverageOf(next); got != OutcomeCoveragePartial {
		t.Fatalf("restart with that window unsent: %q, want partial", got)
	}
}

// The graceful-shutdown flush clears the marker when it lands.
func TestOutcomeWindow_ShutdownFlushClearsMarker(t *testing.T) {
	srv, received, setStatus := newOutcomeCaptureServer(t)
	setStatus(http.StatusNoContent)
	db, _ := openOutcomeDB(t)
	p := startOutcomeProcess(t, srv.URL, db, true, true)
	p.Registry().RecordUpstreamOutcome(true, false)
	p.flushEligible.Store(true)
	p.flushFinalHeartbeat()
	if n := len(received()); n != 1 {
		t.Fatalf("flush sent %d heartbeats, want 1", n)
	}
	if set, _ := markerOf(t, db); set {
		t.Fatal("a landed shutdown flush must clear the marker")
	}
}

// window_age is the install age when the window OPENED — process start or the
// last Drain, never the accepted send — frozen into the drained snapshot, so a
// retry re-sends the age it was drained with.
func TestOutcomeWindow_WindowAgeOpensAtDrainAndIsFrozen(t *testing.T) {
	srv, received, setStatus := newOutcomeCaptureServer(t)
	db, _ := openOutcomeDB(t)
	start := time.Now().UTC()
	anchor := start.Add(-time.Hour)
	svc := newOutcomeService(t, srv.URL, "personal")
	svc.SetActivationStore(NewActivationStore(), db)
	svc.SetInstallAnchorProvider(func() (time.Time, bool, error) { return anchor, true, nil })
	reg := svc.Registry()
	var (
		mu    sync.Mutex
		clock = start
	)
	setClock := func(t time.Time) {
		mu.Lock()
		clock = t
		mu.Unlock()
	}
	reg.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	}
	reg.outcomes.mu.Lock()
	reg.outcomes.openedAt = start // process start
	reg.outcomes.mu.Unlock()
	svc.InitOutcomeWindow(true)
	reg.RecordUpstreamOutcome(true, false)

	setClock(anchor.AddDate(0, 0, 8))       // the first send drains on day 8
	svc.sendHeartbeat(context.Background()) // 500: window 1 frozen, window 2 opens now
	reg.RecordUpstreamOutcome(true, false)
	setClock(anchor.AddDate(0, 0, 15)) // the retry is accepted on day 15
	setStatus(http.StatusNoContent)
	svc.sendHeartbeat(context.Background()) // retry of window 1, accepted
	svc.sendHeartbeat(context.Background()) // window 2

	got := received()
	if len(got) != 3 {
		t.Fatalf("received %d heartbeats", len(got))
	}
	if a := got[0].payload.UpstreamOutcomes.WindowAge; a != InstallAgeD0to6 {
		t.Fatalf("window 1 opened at process start (d0_6), got %q", a)
	}
	if a := got[1].payload.UpstreamOutcomes.WindowAge; a != InstallAgeD0to6 {
		t.Fatalf("the retry must re-send the frozen window_age d0_6, got %q", a)
	}
	if a := got[2].payload.UpstreamOutcomes.WindowAge; a != InstallAgeD7to13 {
		t.Fatalf("window 2 opened at the drain on day 8 (d7_13), not at the accept on day 15; got %q", a)
	}
}

// window_age is absent on an existing install and on the server edition.
func TestOutcomeWindow_WindowAgeOnlyOnFreshPersonal(t *testing.T) {
	cases := []struct {
		name      string
		edition   string
		hasAnchor bool
		want      string
	}{
		{"personal fresh", "personal", true, InstallAgeD0to6},
		{"personal existing", "personal", false, ""},
		{"server fresh", "server", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, _ := openOutcomeDB(t)
			svc := newOutcomeService(t, "http://127.0.0.1:1", c.edition)
			svc.SetActivationStore(NewActivationStore(), db)
			svc.SetInstallAnchorProvider(func() (time.Time, bool, error) {
				return time.Now().UTC(), c.hasAnchor, nil
			})
			svc.InitOutcomeWindow(c.hasAnchor)
			if got := svc.Registry().Drain().UpstreamOutcomes.WindowAge; got != c.want {
				t.Fatalf("window_age = %q, want %q", got, c.want)
			}
		})
	}
}

// FR-036 (r3): an absent marker gives "partial" once only when install_origin
// is "existing". A fresh install that crashed before its start-time marker
// write (anchor present, marker absent) starts "full".
func TestOutcomeWindow_AbsentMarkerOnFreshOriginIsFull(t *testing.T) {
	db, _ := openOutcomeDB(t)
	if _, exists := markerOf(t, db); exists {
		t.Fatal("precondition: no marker")
	}
	p := startOutcomeProcess(t, "http://127.0.0.1:1", db, false, true)
	if got := coverageOf(p); got != OutcomeCoverageFull {
		t.Fatalf("fresh origin, absent marker: %q, want full", got)
	}
}

// FR-036 (r3): the first window opens at process start. The marker is written
// false at start, outcomes arrive before the first send, the process is
// killed: the next start reports "partial".
func TestOutcomeWindow_OutcomesBeforeFirstSendThenKillIsPartial(t *testing.T) {
	db, _ := openOutcomeDB(t)
	p := startOutcomeProcess(t, "http://127.0.0.1:1", db, true, true)
	if set, exists := markerOf(t, db); !exists || set {
		t.Fatalf("start must write the marker false: set=%v exists=%v", set, exists)
	}
	p.Registry().RecordUpstreamOutcome(true, false)
	p.Registry().RecordUpstreamOutcome(false, false)
	// killed: no send, no flush
	next := startOutcomeProcess(t, "http://127.0.0.1:1", db, false, true)
	if got := coverageOf(next); got != OutcomeCoveragePartial {
		t.Fatalf("restart after outcomes that never left: %q, want partial", got)
	}
}

// FR-034/FR-035 (r3): the anchor is read once per process, and day_two and
// window_age use that one answer, so they can never disagree.
func TestOutcomeWindow_AnchorReadOncePerProcess(t *testing.T) {
	db, _ := openOutcomeDB(t)
	srv, received, setStatus := newOutcomeCaptureServer(t)
	setStatus(http.StatusNoContent)
	svc := newOutcomeService(t, srv.URL, "personal")
	svc.SetActivationStore(NewActivationStore(), db)
	var calls int
	answers := []bool{true, false, false} // a provider that would flip if re-read
	svc.SetInstallAnchorProvider(func() (time.Time, bool, error) {
		ok := answers[calls%len(answers)]
		calls++
		return time.Now().UTC(), ok, nil
	})
	svc.InitOutcomeWindow(true)
	for i := 0; i < 3; i++ {
		svc.Registry().RecordUpstreamOutcome(true, false)
		svc.sendHeartbeat(context.Background())
	}
	if calls != 1 {
		t.Fatalf("anchor provider called %d times, want once per process", calls)
	}
	for i, hb := range received() {
		if hb.payload.DayTwo == nil || hb.payload.DayTwo.InstallOrigin != InstallOriginFresh {
			t.Fatalf("heartbeat %d: day_two = %+v, want fresh", i, hb.payload.DayTwo)
		}
		if hb.payload.UpstreamOutcomes.WindowAge == "" {
			t.Fatalf("heartbeat %d: a fresh install must carry window_age", i)
		}
	}
}

// FR-036 (r3): the accept path's clear and the outcome path's set are
// serialised with the accept decision, so a stale "false" never overwrites a
// "true". Hammer concurrent outcomes against accepted sends; whenever the live
// window still holds outcomes at the end, the marker must be set. Run under
// -race.
func TestOutcomeWindow_MarkerNeverStaleUnderConcurrency(t *testing.T) {
	srv, _, setStatus := newOutcomeCaptureServer(t)
	setStatus(http.StatusNoContent)
	for round := 0; round < 20; round++ {
		db, _ := openOutcomeDB(t)
		p := startOutcomeProcess(t, srv.URL, db, true, true)
		var wg sync.WaitGroup
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 50; i++ {
					p.Registry().RecordUpstreamOutcome(i%2 == 0, false)
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				p.sendHeartbeat(context.Background())
			}
		}()
		wg.Wait()
		set, _ := markerOf(t, db)
		if p.Registry().HasPendingUpstreamOutcomes() && !set {
			t.Fatalf("round %d: outcomes are undelivered but the marker is clear", round)
		}
		// Deliver the rest: with nothing recorded since, the marker clears.
		p.sendHeartbeat(context.Background())
		if p.Registry().HasPendingUpstreamOutcomes() {
			continue
		}
		if set, _ := markerOf(t, db); set {
			t.Fatalf("round %d: everything delivered but the marker is still set", round)
		}
	}
}

// FR-036 (r3): an outcome recorded while a send is IN FLIGHT (after its drain,
// before its 2xx) belongs to the next window, so the accept must leave the
// marker set; a kill then makes the next start "partial".
func TestOutcomeWindow_OutcomeDuringInFlightSendKeepsMarker(t *testing.T) {
	db, _ := openOutcomeDB(t)
	var p *Service
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		p.Registry().RecordUpstreamOutcome(true, false) // the request is in flight
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	p = startOutcomeProcess(t, srv.URL, db, true, true)
	p.Registry().RecordUpstreamOutcome(true, false)
	p.sendHeartbeat(context.Background()) // drains, then the handler records, then 2xx
	if !p.Registry().HasPendingUpstreamOutcomes() {
		t.Fatal("precondition: the in-flight outcome is in the next window")
	}
	if set, _ := markerOf(t, db); !set {
		t.Fatal("an outcome recorded after the accepted snapshot's drain is undelivered: the marker must stay set")
	}
	next := startOutcomeProcess(t, srv.URL, db, false, true)
	if got := coverageOf(next); got != OutcomeCoveragePartial {
		t.Fatalf("restart after the kill: %q, want partial", got)
	}
}

// FR-035 (r4): an outcome is pending usage data on its own. A window whose
// only content is an outcome (dispatch counter 0) must still be sent by the
// graceful-shutdown flush, and Reset must zero it.
func TestOutcomeWindow_OutcomeOnlyWindowIsFlushedAndReset(t *testing.T) {
	srv, received, setStatus := newOutcomeCaptureServer(t)
	setStatus(http.StatusNoContent)
	db, _ := openOutcomeDB(t)
	p := startOutcomeProcess(t, srv.URL, db, true, true)
	reg := p.Registry()
	reg.RecordUpstreamOutcome(false, false)
	if got := reg.Snapshot().UpstreamToolCallCountBucket; got != "0" {
		t.Fatalf("precondition: dispatch bucket %q, want 0", got)
	}
	if !reg.HasPendingCounters() {
		t.Fatal("an outcome alone must count as pending for the shutdown flush")
	}
	p.flushEligible.Store(true)
	p.flushFinalHeartbeat()
	got := received()
	if len(got) != 1 {
		t.Fatalf("the shutdown flush sent %d heartbeats, want 1", len(got))
	}
	if o := got[0].payload.UpstreamOutcomes; o == nil || o.Unusable != "1-10" {
		t.Fatalf("flushed window = %+v, want unusable 1-10", o)
	}
	if set, _ := markerOf(t, db); set {
		t.Fatal("the landed flush must clear the marker")
	}

	reg.RecordUpstreamOutcome(true, false)
	reg.Reset()
	if reg.HasPendingCounters() || reg.HasPendingUpstreamOutcomes() {
		t.Fatal("Reset must zero the outcome counts")
	}
}

// FR-035 (r4): the server edition sends upstream_outcomes without window_age
// (it has no install_origin), even with an anchor provider wired.
func TestOutcomeWindow_ServerEditionSendsNoWindowAge(t *testing.T) {
	srv, received, setStatus := newOutcomeCaptureServer(t)
	setStatus(http.StatusNoContent)
	db, _ := openOutcomeDB(t)
	svc := newOutcomeService(t, srv.URL, "server")
	svc.SetActivationStore(NewActivationStore(), db)
	svc.SetInstallAnchorProvider(func() (time.Time, bool, error) { return time.Now().UTC(), true, nil })
	svc.InitOutcomeWindow(true)
	svc.Registry().RecordUpstreamOutcome(true, false)
	svc.sendHeartbeat(context.Background())
	got := received()
	if len(got) != 1 {
		t.Fatalf("received %d heartbeats", len(got))
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(got[0].raw["upstream_outcomes"], &obj); err != nil {
		t.Fatal(err)
	}
	if _, has := obj["window_age"]; has {
		t.Fatalf("server edition sent window_age: %s", got[0].raw["upstream_outcomes"])
	}
	if _, has := got[0].raw["day_two"]; has {
		t.Fatal("server edition sent day_two")
	}
}
