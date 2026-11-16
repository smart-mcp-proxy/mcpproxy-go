package core

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

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// GH #1910: streamable-HTTP upstreams that advertise tools.listChanged get a
// standing GET stream, so their notifications/tools/list_changed reach the
// discovery callback within seconds instead of on the next discovery poll.

// listenUpstream is a legacy-era streamable-HTTP upstream with sessions and a
// configurable answer to the standalone GET.
type listenUpstream struct {
	listChanged bool
	getStatus   int // 0: open an event stream; otherwise answer with this status

	mu       sync.Mutex
	seq      int
	live     map[string]bool
	streams  map[int]chan string // open GET streams -> pushed notification methods
	nextID   int
	getSIDs  []string
	gets     atomic.Int32
	open     atomic.Int32
	toolName string
}

func newListenUpstream(listChanged bool, getStatus int) *listenUpstream {
	return &listenUpstream{listChanged: listChanged, getStatus: getStatus,
		live: map[string]bool{}, streams: map[int]chan string{}, toolName: "first"}
}

func (u *listenUpstream) forget() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.live = map[string]bool{}
}

// push sends a notification on every open GET stream.
func (u *listenUpstream) push(method string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, ch := range u.streams {
		ch <- method
	}
}

func (u *listenUpstream) getSessionIDs() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.getSIDs...)
}

func (u *listenUpstream) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			u.serveGet(w, r)
			return
		case http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		write := func(res map[string]any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID), "result": res})
		}
		if req.Method == "initialize" {
			u.mu.Lock()
			u.seq++
			sid := fmt.Sprintf("sess-%04d", u.seq)
			u.live[sid] = true
			u.mu.Unlock()
			w.Header().Set("Mcp-Session-Id", sid)
			tools := map[string]any{}
			if u.listChanged {
				tools["listChanged"] = true
			}
			write(map[string]any{"protocolVersion": req.Params.ProtocolVersion,
				"capabilities": map[string]any{"tools": tools}, "serverInfo": map[string]any{"name": "listen", "version": "1"}})
			return
		}
		u.mu.Lock()
		ok := u.live[r.Header.Get("Mcp-Session-Id")]
		name := u.toolName
		u.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch req.Method {
		case "tools/list":
			write(map[string]any{"tools": []any{map[string]any{"name": name, "inputSchema": map[string]any{"type": "object"}}}})
		default:
			if len(req.ID) == 0 {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			write(map[string]any{})
		}
	})
}

func (u *listenUpstream) serveGet(w http.ResponseWriter, r *http.Request) {
	u.gets.Add(1)
	sid := r.Header.Get("Mcp-Session-Id")
	u.mu.Lock()
	u.getSIDs = append(u.getSIDs, sid)
	live := u.live[sid]
	u.mu.Unlock()
	if u.getStatus != 0 {
		w.WriteHeader(u.getStatus)
		return
	}
	if !live {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	ch := make(chan string, 8)
	u.mu.Lock()
	id := u.nextID
	u.nextID++
	u.streams[id] = ch
	u.mu.Unlock()
	u.open.Add(1)
	defer func() {
		u.mu.Lock()
		delete(u.streams, id)
		u.mu.Unlock()
		u.open.Add(-1)
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case m := <-ch:
			_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":%q}\n\n", m)
			w.(http.Flusher).Flush()
		}
	}
}

func connectListenClient(t *testing.T, url string) (*Client, chan string) {
	t.Helper()
	disableOAuthForTest(t)
	cfg := &config.ServerConfig{Name: "listen", Protocol: "streamable-http", URL: url, Enabled: true}
	c, err := NewClient("listen", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	changed := make(chan string, 8)
	c.SetOnToolsChangedCallback(func(name string) { changed <- name })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect() })
	return c, changed
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Real mcp-go server: AddTool on an upstream with listChanged sends
// notifications/tools/list_changed on the GET stream, and mcpproxy's handler
// fires within seconds, with no discovery poll involved.
func TestListChangedStream_MCPGoServerPushReachesHandler(t *testing.T) {
	upstream := mcpserver.NewMCPServer("listen-upstream", "1", mcpserver.WithToolCapabilities(true))
	upstream.AddTool(mcp.NewTool("first"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	var gets atomic.Int32
	inner := mcpserver.NewStreamableHTTPServer(upstream)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gets.Add(1)
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	c, changed := connectListenClient(t, srv.URL)
	waitFor(t, "the GET stream", func() bool { return gets.Load() == 1 })
	// Give the server a moment to register the stream's session before the push.
	time.Sleep(100 * time.Millisecond)

	upstream.AddTool(mcp.NewTool("second"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	select {
	case name := <-changed:
		assert.Equal(t, "listen", name)
	case <-time.After(3 * time.Second):
		t.Fatal("tools/list_changed from the upstream's GET stream did not reach the handler")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tools, err := c.ListTools(ctx)
	require.NoError(t, err)
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name] = true
	}
	assert.True(t, names["second"], "re-listing after the notification shows the new tool")
}

func TestListChangedStream_NotificationReachesHandler(t *testing.T) {
	up := newListenUpstream(true, 0)
	srv := httptest.NewServer(up.handler())
	t.Cleanup(srv.Close)

	_, changed := connectListenClient(t, srv.URL)
	waitFor(t, "the GET stream", func() bool { return up.open.Load() == 1 })
	assert.Equal(t, []string{"sess-0001"}, up.getSessionIDs(), "the GET carries the session id")

	up.push(string(mcp.MethodNotificationToolsListChanged))
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatal("notification did not reach the handler")
	}
}

// No tools.listChanged in the initialize result: no GET, no held connection.
func TestListChangedStream_NoCapabilityNoGET(t *testing.T) {
	up := newListenUpstream(false, 0)
	srv := httptest.NewServer(up.handler())
	t.Cleanup(srv.Close)

	connectListenClient(t, srv.URL)
	time.Sleep(1500 * time.Millisecond)
	assert.Equal(t, int32(0), up.gets.Load(), "an upstream without tools.listChanged must not be sent a GET")
}

// A server refusing the GET falls back to polling after ONE request.
func TestListChangedStream_RefusedGETIsNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusMethodNotAllowed, http.StatusBadRequest, http.StatusNotAcceptable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			up := newListenUpstream(true, status)
			srv := httptest.NewServer(up.handler())
			t.Cleanup(srv.Close)

			c, _ := connectListenClient(t, srv.URL)
			waitFor(t, "the GET", func() bool { return up.gets.Load() >= 1 })
			time.Sleep(2500 * time.Millisecond)
			assert.Equal(t, int32(1), up.gets.Load(), "a refused GET must not be retried")
			assert.False(t, c.listChangedStreamRunning(), "the listener ends after a refusal")
		})
	}
}

