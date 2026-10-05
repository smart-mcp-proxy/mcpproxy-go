package managed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// sessUpstream is a legacy-era Streamable HTTP upstream with real session
// semantics (Spec 113-e): initialize issues a session id; any other request
// without a live id is answered 404 before executing.
type sessUpstream struct {
	mu          sync.Mutex
	seq         int
	live        map[string]bool
	inits       int
	noSession   int // non-initialize requests that arrived with no session id
	executed    map[string]int
	readDesc    string        // description of read_thing; changing it changes its hash
	rejectAfter bool          // when set, every non-initialize request is 404 (even on a fresh session)
	notReadOnly bool          // when set, read_thing is listed without readOnlyHint
	listEntered chan struct{} // when non-nil, tools/list signals here then blocks on listRelease
	listRelease chan struct{}
}

func newSessUpstream() *sessUpstream {
	return &sessUpstream{live: map[string]bool{}, executed: map[string]int{}, readDesc: "reads"}
}

func (u *sessUpstream) forget() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.live = map[string]bool{}
}

func (u *sessUpstream) snapshot() (inits, noSession int, executed map[string]int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	ex := map[string]int{}
	for k, v := range u.executed {
		ex[k] = v
	}
	return u.inits, u.noSession, ex
}

func (u *sessUpstream) handler() http.Handler {
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
				Name            string `json:"name"`
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
		sid := r.Header.Get("Mcp-Session-Id")
		u.mu.Lock()
		ok := u.live[sid] && !u.rejectAfter
		if sid == "" {
			u.noSession++
		}
		desc := u.readDesc
		notRO := u.notReadOnly
		entered, release := u.listEntered, u.listRelease
		u.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch req.Method {
		case "tools/call":
			u.mu.Lock()
			u.executed[req.Params.Name]++
			u.mu.Unlock()
			write(map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}})
		case "tools/list":
			if entered != nil {
				entered <- struct{}{}
				<-release
			}
			ann := map[string]any{"readOnlyHint": !notRO}
			write(map[string]any{"tools": []any{
				map[string]any{"name": "read_thing", "description": desc, "inputSchema": map[string]any{"type": "object"}, "annotations": ann},
				map[string]any{"name": "write_thing", "description": "writes", "inputSchema": map[string]any{"type": "object"}},
			}})
		default:
			if len(req.ID) == 0 {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			write(map[string]any{})
		}
	})
}

func newSessionClient(t *testing.T) (*Client, *sessUpstream) {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	up := newSessUpstream()
	srv := httptest.NewServer(up.handler())
	t.Cleanup(srv.Close)

	cfg := &config.ServerConfig{Name: "sess", Protocol: "streamable-http", URL: srv.URL, Enabled: true}
	mc, err := NewClient("sess", cfg, zap.NewNop(), nil, &config.Config{}, nil, secret.NewResolver())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	require.NoError(t, mc.Connect(ctx))
	t.Cleanup(func() { _ = mc.Disconnect() })
	// Discovery baseline: the identity hashes a certified call is held against.
	_, err = mc.ListTools(ctx)
	require.NoError(t, err)
	return mc, up
}

func tctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// SC-006: a session forgotten once -> one re-init, the read-only call succeeds
// once, the client stays Ready, nothing was sent without a session id.
func TestSessionReinit_SingleReadCall(t *testing.T) {
	mc, up := newSessionClient(t)
	up.forget()

	res, err := mc.CallTool(tctx(t), "read_thing", nil)
	require.NoError(t, err)
	require.NotNil(t, res)

	inits, noSession, executed := up.snapshot()
	assert.Equal(t, 2, inits, "exactly one re-initialize")
	assert.Equal(t, 1, executed["read_thing"])
	assert.Equal(t, int64(1), mc.SessionReinitCount())
	assert.True(t, mc.IsConnected())
	assert.Equal(t, "Ready", mc.StateManager.GetState().String())
	_ = noSession
}

// FR-082: 10 concurrent callers on one terminated session share one re-init.
func TestSessionReinit_ConcurrentCallsSingleFlight(t *testing.T) {
	mc, up := newSessionClient(t)
	up.forget()

	const n = 10
	var wg sync.WaitGroup
	var failures atomic.Int32
	ctx := tctx(t)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := mc.CallTool(ctx, "read_thing", nil); err != nil {
				failures.Add(1)
				t.Logf("call error: %v", err)
			}
		}()
	}
	wg.Wait()

	inits, _, executed := up.snapshot()
	assert.Zero(t, failures.Load())
	assert.Equal(t, 2, inits, "10 concurrent callers must trigger exactly one re-initialize")
	assert.Equal(t, n, executed["read_thing"])
	assert.Equal(t, int64(1), mc.SessionReinitCount())
	assert.Equal(t, "Ready", mc.StateManager.GetState().String())
}

// FR-081: a write tool is not repeated; the session is re-established and the
// next call works. No Error state.
func TestSessionReinit_WriteToolNotRepeated(t *testing.T) {
	mc, up := newSessionClient(t)
	up.forget()

	_, err := mc.CallTool(tctx(t), "write_thing", nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSessionReestablished), "got %v", err)

	_, _, executed := up.snapshot()
	assert.Zero(t, executed["write_thing"], "the write must not be repeated")
	assert.Equal(t, "Ready", mc.StateManager.GetState().String())

	_, err = mc.CallTool(tctx(t), "write_thing", nil)
	require.NoError(t, err)
	inits, _, executed := up.snapshot()
	assert.Equal(t, 2, inits)
	assert.Equal(t, 1, executed["write_thing"])
}

