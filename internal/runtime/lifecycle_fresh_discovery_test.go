package runtime

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// heldToolsListHandler computes a tools/list response immediately but, while
// armed, holds it back until released: the response a client receives then
// describes the tools as they were when the request was answered, not when it
// is delivered (an in-flight list that predates a change).
type heldToolsListHandler struct {
	next     http.Handler
	mu       sync.Mutex
	armed    bool
	captured chan struct{}
	release  chan struct{}
}

func (h *heldToolsListHandler) arm() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.armed = true
	h.captured = make(chan struct{})
	h.release = make(chan struct{})
}

func (h *heldToolsListHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	req.Body = io.NopCloser(bytes.NewReader(body))
	h.mu.Lock()
	hold := h.armed && strings.Contains(string(body), `"tools/list"`)
	var captured, release chan struct{}
	if hold {
		h.armed = false // hold exactly one response
		captured, release = h.captured, h.release
	}
	h.mu.Unlock()
	if !hold {
		h.next.ServeHTTP(w, req)
		return
	}
	rec := httptest.NewRecorder()
	h.next.ServeHTTP(rec, req)
	close(captured)
	<-release
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

// TestToolsListChanged_TrustedServer_DoesNotJoinPreChangeList (Sol r6 P2):
// a trusted server's notification-driven discovery must not join a
// tools/list that was already in flight (answered before the change) when
// notifications/tools/list_changed arrived. It used the coalescing ListTools
// and adopted the pre-change response, keeping the stale approval — which
// persists when periodic discovery is disabled.
func TestToolsListChanged_TrustedServer_DoesNotJoinPreChangeList(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	// tools.listChanged is NOT advertised. The test delivers the one
	// notification itself, below, and requires that very call to own the
	// discovery. An advertising upstream gets a standing GET stream (GH
	// #1910), so DeleteTools/AddTool would also push a real list_changed:
	// whichever of the two took the discovery marker first ran, the other was
	// coalesced and returned at once, and the record was read before the
	// owning run had stored it ("approved", in 0.27s, on CI).
	mcpSrv := mcpserver.NewMCPServer("stage", "0.0.1", mcpserver.WithToolCapabilities(false))
	handler := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}
	mcpSrv.AddTool(mcp.NewTool("calendar_get_events", mcp.WithDescription("Lists calendar events.")), handler)
	held := &heldToolsListHandler{next: mcpserver.NewStreamableHTTPServer(mcpSrv)}
	upstream := httptest.NewServer(held)
	t.Cleanup(upstream.Close)

	cfg := &config.ServerConfig{Name: "trusted", URL: upstream.URL, Protocol: "streamable-http", Enabled: true, TrustMode: string(config.TrustModeManual)}
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{cfg}}, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	// A server that already went through admission: known to config.db,
	// unquarantined, with the approved baseline.
	require.NoError(t, rt.storageManager.SaveUpstreamServer(&config.ServerConfig{
		Name: "trusted", URL: upstream.URL, Protocol: "streamable-http", Enabled: true, TrustMode: string(config.TrustModeManual),
	}))
	approvedSchema := normalizeJSON(`{"type":"object","properties":{},"required":[]}`)
	approvedHash := calculateToolApprovalHashWithOutputSchema("calendar_get_events", "Lists calendar events.", approvedSchema, "", nil)
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "trusted", ToolName: "calendar_get_events",
		ApprovedHash: approvedHash, CurrentHash: approvedHash,
		HashSchemaVersion:  storage.ToolApprovalHashSchemaVersion,
		Status:             storage.ToolApprovalStatusApproved,
		ApprovedBy:         "operator",
		CurrentDescription: "Lists calendar events.",
		CurrentSchema:      approvedSchema,
		// mcp-go's NewTool serves the MCP default hints explicitly; the
		// approved snapshot must say so too (spec 120 FR-009).
		CurrentAnnotations: mcpGoDefaultAnnotations(),
		IdentityKeyed:      true,
	}))

	// Both hooks are installed before any runtime goroutine starts. The
	// connect hook counts finished connect-time discoveries; the
	// notification hook counts runs of the test's own notification call
	// (the only one whose context carries ownNotificationRun; a coalesced
	// follow-up runs on a fresh runtime context).
	var connectRuns, ownRuns atomic.Int32
	rt.connectDiscoverHook = func(ctx context.Context, serverName string) error {
		defer connectRuns.Add(1)
		return rt.DiscoverAndIndexToolsForServer(ctx, serverName)
	}
	rt.notificationDiscoverHook = func(ctx context.Context, serverName string) error {
		if ctx.Value(ownNotificationRun{}) != nil {
			ownRuns.Add(1)
		}
		return rt.DiscoverAndIndexToolsForServer(ctx, serverName)
	}

	rt.StartBackgroundInitialization()
	require.Eventually(t, func() bool {
		client, ok := rt.UpstreamManager().GetClient("trusted")
		return ok && client.IsConnected() && !rt.serverIsQuarantined("trusted")
	}, 15*time.Second, 50*time.Millisecond, "the trusted server must connect unquarantined")
	// The trusted server's connect-time discovery keeps the baseline approved.
	// Wait for that discovery to have run, not only for the pre-seeded
	// record: it is started from the supervisor's connected event and would
	// otherwise take the marker after arming and absorb the notification.
	requireConnectDiscoverySettled(t, rt, &connectRuns)

	client, ok := rt.UpstreamManager().GetClient("trusted")
	require.True(t, ok)

	// Observe ListTools coalescing so the test acknowledges each step
	// instead of sleeping (Sol r7 P3).
	events := make(chan managed.ListToolsEvent, 64)
	client.SetListToolsObserver(func(ev managed.ListToolsEvent) {
		select {
		case events <- ev:
		default:
		}
	})
	t.Cleanup(func() { client.SetListToolsObserver(nil) })

	// A tools/list (e.g. a tool-count refresh) is answered with the approved
	// contract and held in flight.
	held.arm()
	// Release the held response on every exit path: a t.Fatal below would
	// otherwise leave the handler blocked and upstream.Close (an earlier
	// cleanup, so it runs after this one) hanging (Sol r8 P2).
	var releaseOnce sync.Once
	releaseHeld := func() { releaseOnce.Do(func() { close(held.release) }) }
	t.Cleanup(releaseHeld)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	inFlight := make(chan struct{})
	go func() {
		defer close(inFlight)
		_, _ = client.ListTools(ctx)
	}()
	var heldSeq uint64
	for heldSeq == 0 {
		select {
		case ev := <-events:
			if ev.Leader {
				heldSeq = ev.Seq
			}
		case <-ctx.Done():
			t.Fatal("the in-flight tools/list never became a leader")
		}
	}
	select {
	case <-held.captured:
	case <-ctx.Done():
		t.Fatal("the in-flight tools/list never reached the upstream")
	}

	// The tool is armed and the server signals list_changed while that
	// pre-change response is still in flight.
	mcpSrv.DeleteTools("calendar_get_events")
	mcpSrv.AddTool(mcp.NewTool("calendar_get_events", mcp.WithDescription("armed definition")), handler)
	notified := make(chan error, 1)
	ownCtx := context.WithValue(ctx, ownNotificationRun{}, true)
	go func() { notified <- rt.onUpstreamToolsListChanged(ownCtx, "trusted") }()
	// Release the pre-change response only once the notification's
	// discovery has attached to that in-flight list: a discovery that joins
	// it (the old coalescing) would then adopt the stale response.
	for attached := false; !attached; {
		select {
		case ev := <-events:
			attached = !ev.Leader && ev.Seq == heldSeq
		case <-ctx.Done():
			t.Fatal("the notification's discovery never reached the in-flight tools/list")
		}
	}
	releaseHeld()
	<-inFlight

	select {
	case err := <-notified:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("notification discovery did not finish")
	}
	require.Equal(t, int32(1), ownRuns.Load(),
		"the test's own notification call must have run its discovery, not been coalesced into another owner's")
	rec, err := rt.storageManager.GetToolApproval("trusted", "calendar_get_events")
	require.NoError(t, err)
	require.Equal(t, storage.ToolApprovalStatusChanged, rec.Status,
		"the notification's discovery must hash the post-change contract, not the pre-change in-flight list")
	require.Equal(t, "armed definition", rec.CurrentDescription)
}

