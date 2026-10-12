package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/headerfwd"
)

// Spec 116 PR1, review round 1 (Sol) regressions.

// R1.1: a commit whose stored view cannot be read aborts. Disk, the live
// snapshot, its generation and storage stay as they were (on main,
// EnableServer returned the storage error).
func TestCommit_R1_StoredViewReadFailureAborts(t *testing.T) {
	rt, cfgPath := newSpec116Runtime(t, nil, disabledStdio("a"))
	diskBefore, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	verBefore := rt.ConfigSnapshot().Version
	rowBefore := storedStamp(t, rt, "a")

	rt.testHooks.storedReadFault = func(string) error { return errors.New("injected read failure") }
	err = rt.EnableServer("a", true)
	rt.testHooks.storedReadFault = nil
	require.Error(t, err, "a commit that cannot read the stored view must fail")

	diskAfter, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(diskBefore, diskAfter), "the file must not be written")
	assert.Equal(t, verBefore, rt.ConfigSnapshot().Version, "nothing must be published")
	assert.False(t, spec116Live(rt, "a").Enabled, "the live config keeps the old value")
	rowAfter := storedStamp(t, rt, "a")
	assert.Equal(t, rowBefore.Stamp, rowAfter.Stamp)
	assert.False(t, rowAfter.Config.Enabled)
}

// R1.2: the restart refresh imports one server from the file and must not
// rewrite the file: an unreloaded external edit of another server or of a
// global field stays on disk for the watcher to apply.
func TestCommit_R1_RestartRefreshDoesNotRewriteFile(t *testing.T) {
	rt, cfgPath := newSpec116Runtime(t, nil, disabledStdio("a"), disabledStdio("b"))

	edited, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	for _, sc := range edited.Servers {
		switch sc.Name {
		case "a":
			sc.Args = []string{"from-disk-a"}
		case "b":
			sc.Args = []string{"from-disk-b"}
		}
	}
	edited.Registries = append(edited.Registries, config.RegistryEntry{ID: "acme", Name: "Acme", URL: "https://acme.example"})
	require.NoError(t, config.SaveConfig(edited, cfgPath))
	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	got, err := rt.refreshServerFromDiskForRestart("a")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, []string{"from-disk-a"}, got.Args, "the restarted server takes its file entry")
	assert.Nil(t, spec116Live(rt, "b").Args, "the other server's edit waits for the reload")

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(before, after), "the file must keep the unreloaded external edit")
}

// R1.3: a reload that changes nothing hot but cancels a pending
// restart-required change updates the desired configuration, so the next
// write does not re-persist the cancelled value.
func TestCommit_R1_NoopReloadUpdatesDesired(t *testing.T) {
	rt, cfgPath := newSpec116Runtime(t, func(c *config.Config) {
		c.RoutingMode = config.RoutingModeRetrieveTools
	}, disabledStdio("a"))

	pending := cloneRuntimeConfig(t, rt.Config())
	pending.RoutingMode = config.RoutingModeDirect
	res, err := rt.ApplyConfig(pending, cfgPath)
	require.NoError(t, err)
	require.True(t, res.RequiresRestart)

	// The operator reverts the pending change by hand.
	onDisk, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	onDisk.RoutingMode = config.RoutingModeRetrieveTools
	require.NoError(t, config.SaveConfig(onDisk, cfgPath))
	require.NoError(t, rt.ReloadConfiguration())

	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	assert.Equal(t, config.RoutingModeRetrieveTools, desired.RoutingMode, "the reload must cancel the pending change")

	require.NoError(t, rt.EnableServer("a", true))
	final, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, config.RoutingModeRetrieveTools, final.RoutingMode, "the next write must not resurrect the cancelled change")
}

// R1.4: a global-only commit publishes a new generation and records it in
// storage, so the generation stays monotonic across a restart.
func TestCommit_R1_GlobalOnlyCommitRecordsGeneration(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil, disabledStdio("a"))
	res, err := rt.Commit("global_only", func(tx *CommitTx) error {
		return tx.MutateGlobal(func(cfg *config.Config) error {
			cfg.Registries = append(cfg.Registries, config.RegistryEntry{ID: "acme", Name: "Acme", URL: "https://acme.example"})
			return nil
		})
	})
	require.NoError(t, err)
	require.True(t, res.Published)
	seed, committed, err := rt.storageManager.ConfigCommitState()
	require.NoError(t, err)
	require.True(t, committed)
	assert.Equal(t, res.Generation, seed, "meta.config_commit_generation must follow every published generation")
}

