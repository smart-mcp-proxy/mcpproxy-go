package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// T325: egress, redirect policy, status mapping and host-only logging (tasks
// B6, B11, B12). Everything runs against loopback httptest servers or fake
// transports; nothing touches the real network.

// --- the egress predicate ----------------------------------------------------

func TestEgressAllowed(t *testing.T) {
	always := []string{"169.254.169.254", "169.254.1.1", "fe80::1", "0.0.0.0", "::", "224.0.0.1", "ff02::1", "::ffff:169.254.169.254", "0.1.2.3"}
	loopback := []string{"127.0.0.1", "127.9.9.9", "::1", "::ffff:127.0.0.1"}
	private := []string{"10.0.0.5", "172.16.0.1", "172.31.255.254", "192.168.1.1", "fd00::1", "fc00::1", "100.64.0.1", "100.127.255.254", "::ffff:10.0.0.5"}
	public := []string{"93.184.216.34", "8.8.8.8", "2606:2800:220:1:248:1893:25c8:1946", "172.32.0.1", "100.128.0.1"}

	for _, class := range []egressClass{egressPublic, egressLoopback, egressPrivate} {
		for _, s := range always {
			assert.False(t, egressAllowed(net.ParseIP(s), class), "%s must always be refused (class %d)", s, class)
		}
		for _, s := range public {
			assert.True(t, egressAllowed(net.ParseIP(s), class), "%s is public (class %d)", s, class)
		}
	}
	for _, s := range loopback {
		assert.False(t, egressAllowed(net.ParseIP(s), egressPublic), "%s from a public configured host", s)
		assert.True(t, egressAllowed(net.ParseIP(s), egressLoopback), "%s from a loopback configured host", s)
		assert.False(t, egressAllowed(net.ParseIP(s), egressPrivate), "%s from a LAN configured host", s)
	}
	for _, s := range private {
		assert.False(t, egressAllowed(net.ParseIP(s), egressPublic), "%s from a public configured host", s)
		assert.False(t, egressAllowed(net.ParseIP(s), egressLoopback), "%s from a loopback configured host", s)
		assert.True(t, egressAllowed(net.ParseIP(s), egressPrivate), "%s from a LAN configured host", s)
	}
	assert.False(t, egressAllowed(nil, egressPublic), "an unparsable address is refused")
}

func TestEgressDialControl(t *testing.T) {
	ctl := egressDialControl(egressPublic)
	require.NotNil(t, ctl)
	assert.Error(t, ctl("tcp", "127.0.0.1:80", nil))
	assert.Error(t, ctl("tcp", "169.254.169.254:80", nil))
	assert.Error(t, ctl("tcp6", "[fe80::1]:443", nil))
	assert.Error(t, ctl("tcp", "10.0.0.5:443", nil))
	assert.NoError(t, ctl("tcp", "93.184.216.34:443", nil))
	assert.Error(t, ctl("tcp", "not-an-address", nil), "unparsable: refuse")

	assert.NoError(t, egressDialControl(egressLoopback)("tcp", "127.0.0.1:8080", nil))
	assert.Error(t, egressDialControl(egressLoopback)("tcp", "169.254.169.254:80", nil), "link-local is refused for every class")
	assert.NoError(t, egressDialControl(egressPrivate)("tcp", "192.168.1.9:443", nil))
}

// --- redirects and the application-layer predicate ----------------------------

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func respond(r *http.Request, status int, body []byte, hdr map[string]string) *http.Response {
	resp := &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}
	for k, v := range hdr {
		resp.Header.Set(k, v)
	}
	return resp
}

type fakeResolver map[string][]string

