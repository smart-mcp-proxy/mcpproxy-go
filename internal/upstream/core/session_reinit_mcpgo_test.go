package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// sessionUpstream is a minimal legacy-era Streamable HTTP upstream with real
// session semantics: initialize issues a new session id; every other request
// must carry a live one or it is answered 404 before anything executes.
type sessionUpstream struct {
	mu    sync.Mutex
	seq   int
	live  map[string]bool
	inits int
}

func newSessionUpstream() *sessionUpstream { return &sessionUpstream{live: map[string]bool{}} }

// forget makes the upstream drop every session it has issued (server restart).
func (u *sessionUpstream) forget() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.live = map[string]bool{}
}

func (u *sessionUpstream) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
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
			u.inits++
			u.seq++
			sid := fmt.Sprintf("sess-%08d", u.seq)
			u.live[sid] = true
			u.mu.Unlock()
			w.Header().Set("Mcp-Session-Id", sid)
			write(map[string]any{"protocolVersion": req.Params.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "sess", "version": "1"}})
			return
		}
		u.mu.Lock()
		ok := u.live[r.Header.Get("Mcp-Session-Id")]
		u.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch req.Method {
		case "tools/list":
			write(map[string]any{"tools": []any{}})
		default:
			if len(req.ID) == 0 {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			write(map[string]any{})
		}
	})
}

// T220 / FR-088: pins the mcp-go v1.0.0 behaviour the re-init design relies on.
func TestMCPGo_SessionTerminated_ReinitializeOnSameTransport(t *testing.T) {
	up := newSessionUpstream()
	srv := httptest.NewServer(up.handler())
	defer srv.Close()

	tr, err := transport.NewStreamableHTTP(srv.URL)
	require.NoError(t, err)
	cl := client.NewClient(tr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.Start(ctx))
	t.Cleanup(func() { _ = cl.Close() })

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_LEGACY_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "t", Version: "1"}
	_, err = cl.Initialize(ctx, initReq)
	require.NoError(t, err)
	first := tr.GetSessionId()
	require.NotEmpty(t, first)

	// 404 on a non-initialize POST -> ErrSessionTerminated, session id cleared.
	up.forget()
	_, err = cl.ListTools(ctx, mcp.ListToolsRequest{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, transport.ErrSessionTerminated), "got %v", err)
	assert.Empty(t, tr.GetSessionId(), "mcp-go clears the session id on 404")

	// 404 with no session id at all is also ErrSessionTerminated (the next
	// request goes out without a header, which this upstream rejects).
	_, err = cl.ListTools(ctx, mcp.ListToolsRequest{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, transport.ErrSessionTerminated), "got %v", err)

	// A second Initialize on the same client/transport stores the new id.
	_, err = cl.Initialize(ctx, initReq)
	require.NoError(t, err)
	second := tr.GetSessionId()
	require.NotEmpty(t, second)
	assert.NotEqual(t, first, second)
	_, err = cl.ListTools(ctx, mcp.ListToolsRequest{})
	require.NoError(t, err)
}

func TestCoreSessionSnapshotAndReinitialize(t *testing.T) {
	disableOAuthForTest(t)
	up := newSessionUpstream()
	srv := httptest.NewServer(up.handler())
	defer srv.Close()

	cfg := &config.ServerConfig{Name: "sess", Protocol: "streamable-http", URL: srv.URL, Enabled: true}
	c, err := NewClient("sess", cfg, zap.NewNop(), nil, nil, nil, secret.NewResolver())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect() })

	id, modern := c.SessionSnapshot()
	assert.NotEmpty(t, id)
	assert.False(t, modern)

	up.forget()
	_, err = c.ListTools(ctx)
	require.Error(t, err)
	assert.True(t, errors.Is(err, transport.ErrSessionTerminated), "got %v", err)
	id2, _ := c.SessionSnapshot()
	assert.Empty(t, id2)

	require.NoError(t, c.ReinitializeSession(ctx))
	id3, _ := c.SessionSnapshot()
	assert.NotEmpty(t, id3)
	assert.NotEqual(t, id, id3)
	_, err = c.ListTools(ctx)
	require.NoError(t, err)
}
