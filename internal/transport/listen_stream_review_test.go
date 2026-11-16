package transport

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
)

// GH #1910 review: hardening of the standalone GET stream.

func runListen(t *testing.T, ls *ListenStream) (context.CancelFunc, <-chan ListenEnd) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan ListenEnd, 1)
	go func() { done <- ls.Run(ctx) }()
	return cancel, done
}

// An event that grows past the size limit through many short data: lines is
// dropped whole; the stream keeps going and the next event is delivered.
func TestListenStream_OversizedEventIsDropped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		openSSE(w)
		line := "data: " + strings.Repeat("x", 64*1024) + "\n"
		for i := 0; i < (listenMaxEventBytes/(64*1024))+4; i++ {
			_, _ = fmt.Fprint(w, line)
		}
		_, _ = fmt.Fprint(w, "\n")
		writeSSE(w, map[string]any{"jsonrpc": "2.0", "method": string(mcp.MethodNotificationToolsListChanged)})
		<-r.Context().Done()
	}))
	defer srv.Close()

	cfg := &HTTPTransportConfig{URL: srv.URL}
	got := make(chan string, 4)
	ls := cfg.NewListenStream(sessionTransport(t, srv.URL, "s"), fastListenOptions(func(n mcp.JSONRPCNotification) { got <- n.Method }))
	cancel, done := runListen(t, ls)
	defer func() { cancel(); <-done }()

	select {
	case m := <-got:
		assert.Equal(t, string(mcp.MethodNotificationToolsListChanged), m)
	case <-time.After(5 * time.Second):
		t.Fatal("the event after an oversized one was not delivered")
	}
}

// The GET stream never follows a redirect to another origin: the static
// headers (an API key, say) must not reach it. Polling covers the server.
func TestListenStream_RefusesCrossOriginRedirect(t *testing.T) {
	var otherHits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/mcp", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	cfg := &HTTPTransportConfig{URL: srv.URL, Headers: map[string]string{"X-API-Key": "secret"}}
	ls := cfg.NewListenStream(sessionTransport(t, srv.URL, "s"), fastListenOptions(nil))
	cancel, done := runListen(t, ls)
	defer cancel()
	select {
	case end := <-done:
		assert.Equal(t, ListenUnsupported, end.Reason)
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not end on a cross-origin redirect")
	}
	assert.Zero(t, otherHits.Load(), "the redirect target must not be contacted")
}

// A refusal whose body never finishes must not hold the listener: Run returns
// at once instead of blocking on draining the body.
func TestListenStream_RefusalWithStalledBodyReturns(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = fmt.Fprint(w, "partial")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	cfg := &HTTPTransportConfig{URL: srv.URL}
	ls := cfg.NewListenStream(sessionTransport(t, srv.URL, "s"), fastListenOptions(nil))
	cancel, done := runListen(t, ls)
	defer cancel()
	select {
	case end := <-done:
		assert.Equal(t, ListenUnsupported, end.Reason)
		assert.Equal(t, http.StatusMethodNotAllowed, end.Status)
	case <-time.After(3 * time.Second):
		t.Fatal("Run blocked draining a stalled refusal body")
	}
}

// A request received on a stream is answered on the session that opened that
// stream, even if the transport's session changed meanwhile (re-initialize).
func TestListenStream_AnswersOnTheStreamsOwnSession(t *testing.T) {
	answered := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			answered <- r.Header.Get(mcptransport.HeaderKeySessionID)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		openSSE(w)
		writeSSE(w, map[string]any{"jsonrpc": "2.0", "id": 7, "method": string(mcp.MethodPing)})
		<-r.Context().Done()
	}))
	defer srv.Close()

	cfg := &HTTPTransportConfig{URL: srv.URL}
	ls := cfg.NewListenStream(sessionTransport(t, srv.URL, "old"), fastListenOptions(nil))
	var calls atomic.Int32
	ls.sessionID = func() string {
		if calls.Add(1) == 1 {
			return "old"
		}
		return "new" // the transport re-initialized after the GET opened
	}
	cancel, done := runListen(t, ls)
	defer func() { cancel(); <-done }()

	select {
	case sid := <-answered:
		assert.Equal(t, "old", sid)
	case <-time.After(3 * time.Second):
		t.Fatal("ping on the stream was not answered")
	}
}
