package runtime

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Spec 117 PR-3 (#1565): tool inventories are applied in capture order. An
// older tools/list result that reaches the apply after a newer one writes
// nothing: no approval record, no index entry, no last-good snapshot and no
// StateView (dispatch) metadata. These tests drive the real discovery paths
// against a live streamable-HTTP upstream whose inventory the test switches
// between lists, and park the older pass at a deterministic point.

// inventoryUpstream is an MCP upstream whose tools/list answer is switched
// by the test: tool "t" is read-only or destructive, and "x" is listed or not.
type inventoryUpstream struct {
	destructive atomic.Bool
	withX       atomic.Bool
	empty       atomic.Bool
	url         string
}

func newInventoryUpstream(t *testing.T) *inventoryUpstream {
	t.Helper()
	u := &inventoryUpstream{}
	filter := func(_ context.Context, tools []mcp.Tool) []mcp.Tool {
		if u.empty.Load() {
			return []mcp.Tool{}
		}
		out := make([]mcp.Tool, 0, len(tools))
		for _, tl := range tools {
			switch tl.Name {
			case "x":
				if !u.withX.Load() {
					continue
				}
			case "t":
				ro, destr := true, false
				if u.destructive.Load() {
					ro, destr = false, true
				}
				tl.Annotations = mcp.ToolAnnotation{ReadOnlyHint: &ro, DestructiveHint: &destr}
			}
			out = append(out, tl)
		}
		return out
	}
	up := mcpserver.NewMCPServer("inventory", "0.0.1",
		mcpserver.WithToolCapabilities(false), mcpserver.WithToolFilter(filter))
	ok := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}
	up.AddTool(mcp.NewTool("t", mcp.WithDescription("tool t")), ok)
	up.AddTool(mcp.NewTool("x", mcp.WithDescription("tool x")), ok)
	srv := httptest.NewServer(mcpserver.NewStreamableHTTPServer(up))
	t.Cleanup(srv.Close)
	u.url = srv.URL
	return u
}

// newInventoryRuntime starts a trusted server "inv" on up, connects it and
// marks its reactive discovery as in progress, so only the passes the test
// starts itself list the server.
func newInventoryRuntime(t *testing.T, up *inventoryUpstream) *Runtime {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	rt, _ := newSpec116Runtime(t, nil, explicitServer(&config.ServerConfig{
		Name: "inv", URL: up.url, Protocol: "streamable-http", Enabled: true,
	}))
	rt.discoveryInProgress.Store("inv", struct{}{})
	require.NoError(t, rt.upstreamManager.AddServerAt("inv", spec116Live(rt, "inv"), storedStamp(t, rt, "inv").Stamp.Gen))
	c, ok := rt.upstreamManager.GetClient("inv")
	require.True(t, ok)
	require.Eventually(t, c.IsConnected, 10*time.Second, 5*time.Millisecond)
	return rt
}

// parkFirstAfterList parks the first single-server pass that returns from
// its tools/list until release is closed; later passes run through.
func parkFirstAfterList(rt *Runtime) (parked, release chan struct{}) {
	parked, release = make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	rt.testHooks.discoveryAfterList = func(string) {
		if first.CompareAndSwap(false, true) {
			close(parked)
			<-release
		}
	}
	return parked, release
}

func invRecord(t *testing.T, rt *Runtime, tool string) *storage.ToolApprovalRecord {
	t.Helper()
	rec, err := rt.storageManager.GetToolApproval("inv", tool)
	if err != nil {
		return nil
	}
	return rec
}

func invIndexed(t *testing.T, rt *Runtime) map[string]*config.ToolMetadata {
	t.Helper()
	tools, err := rt.indexManager.GetToolsByServer("inv")
	require.NoError(t, err)
	out := make(map[string]*config.ToolMetadata, len(tools))
	for _, tl := range tools {
		out[config.RawToolName(tl)] = tl
	}
	return out
}

func invStateViewDestructive(t *testing.T, rt *Runtime, tool string) (found, destructive bool) {
	t.Helper()
	st, ok := rt.supervisor.StateView().GetServer("inv")
	require.True(t, ok)
	for _, ti := range st.Tools {
		if config.RawToolName(&config.ToolMetadata{Name: ti.Name, ServerName: "inv"}) != tool && ti.Name != tool && ti.Name != "inv:"+tool {
			continue
		}
		return true, ti.Annotations != nil && ti.Annotations.DestructiveHint != nil && *ti.Annotations.DestructiveHint
	}
	return false, false
}

func snapshotDestructive(t *testing.T, rt *Runtime, tool string) bool {
	t.Helper()
	for _, tl := range rt.lastGoodToolsSnapshot("inv") {
		if config.RawToolName(tl) == tool {
			return tl.Annotations != nil && tl.Annotations.DestructiveHint != nil && *tl.Annotations.DestructiveHint
		}
	}
	t.Fatalf("tool %s missing from the last-good snapshot", tool)
	return false
}

