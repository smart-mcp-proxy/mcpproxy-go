package updatecheck

import (
	"fmt"
	"time"

	"golang.org/x/mod/semver"
)

// Spec 079 FR-002 — the "N releases / M weeks behind" delta.
//
// This file is deliberately pure: no network, no clock, no locks. Everything
// it needs arrives as arguments so the whole rule set is a table test. The
// fetching lives in checker.go (the deltaFunc seam) and github.go.
//
// The delta is ENRICHMENT of the single update-check result, never a second
// check pipeline (FR-001). It can only make an existing "update available"
// message more specific; it can never create, suppress or contradict one.

// deltaScanPages is how many 100-release pages the delta scan reads. One page
// currently reaches back about four months (v0.62.0 → v0.26.2 as of
// 2026-08-29); anything older is reported as a lower bound rather than paged
// for. Raising this is a one-line change if the saturated cohort turns out to
// matter more than the extra requests.
const deltaScanPages = 1

// releasesPerPage is GitHub's maximum page size for the releases list.
const releasesPerPage = 100

// deltaBudget caps the WALL CLOCK the delta enrichment may add to a check.
//
// This is not the client's per-request httpTimeout: the delta issues up to two
// more sequential requests on that shared client, so a per-request timeout
// bounds each hop and nothing bounds the total. It is enforced as a context
// deadline covering BOTH requests, because `CheckNow` runs SYNCHRONOUSLY
// inside GET /api/v1/info?refresh=true (the tray's "Check for Updates"), where
// an unbounded delta would be a visible stall on a request a user is waiting
// on. Whatever is resolved when the deadline expires is what gets reported;
// the rest is omitted.
const deltaBudget = 8 * time.Second

// deltaCacheTTL ages out a resolved delta.
//
// The (channel, current, latest) triple is very nearly immutable — but not
// quite: a release can be published for an older tag, deleted, or reclassified
// between prerelease and stable, all of which change the correct count without
// changing the key. Without a TTL a count resolved once would stand for the
// life of the process. A day matches the check cadence, so in the steady state
// this still costs no extra requests per check.
const deltaCacheTTL = 24 * time.Hour

// ReleaseDelta is how far the running build is behind the offered release.
//
// The two "known" flags exist because zero is a legitimate value for both
// figures and must stay distinguishable from "we could not work it out".
type ReleaseDelta struct {
	// ReleasesBehind counts releases on the OFFERED channel that are newer
	// than the running version and no newer than the offered one.
	ReleasesBehind int

	// Saturated marks ReleasesBehind as a lower bound: the running version
	// predates the scanned window, so releases older than it were never seen.
	//
	// Note this flag catches the common case, not every case. The scan reads
	// one page ordered by created_at, so a release that is semver-in-range but
	// was CREATED long ago (a late-published backport, say) can fall off the
	// page and be missed without setting this flag — the count would then be
	// one short and not marked as a bound. Spec 079's own assumption (L159)
	// allows an approximation where an exact count is unavailable, and the
	// figure is motivational rather than load-bearing, so this is accepted
	// deliberately rather than paged for.
	Saturated bool

	// WeeksBehind is whole weeks between the two releases' publish dates.
	// Only meaningful when WeeksKnown is true.
	WeeksBehind int

	// WeeksKnown is false when either publish date was missing, unparseable,
	// or ordered backwards (a backport published after a newer minor).
	WeeksKnown bool
}

