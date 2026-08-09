package server

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

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
			write: 120 * time.Second,
			idle:  180 * time.Second,
		},
		{
			// The GH #965 fix keeps the 120s write deadline for ordinary
			// endpoints — the streaming routes escape it per-request through
			// streamingNoDeadline instead of the process disabling it globally.
			name:  "unset keys → built-in defaults",
			cfg:   config.DefaultConfig(),
			read:  120 * time.Second,
			write: 120 * time.Second,
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
// takes longer than it to produce, (b) wrapping the same route in
// streamingNoDeadline rescues it, and (c) an explicit "0s" still disables the
// deadline process-wide. (b) is the shape the MCP routes and /events ship with:
// the deadline stays on for REST/UI/health, the streaming routes opt out.
func TestHTTPWriteTimeoutRealBehavior(t *testing.T) {
	const handlerDelay = 600 * time.Millisecond
	const body = "slow-but-complete"

	run := func(t *testing.T, cfg *config.Config, wrap bool) (string, error) {
		t.Helper()

		_, write, idle := httpServerTimeouts(cfg)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}

		var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(handlerDelay)
			fmt.Fprint(w, body)
		})
		if wrap {
			handler = (&Server{logger: zap.NewNop()}).streamingNoDeadline(handler)
		}
		mux := http.NewServeMux()
		mux.Handle("/slow", handler)
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
		got, err := run(t, &config.Config{HTTPWriteTimeout: durPtrFor(t, "300ms")}, false)
		if err == nil && got == body {
			t.Fatalf("expected the write deadline to destroy the response, got %q", got)
		}
	})

	t.Run("streamingNoDeadline rescues the same slow response", func(t *testing.T) {
		got, err := run(t, &config.Config{HTTPWriteTimeout: durPtrFor(t, "300ms")}, true)
		if err != nil {
			t.Fatalf("request failed through streamingNoDeadline: %v", err)
		}
		if got != body {
			t.Fatalf("body = %q, want %q", got, body)
		}
	})

	t.Run("write timeout disabled (0s) delivers the full response", func(t *testing.T) {
		got, err := run(t, &config.Config{HTTPWriteTimeout: durPtrFor(t, "0s")}, false)
		if err != nil {
			t.Fatalf("request failed with the write deadline disabled: %v", err)
		}
		if got != body {
			t.Fatalf("body = %q, want %q", got, body)
		}
	})
}

// TestStreamingNoDeadlineKeepsGETStreamAlive is the SSE half of the GH #965 fix.
// A long-lived GET stream is killed by BOTH http.Server deadlines: WriteTimeout
// caps the whole response, and ReadTimeout (armed when the request is read) fires
// mid-stream too. streamingNoDeadline clears both for GET/HEAD — those carry no
// request body, so nothing is left unbounded that a slow-body upload would be.
func TestStreamingNoDeadlineKeepsGETStreamAlive(t *testing.T) {
	const (
		chunks       = 5
		chunkGap     = 150 * time.Millisecond
		serverDeadln = 300 * time.Millisecond // < chunks*chunkGap on purpose
	)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	stream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i := 0; i < chunks; i++ {
			time.Sleep(chunkGap)
			fmt.Fprintf(w, "data: %d\n\n", i)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	})

	mux := http.NewServeMux()
	mux.Handle("/events", (&Server{logger: zap.NewNop()}).streamingNoDeadline(stream))
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 60 * time.Second,
		ReadTimeout:       serverDeadln,
		WriteTimeout:      serverDeadln,
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("http://" + ln.Addr().String() + "/events") //nolint:noctx // short-lived test client
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the stream failed (deadline not cleared?): %v (read %q)", err, data)
	}
	for i := 0; i < chunks; i++ {
		want := fmt.Sprintf("data: %d\n\n", i)
		if !strings.Contains(string(data), want) {
			t.Fatalf("stream missing chunk %d: got %q", i, data)
		}
	}
}