// US3 scenario 1 (failing-first): capture C1 lists t read-only and is parked
// before its apply; the upstream turns t destructive and capture C2 records
// that (t held as changed, not indexed, StateView destructive). When C1
// resumes it must write nothing. On main C1 is applied: its read-only
// definition matches the approved contract, so the changed hold is "reverted"
// to approved, t is re-indexed and the StateView is republished read-only,
// which lets a read-scoped caller dispatch a destructive tool.
func TestInventoryOrder_OlderCaptureCannotRevertNewerHold(t *testing.T) {
	up := newInventoryUpstream(t)
	rt := newInventoryRuntime(t, up)
	ctx := context.Background()

	require.NoError(t, rt.discoverAndIndexToolsForServer(ctx, "inv", false))
	require.Equal(t, storage.ToolApprovalStatusApproved, invRecord(t, rt, "t").Status, "baseline")
	require.Contains(t, invIndexed(t, rt), "t")

	parked, release := parkFirstAfterList(rt)
	c1 := make(chan error, 1)
	go func() { c1 <- rt.discoverAndIndexToolsForServer(ctx, "inv", false) }()
	spec116Await(t, parked, "capture C1 (t read-only) to return from its tools/list")

	up.destructive.Store(true)
	require.NoError(t, rt.discoverAndIndexToolsForServer(ctx, "inv", false), "capture C2")
	require.Equal(t, storage.ToolApprovalStatusChanged, invRecord(t, rt, "t").Status, "C2 holds the destructive t")
	require.NotContains(t, invIndexed(t, rt), "t")

	close(release)
	require.NoError(t, spec116AwaitErr(t, c1, "capture C1"))

	rec := invRecord(t, rt, "t")
	require.NotNil(t, rec)
	assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status, "the older capture must not revert the newer hold")
	assert.NotContains(t, invIndexed(t, rt), "t", "the older capture must not re-index the held tool")
	assert.True(t, snapshotDestructive(t, rt, "t"), "the last-good snapshot keeps the newer capture")
	if found, destructive := invStateViewDestructive(t, rt, "t"); found {
		assert.True(t, destructive, "dispatch metadata keeps the newer capture's hints")
	}
}

// US3 scenario 3 (failing-first): an older capture listing x reaches the
// apply after a newer capture without x removed it: x's record is neither
// resurrected nor indexed. On main the older capture re-files x.
func TestInventoryOrder_OlderCaptureCannotResurrectRemovedTool(t *testing.T) {
	up := newInventoryUpstream(t)
	up.withX.Store(true)
	rt := newInventoryRuntime(t, up)
	ctx := context.Background()

	require.NoError(t, rt.discoverAndIndexToolsForServer(ctx, "inv", false))
	require.NotNil(t, invRecord(t, rt, "x"))
	require.Contains(t, invIndexed(t, rt), "x")

	parked, release := parkFirstAfterList(rt)
	c1 := make(chan error, 1)
	go func() { c1 <- rt.discoverAndIndexToolsForServer(ctx, "inv", false) }()
	spec116Await(t, parked, "capture C1 (with x) to return from its tools/list")

	up.withX.Store(false)
	require.NoError(t, rt.discoverAndIndexToolsForServer(ctx, "inv", true), "capture C2 without x")
	require.Nil(t, invRecord(t, rt, "x"), "C2 removed x")

	close(release)
	require.NoError(t, spec116AwaitErr(t, c1, "capture C1"))

	assert.Nil(t, invRecord(t, rt, "x"), "the older capture must not resurrect x's record")
	assert.NotContains(t, invIndexed(t, rt), "x", "the older capture must not re-index x")
	for _, tl := range rt.lastGoodToolsSnapshot("inv") {
		assert.NotEqual(t, "x", config.RawToolName(tl), "the last-good snapshot keeps the newer capture")
	}
}

// FR-033a (5e, failing-first): a newer lenient-empty result is an observation
// fence. An older non-empty capture that lists a new tool x and resumes after
// it writes nothing. On main it files x.
func TestInventoryOrder_LenientEmptyFencesOlderCapture(t *testing.T) {
	up := newInventoryUpstream(t)
	rt := newInventoryRuntime(t, up)
	ctx := context.Background()

	require.NoError(t, rt.discoverAndIndexToolsForServer(ctx, "inv", false))
	require.Nil(t, invRecord(t, rt, "x"))

	up.withX.Store(true)
	parked, release := parkFirstAfterList(rt)
	c1 := make(chan error, 1)
	go func() { c1 <- rt.discoverAndIndexToolsForServer(ctx, "inv", false) }()
	spec116Await(t, parked, "capture C1 (with x) to return from its tools/list")

	up.empty.Store(true)
	require.NoError(t, rt.discoverAndIndexToolsForServer(ctx, "inv", false), "lenient-empty C2")

	close(release)
	require.NoError(t, spec116AwaitErr(t, c1, "capture C1"))

	assert.Nil(t, invRecord(t, rt, "x"), "the older capture is fenced by the newer empty observation")
	assert.NotContains(t, invIndexed(t, rt), "x")
	assert.Contains(t, invIndexed(t, rt), "t", "the lenient empty result keeps the retained inventory")
}

