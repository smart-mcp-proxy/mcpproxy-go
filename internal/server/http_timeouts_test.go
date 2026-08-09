package server

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func durPtrFor(t *testing.T, s string) *config.Duration {
	t.Helper()
	d, err := time.ParseDuration(s)
	if err != nil {
		t.Fatalf("bad duration %q: %v", s, err)
	}
	v := config.Duration(d)
	return &v
}

// TestHTTPServerTimeouts covers the pure resolver seam feeding http.Server's
// Read/Write/IdleTimeout (GH #965). Extracted from startCustomHTTPServer so the
// policy is testable without binding a socket (same pattern as the
// registerHTTPHandlers extraction).
func TestHTTPServerTimeouts(t *testing.T) {
	cases := []struct {
		name              string
		cfg               *config.Config
		read, write, idle time.Duration
	}{
		{
			name:  "nil config → built-in defaults",
			cfg:   nil,
			read:  120 * time.Second,
			write: 0,
			idle:  180 * time.Second,
		},
		{
			// The GH #965 fix: out of the box there is NO write deadline, so a
			// slow tool call or a long-lived SSE /events stream is never cut off.
			name:  "unset keys → defaults, write disabled",
			cfg:   config.DefaultConfig(),
			read:  120 * time.Second,
			write: 0,
			idle:  180 * time.Second,
		},
		{
			name: "explicit values are honoured",
			cfg: &config.Config{
				HTTPReadTimeout:  durPtrFor(t, "5m"),
				HTTPWriteTimeout: durPtrFor(t, "10m"),
				HTTPIdleTimeout:  durPtrFor(t, "1h"),
			},
			read:  5 * time.Minute,
			write: 10 * time.Minute,
			idle:  time.Hour,
		},
		{
			name: "explicit zeros disable every deadline",
			cfg: &config.Config{
				HTTPReadTimeout:  durPtrFor(t, "0s"),
				HTTPWriteTimeout: durPtrFor(t, "0s"),
				HTTPIdleTimeout:  durPtrFor(t, "0s"),
			},
			read:  0,
			write: 0,
			idle:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			read, write, idle := httpServerTimeouts(tc.cfg)
			if read != tc.read || write != tc.write || idle != tc.idle {
				t.Errorf("httpServerTimeouts = (%v, %v, %v), want (%v, %v, %v)",
					read, write, idle, tc.read, tc.write, tc.idle)
			}
		})
	}
}

// TestHTTPWriteTimeoutRealBehavior is the behavioural half of the GH #965 fix:
// it stands up a real net/http server configured through httpServerTimeouts and
// proves that (a) a positive write deadline really does destroy a response that
// takes longer than it to produce, and (b) the new default of 0 lets that same
// slow response through intact. Without (b), every tool call slower than the old
// hardcoded 120s returned a truncated/failed HTTP response to the agent.
func TestHTTPWriteTimeoutRealBehavior(t *testing.T) {
	const handlerDelay = 600 * time.Millisecond
	const body = "slow-but-complete"

	run := func(t *testing.T, cfg *config.Config) (string, error) {
		t.Helper()

		_, write, idle := httpServerTimeouts(cfg)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}

		mux := http.NewServeMux()
		mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(handlerDelay)
			fmt.Fprint(w, body)
		})
		srv := &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 60 * time.Second,
			WriteTimeout:      write,
			IdleTimeout:       idle,
		}
		go func() { _ = srv.Serve(ln) }()
		t.Cleanup(func() { _ = srv.Close() })

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get("http://" + ln.Addr().String() + "/slow") //nolint:noctx // short-lived test client
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		return string(data), err
	}

	t.Run("300ms write deadline truncates a 600ms response", func(t *testing.T) {
		got, err := run(t, &config.Config{HTTPWriteTimeout: durPtrFor(t, "300ms")})
		if err == nil && got == body {
			t.Fatalf("expected the write deadline to destroy the response, got %q", got)
		}
	})

	t.Run("write timeout disabled (0s) delivers the full response", func(t *testing.T) {
		got, err := run(t, &config.Config{HTTPWriteTimeout: durPtrFor(t, "0s")})
		if err != nil {
			t.Fatalf("request failed with the write deadline disabled: %v", err)
		}
		if got != body {
			t.Fatalf("body = %q, want %q", got, body)
		}
	})

	t.Run("default config delivers the full response", func(t *testing.T) {
		got, err := run(t, config.DefaultConfig())
		if err != nil {
			t.Fatalf("request failed under the default config: %v", err)
		}
		if got != body {
			t.Fatalf("body = %q, want %q", got, body)
		}
	})
}