// Disconnect ends the stream (the server sees the request go away) and the
// listener goroutine; a reconnect opens exactly one new stream.
func TestListChangedStream_StopsOnDisconnectAndReconnects(t *testing.T) {
	up := newListenUpstream(true, 0)
	srv := httptest.NewServer(up.handler())
	t.Cleanup(srv.Close)

	c, _ := connectListenClient(t, srv.URL)
	waitFor(t, "the GET stream", func() bool { return up.open.Load() == 1 })

	require.NoError(t, c.Disconnect())
	waitFor(t, "the stream to close", func() bool { return up.open.Load() == 0 })
	assert.False(t, c.listChangedStreamRunning())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	waitFor(t, "the new GET stream", func() bool { return up.open.Load() == 1 })
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(1), up.open.Load(), "one stream after a reconnect")
	assert.Equal(t, int32(2), up.gets.Load())
}

// Retiring the instance (server removal) ends the stream too.
func TestListChangedStream_StopsOnRetire(t *testing.T) {
	up := newListenUpstream(true, 0)
	srv := httptest.NewServer(up.handler())
	t.Cleanup(srv.Close)

	c, _ := connectListenClient(t, srv.URL)
	waitFor(t, "the GET stream", func() bool { return up.open.Load() == 1 })
	c.Retire()
	waitFor(t, "the stream to close", func() bool { return up.open.Load() == 0 })
	waitFor(t, "the listener to exit", func() bool { return !c.listChangedStreamRunning() })
}

// A session re-initialize moves the stream to the new session, ending with
// exactly one listener.
func TestListChangedStream_SessionReinitMovesToNewSession(t *testing.T) {
	up := newListenUpstream(true, 0)
	srv := httptest.NewServer(up.handler())
	t.Cleanup(srv.Close)

	c, changed := connectListenClient(t, srv.URL)
	waitFor(t, "the GET stream", func() bool { return up.open.Load() == 1 })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.ReinitializeSession(ctx))
	waitFor(t, "a GET on the new session", func() bool {
		ids := up.getSessionIDs()
		return len(ids) >= 2 && ids[len(ids)-1] == "sess-0002"
	})
	waitFor(t, "one open stream", func() bool { return up.open.Load() == 1 })
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(1), up.open.Load(), "a re-init must not end with two listeners")

	up.push(string(mcp.MethodNotificationToolsListChanged))
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatal("notification on the re-initialized session's stream did not reach the handler")
	}
}

// A re-initialize finishing after Disconnect released the connection starts
// no stream on the closed transport.
func TestListChangedStream_NoRestartAfterDisconnect(t *testing.T) {
	up := newListenUpstream(true, 0)
	srv := httptest.NewServer(up.handler())
	t.Cleanup(srv.Close)

	c, _ := connectListenClient(t, srv.URL)
	waitFor(t, "the GET stream", func() bool { return up.open.Load() == 1 })
	c.mu.RLock()
	cl, info := c.client, c.serverInfo
	c.mu.RUnlock()

	require.NoError(t, c.Disconnect())
	c.restartListChangedStream(cl, info)
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(1), up.gets.Load(), "no GET after the connection was released")
	assert.False(t, c.listChangedStreamRunning())
}

