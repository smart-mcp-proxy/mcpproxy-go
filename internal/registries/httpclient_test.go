package registries

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// withFastRetries shrinks the backoff so retry tests run in milliseconds, and
// restores the production delay afterwards.
func withFastRetries(t *testing.T) {
	t.Helper()
	prev := registryRetryBaseDelay
	registryRetryBaseDelay = time.Millisecond
	t.Cleanup(func() { registryRetryBaseDelay = prev })
}

// swapRegistryClient overrides the shared registry HTTP client for a test and
// restores a working client on cleanup (never leaving a nil singleton, even if
// this test ran before any other use).
func swapRegistryClient(t *testing.T, c *http.Client) {
	t.Helper()
	registryHTTPClientOnce.Do(func() {}) // consume so sharedRegistryClient won't rebuild
	prev := registryHTTPClient
	if prev == nil {
		prev = buildRegistryClient()
	}
	registryHTTPClient = c
	t.Cleanup(func() { registryHTTPClient = prev })
}

// TestRegistryGet_RetriesTransientStatus verifies that a transient 5xx is
// retried and a subsequent 200 succeeds — the core robustness fix for the
// "Official MCP Registry returned no results" timeout.
func TestRegistryGet_RetriesTransientStatus(t *testing.T) {
	withFastRetries(t)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	reg := &RegistryEntry{ID: "official", Name: "Official", ServersURL: srv.URL, Protocol: protocolOfficial}
	body, err := registryGet(context.Background(), reg, srv.URL)
	if err != nil {
		t.Fatalf("registryGet: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %q, want the success payload", string(body))
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("server hit %d times, want 3 (2 retries)", got)
	}
}

// TestRegistryGet_ExhaustsAttempts verifies that a persistently failing registry
// is retried up to the cap and then a status error is surfaced (rather than
// retrying forever or hanging).
func TestRegistryGet_ExhaustsAttempts(t *testing.T) {
	withFastRetries(t)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	reg := &RegistryEntry{ID: "official", Name: "Official", ServersURL: srv.URL, Protocol: protocolOfficial}
	if _, err := registryGet(context.Background(), reg, srv.URL); err == nil {
		t.Fatal("expected a status error after exhausting retries, got nil")
	}
	if got := atomic.LoadInt32(&hits); got != registryMaxAttempts {
		t.Fatalf("server hit %d times, want %d", got, registryMaxAttempts)
	}
}