// FR-081(b)/FR-083a: a read-only tool whose identity hash changed across the
// re-init is not retried.
func TestSessionReinit_ChangedToolHashNotRetried(t *testing.T) {
	mc, up := newSessionClient(t)
	up.mu.Lock()
	up.readDesc = "reads, but now differently"
	up.mu.Unlock()
	up.forget()

	_, err := mc.CallTool(tctx(t), "read_thing", nil)
	require.Error(t, err)
	_, _, executed := up.snapshot()
	assert.Zero(t, executed["read_thing"])

	// A pinned call on the same situation is refused with the generation error.
	up.forget()
	up.mu.Lock()
	up.readDesc = "changed again"
	up.mu.Unlock()
	epoch := mc.ConnectionEpoch()
	_, err = mc.CallToolOnEpoch(tctx(t), "read_thing", nil, epoch)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrConnectionGenerationChanged), "got %v", err)
}

// FR-080/FR-084: a ListTools 404 re-inits, retries once, no Error state.
func TestSessionReinit_ListToolsRetried(t *testing.T) {
	mc, up := newSessionClient(t)
	up.forget()

	tools, err := mc.ListTools(tctx(t))
	require.NoError(t, err)
	assert.Len(t, tools, 2)
	assert.Equal(t, int64(1), mc.SessionReinitCount())
	assert.Equal(t, "Ready", mc.StateManager.GetState().String())
}

// FR-080: a health ping that hits the 404 first re-inits; the following call
// joins the already-new session and never sends a request without an id.
func TestSessionReinit_PingFirstThenCall(t *testing.T) {
	mc, up := newSessionClient(t)
	up.forget()

	require.NoError(t, mc.probeLiveness(tctx(t), mc.coreClient))
	_, noSession0, _ := up.snapshot()

	_, err := mc.CallTool(tctx(t), "read_thing", nil)
	require.NoError(t, err)
	inits, noSession, executed := up.snapshot()
	assert.Equal(t, 2, inits)
	assert.Equal(t, 1, executed["read_thing"])
	assert.Equal(t, noSession0, noSession, "the call must not go out without a session id")
	assert.Equal(t, int64(1), mc.SessionReinitCount())
}

// FR-084/FR-086: a retry that is also 404 fails and goes through the existing
// path (the tool is not executed).
func TestSessionReinit_RetryAlso404(t *testing.T) {
	mc, up := newSessionClient(t)
	up.mu.Lock()
	up.rejectAfter = true
	up.mu.Unlock()

	_, err := mc.CallTool(tctx(t), "read_thing", nil)
	require.Error(t, err)
	_, _, executed := up.snapshot()
	assert.Zero(t, executed["read_thing"])
}

// FR-083: a pinned call succeeds after a re-init and the epoch is unchanged.
func TestSessionReinit_PinnedCallEpochUnchanged(t *testing.T) {
	mc, up := newSessionClient(t)
	epoch := mc.ConnectionEpoch()
	up.forget()

	_, err := mc.CallToolOnEpoch(tctx(t), "read_thing", nil, epoch)
	require.NoError(t, err)
	assert.Equal(t, epoch, mc.ConnectionEpoch())
	assert.Equal(t, int64(1), mc.SessionReinitCount())
}

// Annotations are not part of the identity hash: a tool that stops being
// read-only across the re-init (same hash) must not be repeated.
func TestSessionReinit_ReadOnlyFlippedNotRetried(t *testing.T) {
	mc, up := newSessionClient(t)
	up.mu.Lock()
	up.live = map[string]bool{}
	up.notReadOnly = true
	up.mu.Unlock()

	_, err := mc.CallTool(tctx(t), "read_thing", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionReestablished)
	_, _, executed := up.snapshot()
	assert.Zero(t, executed["read_thing"], "no longer read-only: must not be executed again")
	assert.Equal(t, "Ready", mc.StateManager.GetState().String())
}

// FR-082/083a: a caller arriving while a flight is between initialize (new id
// installed) and its verifying tools/list must wait, not send on the new id.
func TestSessionReinit_CallerDuringFlightWaits(t *testing.T) {
	mc, up := newSessionClient(t)
	up.forget()
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	up.mu.Lock()
	up.listEntered, up.listRelease = entered, release
	up.mu.Unlock()

	ctx := tctx(t)
	errs := make(chan error, 2)
	go func() { _, err := mc.CallTool(ctx, "read_thing", nil); errs <- err }()
	select {
	case <-entered: // flight is inside its tools/list; new session id already installed
	case <-time.After(10 * time.Second):
		t.Fatal("re-init flight never reached tools/list")
	}
	go func() { _, err := mc.CallTool(ctx, "read_thing", nil); errs <- err }()
	time.Sleep(300 * time.Millisecond)
	_, _, executed := up.snapshot()
	assert.Zero(t, executed["read_thing"], "no call may execute before the flight verified the toolset")

	close(release)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	inits, _, executed := up.snapshot()
	assert.Equal(t, 2, inits)
	assert.Equal(t, 2, executed["read_thing"])
}