// The upstream forgetting the session ends the stream with a 404; it is not
// retried until a re-initialize issues a new session.
func TestListChangedStream_SessionLostWaitsForReinit(t *testing.T) {
	up := newListenUpstream(true, 0)
	srv := httptest.NewServer(up.handler())
	t.Cleanup(srv.Close)

	c, _ := connectListenClient(t, srv.URL)
	waitFor(t, "the GET stream", func() bool { return up.open.Load() == 1 })

	// Server restart: sessions gone, open streams dropped.
	up.forget()
	srv.CloseClientConnections()
	waitFor(t, "the re-opened GET to be refused", func() bool { return up.gets.Load() >= 2 })
	waitFor(t, "the listener to stop", func() bool { return !c.listChangedStreamRunning() })
	time.Sleep(1500 * time.Millisecond)
	assert.Equal(t, int32(2), up.gets.Load(), "a 404 GET is not retried on the dead session")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.ReinitializeSession(ctx))
	waitFor(t, "a GET on the new session", func() bool { return up.open.Load() == 1 })
}

// A managed caller holds its own lock across core Disconnect, and the
// tools-changed callback takes that lock. A notification whose dispatch is
// blocked on it must not hold Disconnect for the listener stop timeout.
func TestListChangedStream_DisconnectDoesNotWaitOnBlockedDispatch(t *testing.T) {
	up := newListenUpstream(true, 0)
	srv := httptest.NewServer(up.handler())
	t.Cleanup(srv.Close)

	c, _ := connectListenClient(t, srv.URL)
	waitFor(t, "the GET stream", func() bool { return up.open.Load() == 1 })

	var managedMu sync.RWMutex // stands in for managed.Client.mu
	entered := make(chan struct{}, 8)
	c.SetOnToolsChangedCallback(func(string) {
		entered <- struct{}{}
		managedMu.RLock()
		defer managedMu.RUnlock()
	})

	managedMu.Lock()
	var unlockOnce sync.Once
	unlock := func() { unlockOnce.Do(managedMu.Unlock) }
	defer unlock()

	up.push(string(mcp.MethodNotificationToolsListChanged))
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("notification did not reach the callback")
	}

	start := time.Now()
	require.NoError(t, c.Disconnect())
	elapsed := time.Since(start)
	unlock()
	assert.Less(t, elapsed, 2*time.Second,
		"Disconnect waited on a dispatch blocked behind the caller's lock")
	waitFor(t, "the stream to close", func() bool { return up.open.Load() == 0 })
}

// The dispatch queue never blocks the listener and coalesces per method, so a
// flood while a handler is stuck holds a bounded amount.
func TestNotificationQueue_CoalescesPerMethod(t *testing.T) {
	q := newNotificationQueue()
	tools := mcp.JSONRPCNotification{Notification: mcp.Notification{Method: string(mcp.MethodNotificationToolsListChanged)}}
	prompts := mcp.JSONRPCNotification{Notification: mcp.Notification{Method: string(mcp.MethodNotificationPromptsListChanged)}}
	for range 100 {
		q.push(tools)
	}
	q.push(prompts)
	got := q.take()
	require.Len(t, got, 2)
	assert.Equal(t, tools.Method, got[0].Method)
	assert.Equal(t, prompts.Method, got[1].Method)
	assert.Empty(t, q.take())

	// Notifications the dispatcher ignores are never queued, so any number of
	// them cannot crowd out a later list_changed.
	for i := range 4 * maxQueuedNotificationMethods {
		q.push(mcp.JSONRPCNotification{Notification: mcp.Notification{Method: fmt.Sprintf("notifications/x/%d", i)}})
	}
	q.push(tools)
	got = q.take()
	require.Len(t, got, 1)
	assert.Equal(t, tools.Method, got[0].Method)
	select {
	case <-q.wake:
	default:
		t.Fatal("push did not wake the dispatcher")
	}
}

// A re-initialize of an old connection that fails after a reconnect opened a
// newer connection's stream leaves that newer stream running.
func TestListChangedStream_LateReinitFailureKeepsNewerStream(t *testing.T) {
	up := newListenUpstream(true, 0)
	srv := httptest.NewServer(up.handler())
	t.Cleanup(srv.Close)

	c, _ := connectListenClient(t, srv.URL)
	waitFor(t, "the GET stream", func() bool { return up.open.Load() == 1 })
	c.mu.RLock()
	oldCl := c.client
	c.mu.RUnlock()

	// Reconnect: the old connection is closed, a new one holds the stream.
	require.NoError(t, c.Disconnect())
	waitFor(t, "the old stream to close", func() bool { return up.open.Load() == 0 })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	waitFor(t, "the new GET stream", func() bool { return up.open.Load() == 1 })

	// A re-initialize that captured the old connection fails late.
	c.mu.Lock()
	newCl := c.client
	c.client = oldCl
	c.mu.Unlock()
	err := c.ReinitializeSession(ctx)
	c.mu.Lock()
	c.client = newCl
	c.mu.Unlock()
	require.Error(t, err, "a re-initialize on the closed connection fails")

	time.Sleep(300 * time.Millisecond)
	assert.True(t, c.listChangedStreamRunning(), "the newer connection's listener was stopped")
	assert.Equal(t, int32(1), up.open.Load(), "the newer connection's GET stream was closed")
}
