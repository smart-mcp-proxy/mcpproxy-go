package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

// Integration cover for the FETCH half of the FR-002 delta — the part
// delta_test.go deliberately leaves out because it is pure. This drives the
// real GitHubClient against an httptest GitHub, so ListReleases, the ETag
// conditional request, the by-tag fallback and fetchReleaseDelta are all
// exercised together, ending at the JSON the API actually serves.

type fakeGitHub struct {
	releases    []GitHubRelease
	listCalls   atomic.Int32
	tagCalls    atomic.Int32
	etag        string
	tagNotFound bool
}

func (f *fakeGitHub) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/repos/smart-mcp-proxy/mcpproxy-go/releases", func(w http.ResponseWriter, r *http.Request) {
		f.listCalls.Add(1)
		if f.etag != "" {
			if r.Header.Get("If-None-Match") == f.etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", f.etag)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.releases)
	})

	mux.HandleFunc("/repos/smart-mcp-proxy/mcpproxy-go/releases/tags/", func(w http.ResponseWriter, r *http.Request) {
		f.tagCalls.Add(1)
		if f.tagNotFound {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		tag := r.URL.Path[len("/repos/smart-mcp-proxy/mcpproxy-go/releases/tags/"):]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(GitHubRelease{
			TagName:     tag,
			PublishedAt: "2026-01-01T00:00:00Z",
		})
	})

	return mux
}

// wideReleases builds a page of `count` stable releases descending from
// v0.62.0, one week apart, newest first.
func wideReleases(count int) []GitHubRelease {
	out := make([]GitHubRelease, 0, count)
	for i := 0; i < count; i++ {
		r := rel(fmt.Sprintf("v0.%d.0", 62-i), 700-(i*7), false)
		r.HTMLURL = "https://example.invalid/releases/" + r.TagName
		out = append(out, r)
	}
	return out
}

func fetchTestChecker(t *testing.T, version string, fake *fakeGitHub) (*Checker, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	c := New(zaptest.NewLogger(t), version)
	c.githubClient.SetAPIBase(srv.URL)
	c.SetCheckFunc(func() (*GitHubRelease, error) {
		return &fake.releases[0], nil
	})
	return c, srv
}

// The end-to-end happy path, all the way to the wire DTO.
func TestDeltaFetchEndToEnd(t *testing.T) {
	fake := &fakeGitHub{releases: wideReleases(10)}
	c, _ := fetchTestChecker(t, "v0.58.0", fake)

	info := c.CheckNow()

	// v0.58.0 -> v0.62.0 is 4 releases, published 4 weeks apart in the fixture.
	if info.BehindSummary != "4 releases / ~4 weeks behind" {
		t.Fatalf("BehindSummary = %q", info.BehindSummary)
	}
	if fake.tagCalls.Load() != 0 {
		t.Errorf("by-tag lookup ran %d times; the running version is on the page, so it is unnecessary",
			fake.tagCalls.Load())
	}

	// The API DTO is a hand-copied struct, so verify the fields actually reach
	// the wire rather than trusting the mapper.
	payload, err := json.Marshal(info.ToAPIResponse())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for key, want := range map[string]any{
		"behind_summary":  "4 releases / ~4 weeks behind",
		"releases_behind": float64(4),
		"weeks_behind":    float64(4),
	} {
		if got := wire[key]; got != want {
			t.Errorf("wire[%q] = %v, want %v", key, got, want)
		}
	}
	// FR-021: the original contract is untouched.
	for _, key := range []string{"available", "latest_version", "release_url", "checked_at"} {
		if _, ok := wire[key]; !ok {
			t.Errorf("wire lost the pre-existing field %q", key)
		}
	}
}

// The far-behind cohort: the running build predates the page, so the count is
// a lower bound — but the by-tag lookup still recovers an exact age. This is
// the case the second request exists for.
func TestDeltaFetchFallsBackToTheTagLookup(t *testing.T) {
	fake := &fakeGitHub{releases: wideReleases(10)} // reaches back only to v0.53.0
	c, _ := fetchTestChecker(t, "v0.30.0", fake)

	info := c.CheckNow()

	if fake.tagCalls.Load() != 1 {
		t.Errorf("by-tag lookup ran %d times, want 1", fake.tagCalls.Load())
	}
	if info.ReleasesBehind == nil || !info.ReleasesBehindSaturated {
		t.Fatalf("expected a saturated count, got %+v", info)
	}
	if info.WeeksBehind == nil {
		t.Error("the by-tag lookup should have recovered the age")
	}
	if got := info.BehindSummary; got != "10+ releases / ~100 weeks behind" {
		t.Errorf("BehindSummary = %q", got)
	}
}