// FR-037 (6b, failing-first): after a reconnect the retained snapshot no
// longer describes the live connection. The approval reindex must re-list
// instead of re-applying it. Here the upstream turned t destructive while the
// connection was replaced; on main the reindex re-applies the old read-only
// snapshot and t stays approved and indexed.
func TestInventoryOrder_ReindexAfterReconnectRelists(t *testing.T) {
	up := newInventoryUpstream(t)
	rt := newInventoryRuntime(t, up)
	ctx := context.Background()

	require.NoError(t, rt.discoverAndIndexToolsForServer(ctx, "inv", false))
	require.Equal(t, storage.ToolApprovalStatusApproved, invRecord(t, rt, "t").Status)

	c, ok := rt.upstreamManager.GetClient("inv")
	require.True(t, ok)
	require.NoError(t, c.Disconnect())
	up.destructive.Store(true)
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(cctx))
	require.Eventually(t, c.IsConnected, 10*time.Second, 5*time.Millisecond)

	rt.reindexServerToolsAfterApprovalChange("inv")

	rec := invRecord(t, rt, "t")
	require.NotNil(t, rec)
	assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status, "the reindex re-lists the reconnected server")
	assert.NotContains(t, invIndexed(t, rt), "t")
}

// parkFirstBeforeApply parks the first inventory write that reaches the
// capture-order gate (inventoryBeforeApplyLock) until release is closed.
func parkFirstBeforeApply(rt *Runtime) (parked, release chan struct{}) {
	parked, release = make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	rt.testHooks.inventoryBeforeApplyLock = func(string, uint64) {
		if first.CompareAndSwap(false, true) {
			close(parked)
			<-release
		}
	}
	return parked, release
}

// FR-031: the sweep applies each server's result with that server's own
// capture. A sweep result parked before its apply loses to a newer
// single-server capture and writes nothing.
func TestInventoryOrder_SweepResultOlderThanReactiveCaptureIsDropped(t *testing.T) {
	up := newInventoryUpstream(t)
	rt := newInventoryRuntime(t, up)
	ctx := context.Background()

	require.NoError(t, rt.discoverAndIndexToolsForServer(ctx, "inv", false))
	require.Equal(t, storage.ToolApprovalStatusApproved, invRecord(t, rt, "t").Status)

	parked, release := parkFirstBeforeApply(rt)
	sweep := make(chan error, 1)
	go func() { sweep <- rt.DiscoverAndIndexTools(ctx) }()
	spec116Await(t, parked, "the sweep's capture (t read-only) to reach its apply")

	up.destructive.Store(true)
	require.NoError(t, rt.discoverAndIndexToolsForServer(ctx, "inv", false))
	require.Equal(t, storage.ToolApprovalStatusChanged, invRecord(t, rt, "t").Status)

	close(release)
	require.NoError(t, spec116AwaitErr(t, sweep, "the sweep"))

	assert.Equal(t, storage.ToolApprovalStatusChanged, invRecord(t, rt, "t").Status, "the older sweep result must not revert the hold")
	assert.NotContains(t, invIndexed(t, rt), "t")
	assert.True(t, snapshotDestructive(t, rt, "t"))
}

// Edge case "capture sequence 0": an unordered inventory (no managed client)
// is applied only while no ordered capture was applied for the server.
func TestInventoryOrder_UnorderedCaptureOnlyBeforeOrdered(t *testing.T) {
	up := newInventoryUpstream(t)
	rt := newInventoryRuntime(t, up)
	ctx := context.Background()

	y := &config.ToolMetadata{ServerName: "inv", Name: "y", Description: "tool y", ParamsJSON: `{"type":"object"}`}
	_, err := rt.commitDiscoveredTools(ctx, "inv", []*config.ToolMetadata{y}, 0)
	require.NoError(t, err)
	require.NotNil(t, invRecord(t, rt, "y"), "an unordered inventory applies while nothing ordered was applied")

	require.NoError(t, rt.discoverAndIndexToolsForServer(ctx, "inv", true))
	require.Nil(t, invRecord(t, rt, "y"), "the ordered authoritative capture removed y")

	_, err = rt.commitDiscoveredTools(ctx, "inv", []*config.ToolMetadata{y}, 0)
	require.NoError(t, err)
	assert.Nil(t, invRecord(t, rt, "y"), "an unordered inventory never applies after an ordered one")
}