// R1.5: a commit that changes a connection field outside the manager's old
// eight-field comparison (isolation, OAuth, working_dir, init timeout)
// replaces the running instance instead of updating it in place.
func TestCommit_R1_ConnectionFieldChangeReplacesInstance(t *testing.T) {
	cases := map[string]func(*config.ServerConfig){
		"working_dir":  func(sc *config.ServerConfig) { sc.WorkingDir = "/tmp" },
		"oauth":        func(sc *config.ServerConfig) { sc.OAuth = &config.OAuthConfig{ClientID: "new-client"} },
		"isolation":    func(sc *config.ServerConfig) { sc.Isolation = &config.IsolationConfig{Image: "alpine:3"} },
		"init_timeout": func(sc *config.ServerConfig) { d := config.Duration(7 * time.Second); sc.InitTimeout = &d },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rt, _ := newSpec116Runtime(t, nil, unreachableHTTP("u"))
			require.NoError(t, rt.upstreamManager.AddServerConfigAt("u", spec116Live(rt, "u"), storedStamp(t, rt, "u").Stamp.Gen))
			before, ok := rt.upstreamManager.GetClient("u")
			require.True(t, ok)

			res, err := rt.Commit("patch_connection_field", func(tx *CommitTx) error {
				return tx.UpdateServer("u", func(cur *config.ServerConfig) error {
					mutate(cur)
					return nil
				})
			})
			require.NoError(t, err)
			require.True(t, res.Changes["u"].Reconnect, "%s is a connection field", name)
			require.Eventually(t, func() bool {
				after, ok := rt.upstreamManager.GetClient("u")
				return ok && after != before && after.Generation() == res.Generation
			}, 10*time.Second, 5*time.Millisecond, "the running instance must be replaced for a %s change", name)
		})
	}
}

// R1.8 (FR-008a, D15): a discovery result from an older generation writes
// nothing: not the index, not the last-good snapshot, not the StateView.
func TestCommit_R1_StaleDiscoveryWritesNoLastGoodOrStateView(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil,
		explicitServer(&config.ServerConfig{Name: "omega", Command: "true", Protocol: "stdio", Enabled: true}),
	)
	oldGen := storedStamp(t, rt, "omega").Stamp.Gen
	_, err := rt.Commit("bump", func(tx *CommitTx) error {
		return tx.UpdateServer("omega", func(cur *config.ServerConfig) error {
			cur.Args = []string{"v2"}
			return nil
		})
	})
	require.NoError(t, err)
	require.NotEqual(t, oldGen, storedStamp(t, rt, "omega").Stamp.Gen)

	tools := []*config.ToolMetadata{
		{ServerName: "omega", Name: "stale_tool", Description: "from the old instance", ParamsJSON: `{"type":"object"}`, Hash: "hs"},
	}
	// A generation mismatch reports eligible (the caller re-lists the current
	// instance, review round 7) but writes nothing.
	eligible, err := rt.commitDiscoveredTools(context.Background(), "omega", tools, oldGen)
	require.NoError(t, err)
	assert.True(t, eligible)
	assert.Nil(t, rt.lastGoodToolsSnapshot("omega"), "a rejected pass must not overwrite the last-good snapshot")
	capture := rt.discoveryGeneration("omega")
	eligible, published, err := rt.commitDiscoveredToolsAt(context.Background(), "omega", tools, oldGen, &capture)
	require.NoError(t, err)
	assert.True(t, eligible)
	assert.False(t, published, "a rejected pass is not published; the caller re-lists")
	assert.Nil(t, rt.lastGoodToolsSnapshot("omega"))
	if st, ok := rt.supervisor.StateView().GetServer("omega"); ok && st != nil {
		assert.Empty(t, st.Tools, "a rejected pass must not publish into the StateView")
		assert.False(t, st.ToolsDiscovered)
	}
	indexed, err := rt.indexManager.GetToolsByServer("omega")
	require.NoError(t, err)
	assert.Empty(t, indexed)

	// The current generation is accepted.
	curGen := storedStamp(t, rt, "omega").Stamp.Gen
	eligible, published, err = rt.commitDiscoveredToolsAt(context.Background(), "omega", tools, curGen, &capture)
	require.NoError(t, err)
	assert.True(t, eligible)
	assert.True(t, published)
	assert.Len(t, rt.lastGoodToolsSnapshot("omega"), 1)
	st, ok := rt.supervisor.StateView().GetServer("omega")
	require.True(t, ok)
	assert.Len(t, st.Tools, 1, "the accepted pass publishes into the StateView")
}

// headerEchoUpstream is a streamable-HTTP MCP upstream that records the
// X-Tenant header of every tools/call and counts tools/list requests.
type headerEchoUpstream struct {
	srv       *httptest.Server
	mu        sync.Mutex
	tenants   []string
	listCalls atomic.Int32
}