// A 404 from the tag lookup is ordinary (pseudo-version, yanked tag): the
// count survives, only the age is withheld.
func TestDeltaFetchSurvivesAMissingTag(t *testing.T) {
	fake := &fakeGitHub{releases: wideReleases(10), tagNotFound: true}
	c, _ := fetchTestChecker(t, "v0.30.0", fake)

	info := c.CheckNow()

	if info.BehindSummary != "10+ releases behind" {
		t.Errorf("BehindSummary = %q, want the count with no age clause", info.BehindSummary)
	}
	if info.WeeksBehind != nil {
		t.Errorf("WeeksBehind = %v, want absent", *info.WeeksBehind)
	}
	if info.CheckError != "" {
		t.Errorf("CheckError = %q; a missing tag is not a check failure", info.CheckError)
	}
}

// A conditional re-fetch must reuse the cached page rather than re-decoding,
// and GitHub does not charge rate-limit budget for a 304.
func TestListReleasesReusesTheCachedPageOn304(t *testing.T) {
	fake := &fakeGitHub{releases: wideReleases(5), etag: `W/"abc123"`}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	client := NewGitHubClient(zaptest.NewLogger(t))
	client.SetAPIBase(srv.URL)

	first, err := client.ListReleases(context.Background(), releasesPerPage)
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	second, err := client.ListReleases(context.Background(), releasesPerPage)
	if err != nil {
		t.Fatalf("second fetch (expected a 304): %v", err)
	}

	if len(first) != len(second) {
		t.Fatalf("304 returned %d releases, want the cached %d", len(second), len(first))
	}
	if second[0].TagName != first[0].TagName {
		t.Errorf("304 returned a different page: %q vs %q", second[0].TagName, first[0].TagName)
	}
	if fake.listCalls.Load() != 2 {
		t.Errorf("list endpoint hit %d times, want 2 (the second answered 304)", fake.listCalls.Load())
	}
}

// GitHub orders the list by created_at. Picking releases[0] can therefore
// offer an RC user a release older than one already published — the bug this
// replaced.
func TestLatestIncludingPrereleasesPicksBySemverNotPosition(t *testing.T) {
	fake := &fakeGitHub{releases: []GitHubRelease{
		rel("v0.53.0-rc.9", 10, true), // created first, listed first
		rel("v0.53.0-rc.10", 11, true),
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	client := NewGitHubClient(zaptest.NewLogger(t))
	client.SetAPIBase(srv.URL)

	latest, err := client.GetLatestReleaseIncludingPrereleases()
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if latest.TagName != "v0.53.0-rc.10" {
		t.Errorf("offered %q; rc.10 is newer than rc.9 despite being listed second", latest.TagName)
	}
}

// The tag reaches a request path, and the delta now feeds it a build-stamped
// string rather than a user-typed flag.
func TestGetReleaseByTagRejectsAMalformedTag(t *testing.T) {
	client := NewGitHubClient(zaptest.NewLogger(t))
	client.SetAPIBase("http://127.0.0.1:1") // must never be dialled

	for _, tag := range []string{"../../../etc/passwd", "v1.0.0/../../x", "not a version", "v1.0.0 x"} {
		if _, err := client.GetReleaseByTag(tag); err == nil {
			t.Errorf("GetReleaseByTag(%q) was accepted", tag)
		}
	}
}

// The delta budget must bound the TOTAL added wall clock, not merely decide
// whether the second request starts. Gating only between stages left stage 1
// free to spend nearly the whole budget and stage 2 to then take the client's
// full per-request timeout on top — and CheckNow runs synchronously inside
// GET /api/v1/info?refresh=true, so that lands on a user-facing request.
func TestDeltaBudgetBoundsTotalWallClock(t *testing.T) {
	// A server that never answers: without a deadline covering the request,
	// this would hang for the client's full 10s httpTimeout.
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-blocked:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	client := NewGitHubClient(zaptest.NewLogger(t))
	client.SetAPIBase(srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := client.ListReleases(ctx, releasesPerPage)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected the deadline to abort the request")
	}
	// Generous bound: the point is that it is governed by the context, not by
	// the 10s per-request timeout.
	if elapsed > 3*time.Second {
		t.Errorf("request took %s; the context deadline did not bound it", elapsed)
	}
}