func (f fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	ips, ok := f[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	var out []net.IPAddr
	for _, s := range ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out, nil
}

var egressResolver = fakeResolver{
	"pub.test":       {"93.184.216.34"},
	"objects.test":   {"93.184.216.35"},
	"cdn.test":       {"93.184.216.36"},
	"lan.test":       {"192.168.1.20"},
	"inward.test":    {"10.0.0.5"},
	"metadata.test":  {"169.254.169.254"},
	"mixed.test":     {"93.184.216.34", "10.0.0.5"},
	"linklocal.test": {"169.254.169.254"},
}

type redirectRig struct {
	mu    sync.Mutex
	calls []string
}

func (r *redirectRig) record(u string) {
	r.mu.Lock()
	r.calls = append(r.calls, u)
	r.mu.Unlock()
}

func (r *redirectRig) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func redirectSettings(t *testing.T, url string, rt http.RoundTripper) fetchSettings {
	return fetchTestSettings(t, url, func(s *fetchSettings) {
		s.Client = &http.Client{Transport: rt}
		s.Resolver = egressResolver
	})
}

func redirectingTo(rig *redirectRig, loc func(n int) string) http.RoundTripper {
	return rtFunc(func(r *http.Request) (*http.Response, error) {
		rig.record(r.URL.String())
		return respond(r, http.StatusFound, nil, map[string]string{"Location": loc(rig.count())}), nil
	})
}

func TestEgress_RedirectPolicy(t *testing.T) {
	const url = "https://pub.test/repos/acme/tpa-db/releases/latest"

	refused := func(t *testing.T, res fetchResult) {
		t.Helper()
		assert.Equal(t, RefreshOutcomeFetchUnavailable, res.Outcome, "detail=%s", res.Detail)
		assert.Equal(t, RefreshReasonNetworkError, res.Reason)
		assert.Nil(t, res.Candidate)
	}

	t.Run("https to http is refused", func(t *testing.T) {
		rig := &redirectRig{}
		res := fetchCandidate(fetchCtx(t), redirectSettings(t, url, redirectingTo(rig, func(int) string { return "http://pub.test/x" })))
		refused(t, res)
		assert.Equal(t, 1, rig.count(), "the downgraded hop was never sent")
	})

	t.Run("more than 5 hops is refused", func(t *testing.T) {
		rig := &redirectRig{}
		res := fetchCandidate(fetchCtx(t), redirectSettings(t, url, redirectingTo(rig, func(n int) string { return fmt.Sprintf("https://pub.test/hop%d", n) })))
		refused(t, res)
		assert.Equal(t, 6, rig.count(), "the initial request plus 5 followed hops")
	})

	for name, loc := range map[string]string{
		"a loopback literal":        "https://127.0.0.1/x",
		"an IPv6 loopback literal":  "https://[::1]/x",
		"a link-local literal":      "https://169.254.169.254/latest/meta-data/",
		"a private literal":         "https://10.0.0.5/x",
		"a name resolving inward":   "https://inward.test/x",
		"the metadata host by name": "https://metadata.test/x",
		"localhost":                 "http://localhost:8080/x",
	} {
		t.Run("a public host redirecting to "+name+" is refused", func(t *testing.T) {
			rig := &redirectRig{}
			res := fetchCandidate(fetchCtx(t), redirectSettings(t, url, redirectingTo(rig, func(int) string { return loc })))
			refused(t, res)
			assert.Equal(t, 1, rig.count())
		})
	}

	t.Run("with HTTPS_PROXY set an inward-resolving hop is still refused at the application layer", func(t *testing.T) {
		t.Setenv("HTTPS_PROXY", "http://proxy.test:3128")
		t.Setenv("https_proxy", "http://proxy.test:3128")
		rig := &redirectRig{}
		res := fetchCandidate(fetchCtx(t), redirectSettings(t, url, redirectingTo(rig, func(int) string { return "https://inward.test/x" })))
		refused(t, res)
		assert.Equal(t, 1, rig.count())
	})

	t.Run("the configured host itself resolving to link-local is refused before any request", func(t *testing.T) {
		rig := &redirectRig{}
		res := fetchCandidate(fetchCtx(t), redirectSettings(t, "https://linklocal.test/repos/a/b/releases/latest", redirectingTo(rig, func(int) string { return "" })))
		refused(t, res)
		assert.Zero(t, rig.count())
	})

	t.Run("a DNS failure is network_error", func(t *testing.T) {
		rig := &redirectRig{}
		res := fetchCandidate(fetchCtx(t), redirectSettings(t, "https://nxdomain.test/repos/a/b/releases/latest", redirectingTo(rig, func(int) string { return "" })))
		refused(t, res)
		assert.Zero(t, rig.count())
	})

	t.Run("public hops across hosts are allowed (GitHub asset downloads redirect off api.github.com)", func(t *testing.T) {
		pair := signedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "cdn"})
		rig := &redirectRig{}
		releases, _ := json.Marshal(map[string]any{"assets": []map[string]string{
			{"name": "scanner-bundle-2.json", "browser_download_url": "https://objects.test/a/scanner-bundle-2.json"},
			{"name": "scanner-bundle-2.json.sig", "browser_download_url": "https://objects.test/a/scanner-bundle-2.json.sig"},
		}})
		rt := rtFunc(func(r *http.Request) (*http.Response, error) {
			rig.record(r.URL.String())
			switch r.URL.Host + r.URL.Path {
			case "pub.test/repos/acme/tpa-db/releases/latest":
				return respond(r, 200, releases, nil), nil
			case "objects.test/a/scanner-bundle-2.json":
				return respond(r, 302, nil, map[string]string{"Location": "https://cdn.test/blob/bundle"}), nil
			case "objects.test/a/scanner-bundle-2.json.sig":
				return respond(r, 302, nil, map[string]string{"Location": "https://cdn.test/blob/sig"}), nil
			case "cdn.test/blob/bundle":
				return respond(r, 200, pair.Bundle, nil), nil
			case "cdn.test/blob/sig":
				return respond(r, 200, pair.Sidecar, nil), nil
			}
			return respond(r, 404, nil, nil), nil
		})

		res := fetchCandidate(fetchCtx(t), redirectSettings(t, url, rt))

		require.Equal(t, fetchOutcomeCandidate, res.Outcome, "reason=%s detail=%s", res.Reason, res.Detail)
		assert.Equal(t, pair.Bundle, res.Candidate.Bundle)
		assert.Equal(t, pair.Sidecar, res.Candidate.Sidecar)
		assert.Equal(t, "https://pub.test", res.Candidate.Path)
		assert.Equal(t, 5, rig.count())
	})

	t.Run("a LAN mirror may be configured and reached", func(t *testing.T) {
		pair := signedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "lan"})
		rt := rtFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, ".sig") {
				return respond(r, 200, pair.Sidecar, nil), nil
			}
			return respond(r, 200, pair.Bundle, nil), nil
		})
		res := fetchCandidate(fetchCtx(t), redirectSettings(t, "https://lan.test/bundles/scanner-bundle.json", rt))
		assert.Equal(t, fetchOutcomeCandidate, res.Outcome, "reason=%s detail=%s", res.Reason, res.Detail)
	})

	t.Run("a hop whose name also resolves inward is refused", func(t *testing.T) {
		rig := &redirectRig{}
		// mixed.test answers with a public AND a private address. Every address
		// must be permitted, so the hop is refused before it is sent.
		res := fetchCandidate(fetchCtx(t), redirectSettings(t, url, redirectingTo(rig, func(int) string { return "https://mixed.test/x" })))
		refused(t, res)
		assert.Equal(t, 1, rig.count())
	})

	t.Run("a configured host whose answer mixes public and private space is refused", func(t *testing.T) {
		rig := &redirectRig{}
		res := fetchCandidate(fetchCtx(t), redirectSettings(t, "https://mixed.test/repos/a/b/releases/latest", redirectingTo(rig, func(int) string { return "" })))
		refused(t, res)
		assert.Zero(t, rig.count())
	})

	t.Run("a loopback mirror may redirect to another loopback port but not to the LAN", func(t *testing.T) {
		rig := &redirectRig{}
		loop := rtFunc(func(r *http.Request) (*http.Response, error) {
			rig.record(r.URL.String())
			return respond(r, 302, nil, map[string]string{"Location": "http://127.0.0.1:9999/y"}), nil
		})
		fetchCandidate(fetchCtx(t), redirectSettings(t, "http://127.0.0.1:9000/bundles/scanner-bundle.json", loop))
		assert.GreaterOrEqual(t, rig.count(), 2, "loopback to loopback is allowed")

		rig2 := &redirectRig{}
		lan := rtFunc(func(r *http.Request) (*http.Response, error) {
			rig2.record(r.URL.String())
			return respond(r, 302, nil, map[string]string{"Location": "http://10.0.0.5/y"}), nil
		})
		res := fetchCandidate(fetchCtx(t), redirectSettings(t, "http://127.0.0.1:9000/bundles/scanner-bundle.json", lan))
		refused(t, res)
		assert.Equal(t, 1, rig2.count())
	})
}

