package updatecheck

import (
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
)

// Spec 079 US3 / FR-018: the automated check is rate-limited to at most a
// daily check and backs off on failure (rate-limit and transient errors are
// "unknown", never a reason to hammer GitHub).

func TestChecker_DefaultIntervalIsDaily(t *testing.T) {
	if DefaultCheckInterval != 24*time.Hour {
		t.Fatalf("DefaultCheckInterval = %v, want 24h (FR-018: at most a daily check)", DefaultCheckInterval)
	}
}

func TestChecker_BackoffAfterFailure(t *testing.T) {
	checker := New(zaptest.NewLogger(t), "v1.0.0")
	checker.SetCheckInterval(time.Hour)

	base := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	now := base
	checker.nowFn = func() time.Time { return now }

	calls := 0
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		calls++
		return nil, errors.New("rate limited")
	})

	// First periodic check runs and fails.
	checker.check()
	if calls != 1 {
		t.Fatalf("first check: calls = %d, want 1", calls)
	}

	// Immediately after a failure the periodic path must NOT hit the network
	// again (backoff), even though the caller (loop tick) asked.
	checker.check()
	if calls != 1 {
		t.Fatalf("within backoff: calls = %d, want 1 (no retry before backoff expires)", calls)
	}

	// One base interval later is still inside the doubled (2x) window.
	now = base.Add(time.Hour)
	checker.check()
	if calls != 1 {
		t.Fatalf("at 1x interval after 1 failure: calls = %d, want 1", calls)
	}

	// At 2x the interval the backoff window has expired: retry happens (and
	// fails again, doubling the window to 4x).
	now = base.Add(2 * time.Hour)
	checker.check()
	if calls != 2 {
		t.Fatalf("at 2x interval: calls = %d, want 2", calls)
	}

	// 2x after the second failure is inside its 4x window.
	now = now.Add(2 * time.Hour)
	checker.check()
	if calls != 2 {
		t.Fatalf("inside 4x window: calls = %d, want 2", calls)
	}

	// The backoff is capped: even many failures later the window never
	// exceeds 8x the interval.
	now = now.Add(4 * time.Hour)
	checker.check() // 3rd failure -> 8x cap
	if calls != 3 {
		t.Fatalf("after 4x window: calls = %d, want 3", calls)
	}
	now = now.Add(8 * time.Hour)
	checker.check() // 4th failure -> still 8x
	if calls != 4 {
		t.Fatalf("after capped 8x window: calls = %d, want 4", calls)
	}
}

func TestChecker_BackoffResetsOnSuccess(t *testing.T) {
	checker := New(zaptest.NewLogger(t), "v1.0.0")
	checker.SetCheckInterval(time.Hour)

	base := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	now := base
	checker.nowFn = func() time.Time { return now }

	calls := 0
	fail := true
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		calls++
		if fail {
			return nil, errors.New("offline")
		}
		return &GitHubRelease{TagName: "v1.1.0", HTMLURL: "https://example.com"}, nil
	})

	checker.check() // failure #1 -> 2x window
	fail = false
	now = base.Add(2 * time.Hour)
	checker.check() // success -> backoff cleared
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}

	// After a success the periodic cadence is governed by the ticker alone:
	// the next tick must run (no lingering backoff window).
	now = now.Add(time.Hour)
	checker.check()
	if calls != 3 {
		t.Fatalf("post-success tick: calls = %d, want 3 (backoff must reset)", calls)
	}
}

func TestChecker_CheckNowBypassesBackoff(t *testing.T) {
	checker := New(zaptest.NewLogger(t), "v1.0.0")
	checker.SetCheckInterval(time.Hour)

	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	checker.nowFn = func() time.Time { return now }

	calls := 0
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		calls++
		return nil, errors.New("offline")
	})

	checker.check() // failure -> backoff active
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}

	// A user-initiated refresh (/api/v1/info?refresh=true) is explicit intent
	// and must not be silently swallowed by the failure backoff.
	checker.CheckNow()
	if calls != 2 {
		t.Fatalf("CheckNow within backoff: calls = %d, want 2 (manual refresh bypasses backoff)", calls)
	}
}

// Spec 079 US3 / FR-019: in CI / non-interactive contexts UI nudges are
// suppressed while machine-readable surfaces still report the facts.

func TestChecker_CISuppressesNudges(t *testing.T) {
	t.Setenv("CI", "true")

	core, logs := observer.New(zap.InfoLevel)
	checker := New(zap.New(core), "v1.0.0")
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		return &GitHubRelease{TagName: "v2.0.0", HTMLURL: "https://example.com/rel"}, nil
	})

	info := checker.CheckNow()
	if info == nil {
		t.Fatal("CheckNow returned nil")
	}
	// The facts still flow to machine-readable consumers…
	if !info.UpdateAvailable || info.LatestVersion != "v2.0.0" {
		t.Fatalf("update facts must be reported in CI: %+v", info)
	}
	// …but the payload tells UI surfaces to stay quiet…
	if !info.NudgesSuppressed {
		t.Fatal("NudgesSuppressed = false in CI, want true (FR-019)")
	}
	if resp := info.ToAPIResponse(); !resp.NudgesSuppressed {
		t.Fatal("ToAPIResponse().NudgesSuppressed = false in CI, want true")
	}
	// …and the core does not emit the per-run availability nag.
	for _, entry := range logs.All() {
		if entry.Message == "Update available" {
			t.Fatal("'Update available' logged at info level in CI (FR-019: no per-run nag)")
		}
	}
}

func TestChecker_NoSuppressionOutsideCI(t *testing.T) {
	t.Setenv("CI", "")

	checker := New(zaptest.NewLogger(t), "v1.0.0")
	checker.SetCheckFunc(func() (*GitHubRelease, error) {
		return &GitHubRelease{TagName: "v2.0.0", HTMLURL: "https://example.com/rel"}, nil
	})

	info := checker.CheckNow()
	if info == nil {
		t.Fatal("CheckNow returned nil")
	}
	if info.NudgesSuppressed {
		t.Fatal("NudgesSuppressed = true outside CI, want false")
	}
}
