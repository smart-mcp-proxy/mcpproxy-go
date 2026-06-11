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
