package updatecheck

import (
	"testing"
	"time"
)

// Spec 079 FR-002. The delta rules are all here because ComputeReleaseDelta is
// pure — no network, no clock — so every edge case in the spec is a table row.

// rel builds a release fixture. day is an offset in days from a fixed epoch,
// so week arithmetic in the expectations is readable.
func rel(tag string, day int, prerelease bool) GitHubRelease {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return GitHubRelease{
		TagName:     tag,
		Prerelease:  prerelease,
		PublishedAt: epoch.AddDate(0, 0, day).Format(time.RFC3339),
	}
}

// page mirrors the shape GitHub actually returns: ordered by created_at, which
// is neither semver order nor publish order.
func page() []GitHubRelease {
	return []GitHubRelease{
		rel("v0.46.0", 98, false),
		rel("v0.45.0", 91, false),
		rel("v0.45.0-rc.2", 88, true),
		rel("v0.45.0-rc.10", 89, true), // deliberately after rc.2: created_at order lies
		rel("v0.44.0", 70, false),
		rel("v0.43.0", 42, false),
		rel("v0.42.0", 0, false),
	}
}

func TestComputeReleaseDelta(t *testing.T) {
	tests := []struct {
		name               string
		current, latest    string
		includePrereleases bool
		currentRelease     *GitHubRelease
		wantOK             bool
		wantBehind         int
		wantSaturated      bool
		wantWeeks          int
		wantWeeksKnown     bool
		wantSummary        string
	}{
		{
			name: "counts stable releases and weeks between the two publish dates",
			// v0.43.0 (day 42) -> v0.46.0 (day 98) = 56 days = exactly 8 weeks.
			// Newer stable releases: 0.44.0, 0.45.0, 0.46.0.
			current: "v0.43.0", latest: "v0.46.0",
			wantOK: true, wantBehind: 3, wantWeeks: 8, wantWeeksKnown: true,
			wantSummary: "3 releases / ~8 weeks behind",
		},
		{
			name: "a stable user never counts prereleases",
			// On the rc channel the same span also picks up rc.2 and rc.10.
			current: "v0.44.0", latest: "v0.46.0",
			wantOK: true, wantBehind: 2, wantWeeks: 4, wantWeeksKnown: true,
			wantSummary: "2 releases / ~4 weeks behind",
		},
		{
			name:    "an rc user counts prereleases too",
			current: "v0.44.0", latest: "v0.46.0", includePrereleases: true,
			wantOK: true, wantBehind: 4, wantWeeks: 4, wantWeeksKnown: true,
			wantSummary: "4 releases / ~4 weeks behind",
		},
		{
			name: "singular for exactly one release",
			// v0.45.0 (day 91) -> v0.46.0 (day 98) = 7 days = 1 week.
			current: "v0.45.0", latest: "v0.46.0",
			wantOK: true, wantBehind: 1, wantWeeks: 1, wantWeeksKnown: true,
			wantSummary: "1 release / ~1 week behind",
		},
		{
			name: "FR-016: no delta when the offered version is not newer",
			// The guard that makes a negative delta structurally impossible.
			current: "v0.46.0", latest: "v0.46.0",
			wantOK: false,
		},
		{
			name:    "FR-016: no delta when the running build is newer than the offer",
			current: "v0.47.0", latest: "v0.46.0",
			wantOK: false,
		},
		{
			name:    "FR-017: no delta for an unversioned build",
			current: "development", latest: "v0.46.0",
			wantOK: false,
		},
		{
			name: "saturates when the running build predates the scanned window",
			// v0.30.0 is older than every tag on the page, so releases between
			// it and v0.42.0 were never seen: the count is a lower bound and
			// no publish date is available for the week figure.
			current: "v0.30.0", latest: "v0.46.0",
			wantOK: true, wantBehind: 5, wantSaturated: true, wantWeeksKnown: false,
			wantSummary: "5+ releases behind",
		},
		{
			name: "a by-tag lookup recovers the age even when the count saturates",
			// This is the whole reason the second request exists: the far-behind
			// cohort still gets an exact "M weeks", which is the motivating part.
			current: "v0.30.0", latest: "v0.46.0",
			currentRelease: ptrRel(rel("v0.30.0", -14, false)), // 112 days before v0.46.0
			wantOK:         true, wantBehind: 5, wantSaturated: true,
			wantWeeks: 16, wantWeeksKnown: true,
			wantSummary: "5+ releases / ~16 weeks behind",
		},
		{
			name: "under a week reports the count without a time clause",
			// v0.45.0-rc.10 (day 89) -> v0.46.0 (day 98) is 9 days, but the
			// rc user is 2 releases behind and the week figure is 1.
			current: "v0.45.0-rc.2", latest: "v0.45.0", includePrereleases: true,
			// rc.2 (day 88) -> v0.45.0 (day 91) = 3 days = 0 weeks.
			wantOK: true, wantBehind: 2, wantWeeks: 0, wantWeeksKnown: true,
			wantSummary: "2 releases behind",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ComputeReleaseDelta(tc.current, tc.latest, page(), tc.currentRelease, tc.includePrereleases)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (delta %+v)", ok, tc.wantOK, got)
			}
			if !tc.wantOK {
				return
			}
			if got.ReleasesBehind != tc.wantBehind {
				t.Errorf("ReleasesBehind = %d, want %d", got.ReleasesBehind, tc.wantBehind)
			}
			if got.Saturated != tc.wantSaturated {
				t.Errorf("Saturated = %v, want %v", got.Saturated, tc.wantSaturated)
			}
			if got.WeeksKnown != tc.wantWeeksKnown {
				t.Errorf("WeeksKnown = %v, want %v", got.WeeksKnown, tc.wantWeeksKnown)
			}
			if tc.wantWeeksKnown && got.WeeksBehind != tc.wantWeeks {
				t.Errorf("WeeksBehind = %d, want %d", got.WeeksBehind, tc.wantWeeks)
			}
			if summary := FormatBehindSummary(got); summary != tc.wantSummary {
				t.Errorf("FormatBehindSummary = %q, want %q", summary, tc.wantSummary)
			}
		})
	}
}

