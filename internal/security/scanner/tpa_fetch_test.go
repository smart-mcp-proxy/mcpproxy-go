package scanner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Loopback (httptest) tests of the fetch adapter (tasks T324, B4, B5, B11). No
// real network is ever used.

// recordedRequest is one request the test channel served.
type recordedRequest struct {
	Method, Path, Accept, UserAgent, IfNoneMatch string
}

// testChannel is an httptest server with a request log and per-path handlers.
type testChannel struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	reqs     []recordedRequest
	handlers map[string]http.HandlerFunc
}

func newTestChannel(t *testing.T) *testChannel {
	t.Helper()
	c := &testChannel{t: t, handlers: map[string]http.HandlerFunc{}}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.reqs = append(c.reqs, recordedRequest{r.Method, r.URL.Path, r.Header.Get("Accept"), r.Header.Get("User-Agent"), r.Header.Get("If-None-Match")})
		h := c.handlers[r.URL.Path]
		c.mu.Unlock()
		if h == nil {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *testChannel) handle(path string, h http.HandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[path] = h
}

func (c *testChannel) serve(path string, status int, body []byte, hdr map[string]string) {
	c.handle(path, func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}

func (c *testChannel) requests() []recordedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]recordedRequest(nil), c.reqs...)
}

func (c *testChannel) count() int { return len(c.requests()) }

func (c *testChannel) pathsRequested() []string {
	var out []string
	for _, r := range c.requests() {
		out = append(out, r.Path)
	}
	return out
}

const releasesPath = "/repos/acme/tpa-db/releases/latest"

// releasesJSON renders a Releases-API body whose download URLs point back at the channel.
func (c *testChannel) releasesJSON(names ...string) []byte {
	type asset struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int    `json:"size"`
	}
	var assets []asset
	for _, n := range names {
		assets = append(assets, asset{Name: n, URL: c.srv.URL + "/dl/" + n, Size: 1})
	}
	raw, err := json.Marshal(map[string]any{"tag_name": "v1", "assets": assets})
	require.NoError(c.t, err)
	return raw
}

// settings builds the fetch settings for url against an operator-root policy
// that trusts keyOperator.
func fetchTestSettings(t *testing.T, url string, mutate func(*fetchSettings)) fetchSettings {
	t.Helper()
	withTrustAnchors(t, nil, 0, nil)
	sec := &config.SecurityConfig{
		TPAActiveRoot:       config.TPARootOperator,
		TPAOperatorSigners:  []string{keyOperator.b64},
		TPAOperatorBaseline: u64p(1),
		TPAFetchEnabled:     true,
		TPAFetchURL:         url,
	}
	s := fetchSettings{Enabled: true, URL: url, Version: "1.2.3", Policy: policyFromConfig(sec)}
	if mutate != nil {
		mutate(&s)
	}
	return s
}

func fetchCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// publish serves a signed pair as release assets and returns the pair.
func (c *testChannel) publishRelease(seq uint64, tag string) Candidate {
	pair := signedCandidate(c.t, keyOperator, bundleOpts{Seq: seq, Tag: tag})
	name := "scanner-bundle-" + itoa(seq) + ".json"
	c.serve("/dl/"+name, 200, pair.Bundle, nil)
	c.serve("/dl/"+name+".sig", 200, pair.Sidecar, nil)
	c.serve(releasesPath, 200, c.releasesJSON(name, name+".sig", "notes.txt"), map[string]string{"ETag": `"rel-` + itoa(seq) + `"`})
	return pair
}

func itoa(v uint64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestFetch_ReleasesModeYieldsOneCandidate(t *testing.T) {
	ch := newTestChannel(t)
	pair := ch.publishRelease(2, "r2")
	url := ch.srv.URL + releasesPath

	res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, url, nil))

	require.Equal(t, fetchOutcomeCandidate, res.Outcome, "reason=%s detail=%s", res.Reason, res.Detail)
	require.NotNil(t, res.Candidate)
	assert.Equal(t, pair.Bundle, res.Candidate.Bundle)
	assert.Equal(t, pair.Sidecar, res.Candidate.Sidecar)
	assert.True(t, res.Candidate.SidecarPresent)
	assert.Equal(t, BundleSourceFetch, res.Candidate.Source)
	assert.Equal(t, "http://127.0.0.1", res.Candidate.Path, "scheme and host only, never the URL")
	assert.Equal(t, `"rel-2"`, res.ETag)

	reqs := ch.requests()
	require.Len(t, reqs, 3, "the release JSON, the bundle and its sidecar")
	assert.Equal(t, "application/vnd.github+json", reqs[0].Accept)
	assert.Equal(t, "mcpproxy/1.2.3", reqs[0].UserAgent)
	assert.Equal(t, releasesPath, reqs[0].Path)
	for _, r := range reqs[1:] {
		assert.Equal(t, "application/octet-stream", r.Accept, r.Path)
		assert.Equal(t, "mcpproxy/1.2.3", r.UserAgent, r.Path)
		assert.Equal(t, http.MethodGet, r.Method)
	}
	assert.ElementsMatch(t, []string{"/dl/scanner-bundle-2.json", "/dl/scanner-bundle-2.json.sig"}, ch.pathsRequested()[1:])
}

