package telemetry

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// Spec 126 FR-034 (T083): install origin and age.
//
// install_age is the difference in UTC CALENDAR DATES between the anchor's
// UTC date and the UTC date the heartbeat is built on, so it is constant
// within one UTC date (spec update ce1c5c91c).

func TestInstallAgeBucket_CalendarDateBoundaries(t *testing.T) {
	anchor := time.Date(2026, 11, 1, 23, 50, 0, 0, time.UTC) // day D, 23:50 UTC
	day := func(offset, h, m int) time.Time {
		return time.Date(2026, 11, 1+offset, h, m, 0, 0, time.UTC)
	}
	cases := []struct {
		name string
		now  time.Time
		want string
	}{
		{"same instant", anchor, "d0_6"},
		{"D+1 00:10, 20 minutes later", day(1, 0, 10), "d0_6"},
		{"D+6 23:59", day(6, 23, 59), "d0_6"},
		{"D+7 00:10, less than 7x24h later", day(7, 0, 10), "d7_13"},
		{"D+13 23:59", day(13, 23, 59), "d7_13"},
		{"D+14 00:00", day(14, 0, 0), "d14_plus"},
		{"D+400", day(400, 12, 0), "d14_plus"},
		{"future anchor (clock skew)", day(-3, 12, 0), "d0_6"},
	}
	for _, c := range cases {
		if got := InstallAgeBucket(anchor, c.now); got != c.want {
			t.Errorf("%s: InstallAgeBucket = %q, want %q", c.name, got, c.want)
		}
	}
}

// The date is the UTC date, whatever zone the times are expressed in.
func TestInstallAgeBucket_UsesUTCDates(t *testing.T) {
	east := time.FixedZone("UTC+10", 10*3600)
	anchor := time.Date(2026, 11, 2, 9, 50, 0, 0, east) // 2026-11-01 23:50 UTC
	now := time.Date(2026, 11, 8, 10, 10, 0, 0, east)   // 2026-11-08 00:10 UTC
	if got := InstallAgeBucket(anchor, now); got != "d7_13" {
		t.Fatalf("InstallAgeBucket = %q, want d7_13 (UTC dates 11-01 -> 11-08)", got)
	}
}

func TestBuildDayTwo_OriginAndAge(t *testing.T) {
	anchor := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	now := anchor.Add(8 * 24 * time.Hour)

	fresh := BuildDayTwo(anchor, true, SuccessScopeNonStarter, now)
	if fresh == nil || fresh.InstallOrigin != "fresh" || fresh.InstallAge != "d7_13" || fresh.SuccessScopeEver != "non_starter" {
		t.Fatalf("fresh install: got %+v", fresh)
	}

	existing := BuildDayTwo(time.Time{}, false, SuccessScopeNone, now)
	if existing == nil || existing.InstallOrigin != "existing" || existing.InstallAge != "" || existing.SuccessScopeEver != "none" {
		t.Fatalf("existing install: got %+v (install_age must be absent)", existing)
	}
}

// day_two is personal-edition only. A server-edition heartbeat omits it even
// when the anchor provider is wired; upstream_outcomes stays.
func TestDayTwo_PersonalOnly(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()
	anchor := time.Now().UTC().Add(-time.Hour)

	for _, edition := range []string{"personal", "server"} {
		t.Run(edition, func(t *testing.T) {
			srv, received, setStatus := newOutcomeCaptureServer(t)
			setStatus(http.StatusNoContent)
			svc := newOutcomeService(t, srv.URL, edition)
			svc.SetActivationStore(NewActivationStore(), db)
			svc.SetInstallAnchorProvider(func() (time.Time, bool, error) { return anchor, true, nil })
			svc.sendHeartbeat(context.Background())
			got := received()
			if len(got) != 1 {
				t.Fatalf("received %d heartbeats", len(got))
			}
			_, hasDayTwo := got[0].raw["day_two"]
			if edition == "server" && hasDayTwo {
				t.Fatal("server edition must omit day_two")
			}
			if edition == "personal" {
				if !hasDayTwo || got[0].payload.DayTwo == nil {
					t.Fatal("personal edition must carry day_two")
				}
				if dt := got[0].payload.DayTwo; dt.InstallOrigin != "fresh" || dt.InstallAge != "d0_6" || dt.SuccessScopeEver != "none" {
					t.Fatalf("day_two = %+v", dt)
				}
			}
			if _, ok := got[0].raw["upstream_outcomes"]; !ok {
				t.Fatalf("%s edition must carry upstream_outcomes", edition)
			}
		})
	}
}

// A provider error omits day_two for that heartbeat rather than reporting an
// install as existing; the heartbeat itself still goes out.
func TestDayTwo_AnchorReadErrorOmitsBlock(t *testing.T) {
	db, cleanup := newTestActivationDB(t)
	defer cleanup()
	srv, received, setStatus := newOutcomeCaptureServer(t)
	setStatus(http.StatusNoContent)
	svc := newOutcomeService(t, srv.URL, "personal")
	svc.SetActivationStore(NewActivationStore(), db)
	svc.SetInstallAnchorProvider(func() (time.Time, bool, error) { return time.Time{}, false, errors.New("boom") })
	svc.sendHeartbeat(context.Background())
	got := received()
	if len(got) != 1 {
		t.Fatalf("received %d heartbeats", len(got))
	}
	if _, ok := got[0].raw["day_two"]; ok {
		t.Fatal("an unreadable anchor must omit day_two, not guess an origin")
	}
}
