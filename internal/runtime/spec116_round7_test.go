package runtime

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// namedToolUpstream is a streamable-HTTP MCP upstream exposing one tool and
// counting tools/list requests.
type namedToolUpstream struct {
	srv       *httptest.Server
	listCalls atomic.Int32
}

func newNamedToolUpstream(t *testing.T, tool string) *namedToolUpstream {
	t.Helper()
	up := mcpserver.NewMCPServer("named-"+tool, "0.0.1", mcpserver.WithToolCapabilities(true))
	up.AddTool(mcp.NewTool(tool, mcp.WithDescription("tool "+tool)), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	h := mcpserver.NewStreamableHTTPServer(up)
	u := &namedToolUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if bytes.Contains(body, []byte(`"tools/list"`)) {
			u.listCalls.Add(1)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

// Review round 7, finding A (D15): a single-server discovery whose tools/list
// outlived an endpoint-changing commit is rejected by the generation check.
// The replacement instance's own discovery was deduped (the reactive
// connect callback skips a server whose discovery is in progress), so the
// rejected pass must re-list the CURRENT instance itself: index, last-good
// snapshot and StateView carry the new endpoint's tools before any sweep.
// Removal, quarantine and disable stay terminal (no re-list).
func TestCommit_R7_StaleGenerationDiscoveryRelistsCurrentInstance(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	oldUp := newNamedToolUpstream(t, "old_tool")
	newUp := newNamedToolUpstream(t, "new_tool")
	rt, _ := newSpec116Runtime(t, nil, explicitServer(&config.ServerConfig{
		Name: "ep", URL: oldUp.srv.URL, Protocol: "streamable-http", Enabled: true,
	}))
	require.NoError(t, rt.upstreamManager.AddServerAt("ep", spec116Live(rt, "ep"), storedStamp(t, rt, "ep").Stamp.Gen))
	oldClient, ok := rt.upstreamManager.GetClient("ep")
	require.True(t, ok)
	require.Eventually(t, oldClient.IsConnected, 10*time.Second, 5*time.Millisecond)

	parked := make(chan struct{})
	release := make(chan struct{})
	var first atomic.Bool
	rt.testHooks.discoveryAfterList = func(string) {
		if first.CompareAndSwap(false, true) {
			close(parked)
			<-release
		}
	}
	// The reactive discovery marker is held for the whole pass, as the
	// connect callback does: the replacement's own discovery is deduped.
	rt.discoveryInProgress.Store("ep", struct{}{})
	done := make(chan error, 1)
	go func() {
		defer rt.discoveryInProgress.Delete("ep")
		done <- rt.discoverAndIndexToolsForServer(context.Background(), "ep", false)
	}()
	spec116Await(t, parked, "the old instance's tools/list to return")

	res, err := rt.Commit("endpoint_change", func(tx *CommitTx) error {
		return tx.UpdateServer("ep", func(cur *config.ServerConfig) error {
			cur.URL = newUp.srv.URL
			return nil
		})
	})
	require.NoError(t, err)
	require.True(t, res.Changes["ep"].Reconnect)
	var newClient interface {
		IsConnected() bool
		Generation() int64
	}
	require.Eventually(t, func() bool {
		c, ok := rt.upstreamManager.GetClient("ep")
		if !ok || c == oldClient || c.Generation() != res.Generation || !c.IsConnected() {
			return false
		}
		newClient = c
		return true
	}, 10*time.Second, 5*time.Millisecond, "the replacement instance must connect")
	require.NotNil(t, newClient)
	listsBefore := newUp.listCalls.Load()

	close(release)
	require.NoError(t, spec116AwaitErr(t, done, "the discovery pass"))

	assert.Greater(t, newUp.listCalls.Load(), listsBefore, "the rejected pass must re-list the current instance")
	indexed, err := rt.indexManager.GetToolsByServer("ep")
	require.NoError(t, err)
	names := make([]string, 0, len(indexed))
	for _, tl := range indexed {
		names = append(names, config.RawToolName(tl))
	}
	assert.Equal(t, []string{"new_tool"}, names, "the index carries the current endpoint's tools")
	snap := rt.lastGoodToolsSnapshot("ep")
	require.Len(t, snap, 1)
	assert.Equal(t, "new_tool", config.RawToolName(snap[0]))
	st, ok := rt.supervisor.StateView().GetServer("ep")
	require.True(t, ok)
	require.Len(t, st.Tools, 1, "the StateView carries the current endpoint's tools")
}

// Review round 7, finding A: the D15 rejection reports a generation mismatch
// as eligible-but-unpublished (re-list), and an ineligible row (quarantined,
// disabled, removed) as ineligible (terminal).
func TestCommit_R7_D15VerdictSeparatesStaleFromIneligible(t *testing.T) {
	rt, _ := newSpec116Runtime(t, nil,
		explicitServer(&config.ServerConfig{Name: "s", Command: "true", Protocol: "stdio", Enabled: true}),
	)
	tools := []*config.ToolMetadata{{ServerName: "s", Name: "t", ParamsJSON: `{"type":"object"}`, Hash: "h"}}
	oldGen := storedStamp(t, rt, "s").Stamp.Gen
	_, err := rt.Commit("bump", func(tx *CommitTx) error {
		return tx.UpdateServer("s", func(cur *config.ServerConfig) error { cur.Args = []string{"v2"}; return nil })
	})
	require.NoError(t, err)
	capture := rt.discoveryGeneration("s")
	eligible, published, err := rt.commitDiscoveredToolsAt(context.Background(), "s", tools, oldGen, &capture)
	require.NoError(t, err)
	assert.True(t, eligible, "a generation mismatch is not terminal")
	assert.False(t, published, "a generation mismatch must be re-listed")

	curGen := storedStamp(t, rt, "s").Stamp.Gen
	_, err = rt.Commit("quarantine", func(tx *CommitTx) error {
		return tx.UpdateServer("s", func(cur *config.ServerConfig) error { cur.Quarantined = true; return nil })
	})
	require.NoError(t, err)
	eligible, published, err = rt.commitDiscoveredToolsAt(context.Background(), "s", tools, curGen, &capture)
	require.NoError(t, err)
	assert.False(t, eligible, "quarantine is terminal")
	assert.False(t, published)
}