// ComputeReleaseDelta works out how far `current` is behind `latest` given one
// page of releases and, optionally, the running version's own release record
// (used only when that version is absent from the page).
//
// It returns ok=false when no honest statement can be made — an invalid
// version on either side, or a `latest` that is not actually newer. Callers
// treat !ok as "emit nothing" rather than as an error: FR-016 forbids a
// negative delta and FR-006 forbids letting a missing delta degrade anything.
func ComputeReleaseDelta(current, latest string, page []GitHubRelease, currentRelease *GitHubRelease, includePrereleases bool) (ReleaseDelta, bool) {
	cur := ensureVPrefix(current)
	lat := ensureVPrefix(latest)
	if !semver.IsValid(cur) || !semver.IsValid(lat) {
		return ReleaseDelta{}, false
	}
	// FR-016: the delta exists only when there is genuinely something newer.
	if semver.Compare(cur, lat) >= 0 {
		return ReleaseDelta{}, false
	}

	var d ReleaseDelta

	// Count by semver, never by list position. GitHub orders the list by
	// created_at, which is not publish order and not semver order: the live
	// data contains v0.53.0-rc.9 listed ahead of the later-published
	// v0.53.0-rc.10.
	oldest := "" // oldest tag seen on the page, any channel — the window floor
	for i := range page {
		tag := ensureVPrefix(page[i].TagName)
		if !semver.IsValid(tag) {
			continue
		}
		if oldest == "" || semver.Compare(tag, oldest) < 0 {
			oldest = tag
		}
		if !releaseOnChannel(&page[i], includePrereleases) {
			continue
		}
		if semver.Compare(cur, tag) < 0 && semver.Compare(tag, lat) <= 0 {
			d.ReleasesBehind++
		}
	}

	// The running build predates everything we looked at, so releases between
	// it and the window floor were never counted.
	if oldest != "" && semver.Compare(cur, oldest) < 0 {
		d.Saturated = true
	}

	// A count of zero with an update genuinely available means the page did
	// not reach the relevant releases at all — say nothing rather than "0".
	if d.ReleasesBehind <= 0 {
		return ReleaseDelta{}, false
	}

	// Weeks: the distance between the two releases, not "now minus yours".
	// That keeps the figure stable while a machine is offline, immune to
	// clock skew, and deterministic in tests.
	curPub, curOK := publishedAt(findRelease(page, cur), currentRelease)
	latPub, latOK := publishedAt(findRelease(page, lat), nil)
	if curOK && latOK && !latPub.Before(curPub) {
		d.WeeksBehind = int(latPub.Sub(curPub) / (7 * 24 * time.Hour))
		d.WeeksKnown = true
	}

	return d, true
}

// releaseOnChannel reports whether a release belongs to the channel currently
// being offered. On the stable channel both guards matter: GitHub's
// `prerelease` flag is set by whoever published, while the tag's semver
// prerelease segment is structural. Trusting only one lets an "-rc.1" tagged
// as a full release inflate a stable user's count.
func releaseOnChannel(r *GitHubRelease, includePrereleases bool) bool {
	if includePrereleases {
		return true
	}
	return !r.Prerelease && semver.Prerelease(ensureVPrefix(r.TagName)) == ""
}

// findRelease returns the release carrying an exact semver-equal tag.
func findRelease(page []GitHubRelease, want string) *GitHubRelease {
	for i := range page {
		tag := ensureVPrefix(page[i].TagName)
		if semver.IsValid(tag) && semver.Compare(tag, want) == 0 {
			return &page[i]
		}
	}
	return nil
}

// publishedAt parses the publish timestamp of the first release that has one.
func publishedAt(candidates ...*GitHubRelease) (time.Time, bool) {
	for _, r := range candidates {
		if r == nil || r.PublishedAt == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, r.PublishedAt)
		if err != nil {
			continue
		}
		return t, true
	}
	return time.Time{}, false
}

// FormatBehindSummary renders the delta clause every surface prints verbatim —
// "8 releases / ~14 weeks behind", "59+ releases / ~18 weeks behind",
// "1 release behind".
//
// It is authored once here, in Go, and shipped pre-rendered in the API payload
// so the Vue banner, the Swift tray, status and doctor cannot drift apart
// (FR-002 demands consistent framing across all of them). The raw numbers ship
// alongside it, so machine consumers never have to parse this string.
//
// Returns "" when there is nothing honest to say; callers then render exactly
// what they render today.
func FormatBehindSummary(d ReleaseDelta) string {
	if d.ReleasesBehind <= 0 {
		return ""
	}

	count := fmt.Sprintf("%d", d.ReleasesBehind)
	if d.Saturated {
		count += "+"
	}
	noun := "releases"
	// "1+ releases" is right — the plus already makes it plural.
	if d.ReleasesBehind == 1 && !d.Saturated {
		noun = "release"
	}
	summary := count + " " + noun

	// Under a week reads as "1 release behind", not "~0 weeks behind".
	if d.WeeksKnown && d.WeeksBehind > 0 {
		unit := "weeks"
		if d.WeeksBehind == 1 {
			unit = "week"
		}
		summary += fmt.Sprintf(" / ~%d %s", d.WeeksBehind, unit)
	}

	return summary + " behind"
}
