package registries

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The registries test binary points fetches at httptest servers bound to
// loopback (127.0.0.1), so the SSRF guard is relaxed for the whole binary.
// Tests that exercise the BLOCKING behavior flip it back off locally.
func init() {
	testForceAllowPrivate.Store(true)
	registryAllowPrivateFetch.Store(true)
}

// withGuardActive turns the SSRF guard ON (the production default) for one test,
// undoing the binary-wide test bypass, and restores it on cleanup.
func withGuardActive(t *testing.T) {
	t.Helper()
	prevForce, prev := testForceAllowPrivate.Load(), registryAllowPrivateFetch.Load()
	testForceAllowPrivate.Store(false)
	registryAllowPrivateFetch.Store(false)
	t.Cleanup(func() {
		testForceAllowPrivate.Store(prevForce)
		registryAllowPrivateFetch.Store(prev)
	})
}

// TestIsBlockedIP is the single source of truth for which ranges a registry
// fetch must never reach (SSRF / CWE-918).
func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1",       // loopback
		"127.255.255.254", // loopback /8
		"::1",             // loopback v6
		"10.0.0.1",        // RFC1918
		"172.16.0.1",      // RFC1918
		"172.31.255.255",  // RFC1918
		"192.168.1.1",     // RFC1918
		"169.254.169.254", // link-local / cloud metadata endpoint
		"169.254.0.1",     // link-local
		"fe80::1",         // link-local v6
		"100.64.0.1",      // CGNAT RFC6598
		"100.127.255.255", // CGNAT RFC6598
		"fc00::1",         // unique-local v6 (IsPrivate)
		"fd00::1",         // unique-local v6
		"0.0.0.0",         // unspecified
		"::",              // unspecified v6
		"224.0.0.1",       // multicast
		"ff02::1",         // link-local multicast v6
	}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("test bug: %q is not a valid IP", s)
		}
		if !isBlockedIP(ip) {
			t.Errorf("isBlockedIP(%s) = false, want true (must be blocked)", s)
		}
	}

	allowed := []string{
		"8.8.8.8",              // public
		"1.1.1.1",              // public
		"93.184.216.34",        // example.com
		"172.15.0.1",           // just below RFC1918 172.16/12
		"172.32.0.1",           // just above RFC1918 172.16/12
		"100.63.255.255",       // just below CGNAT 100.64/10
		"100.128.0.1",          // just above CGNAT 100.64/10
		"2606:4700:4700::1111", // public v6 (Cloudflare)
	}
	for _, s := range allowed {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("test bug: %q is not a valid IP", s)
		}
		if isBlockedIP(ip) {
			t.Errorf("isBlockedIP(%s) = true, want false (public host must be allowed)", s)
		}
	}

	// A nil/unparseable IP must fail closed.
	if !isBlockedIP(nil) {
		t.Errorf("isBlockedIP(nil) = false, want true (fail closed)")
	}
}

// TestHostLiteralBlocked: literal IPs in blocked ranges are rejected; hostnames
// pass (they are resolved authoritatively at dial time); the allowLoopback
// escape hatch short-circuits to nil.
func TestHostLiteralBlocked(t *testing.T) {
	cases := []struct {
		host        string
		allowLoop   bool
		wantBlocked bool
	}{
		{"169.254.169.254", false, true},
		{"169.254.169.254:80", false, true},
		{"127.0.0.1", false, true},
		{"127.0.0.1:8080", false, true},
		{"[::1]", false, true},
		{"[::1]:443", false, true},
		{"10.1.2.3", false, true},
		{"registry.example.com", false, false},     // hostname: resolved at dial
		{"registry.example.com:443", false, false}, // hostname:port
		{"8.8.8.8", false, false},                  // public literal
		{"127.0.0.1", true, false},                 // bypass on
	}
	for _, tc := range cases {
		err := hostLiteralBlocked(tc.host, tc.allowLoop)
		if tc.wantBlocked && err == nil {
			t.Errorf("hostLiteralBlocked(%q, %v) = nil, want blocked", tc.host, tc.allowLoop)
		}
		if !tc.wantBlocked && err != nil {
			t.Errorf("hostLiteralBlocked(%q, %v) = %v, want nil", tc.host, tc.allowLoop, err)
		}
		if tc.wantBlocked && err != nil && !errors.Is(err, ErrBlockedRegistryHost) {
			t.Errorf("hostLiteralBlocked(%q) error = %v, want wraps ErrBlockedRegistryHost", tc.host, err)
		}
	}
}

