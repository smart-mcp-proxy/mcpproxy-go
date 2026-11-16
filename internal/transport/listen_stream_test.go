package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// GH #1910: the standalone GET stream mcpproxy opens to a streamable-HTTP
// upstream that advertises tools.listChanged.

// fastListenOptions keeps the backoff short so the retry tests run in
// well under a second per case.
func fastListenOptions(onNote func(mcp.JSONRPCNotification)) ListenStreamOptions {
	return ListenStreamOptions{
		ProtocolVersion: mcp.LATEST_LEGACY_PROTOCOL_VERSION,
		OnNotification:  onNote,
		Logger:          zap.NewNop(),
		MinBackoff:      50 * time.Millisecond,
		MaxBackoff:      200 * time.Millisecond,
		HealthyAfter:    time.Second,
	}
}

func sessionTransport(t *testing.T, url, sid string) *mcptransport.StreamableHTTP {
	t.Helper()
	sh, err := mcptransport.NewStreamableHTTP(url, mcptransport.WithSession(sid))
	require.NoError(t, err)
	return sh
}

func writeSSE(w http.ResponseWriter, msg any) {
	b, _ := json.Marshal(msg)
	_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func openSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func TestListenStream_DeliversNotificationWithSessionHeaders(t *testing.T) {
	var seen atomic.Value // http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		seen.Store(r.Header.Clone())
		openSSE(w)
		writeSSE(w, map[string]any{"jsonrpc": "2.0", "method": string(mcp.MethodNotificationToolsListChanged)})
		<-r.Context().Done()
	}))
	defer srv.Close()

	cfg := &HTTPTransportConfig{URL: srv.URL, Headers: map[string]string{"X-Static": "v"}}
	got := make(chan string, 4)
	ls := cfg.NewListenStream(sessionTransport(t, srv.URL, "sess-1"), fastListenOptions(func(n mcp.JSONRPCNotification) {
		got <- n.Method
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan ListenEnd, 1)
	go func() { done <- ls.Run(ctx) }()

	select {
	case m := <-got:
		assert.Equal(t, string(mcp.MethodNotificationToolsListChanged), m)
	case <-time.After(3 * time.Second):
		t.Fatal("notification on the GET stream was not delivered")
	}
	h, _ := seen.Load().(http.Header)
	require.NotNil(t, h)
	assert.Equal(t, "sess-1", h.Get(mcptransport.HeaderKeySessionID))
	assert.Equal(t, mcp.LATEST_LEGACY_PROTOCOL_VERSION, h.Get(mcptransport.HeaderKeyProtocolVersion))
	assert.Equal(t, "text/event-stream", h.Get("Accept"))
	assert.Equal(t, "v", h.Get("X-Static"))

	cancel()
	select {
	case end := <-done:
		assert.Equal(t, ListenStopped, end.Reason)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

// A refusal ends the stream after ONE request: no retry loop (mcp-go's own
// listener retries every second forever on anything but 405/404).
func TestListenStream_RefusalStopsAfterOneRequest(t *testing.T) {
	cases := []struct {
		name   string
		status int
		ctype  string
		want   ListenEndReason
	}{
		{"405", http.StatusMethodNotAllowed, "", ListenUnsupported},
		{"400", http.StatusBadRequest, "", ListenUnsupported},
		{"406", http.StatusNotAcceptable, "", ListenUnsupported},
		{"401", http.StatusUnauthorized, "", ListenUnsupported},
		{"403", http.StatusForbidden, "", ListenUnsupported},
		{"410", http.StatusGone, "", ListenUnsupported},
		{"200-json", http.StatusOK, "application/json", ListenUnsupported},
		{"404", http.StatusNotFound, "", ListenSessionTerminated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gets atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					gets.Add(1)
				}
				if tc.ctype != "" {
					w.Header().Set("Content-Type", tc.ctype)
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			cfg := &HTTPTransportConfig{URL: srv.URL}
			ls := cfg.NewListenStream(sessionTransport(t, srv.URL, "s"), fastListenOptions(nil))
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			end := ls.Run(ctx)
			assert.Equal(t, tc.want, end.Reason)
			if tc.status != http.StatusOK {
				assert.Equal(t, tc.status, end.Status)
			}
			// Several backoff periods later, still exactly one GET.
			time.Sleep(500 * time.Millisecond)
			assert.Equal(t, int32(1), gets.Load())
		})
	}
}

// Transient failures (5xx, 429, a stream that ends at once) back off
// exponentially to the cap instead of hot-looping.
func TestListenStream_TransientFailuresBackOff(t *testing.T) {
	cases := []struct {
		name    string
		handler func(w http.ResponseWriter)
	}{
		{"503", func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) }},
		{"429", func(w http.ResponseWriter) { w.WriteHeader(http.StatusTooManyRequests) }},
		{"stream-closes-at-once", func(w http.ResponseWriter) { openSSE(w) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var at []time.Time
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				at = append(at, time.Now())
				mu.Unlock()
				tc.handler(w)
			}))
			defer srv.Close()

			cfg := &HTTPTransportConfig{URL: srv.URL}
			ls := cfg.NewListenStream(sessionTransport(t, srv.URL, "s"), fastListenOptions(nil))
			ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
			defer cancel()
			end := ls.Run(ctx)
			assert.Equal(t, ListenStopped, end.Reason)

			mu.Lock()
			defer mu.Unlock()
			// 50ms, 100ms, then 200ms capped: about 7 attempts in 1.2s. A hot
			// loop would make hundreds.
			assert.GreaterOrEqual(t, len(at), 3, "transient failures must be retried")
			assert.LessOrEqual(t, len(at), 10, "retries must back off, got %d attempts", len(at))
			for i := 1; i < len(at); i++ {
				// Jitter never takes a delay below 75% of the minimum.
				assert.GreaterOrEqual(t, at[i].Sub(at[i-1]), 35*time.Millisecond, "attempt %d came too soon", i)
			}
		})
	}
}