// FR-016 says the delta is never negative. A backport patch published after a
// newer minor makes the date arithmetic go backwards; the count survives, the
// age is simply withheld rather than rendered as a negative or an absolute.
func TestComputeReleaseDeltaWithdrawsAnInvertedAge(t *testing.T) {
	releases := []GitHubRelease{
		rel("v0.46.0", 50, false),
		rel("v0.45.1", 60, false), // backport, published AFTER v0.46.0
		rel("v0.45.0", 10, false),
	}
	got, ok := ComputeReleaseDelta("v0.45.1", "v0.46.0", releases, nil, false)
	if !ok {
		t.Fatal("expected a delta: v0.46.0 is semver-newer than v0.45.1")
	}
	if got.WeeksKnown {
		t.Errorf("WeeksKnown = true (%d weeks); an inverted publish order must withhold the age", got.WeeksBehind)
	}
	if want := "1 release behind"; FormatBehindSummary(got) != want {
		t.Errorf("summary = %q, want %q", FormatBehindSummary(got), want)
	}
}

// GitHub orders the releases list by created_at, so list position says nothing
// about which release is newest. Counting by position would miscount whenever
// a prerelease was created before, but published after, a sibling.
func TestComputeReleaseDeltaIgnoresListOrder(t *testing.T) {
	forward := page()
	reversed := make([]GitHubRelease, 0, len(forward))
	for i := len(forward) - 1; i >= 0; i-- {
		reversed = append(reversed, forward[i])
	}

	a, okA := ComputeReleaseDelta("v0.43.0", "v0.46.0", forward, nil, false)
	b, okB := ComputeReleaseDelta("v0.43.0", "v0.46.0", reversed, nil, false)
	if !okA || !okB {
		t.Fatal("expected a delta from both orderings")
	}
	if a != b {
		t.Errorf("delta depends on list order: %+v vs %+v", a, b)
	}
}

// A release carrying an "-rc" tag but published without GitHub's prerelease
// flag (or vice versa) must not inflate a stable user's count.
func TestComputeReleaseDeltaChannelGuardsAreBeltAndBraces(t *testing.T) {
	releases := []GitHubRelease{
		rel("v0.46.0", 98, false),
		{TagName: "v0.46.0-rc.1", Prerelease: false, PublishedAt: rel("x", 95, false).PublishedAt}, // flag lies
		{TagName: "v0.45.5", Prerelease: true, PublishedAt: rel("x", 93, false).PublishedAt},       // flag lies the other way
		rel("v0.45.0", 91, false),
	}
	got, ok := ComputeReleaseDelta("v0.45.0", "v0.46.0", releases, nil, false)
	if !ok {
		t.Fatal("expected a delta")
	}
	// Only v0.46.0 qualifies: the rc tag is excluded by its semver prerelease
	// segment despite the flag, and v0.45.5 by its flag despite a clean tag.
	if got.ReleasesBehind != 1 {
		t.Errorf("ReleasesBehind = %d, want 1 (both channel guards must apply)", got.ReleasesBehind)
	}
}

func TestFormatBehindSummaryEmptyWhenNothingToSay(t *testing.T) {
	if s := FormatBehindSummary(ReleaseDelta{}); s != "" {
		t.Errorf("FormatBehindSummary(zero) = %q, want empty so surfaces keep their pre-delta wording", s)
	}
}

// "1+ releases" is correct — the plus already implies more than one.
func TestFormatBehindSummarySaturatedSingularStaysPlural(t *testing.T) {
	got := FormatBehindSummary(ReleaseDelta{ReleasesBehind: 1, Saturated: true})
	if want := "1+ releases behind"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func ptrRel(r GitHubRelease) *GitHubRelease { return &r }