func TestFetch_ReleasesModeChannelInvalid(t *testing.T) {
	tests := []struct {
		name   string
		assets []string
		body   []byte
		status int
	}{
		{name: "no candidate asset", assets: []string{"notes.txt", "other.json"}},
		{name: "two candidate assets", assets: []string{"scanner-bundle-2.json", "scanner-bundle-2.json.sig", "scanner-bundle-3.json", "scanner-bundle-3.json.sig"}},
		{name: "a missing sidecar asset", assets: []string{"scanner-bundle-2.json"}},
		{name: "a sequence that overflows", assets: []string{"scanner-bundle-99999999999999999999999.json", "scanner-bundle-99999999999999999999999.json.sig"}},
		{name: "not JSON", body: []byte("<html>rate limited</html>"), status: 200},
		{name: "an asset that points at a non-loopback http host", assets: []string{"scanner-bundle-2.json", "scanner-bundle-2.json.sig"}, body: []byte(`{"assets":[{"name":"scanner-bundle-2.json","browser_download_url":"http://evil.example/a"},{"name":"scanner-bundle-2.json.sig","browser_download_url":"http://evil.example/a.sig"}]}`), status: 200},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ch := newTestChannel(t)
			body, status := tc.body, tc.status
			if body == nil {
				body, status = ch.releasesJSON(tc.assets...), 200
			}
			ch.serve(releasesPath, status, body, nil)

			res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+releasesPath, nil))

			assert.Equal(t, RefreshOutcomeChannelInvalid, res.Outcome, "reason=%s", res.Reason)
			assert.Nil(t, res.Candidate)
			assert.Equal(t, 1, ch.count(), "only the release JSON was fetched; nothing was downloaded")
		})
	}
}

func TestFetch_ReleasesModeBodyCap(t *testing.T) {
	ch := newTestChannel(t)
	filler := strings.Repeat("x", (1<<20)+10)
	ch.serve(releasesPath, 200, []byte(`{"assets":[],"body":"`+filler+`"}`), nil)

	res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+releasesPath, nil))

	assert.Equal(t, RefreshOutcomeChannelInvalid, res.Outcome, "a release JSON over 1 MiB is a malformed channel")
	assert.Nil(t, res.Candidate)
}

func TestFetch_ReleasesModeHintSkipsBelowWatermark(t *testing.T) {
	for _, tc := range []struct {
		name     string
		seq, wm  uint64
		download bool
	}{
		{"below the watermark", 2, 5, false},
		{"at the watermark", 5, 5, false},
		{"above the watermark", 6, 5, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := newTestChannel(t)
			ch.publishRelease(tc.seq, "hint")
			var asked string
			res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+releasesPath, func(s *fetchSettings) {
				s.Watermark = func(authority string) (uint64, bool) { asked = authority; return tc.wm, true }
			}))
			assert.Equal(t, AuthorityOperator, asked, "the hint consults the ACTIVE root's watermark")
			if tc.download {
				assert.Equal(t, fetchOutcomeCandidate, res.Outcome)
				assert.Equal(t, 3, ch.count())
				return
			}
			assert.Equal(t, RefreshOutcomeUnchanged, res.Outcome)
			assert.Nil(t, res.Candidate)
			assert.Equal(t, 1, ch.count(), "the filename sequence is an unsigned hint that spares the download")
		})
	}
}

func TestFetch_ReleasesModeETagNotModified(t *testing.T) {
	ch := newTestChannel(t)
	ch.publishRelease(2, "etag")
	ch.handle(releasesPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"rel-2"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		http.Error(w, "expected the cached validator", http.StatusPreconditionFailed)
	})

	res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+releasesPath, func(s *fetchSettings) { s.ETag = `"rel-2"` }))

	assert.Equal(t, RefreshOutcomeNotModified, res.Outcome)
	assert.Nil(t, res.Candidate)
	assert.Equal(t, `"rel-2"`, res.ETag, "the validator is kept")
	assert.Equal(t, 1, ch.count(), "no download after a 304")
	assert.Equal(t, `"rel-2"`, ch.requests()[0].IfNoneMatch)
}