// A server-to-client request on the stream (ping) is answered with a POST
// carrying the same id and session.
func TestListenStream_AnswersPingOnStream(t *testing.T) {
	answered := make(chan map[string]any, 1)
	var answerSession atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			var m map[string]any
			_ = json.Unmarshal(body, &m)
			answerSession.Store(r.Header.Get(mcptransport.HeaderKeySessionID))
			answered <- m
			w.WriteHeader(http.StatusAccepted)
			return
		}
		openSSE(w)
		writeSSE(w, map[string]any{"jsonrpc": "2.0", "id": 7, "method": "ping"})
		<-r.Context().Done()
	}))
	defer srv.Close()

	cfg := &HTTPTransportConfig{URL: srv.URL}
	ls := cfg.NewListenStream(sessionTransport(t, srv.URL, "sess-p"), fastListenOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ls.Run(ctx)

	select {
	case m := <-answered:
		assert.Equal(t, float64(7), m["id"])
		assert.Contains(t, m, "result")
		assert.Equal(t, "sess-p", answerSession.Load())
	case <-time.After(3 * time.Second):
		t.Fatal("ping on the GET stream was not answered")
	}
}

// The GET stream is long-lived: it must not be built on the plain branch's
// 180s total-timeout client, and building it must not change that client.
func TestListenStream_ClientHasNoTotalTimeout(t *testing.T) {
	cfg := &HTTPTransportConfig{URL: "http://127.0.0.1:1/mcp"}
	assert.Equal(t, 180*time.Second, cfg.streamableHTTPClient(false, zap.NewNop()).Timeout,
		"POST client timeout policy is unchanged")
	listen, closeIdle := cfg.listenHTTPClient(zap.NewNop())
	defer closeIdle()
	assert.Zero(t, listen.Timeout)
}

// The OAuth path: the GET carries the bearer from the transport's own token
// store, like every POST on that transport.
func TestListenStream_OAuthTransportSendsBearer(t *testing.T) {
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()

	cfg := &HTTPTransportConfig{URL: srv.URL + "/mcp", OAuthConfig: oauthTestConfig(), UseOAuth: true,
		Headers: map[string]string{"X-Goog-User-Project": "p"}}
	cl, err := CreateHTTPClient(cfg)
	require.NoError(t, err)
	defer cl.Close()
	sh, ok := cl.GetTransport().(*mcptransport.StreamableHTTP)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	end := cfg.NewListenStream(sh, fastListenOptions(nil)).Run(ctx)
	assert.Equal(t, ListenUnsupported, end.Reason)
	assert.Equal(t, "Bearer test-access-token", auth.Load())
}