// TestRegistryGet_RetriesSlowBodyRead verifies the codex review fix: a response
// whose headers arrive fast but whose BODY read times out (Client.Timeout covers
// the whole request) is retried, because the body is read inside the attempt
// loop — not surfaced later as a decode error that escapes retry.
func TestRegistryGet_RetriesSlowBodyRead(t *testing.T) {
	withFastRetries(t)
	// Shrink the per-request ceiling so the test's slow body trips it quickly.
	swapRegistryClient(t, &http.Client{Timeout: 100 * time.Millisecond})

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush() // headers out immediately
		if n == 1 {
			// Stall the body past the client timeout, then bail.
			select {
			case <-time.After(500 * time.Millisecond):
			case <-r.Context().Done():
			}
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	reg := &RegistryEntry{ID: "official", Name: "Official", ServersURL: srv.URL, Protocol: protocolOfficial}
	body, err := registryGet(context.Background(), reg, srv.URL)
	if err != nil {
		t.Fatalf("registryGet should recover from a slow-body attempt: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %q, want the success payload", string(body))
	}
	if got := atomic.LoadInt32(&hits); got < 2 {
		t.Fatalf("server hit %d times, want >=2 (slow body retried)", got)
	}
}

// TestRegistryGet_ParentContextStopsRetry verifies that once the parent context
// is done we stop immediately instead of retrying — a per-request Client.Timeout
// surfaces as context.DeadlineExceeded too, so the decision must be made on the
// parent ctx, not the error value.
func TestRegistryGet_ParentContextStopsRetry(t *testing.T) {
	withFastRetries(t)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done before the first attempt

	reg := &RegistryEntry{ID: "official", Name: "Official", ServersURL: srv.URL, Protocol: protocolOfficial}
	if _, err := registryGet(ctx, reg, srv.URL); err == nil {
		t.Fatal("expected error from canceled context, got nil")
	}
	if got := atomic.LoadInt32(&hits); got > 1 {
		t.Fatalf("server hit %d times after cancel, want <=1 (no retry loop)", got)
	}
}

// TestRegistryGet_RejectsOversizedBody verifies the buffered body is capped so a
// large/hostile registry response fails fast instead of allocating unbounded.
func TestRegistryGet_RejectsOversizedBody(t *testing.T) {
	withFastRetries(t)
	prev := registryMaxBodyBytes
	registryMaxBodyBytes = 16
	t.Cleanup(func() { registryMaxBodyBytes = prev })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"servers":[{"name":"way-bigger-than-sixteen-bytes"}]}`)
	}))
	defer srv.Close()

	reg := &RegistryEntry{ID: "official", Name: "Official", ServersURL: srv.URL, Protocol: protocolOfficial}
	if _, err := registryGet(context.Background(), reg, srv.URL); err == nil {
		t.Fatal("expected an oversized-body error, got nil")
	}
}

// TestValidateRegistryURL guards the SSRF pin: only http(s) on the registry's
// configured host is allowed; a hostile scheme or cross-host cursor is rejected.
func TestValidateRegistryURL(t *testing.T) {
	reg := &RegistryEntry{ServersURL: "https://registry.example.com/v0.1/servers"}
	cases := []struct {
		name    string
		reqURL  string
		wantErr bool
	}{
		{"same host + query", "https://registry.example.com/v0.1/servers?cursor=abc&limit=100", false},
		{"plain http base", "http://registry.example.com/v0.1/servers", false},
		{"cross-host cursor redirect", "https://evil.internal/v0.1/servers?cursor=x", true},
		{"file scheme", "file:///etc/passwd", true},
		{"no host", "https:///v0.1/servers", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateRegistryURL(tc.reqURL, reg)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q, got nil", tc.reqURL)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.reqURL, err)
			}
		})
	}
}

// TestFetchOfficialServers_RetryRecovers is the end-to-end guarantee: a single
// transient page failure no longer fails the whole listing.
func TestFetchOfficialServers_RetryRecovers(t *testing.T) {
	withFastRetries(t)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"servers":[{"server":{"name":"a","packages":[{"registryType":"npm","identifier":"a","runtimeHint":"npx"}]},"_meta":{"io.modelcontextprotocol.registry/official":{"status":"active","isLatest":true}}}],"metadata":{"nextCursor":""}}`)
	}))
	defer srv.Close()

	reg := &RegistryEntry{ID: "official", Name: "Official", ServersURL: srv.URL, Protocol: protocolOfficial}
	servers, err := fetchOfficialServers(context.Background(), reg, nil, "")
	if err != nil {
		t.Fatalf("fetchOfficialServers after transient failure: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server after retry recovery, got %d", len(servers))
	}
}

// TestRegistryGet_RefusesRedirectToBlockedHost: the host pin and the SSRF
// guards were applied to the INITIAL url only, while the shared client followed
// redirects by default. A registry (or a URL a user pastes into add-source)
// could therefore 302 the fetch to the cloud-metadata endpoint or another host.
// With an HTTP(S)_PROXY set, the dial-time guard never even sees the real
// target. Every hop must be re-validated.
func TestRegistryGet_RefusesRedirectToBlockedHost(t *testing.T) {
	withFastRetries(t)
	withGuardActive(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	reg := &RegistryEntry{ID: "evil", ServersURL: srv.URL, Protocol: protocolOfficial}
	if _, err := registryGet(context.Background(), reg, srv.URL); err == nil {
		t.Fatal("expected a redirect to the metadata endpoint to be refused, got nil error")
	}
}

// A redirect to a DIFFERENT host escapes the configured-host pin, which exists
// precisely so a registry-supplied value cannot point our fetch elsewhere.
func TestRegistryGet_RefusesCrossHostRedirect(t *testing.T) {
	withFastRetries(t)

	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"servers":[]}`)
	}))
	defer elsewhere.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/v0.1/servers", http.StatusFound)
	}))
	defer srv.Close()

	reg := &RegistryEntry{ID: "acme", ServersURL: srv.URL, Protocol: protocolOfficial}
	if _, err := registryGet(context.Background(), reg, srv.URL); err == nil {
		t.Fatal("expected a cross-host redirect to be refused, got nil error")
	}
}

// A same-host redirect (the common trailing-slash / scheme normalisation) must
// still be followed, or we would break real registries.
func TestRegistryGet_FollowsSameHostRedirect(t *testing.T) {
	withFastRetries(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0.1/servers" {
			http.Redirect(w, r, "/v0.1/servers/", http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"servers":[],"metadata":{}}`)
	}))
	defer srv.Close()

	reg := &RegistryEntry{ID: "acme", ServersURL: srv.URL, Protocol: protocolOfficial}
	body, err := registryGet(context.Background(), reg, srv.URL+"/v0.1/servers")
	if err != nil {
		t.Fatalf("a same-host redirect must still be followed: %v", err)
	}
	if len(body) == 0 {
		t.Error("expected the redirected body")
	}
}