// --- status mapping -----------------------------------------------------------

func TestEgress_StatusMapping(t *testing.T) {
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	run := func(t *testing.T, status int, hdr map[string]string) fetchResult {
		ch := newTestChannel(t)
		ch.serve(releasesPath, status, []byte("nope"), hdr)
		return fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+releasesPath, func(s *fetchSettings) { s.Now = func() time.Time { return now } }))
	}

	t.Run("403 and 429 are rate_limited", func(t *testing.T) {
		for _, status := range []int{403, 429} {
			res := run(t, status, nil)
			assert.Equal(t, RefreshOutcomeFetchUnavailable, res.Outcome, "status %d", status)
			assert.Equal(t, RefreshReasonRateLimited, res.Reason, "status %d", status)
			assert.Nil(t, res.Candidate)
		}
	})

	t.Run("Retry-After in seconds moves RetryAt", func(t *testing.T) {
		res := run(t, 429, map[string]string{"Retry-After": "120"})
		assert.Equal(t, RefreshReasonRateLimited, res.Reason)
		assert.Equal(t, now.Add(120*time.Second), res.RetryAt)
	})

	t.Run("Retry-After as an HTTP date moves RetryAt", func(t *testing.T) {
		res := run(t, 503, map[string]string{"Retry-After": now.Add(2 * time.Hour).UTC().Format(http.TimeFormat)})
		assert.Equal(t, RefreshReasonRateLimited, res.Reason)
		assert.Equal(t, now.Add(2*time.Hour), res.RetryAt)
	})

	t.Run("X-RateLimit-Remaining 0 with a reset time", func(t *testing.T) {
		reset := now.Add(45 * time.Minute)
		res := run(t, 403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": fmt.Sprint(reset.Unix())})
		assert.Equal(t, RefreshReasonRateLimited, res.Reason)
		assert.Equal(t, reset, res.RetryAt)
	})

	t.Run("a plain failure is http_status with no RetryAt", func(t *testing.T) {
		for _, status := range []int{404, 500, 502} {
			res := run(t, status, nil)
			assert.Equal(t, RefreshOutcomeFetchUnavailable, res.Outcome, "status %d", status)
			assert.Equal(t, RefreshReasonHTTPStatus, res.Reason, "status %d", status)
			assert.True(t, res.RetryAt.IsZero(), "status %d", status)
		}
	})

	t.Run("a client timeout is timeout", func(t *testing.T) {
		ch := newTestChannel(t)
		ch.handle(releasesPath, func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		res := fetchCandidate(ctx, fetchTestSettings(t, ch.srv.URL+releasesPath, nil))
		assert.Equal(t, RefreshOutcomeFetchUnavailable, res.Outcome)
		assert.Equal(t, RefreshReasonTimeout, res.Reason)
	})

	t.Run("a refused connection is network_error", func(t *testing.T) {
		ch := newTestChannel(t)
		url := ch.srv.URL + releasesPath
		ch.srv.Close()
		res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, url, nil))
		assert.Equal(t, RefreshOutcomeFetchUnavailable, res.Outcome)
		assert.Equal(t, RefreshReasonNetworkError, res.Reason)
	})
}

