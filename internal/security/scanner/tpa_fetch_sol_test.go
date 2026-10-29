package scanner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sol round-1 regressions for the fetch adapter.

// A proxy address remembered from one request must not exempt a later request
// that is dialed directly to the same host:port (B11): the exemption belongs to
// the hop whose next hop is the proxy.
func TestProductionTransport_ProxyExemptionDoesNotOutliveItsHop(t *testing.T) {
	proxyAddr := listenerAddr(t)
	proxyURL, err := url.Parse("http://" + proxyAddr)
	require.NoError(t, err)
	tr := productionTransport(t, func(req *http.Request) (*url.URL, error) {
		if req.URL.Hostname() == "mirror.example" {
			return proxyURL, nil // proxied
		}
		return nil, nil // NO_PROXY: dialed directly, and here to the proxy's own address
	})

	ctx := fetchHopContext(context.Background())
	proxied, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://mirror.example/x", nil)
	pu, err := tr.Proxy(proxied)
	require.NoError(t, err)
	require.NotNil(t, pu)
	assert.True(t, dialOKCtx(t, ctx, tr, proxyAddr), "the proxy selected for this hop is exempt")

	// The next hop is a redirect to a NO_PROXY host that is the very address the
	// proxy listens on: a direct dial, so the egress control applies to it.
	direct, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+proxyAddr+"/y", nil)
	pu, err = tr.Proxy(direct)
	require.NoError(t, err)
	require.Nil(t, pu)
	assert.False(t, dialOKCtx(t, ctx, tr, proxyAddr), "a direct dial to an address that once was the proxy is judged by the egress rules")
}

// A dial with no hop on its context is a direct dial: fail closed.
func TestProductionTransport_DialWithoutAHopIsGuarded(t *testing.T) {
	proxyAddr := listenerAddr(t)
	proxyURL, err := url.Parse("http://" + proxyAddr)
	require.NoError(t, err)
	tr := productionTransport(t, func(*http.Request) (*url.URL, error) { return proxyURL, nil })
	req, _ := http.NewRequest(http.MethodGet, "https://mirror.example/x", nil)
	_, err = tr.Proxy(req)
	require.NoError(t, err)
	assert.False(t, dialOKCtx(t, context.Background(), tr, proxyAddr))
}

// End to end: a request that goes through the operator's (loopback) proxy
// works, i.e. the hop reaches the transport's dial through the request context.
func TestProductionTransport_ProxiedRequestReachesPrivateProxy(t *testing.T) {
	hits := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte("via-proxy"))
	}))
	t.Cleanup(proxy.Close)
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	tr := productionTransport(t, func(*http.Request) (*url.URL, error) { return proxyURL, nil })

	ctx := fetchHopContext(fetchCtx(t))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://upstream.example/x", nil)
	resp, err := (&http.Client{Transport: tr}).Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 1, hits)
}

// A malformed redirect Location is rejected by net/http with an error whose
// text embeds the whole Location. Nothing of it may reach the detail (B12).
func TestEgress_MalformedRedirectLocationNeverReachesDetail(t *testing.T) {
	const leak = "private-path-s3cr3t"
	ch := newTestChannel(t)
	ch.handle(releasesPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://cdn.example/"+leak+"/%zz?token="+leak)
		w.WriteHeader(http.StatusFound)
	})
	res := fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+releasesPath, nil))
	assert.Equal(t, RefreshOutcomeFetchUnavailable, res.Outcome)
	assert.Equal(t, RefreshReasonNetworkError, res.Reason)
	assert.NotContains(t, res.Detail, leak)
	assert.True(t, strings.HasPrefix(res.Detail, "127.0.0.1: "), "detail is host plus a classification: %q", res.Detail)
}

// Transport and parser error text is never passed through: the detail is the
// host and a closed classification.
func TestEgress_FailureDetailIsAClassification(t *testing.T) {
	a := &fetchAttempt{host: "mirror.example", s: fetchSettings{URL: "https://mirror.example/p"}}
	leaky := &url.Error{Op: "Get", URL: "https://mirror.example/p?token=abc", Err: fmt.Errorf("weird failure at /p?token=abc")}
	res := a.failure(context.Background(), leaky)
	assert.NotContains(t, res.Detail, "token")
	assert.NotContains(t, res.Detail, "weird")
	assert.True(t, strings.HasPrefix(res.Detail, "mirror.example: "))
}

// Retry-After seconds that overflow time.Duration when multiplied are clamped
// to the refresher's cap rather than wrapping negative and being discarded.
func TestEgress_HugeRetryAfterIsClamped(t *testing.T) {
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	run := func(hdr map[string]string) fetchResult {
		ch := newTestChannel(t)
		ch.serve(releasesPath, 429, []byte("nope"), hdr)
		return fetchCandidate(fetchCtx(t), fetchTestSettings(t, ch.srv.URL+releasesPath, func(s *fetchSettings) { s.Now = func() time.Time { return now } }))
	}
	for _, v := range []string{"9223372037", "9223372036854775807", "100000000000"} {
		res := run(map[string]string{"Retry-After": v})
		assert.Equal(t, RefreshReasonRateLimited, res.Reason, v)
		assert.Equal(t, now.Add(refreshRetryCap), res.RetryAt, "Retry-After %s", v)
	}
	res := run(map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "9223372036854775807"})
	assert.Equal(t, now.Add(refreshRetryCap), res.RetryAt, "a huge reset epoch is clamped too")
}
