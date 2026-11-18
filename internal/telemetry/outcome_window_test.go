package telemetry

import (
	"context"
	"net/http"
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

func startOutcomeProcess(t *testing.T, endpoint string, db *bbolt.DB, storeWasEmpty bool) *Service {
	t.Helper()
	svc := newOutcomeService(t, endpoint, "personal")
	svc.SetActivationStore(NewActivationStore(), db)
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
	svc := startOutcomeProcess(t, "http://127.0.0.1:1", db, true)
	if got := coverageOf(svc); got != OutcomeCoverageFull {
		t.Fatalf("fresh install first window: %q, want full", got)
	}
	if set, exists := markerOf(t, db); !exists || set {
		t.Fatalf("marker after a fresh start: set=%v exists=%v, want written as cleared", set, exists)
	}
}

// The first start of this build on an existing store has no marker yet: that
// window counts as partial, once.
func TestOutcomeWindow_FirstStartOnExistingStoreIsPartialOnce(t *testing.T) {
	db, _ := openOutcomeDB(t)
	first := startOutcomeProcess(t, "http://127.0.0.1:1", db, false)
	if got := coverageOf(first); got != OutcomeCoveragePartial {
		t.Fatalf("first start of this build on an existing store: %q, want partial", got)
	}
	// That process recorded nothing; the next start is full.
	second := startOutcomeProcess(t, "http://127.0.0.1:1", db, false)
	if got := coverageOf(second); got != OutcomeCoverageFull {
		t.Fatalf("second start: %q, want full (partial counts once)", got)
	}
}

// A restart after a delivered window is full; a restart after an undelivered
// one is partial.
func TestOutcomeWindow_RestartCoverageFollowsDelivery(t *testing.T) {
	srv, _, setStatus := newOutcomeCaptureServer(t)
	db, _ := openOutcomeDB(t)

	p1 := startOutcomeProcess(t, srv.URL, db, true)
	p1.Registry().RecordUpstreamOutcome(true, false)
	if set, _ := markerOf(t, db); !set {
		t.Fatal("the first outcome of a window must set the marker")
	}
	setStatus(http.StatusNoContent)
	p1.sendHeartbeat(context.Background())
	if set, _ := markerOf(t, db); set {
		t.Fatal("an accepted send must clear the marker")
	}
	p2 := startOutcomeProcess(t, srv.URL, db, false)
	if got := coverageOf(p2); got != OutcomeCoverageFull {
		t.Fatalf("restart after a delivered window: %q, want full", got)
	}

	p2.Registry().RecordUpstreamOutcome(false, false)
	setStatus(http.StatusInternalServerError)
	p2.sendHeartbeat(context.Background()) // rejected: the window stays undelivered
	p3 := startOutcomeProcess(t, srv.URL, db, false)
	if got := coverageOf(p3); got != OutcomeCoveragePartial {
		t.Fatalf("restart after an undelivered window: %q, want partial", got)
	}
}

// An outcome recorded between the drain and the accepted send lives in the
// next window: the accept must not clear the marker it set.
func TestOutcomeWindow_AcceptKeepsMarkerForNextWindowOutcome(t *testing.T) {
	srv, _, setStatus := newOutcomeCaptureServer(t)
	db, _ := openOutcomeDB(t)
	p := startOutcomeProcess(t, srv.URL, db, true)
	p.Registry().RecordUpstreamOutcome(true, false)
	p.sendHeartbeat(context.Background()) // 500: drained, pending
	p.Registry().RecordUpstreamOutcome(true, false)
	setStatus(http.StatusNoContent)
	p.sendHeartbeat(context.Background()) // accepts the first window only
	if set, _ := markerOf(t, db); !set {
		t.Fatal("the next window holds an outcome: the marker must stay set")
	}
	next := startOutcomeProcess(t, srv.URL, db, false)
	if got := coverageOf(next); got != OutcomeCoveragePartial {
		t.Fatalf("restart with that window unsent: %q, want partial", got)
	}
}

// The graceful-shutdown flush clears the marker when it lands.
func TestOutcomeWindow_ShutdownFlushClearsMarker(t *testing.T) {
	srv, received, setStatus := newOutcomeCaptureServer(t)
	setStatus(http.StatusNoContent)
	db, _ := openOutcomeDB(t)
	p := startOutcomeProcess(t, srv.URL, db, true)
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

// window_age is the install age when the window opened, frozen into the
// drained snapshot: a retry re-sends the age it was drained with, and the next
// window takes the age at the accepted send.
func TestOutcomeWindow_WindowAgeFrozenAndAdvancedAtAccept(t *testing.T) {
	srv, received, setStatus := newOutcomeCaptureServer(t)
	db, _ := openOutcomeDB(t)
	var (
		mu     sync.Mutex
		anchor = time.Now().UTC().Add(-time.Hour)
	)
	svc := newOutcomeService(t, srv.URL, "personal")
	svc.SetActivationStore(NewActivationStore(), db)
	svc.SetInstallAnchorProvider(func() (time.Time, bool, error) {
		mu.Lock()
		defer mu.Unlock()
		return anchor, true, nil
	})
	svc.InitOutcomeWindow(true)
	svc.Registry().RecordUpstreamOutcome(true, false)

	svc.sendHeartbeat(context.Background()) // 500
	// Time passes: the install is now 8 UTC dates old.
	mu.Lock()
	anchor = time.Now().UTC().AddDate(0, 0, -8)
	mu.Unlock()
	setStatus(http.StatusNoContent)
	svc.sendHeartbeat(context.Background()) // retry accepted
	svc.sendHeartbeat(context.Background()) // next window

	got := received()
	if len(got) != 3 {
		t.Fatalf("received %d heartbeats", len(got))
	}
	if a := got[0].payload.UpstreamOutcomes.WindowAge; a != InstallAgeD0to6 {
		t.Fatalf("first window opened at d0_6, got %q", a)
	}
	if a := got[1].payload.UpstreamOutcomes.WindowAge; a != InstallAgeD0to6 {
		t.Fatalf("retry must re-send the frozen window_age d0_6, got %q", a)
	}
	if a := got[2].payload.UpstreamOutcomes.WindowAge; a != InstallAgeD7to13 {
		t.Fatalf("the next window opened at the accepted send (d7_13), got %q", a)
	}
	// day_two.install_age is state at build time, not the window's age.
	if a := got[1].payload.DayTwo.InstallAge; a != InstallAgeD7to13 {
		t.Fatalf("day_two.install_age on the retry = %q, want build-time d7_13", a)
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
