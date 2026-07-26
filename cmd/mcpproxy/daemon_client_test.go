package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// clearDaemonEnv neutralizes environment variables that influence
// daemonEndpoint so tests are hermetic. Empty values are treated as unset.
func clearDaemonEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MCPPROXY_TRAY_ENDPOINT", "")
	t.Setenv("MCPPROXY_API_KEY", "")
}

// newStatusServer returns an httptest server that serves GET /api/v1/status
// only when the request carries the expected X-API-Key.
func newStatusServer(t *testing.T, key string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"running":true}}`))
	}))
}

func TestDaemonEndpoint_TCPFallbackWhenSocketMissing(t *testing.T) {
	clearDaemonEnv(t)

	ts := newStatusServer(t, "secret")
	defer ts.Close()

	listen := strings.TrimPrefix(ts.URL, "http://")
	cfg := &config.Config{
		DataDir: t.TempDir(), // no socket file here
		Listen:  listen,
		APIKey:  "secret",
	}

	endpoint, apiKey, ok := daemonEndpoint(cfg)
	if !ok {
		t.Fatal("expected TCP fallback to find the daemon when socket is missing")
	}
	if endpoint != "http://"+listen {
		t.Fatalf("expected endpoint http://%s, got %s", listen, endpoint)
	}
	if apiKey != "secret" {
		t.Fatalf("expected apiKey 'secret', got %q", apiKey)
	}
}

func TestDaemonEndpoint_SocketPreferred(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket test not applicable on Windows")
	}
	clearDaemonEnv(t)

	// If the socket path exists, no HTTP probe may happen.
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("no HTTP probe expected when socket is available")
	}))
	defer ts.Close()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "mcpproxy.sock")
	if err := os.WriteFile(sockPath, nil, 0o600); err != nil {
		t.Fatalf("failed to create fake socket file: %v", err)
	}

	cfg := &config.Config{
		DataDir: dir,
		Listen:  strings.TrimPrefix(ts.URL, "http://"),
		APIKey:  "secret",
	}

	endpoint, apiKey, ok := daemonEndpoint(cfg)
	if !ok {
		t.Fatal("expected socket endpoint to be detected")
	}
	if endpoint != "unix://"+sockPath {
		t.Fatalf("expected unix://%s, got %s", sockPath, endpoint)
	}
	if apiKey != "" {
		t.Fatalf("socket connections bypass API-key auth; expected empty key, got %q", apiKey)
	}
}

func TestDaemonEndpoint_NoAPIKeyNoProbe(t *testing.T) {
	clearDaemonEnv(t)

	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("no HTTP probe expected without an API key")
	}))
	defer ts.Close()

	cfg := &config.Config{
		DataDir: t.TempDir(),
		Listen:  strings.TrimPrefix(ts.URL, "http://"),
		APIKey:  "",
	}

	if _, _, ok := daemonEndpoint(cfg); ok {
		t.Fatal("expected ok=false when no API key is available (REST over TCP always requires a key)")
	}
}

func TestDaemonEndpoint_WrongKeyOrDaemonDown(t *testing.T) {
	clearDaemonEnv(t)

	// Wrong key: server only accepts "right-key".
	ts := newStatusServer(t, "right-key")
	cfg := &config.Config{
		DataDir: t.TempDir(),
		Listen:  strings.TrimPrefix(ts.URL, "http://"),
		APIKey:  "wrong-key",
	}
	if _, _, ok := daemonEndpoint(cfg); ok {
		t.Fatal("expected ok=false when the API key is rejected by the daemon")
	}

	// Daemon down: closed port.
	addr := ts.Listener.Addr().String()
	ts.Close()
	cfg.Listen = addr
	cfg.APIKey = "right-key"
	if _, _, ok := daemonEndpoint(cfg); ok {
		t.Fatal("expected ok=false when nothing listens on the port")
	}
}

func TestDaemonEndpoint_EnvTrayEndpointHTTP(t *testing.T) {
	clearDaemonEnv(t)

	ts := newStatusServer(t, "secret")
	defer ts.Close()

	t.Setenv("MCPPROXY_TRAY_ENDPOINT", ts.URL)

	cfg := &config.Config{
		DataDir: t.TempDir(),
		Listen:  "127.0.0.1:1", // must not be used
		APIKey:  "secret",
	}

	endpoint, apiKey, ok := daemonEndpoint(cfg)
	if !ok {
		t.Fatal("expected explicit http MCPPROXY_TRAY_ENDPOINT to be used")
	}
	if endpoint != ts.URL {
		t.Fatalf("expected endpoint %s, got %s", ts.URL, endpoint)
	}
	if apiKey != "secret" {
		t.Fatalf("expected apiKey 'secret', got %q", apiKey)
	}
}

func TestTCPFallbackEndpoint(t *testing.T) {
	cases := []struct {
		listen string
		want   string
		ok     bool
	}{
		{":8080", "http://127.0.0.1:8080", true},
		{"0.0.0.0:8080", "http://127.0.0.1:8080", true},
		{"[::]:8080", "http://127.0.0.1:8080", true},
		{"192.168.1.5:8080", "http://192.168.1.5:8080", true},
		{"127.0.0.1:18091", "http://127.0.0.1:18091", true},
		{"garbage", "", false},
		{"", "", false},
	}

	for _, tc := range cases {
		got, ok := tcpFallbackEndpoint(tc.listen)
		if ok != tc.ok {
			t.Errorf("tcpFallbackEndpoint(%q) ok = %v, want %v", tc.listen, ok, tc.ok)
			continue
		}
		if got != tc.want {
			t.Errorf("tcpFallbackEndpoint(%q) = %q, want %q", tc.listen, got, tc.want)
		}
	}
}