func TestFetch_DirectMode(t *testing.T) {
	const path = "/bundles/scanner-bundle.json"

	t.Run("bundle plus sidecar", func(t *testing.T) {
		ch := newTestChannel(t)
		pair := signedCandidate(t, keyOperator, bundleOpts{Seq: 3, Tag: "direct"})
		ch.serve(path, 200, pair.Bundle, map[string]string{"ETag": `"b1"`})
		ch.serve(path+".sig", 200, pair.Sidecar, nil)

		res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+path, func(s *fetchSettings) { s.ETag = `"old"` }))

		require.Equal(t, fetchOutcomeCandidate, res.Outcome, "reason=%s", res.Reason)
		assert.Equal(t, pair.Bundle, res.Candidate.Bundle)
		assert.Equal(t, pair.Sidecar, res.Candidate.Sidecar)
		assert.True(t, res.Candidate.SidecarPresent)
		assert.Equal(t, `"b1"`, res.ETag)
		reqs := ch.requests()
		require.Len(t, reqs, 2)
		assert.Equal(t, path, reqs[0].Path)
		assert.Equal(t, `"old"`, reqs[0].IfNoneMatch, "If-None-Match goes on the bundle")
		assert.Equal(t, path+".sig", reqs[1].Path)
		assert.Empty(t, reqs[1].IfNoneMatch)
		assert.Equal(t, "mcpproxy/1.2.3", reqs[0].UserAgent)
		assert.Equal(t, "application/octet-stream", reqs[0].Accept)
	})

	t.Run("a 404 sidecar means unsigned", func(t *testing.T) {
		ch := newTestChannel(t)
		ch.serve(path, 200, buildBundle(t, bundleOpts{Seq: 3, Tag: "nosig"}), nil)

		res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+path, nil))

		require.Equal(t, fetchOutcomeCandidate, res.Outcome, "reason=%s", res.Reason)
		assert.False(t, res.Candidate.SidecarPresent, "the pipeline itself then refuses it as signature_required")
		assert.Empty(t, res.Candidate.Sidecar)
	})

	t.Run("a 304 bundle is not_modified", func(t *testing.T) {
		ch := newTestChannel(t)
		ch.serve(path, http.StatusNotModified, nil, nil)

		res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+path, func(s *fetchSettings) { s.ETag = `"b1"` }))

		assert.Equal(t, RefreshOutcomeNotModified, res.Outcome)
		assert.Equal(t, 1, ch.count())
	})

	t.Run("another sidecar failure is fetch_unavailable", func(t *testing.T) {
		ch := newTestChannel(t)
		ch.serve(path, 200, buildBundle(t, bundleOpts{Seq: 3}), nil)
		ch.serve(path+".sig", 500, []byte("boom"), nil)

		res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+path, nil))

		assert.Equal(t, RefreshOutcomeFetchUnavailable, res.Outcome)
		assert.Equal(t, RefreshReasonHTTPStatus, res.Reason)
		assert.Nil(t, res.Candidate)
	})

	t.Run("a bundle failure is fetch_unavailable", func(t *testing.T) {
		ch := newTestChannel(t)
		ch.serve(path, 502, []byte("bad gateway"), nil)

		res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+path, nil))

		assert.Equal(t, RefreshOutcomeFetchUnavailable, res.Outcome)
		assert.Equal(t, RefreshReasonHTTPStatus, res.Reason)
	})

	t.Run("over-limit bodies are handed to the pipeline as they are", func(t *testing.T) {
		ch := newTestChannel(t)
		ch.serve(path, 200, make([]byte, MaxBundleBytes+4096), nil)
		ch.serve(path+".sig", 200, make([]byte, MaxSidecarBytes+4096), nil)

		res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+path, nil))

		require.Equal(t, fetchOutcomeCandidate, res.Outcome, "reason=%s", res.Reason)
		assert.Len(t, res.Candidate.Bundle, MaxBundleBytes+1, "one byte over the ceiling is enough for ceiling_exceeded")
		assert.Len(t, res.Candidate.Sidecar, MaxSidecarBytes+1)
	})
}

func TestFetch_ReleasesModeDownloadFailureIsUnavailable(t *testing.T) {
	ch := newTestChannel(t)
	ch.serve(releasesPath, 200, ch.releasesJSON("scanner-bundle-2.json", "scanner-bundle-2.json.sig"), nil)
	ch.serve("/dl/scanner-bundle-2.json", 500, []byte("boom"), nil)

	res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+releasesPath, nil))

	assert.Equal(t, RefreshOutcomeFetchUnavailable, res.Outcome)
	assert.Equal(t, RefreshReasonHTTPStatus, res.Reason)
}

func TestFetch_ModeIsDetectedFromThePathOnly(t *testing.T) {
	for path, releases := range map[string]bool{
		"/repos/a/b/releases/latest":          true,
		"/repos/a/b/releases/latest/":         false,
		"/repos/a/b/releases":                 false,
		"/x/repos/a/b/releases/latest":        false,
		"/repos/a/b/c/releases/latest":        false,
		"/bundles/scanner-bundle.json":        false,
		"/repos/own-er/re.po/releases/latest": true,
	} {
		assert.Equal(t, releases, isReleasesPath(path), path)
	}
}
