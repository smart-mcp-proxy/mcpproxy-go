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
	resp, err := registryGet(context.Background(), reg, srv.URL)
	if err != nil {
		t.Fatalf("registryGet: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("server hit %d times, want 3 (2 retries)", got)
	}
}

// TestRegistryGet_ExhaustsAttempts verifies that a persistently failing registry
// is retried up to the cap and then the final response is returned so the caller
// can surface a meaningful status error (rather than retrying forever).
func TestRegistryGet_ExhaustsAttempts(t *testing.T) {
	withFastRetries(t)

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	reg := &RegistryEntry{ID: "official", Name: "Official", ServersURL: srv.URL, Protocol: protocolOfficial}
	resp, err := registryGet(context.Background(), reg, srv.URL)
	if err != nil {
		t.Fatalf("registryGet returned err, want last response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&hits); got != registryMaxAttempts {
		t.Fatalf("server hit %d times, want %d", got, registryMaxAttempts)
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