func newHeaderEchoUpstream(t *testing.T) *headerEchoUpstream {
	t.Helper()
	up := mcpserver.NewMCPServer("hdr-echo", "0.0.1", mcpserver.WithToolCapabilities(true), mcpserver.WithPromptCapabilities(true))
	up.AddTool(mcp.NewTool("echo", mcp.WithDescription("echo")), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	up.AddPrompt(mcp.NewPrompt("greet", mcp.WithPromptDescription("greet")), func(context.Context, mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return mcp.NewGetPromptResult("greet", nil), nil
	})
	h := mcpserver.NewStreamableHTTPServer(up)
	e := &headerEchoUpstream{}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if bytes.Contains(body, []byte(`"tools/call"`)) {
			e.mu.Lock()
			e.tenants = append(e.tenants, r.Header.Get("X-Tenant"))
			e.mu.Unlock()
		}
		if bytes.Contains(body, []byte(`"tools/list"`)) {
			e.listCalls.Add(1)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *headerEchoUpstream) lastTenant() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.tenants) == 0 {
		return "<none>"
	}
	return e.tenants[len(e.tenants)-1]
}

// R1.11 (T1.19 on a connected upstream): a forward_headers revocation and an
// expose_prompts change reach the CONNECTED retained instance before the
// commit returns (the next call no longer forwards the header, prompts are
// hidden), with no reconnect, and the commit requests one rediscovery pass
// that D15 accepts.
func TestCommit_R1_ConnectedRetainedInstanceGetsNewConfig(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	for _, stub := range []bool{false, true} {
		t.Run(map[bool]string{false: "pipeline", true: "step7_stubbed"}[stub], func(t *testing.T) {
			connectedRetainedInstance(t, stub)
		})
	}
}

func connectedRetainedInstance(t *testing.T, stub bool) {
	echo := newHeaderEchoUpstream(t)
	sc := explicitServer(&config.ServerConfig{
		Name: "hdr", URL: echo.srv.URL, Protocol: "streamable-http", Enabled: true,
		ForwardHeaders: []string{"X-Tenant"},
	})
	rt, _ := newSpec116Runtime(t, nil, sc)
	require.NoError(t, rt.upstreamManager.AddServerAt("hdr", spec116Live(rt, "hdr"), storedStamp(t, rt, "hdr").Stamp.Gen))
	client, ok := rt.upstreamManager.GetClient("hdr")
	require.True(t, ok)
	require.Eventually(t, client.IsConnected, 10*time.Second, 5*time.Millisecond)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("X-Tenant", "t1")
	callCtx := headerfwd.WithSnapshot(context.Background(), headerfwd.Capture(req, map[string]struct{}{"X-Tenant": {}}))
	_, err := client.CallTool(callCtx, "echo", map[string]interface{}{})
	require.NoError(t, err)
	require.Equal(t, "t1", echo.lastTenant(), "precondition: the allowlisted header is forwarded")
	prompts, err := client.ListPrompts(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, prompts, "precondition: prompts are exposed")

	listsBefore := echo.listCalls.Load()
	hide := false
	rt.testHooks.skipAdvance = stub
	res, err := rt.Commit("patch_forward_headers", func(tx *CommitTx) error {
		return tx.UpdateServer("hdr", func(cur *config.ServerConfig) error {
			cur.ForwardHeaders = []string{}
			cur.ExposePrompts = &hide
			return nil
		})
	})
	rt.testHooks.skipAdvance = false
	require.NoError(t, err)
	require.False(t, res.Changes["hdr"].Reconnect)

	after, ok := rt.upstreamManager.GetClient("hdr")
	require.True(t, ok)
	assert.Same(t, client, after, "no reconnect: the same instance is retained")
	assert.True(t, after.IsConnected())

	_, err = after.CallTool(callCtx, "echo", map[string]interface{}{})
	require.NoError(t, err)
	prompts, err = after.ListPrompts(context.Background())
	require.NoError(t, err)
	if stub {
		assert.Equal(t, "t1", echo.lastTenant(), "with step 7 stubbed out the revoked header is still forwarded")
		assert.NotEmpty(t, prompts, "and prompts stay exposed")
		assert.Empty(t, res.Restamped)
		return
	}
	assert.Equal(t, storedStamp(t, rt, "hdr").Stamp.Gen, after.Generation())
	assert.Equal(t, "", echo.lastTenant(), "the revoked header must not be forwarded on the next call")
	assert.Empty(t, prompts, "expose_prompts=false reaches the connected instance")

	require.Contains(t, res.Restamped, "hdr")
	require.Eventually(t, func() bool { return echo.listCalls.Load() > listsBefore }, 10*time.Second, 5*time.Millisecond,
		"the commit must request one rediscovery of the restamped connected server")
	require.Eventually(t, func() bool {
		indexed, err := rt.indexManager.GetToolsByServer("hdr")
		return err == nil && len(indexed) == 1
	}, 10*time.Second, 5*time.Millisecond, "D15 must accept the rediscovery at the restamped generation")
}
