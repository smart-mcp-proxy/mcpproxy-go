package updatecheck

import (
	"errors"
	"testing"

	"go.uber.org/zap/zaptest"
)

// Spec 079 FR-002 at the checker level: how the delta reaches VersionInfo, and
// — more importantly — what must NOT happen when it cannot be resolved.

func deltaTestChecker(t *testing.T, version, latest string) *Checker {
	t.Helper()
	c := New(zaptest.NewLogger(t), version)
	c.SetCheckFunc(func() (*GitHubRelease, error) {
		return &GitHubRelease{
			TagName:     latest,
			HTMLURL:     "https://example.invalid/releases/" + latest,
			PublishedAt: "2026-08-28T00:00:00Z",
		}, nil
	})
	return c
}

func TestCheckerStampsTheDelta(t *testing.T) {
	c := deltaTestChecker(t, "v0.43.0", "v0.46.0")
	c.SetDeltaFunc(func(*GitHubRelease) (*ReleaseDelta, error) {
		return &ReleaseDelta{ReleasesBehind: 3, WeeksBehind: 8, WeeksKnown: true}, nil
	})

	info := c.CheckNow()

	if info.BehindSummary != "3 releases / ~8 weeks behind" {
		t.Errorf("BehindSummary = %q", info.BehindSummary)
	}
	if info.ReleasesBehind == nil || *info.ReleasesBehind != 3 {
		t.Errorf("ReleasesBehind = %v, want 3", info.ReleasesBehind)
	}
	if info.WeeksBehind == nil || *info.WeeksBehind != 8 {
		t.Errorf("WeeksBehind = %v, want 8", info.WeeksBehind)
	}
	if info.ReleasesBehindSaturated {
		t.Error("ReleasesBehindSaturated = true, want false")
	}
}

// The regression that would matter most. A delta-fetch failure must leave the
// upgrade nudge completely intact: writing CheckError here would make
// statusVersionSuffix and doctor treat the whole check as failed and print
// nothing at all — the feature would delete the message it is meant to enrich.
func TestDeltaFailureLeavesTheNudgeIntact(t *testing.T) {
	c := deltaTestChecker(t, "v0.43.0", "v0.46.0")
	c.SetDeltaFunc(func(*GitHubRelease) (*ReleaseDelta, error) {
		return nil, errors.New("github is having a day")
	})

	info := c.CheckNow()

	if !info.UpdateAvailable {
		t.Error("UpdateAvailable = false; a delta miss must not suppress availability")
	}
	if info.LatestVersion != "v0.46.0" {
		t.Errorf("LatestVersion = %q, want v0.46.0", info.LatestVersion)
	}
	if info.ReleaseURL == "" {
		t.Error("ReleaseURL was dropped")
	}
	if info.CheckError != "" {
		t.Errorf("CheckError = %q; a delta miss is not a check failure — this field "+
			"suppresses the nudge on every CLI surface", info.CheckError)
	}
	if info.BehindSummary != "" || info.ReleasesBehind != nil || info.WeeksBehind != nil {
		t.Errorf("delta fields leaked on failure: %+v", info)
	}
}

// A delta miss must not push the real check into FR-018 backoff either.
func TestDeltaFailureDoesNotTriggerBackoff(t *testing.T) {
	c := deltaTestChecker(t, "v0.43.0", "v0.46.0")
	c.SetDeltaFunc(func(*GitHubRelease) (*ReleaseDelta, error) {
		return nil, errors.New("nope")
	})

	c.CheckNow()

	c.mu.RLock()
	failures, next := c.consecutiveFailures, c.nextCheckAt
	c.mu.RUnlock()

	if failures != 0 {
		t.Errorf("consecutiveFailures = %d, want 0", failures)
	}
	if !next.IsZero() {
		t.Errorf("nextCheckAt = %v, want zero (no backoff window)", next)
	}
}

// FR-016: no delta is even attempted when nothing newer is offered, which is
// what makes a negative delta structurally impossible rather than merely
// guarded against.
func TestNoDeltaAttemptedWhenNoUpdateAvailable(t *testing.T) {
	c := deltaTestChecker(t, "v0.46.0", "v0.46.0")
	called := false
	c.SetDeltaFunc(func(*GitHubRelease) (*ReleaseDelta, error) {
		called = true
		return &ReleaseDelta{ReleasesBehind: 99}, nil
	})

	info := c.CheckNow()

	if called {
		t.Error("delta resolver ran even though no update is available")
	}
	if info.BehindSummary != "" {
		t.Errorf("BehindSummary = %q, want empty", info.BehindSummary)
	}
}

// The (channel, current, latest) triple is immutable, so a repeat check must
// not re-fetch. This is what keeps a hammered "Check for Updates" from
// tripling the request cost against a 60/hr unauthenticated budget.
func TestDeltaIsCachedAcrossChecks(t *testing.T) {
	c := deltaTestChecker(t, "v0.43.0", "v0.46.0")
	calls := 0
	c.SetDeltaFunc(func(*GitHubRelease) (*ReleaseDelta, error) {
		calls++
		return &ReleaseDelta{ReleasesBehind: 3}, nil
	})

	for i := 0; i < 3; i++ {
		if got := c.CheckNow(); got.BehindSummary != "3 releases behind" {
			t.Fatalf("check %d: BehindSummary = %q", i, got.BehindSummary)
		}
	}

	if calls != 1 {
		t.Errorf("delta resolver ran %d times across 3 checks, want 1", calls)
	}
}

// A channel switch changes the correct count without changing either version,
// so the cache must not answer for the new channel.
func TestDeltaCacheIsInvalidatedByAChannelSwitch(t *testing.T) {
	c := deltaTestChecker(t, "v0.43.0-rc.1", "v0.46.0")
	calls := 0
	c.SetDeltaFunc(func(*GitHubRelease) (*ReleaseDelta, error) {
		calls++
		return &ReleaseDelta{ReleasesBehind: calls}, nil
	})

	c.CheckNow()
	c.SetConfig(true, true) // stable -> rc
	c.CheckNow()

	if calls != 2 {
		t.Errorf("delta resolver ran %d times, want 2 (the channel switch must invalidate)", calls)
	}
}

// FR-017: a development build never checks, so it can never carry a delta.
func TestDevelopmentBuildCarriesNoDelta(t *testing.T) {
	c := deltaTestChecker(t, "development", "v0.46.0")
	c.SetDeltaFunc(func(*GitHubRelease) (*ReleaseDelta, error) {
		t.Error("delta resolver ran for a development build")
		return nil, nil
	})

	// CheckNow returns nil outright for a non-semver build — it never runs a
	// check, so there is nothing to enrich.
	if info := c.CheckNow(); info != nil &&
		(info.BehindSummary != "" || info.ReleasesBehind != nil) {
		t.Errorf("development build carries a delta: %+v", info)
	}

	if info := c.GetVersionInfo(); info != nil &&
		(info.BehindSummary != "" || info.ReleasesBehind != nil) {
		t.Errorf("development build reports a delta via GetVersionInfo: %+v", info)
	}
}