// FR-036, US3 scenario 6: an inventory captured by a client the manager
// retired (endpoint replacement, removal) writes nothing and is reported as
// not published, so the caller re-lists the current client.
func TestInventoryOrder_RetiredClientCaptureIsDiscarded(t *testing.T) {
	up := newInventoryUpstream(t)
	rt := newInventoryRuntime(t, up)
	ctx := context.Background()
	require.NoError(t, rt.discoverAndIndexToolsForServer(ctx, "inv", false))

	up.withX.Store(true)
	c, ok := rt.upstreamManager.GetClient("inv")
	require.True(t, ok)
	tools, inv, err := c.ListToolsCaptured(ctx)
	require.NoError(t, err)
	require.NotZero(t, inv.Seq)
	c.Retire()

	gen := rt.discoveryGeneration("inv")
	eligible, published, err := rt.commitInventoryAt(ctx, "inv", tools, c.Generation(), &gen, inventoryRef{capture: inv})
	require.NoError(t, err)
	assert.True(t, eligible)
	assert.False(t, published, "a retired client's capture is not published; the caller re-lists")
	assert.Nil(t, invRecord(t, rt, "x"), "nothing of the retired client's inventory is recorded")
	assert.NotContains(t, invIndexed(t, rt), "x")
}

// The quarantined review capture is ordered too: an older capture that
// reaches its persist after a newer one records nothing.
func TestInventoryOrder_QuarantinedReviewCaptureInOrder(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, explicitServer(&config.ServerConfig{
		Name: "q", Command: "true", Protocol: "stdio", Enabled: true, Quarantined: true,
	}))
	a := &config.ToolMetadata{ServerName: "q", Name: "a", Description: "tool a", ParamsJSON: `{"type":"object"}`}
	b := &config.ToolMetadata{ServerName: "q", Name: "b", Description: "tool b", ParamsJSON: `{"type":"object"}`}

	ok, err := rt.persistInventoryDefinitions("q", []*config.ToolMetadata{a}, 0, managed.InventoryCapture{Seq: 200})
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = rt.persistInventoryDefinitions("q", []*config.ToolMetadata{a, b}, 0, managed.InventoryCapture{Seq: 100})
	require.NoError(t, err)
	assert.False(t, ok, "the older review capture is not persisted")
	_, err = rt.storageManager.GetToolApproval("q", "b")
	assert.Error(t, err, "the older capture's extra tool is not recorded")
	snap := rt.lastGoodToolsSnapshot("q")
	require.Len(t, snap, 1, "the last-good snapshot keeps the newer capture")

	ok, err = rt.persistInventoryDefinitions("q", []*config.ToolMetadata{a, b}, 0, managed.InventoryCapture{Seq: 200})
	require.NoError(t, err)
	assert.True(t, ok, "re-applying the same capture is allowed")
}

// US3 scenario 2 (regression since Spec 116 D15, kept by PR-3): a pass that
// decided t is held keeps the approval lock through its index writes, so an
// operator approval of t waits for it and t ends indexed and approved.
func TestInventoryOrder_ApprovalDuringPausedPassEndsIndexed(t *testing.T) {
	up := newInventoryUpstream(t)
	rt := newInventoryRuntime(t, up)
	ctx := context.Background()
	require.NoError(t, rt.discoverAndIndexToolsForServer(ctx, "inv", false))
	up.withX.Store(true)

	parked, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	rt.testHooks.discoveryBeforeIndex = func(string) {
		if first.CompareAndSwap(false, true) {
			close(parked)
			<-release
		}
	}
	pass := make(chan error, 1)
	go func() { pass <- rt.discoverAndIndexToolsForServer(ctx, "inv", false) }()
	spec116Await(t, parked, "the pass to decide x is held")
	require.Equal(t, storage.ToolApprovalStatusPending, invRecord(t, rt, "x").Status)

	approved := make(chan error, 1)
	go func() { approved <- rt.ApproveTools("inv", []string{"x"}, "operator") }()
	select {
	case err := <-approved:
		t.Fatalf("the approval completed while the pass held the lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	require.NoError(t, spec116AwaitErr(t, pass, "the pass"))
	require.NoError(t, spec116AwaitErr(t, approved, "the approval"))

	assert.Equal(t, storage.ToolApprovalStatusApproved, invRecord(t, rt, "x").Status)
	assert.Eventually(t, func() bool { _, ok := invIndexed(t, rt)["x"]; return ok },
		10*time.Second, 10*time.Millisecond, "the approval reindex indexes x")
}