// --- through the refresher ------------------------------------------------------

func TestEgress_RateLimitMovesNextCheckAndNothingIsRecorded(t *testing.T) {
	ch := newTestChannel(t)
	f := newChannelFixture(t, ch)
	h := newRefresherHarness(t, f, func(o *BundleRefresherOptions) { o.IntervalOverride = 10 * time.Minute })
	h.start()
	commits := f.store.commitCount()
	watermark := f.store.persisted(t)

	t.Run("Retry-After beyond the tick moves next_check_at to the reset", func(t *testing.T) {
		ch.serve(releasesPath, 429, nil, map[string]string{"Retry-After": "7200"})
		assert.Equal(t, RefreshClassUnavailable, h.tick())
		info := BundleStatus()
		assert.Equal(t, RefreshReasonRateLimited, info.Refresh.LastReason)
		require.NotNil(t, info.Refresh.NextCheckAt)
		assert.WithinDuration(t, h.clock.Now().Add(2*time.Hour), *info.Refresh.NextCheckAt, 2*time.Second)
		timer := h.clock.loop(t)
		assert.InDelta(t, float64(2*time.Hour), float64(timer.last()), float64(2*time.Second))
	})

	t.Run("the move is capped at 24h", func(t *testing.T) {
		ch.serve(releasesPath, 429, nil, map[string]string{"Retry-After": "360000"})
		h.tick()
		info := BundleStatus()
		assert.WithinDuration(t, h.clock.Now().Add(24*time.Hour), *info.Refresh.NextCheckAt, 2*time.Second)
	})

	t.Run("a reset sooner than the tick leaves the tick", func(t *testing.T) {
		ch.serve(releasesPath, 429, nil, map[string]string{"Retry-After": "30"})
		h.tick()
		info := BundleStatus()
		assert.WithinDuration(t, h.clock.Now().Add(10*time.Minute), *info.Refresh.NextCheckAt, 2*time.Second)
	})

	assert.Nil(t, BundleStatus().LastRejection, "a rate limit is not a verification failure")
	assert.Equal(t, commits, f.store.commitCount(), "and never touches the watermark")
	assert.Equal(t, watermark, f.store.persisted(t))
}