// TestValidateRegistrySourceURL is the add-source/edit-source fail-fast: a
// literal-IP registry source pointed at an internal range is rejected up front,
// while a normal https hostname source is accepted.
func TestValidateRegistrySourceURL(t *testing.T) {
	// ValidateRegistrySourceURL honors the allow-policy, which the test binary
	// pins open; disable it here to exercise the production (blocking) behavior.
	withGuardActive(t)

	if err := ValidateRegistrySourceURL("https://169.254.169.254/v0.1/servers"); !errors.Is(err, ErrBlockedRegistryHost) {
		t.Errorf("metadata-IP source = %v, want ErrBlockedRegistryHost", err)
	}
	if err := ValidateRegistrySourceURL("https://192.168.0.10/v0.1/servers"); !errors.Is(err, ErrBlockedRegistryHost) {
		t.Errorf("private-IP source = %v, want ErrBlockedRegistryHost", err)
	}
	if err := ValidateRegistrySourceURL("https://registry.example.com/v0.1/servers"); err != nil {
		t.Errorf("public hostname source = %v, want nil", err)
	}
}

// TestRegistryGet_BlocksLoopbackWhenGuardActive is the end-to-end guarantee: with
// the guard active (production default), a fetch whose host resolves to a blocked
// range is refused at dial time and never reaches the endpoint.
func TestRegistryGet_BlocksLoopbackWhenGuardActive(t *testing.T) {
	// Exercise the production guard (the test binary otherwise pins it open).
	withGuardActive(t)

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := &RegistryEntry{ID: "evil", Name: "Evil", ServersURL: srv.URL, Protocol: protocolOfficial}
	_, err := registryGet(context.Background(), reg, srv.URL)
	if err == nil {
		t.Fatalf("registryGet to loopback succeeded, want SSRF block")
	}
	if hits != 0 {
		t.Errorf("guarded fetch reached the endpoint %d time(s), want 0", hits)
	}
}

// TestRegistryGet_BlocksPrivateResolvingHostThroughProxy is the proxy-bypass
// regression (CodexReviewer round 2): with an HTTP(S)_PROXY configured the
// transport dials the proxy, so the dial-time Control only validates the
// proxy IP. A public-looking hostname that RESOLVES into a blocked range must
// still be refused — by the application-layer guard that runs before the
// request, independent of the transport/proxy.
func TestRegistryGet_BlocksPrivateResolvingHostThroughProxy(t *testing.T) {
	withGuardActive(t)
	// A proxy is set, so absent the app-layer guard the request would be sent to
	// the proxy (TEST-NET-3, RFC5737) rather than blocked.
	t.Setenv("HTTP_PROXY", "http://203.0.113.9:8080")
	t.Setenv("HTTPS_PROXY", "http://203.0.113.9:8080")

	// Simulate a benign-looking hostname resolving to the cloud metadata IP.
	orig := registryResolveHost
	registryResolveHost = func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("169.254.169.254")}, nil
	}
	t.Cleanup(func() { registryResolveHost = orig })

	reg := &RegistryEntry{ID: "evil", Name: "Evil", ServersURL: "https://registry.evil.example/v0.1/servers", Protocol: protocolOfficial}
	_, err := registryGet(context.Background(), reg, "https://registry.evil.example/v0.1/servers")
	if !errors.Is(err, ErrBlockedRegistryHost) {
		t.Fatalf("private-resolving host through proxy = %v, want ErrBlockedRegistryHost", err)
	}
}

// TestGuardRegistryTargetHost covers the resolution predicate directly: a host
// resolving to ANY blocked IP is rejected; an all-public resolution passes; the
// opt-in bypass and a literal public IP are allowed.
func TestGuardRegistryTargetHost(t *testing.T) {
	withGuardActive(t)
	orig := registryResolveHost
	t.Cleanup(func() { registryResolveHost = orig })

	// Mixed public + private resolution → blocked (any blocked IP rejects).
	registryResolveHost = func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("10.0.0.5")}, nil
	}
	if err := guardRegistryTargetHost(context.Background(), "https://reg.example/x"); !errors.Is(err, ErrBlockedRegistryHost) {
		t.Errorf("mixed public+private resolution = %v, want blocked", err)
	}

	// All-public resolution → allowed.
	registryResolveHost = func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	if err := guardRegistryTargetHost(context.Background(), "https://reg.example/x"); err != nil {
		t.Errorf("all-public resolution = %v, want nil", err)
	}

	// Resolver error → fail-open (dial guard still covers the no-proxy path).
	registryResolveHost = func(_ context.Context, _ string) ([]net.IP, error) {
		return nil, errors.New("nxdomain")
	}
	if err := guardRegistryTargetHost(context.Background(), "https://reg.example/x"); err != nil {
		t.Errorf("resolver error = %v, want nil (fail-open)", err)
	}

	// Literal public IP → allowed without resolution.
	if err := guardRegistryTargetHost(context.Background(), "https://8.8.8.8/x"); err != nil {
		t.Errorf("public literal IP = %v, want nil", err)
	}
}