// ownNotificationRun marks the context of a test's own notification call.
type ownNotificationRun struct{}

// requireConnectDiscoverySettled waits until connect-time discovery has run
// at least once (counted by the test's connectDiscoverHook) and released the
// per-server discovery marker.
func requireConnectDiscoverySettled(t *testing.T, rt *Runtime, connectRuns *atomic.Int32) {
	t.Helper()
	require.Eventually(t, func() bool {
		rec, err := rt.storageManager.GetToolApproval("trusted", "calendar_get_events")
		_, busy := rt.discoveryInProgress.Load("trusted")
		return err == nil && rec.Status == storage.ToolApprovalStatusApproved && connectRuns.Load() > 0 && !busy
	}, 15*time.Second, 20*time.Millisecond, "connect-time discovery must run and keep the trusted baseline approved")
}

// TestToolsListChanged_AdvertisedPush_HoldsChangedContract is the transport
// path the test above deliberately avoids: an upstream that advertises
// tools.listChanged gets a standing GET stream (GH #1910), changes a trusted
// server's approved tool, and pushes notifications/tools/list_changed itself.
// Nothing in the test calls the handler. Once discovery settles, the changed
// contract must be held as "changed" with the old approval kept (no
// auto_approve_tool_changes), and the run that recorded it must be the
// notification's.
func TestToolsListChanged_AdvertisedPush_HoldsChangedContract(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	mcpSrv := mcpserver.NewMCPServer("stage", "0.0.1", mcpserver.WithToolCapabilities(true))
	handler := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}
	mcpSrv.AddTool(mcp.NewTool("calendar_get_events", mcp.WithDescription("Lists calendar events.")), handler)
	upstream := httptest.NewServer(mcpserver.NewStreamableHTTPServer(mcpSrv))
	t.Cleanup(upstream.Close)

	cfg := &config.ServerConfig{Name: "trusted", URL: upstream.URL, Protocol: "streamable-http", Enabled: true, TrustMode: string(config.TrustModeManual)}
	rt, err := New(&config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", Servers: []*config.ServerConfig{cfg}}, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	require.NoError(t, rt.storageManager.SaveUpstreamServer(&config.ServerConfig{
		Name: "trusted", URL: upstream.URL, Protocol: "streamable-http", Enabled: true, TrustMode: string(config.TrustModeManual),
	}))
	approvedSchema := normalizeJSON(`{"type":"object","properties":{},"required":[]}`)
	approvedHash := calculateToolApprovalHashWithOutputSchema("calendar_get_events", "Lists calendar events.", approvedSchema, "", nil)
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "trusted", ToolName: "calendar_get_events",
		ApprovedHash: approvedHash, CurrentHash: approvedHash,
		HashSchemaVersion:  storage.ToolApprovalHashSchemaVersion,
		Status:             storage.ToolApprovalStatusApproved,
		ApprovedBy:         "operator",
		CurrentDescription: "Lists calendar events.",
		CurrentSchema:      approvedSchema,
		CurrentAnnotations: mcpGoDefaultAnnotations(),
		IdentityKeyed:      true,
	}))

	// Installed before any runtime goroutine starts. The notification hook is
	// read only by the notification handler, so a run it counts came from a
	// list_changed; the connect hook counts finished connect-time discoveries.
	var connectRuns, notificationRuns atomic.Int32
	rt.connectDiscoverHook = func(ctx context.Context, serverName string) error {
		defer connectRuns.Add(1)
		return rt.DiscoverAndIndexToolsForServer(ctx, serverName)
	}
	rt.notificationDiscoverHook = func(ctx context.Context, serverName string) error {
		notificationRuns.Add(1)
		return rt.DiscoverAndIndexToolsForServer(ctx, serverName)
	}

	rt.StartBackgroundInitialization()
	require.Eventually(t, func() bool {
		client, ok := rt.UpstreamManager().GetClient("trusted")
		return ok && client.IsConnected() && !rt.serverIsQuarantined("trusted")
	}, 15*time.Second, 50*time.Millisecond, "the trusted server must connect unquarantined")
	// Arm only after connect-time discovery ran: otherwise it can take the
	// marker after the change and absorb the push into its own follow-up.
	requireConnectDiscoverySettled(t, rt, &connectRuns)
	before := notificationRuns.Load()

	// The upstream changes the tool and pushes the notification itself.
	mcpSrv.DeleteTools("calendar_get_events")
	mcpSrv.AddTool(mcp.NewTool("calendar_get_events", mcp.WithDescription("armed definition")), handler)

	require.Eventually(t, func() bool {
		rec, err := rt.storageManager.GetToolApproval("trusted", "calendar_get_events")
		_, busy := rt.discoveryInProgress.Load("trusted")
		return err == nil && !busy && notificationRuns.Load() > before &&
			rec.Status == storage.ToolApprovalStatusChanged && rec.CurrentDescription == "armed definition"
	}, 15*time.Second, 20*time.Millisecond, "the pushed list_changed must leave the changed contract held")

	rec, err := rt.storageManager.GetToolApproval("trusted", "calendar_get_events")
	require.NoError(t, err)
	require.Equal(t, storage.ToolApprovalStatusChanged, rec.Status)
	require.Equal(t, approvedHash, rec.ApprovedHash, "the change must not be approved without auto_approve_tool_changes")
	require.NotEqual(t, approvedHash, rec.CurrentHash)
	require.Equal(t, "Lists calendar events.", rec.PreviousDescription)
}