func TestEgress_CycleDeadlineCutsATrickledBody(t *testing.T) {
	ch := newTestChannel(t)
	reached := make(chan struct{})
	var once sync.Once
	ch.handle(releasesPath, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("{"))
		w.(http.Flusher).Flush()
		once.Do(func() { close(reached) })
		<-r.Context().Done() // a body that never completes
	})
	f := newChannelFixture(t, ch)
	h := newRefresherHarness(t, f, nil)
	h.start()

	h.clock.loop(t).Fire()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the cycle never reached the channel")
	}
	var dl *fakeTimer
	require.Eventually(t, func() bool {
		d := h.clock.deadlines(20 * time.Second)
		if len(d) == 0 {
			return false
		}
		dl = d[len(d)-1]
		return true
	}, 10*time.Second, time.Millisecond, "the cycle must arm a 20s deadline on the injected clock")

	dl.Fire() // 20 seconds pass

	assert.Equal(t, RefreshClassUnavailable, h.waitClass())
	info := BundleStatus()
	assert.Equal(t, RefreshOutcomeFetchUnavailable, info.Refresh.LastOutcome)
	assert.Equal(t, RefreshReasonTimeout, info.Refresh.LastReason)
	assert.Nil(t, info.LastRejection)
}

func TestEgress_URLPathNeverReachesLogsOrStatus(t *testing.T) {
	const secret = "s3cr3t-path"
	ch := newTestChannel(t)
	direct := "/" + secret + "/scanner-bundle.json"
	f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
		s.TPABundlePath = ""
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorBaseline = u64p(1)
		s.TPAFetchEnabled = true
		s.TPAFetchURL = ch.srv.URL + direct
	}})
	h := newRefresherHarness(t, f, nil)
	h.start()

	// 1. a plain failure, 2. a redirect to another secret path that fails, 3. a
	// tampered pair, 4. an activation.
	ch.serve(direct, 500, []byte("boom"), nil)
	h.tick()
	ch.handle(direct, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/"+secret+"-redirect/x", http.StatusFound)
	})
	ch.serve("/"+secret+"-redirect/x", 503, nil, nil)
	h.tick()
	pair := signedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "secret"})
	ch.serve(direct, 200, flipByte(t, pair.Bundle, "bundle-marker"), nil)
	ch.serve(direct+".sig", 200, pair.Sidecar, nil)
	h.tick()
	ch.serve(direct, 200, pair.Bundle, nil)
	h.tick()
	require.Equal(t, BundleSourceFetch, BundleStatus().Source, "the pair activated")

	for _, e := range f.logs.All() {
		line := fmt.Sprint(e.Message, e.ContextMap())
		assert.NotContains(t, line, secret, "log line %q leaks the URL path", e.Message)
	}
	raw, err := json.Marshal(BundleStatus())
	require.NoError(t, err)
	assert.NotContains(t, string(raw), secret)
	info := BundleStatus()
	require.NotNil(t, info.LastRejection)
	assert.NotContains(t, info.LastRejection.Detail, secret)
	assert.Equal(t, "http://127.0.0.1", info.Path, "the status carries scheme and host only")
	require.NotNil(t, info.Refresh)
	assert.Equal(t, "127.0.0.1", info.Refresh.FetchHost)
}
