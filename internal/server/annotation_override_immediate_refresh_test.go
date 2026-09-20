package server

// Regression guard: PATCH annotation_overrides must converge on the NEXT GET.
//
// Root cause: GET /api/v1/servers/{id}/tools is served from the StateView
// snapshot (Runtime.GetServerTools, internal/runtime/runtime.go), which is
// refreshed only by discovery. UpdateServer pushed the merged overrides to
// the live client via SetConfig (hot for the NEXT ListTools) but returned
// PATCH 200 before the background sweep (OnUpstreamServerChange → async
// DiscoverAndIndexTools) re-listed, so the next GET raced one re-list (~10s
// of stale hints). The fix re-lists THAT server synchronously inside
// UpdateServer when the overrides actually changed, reusing the author's
// existing authoritative hook (Runtime.RefreshServerTools) — no fleet sweep,
// no reconnect, no restart.
//
// The test below fails before the fix (served annotations stay stale until
// the background sweep lands) and passes after (synchronous convergence).
// Every assertion after UpdateServer returns is IMMEDIATE — no sleep, no
// Eventually, no clock advance. A secondary delete half proves the same for
// RFC7396 whole-tool delete ({"navigate":null}).

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/mark3labs/mcp-go/server/servertest"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// servedHints returns the (destructiveHint, readOnlyHint) the production GET
// path would serve for the named tool (Runtime.GetServerTools → StateView
// snapshot). The snapshot carries *config.ToolAnnotations, not JSON maps.
func servedHints(t *testing.T, rt *runtime.Runtime, server, tool string) (destructive, readOnly *bool) {
	t.Helper()
	tools, err := rt.GetServerTools(server)
	require.NoError(t, err)
	for _, tm := range tools {
		if tm["name"] == tool {
			ann, ok := tm["annotations"].(*config.ToolAnnotations)
			require.True(t, ok, "served annotations must be *config.ToolAnnotations, got %T", tm["annotations"])
			if ann == nil {
				return nil, nil
			}
			return ann.DestructiveHint, ann.ReadOnlyHint
		}
	}
	t.Fatalf("tool %q not served for server %q", tool, server)
	return nil, nil
}

func boolVal(b *bool) interface{} {
	if b == nil {
		return nil
	}
	return *b
}

// servedAnnotations is the map form the existing test body expects (keys
// "destructiveHint"/"readOnlyHint" etc). Kept for the test assertions that
// compare via map lookup; built from the same StateView snapshot as servedHints.
func servedAnnotations(t *testing.T, rt *runtime.Runtime, server, tool string) map[string]interface{} {
	t.Helper()
	tools, err := rt.GetServerTools(server)
	require.NoError(t, err)
	for _, tm := range tools {
		if tm["name"] == tool {
			out := map[string]interface{}{}
			if ann, ok := tm["annotations"].(*config.ToolAnnotations); ok && ann != nil {
				if ann.DestructiveHint != nil {
					out["destructiveHint"] = *ann.DestructiveHint
				}
				if ann.ReadOnlyHint != nil {
					out["readOnlyHint"] = *ann.ReadOnlyHint
				}
				if ann.IdempotentHint != nil {
					out["idempotentHint"] = *ann.IdempotentHint
				}
				if ann.OpenWorldHint != nil {
					out["openWorldHint"] = *ann.OpenWorldHint
				}
				if ann.Title != "" {
					out["title"] = ann.Title
				}
			}
			return out
		}
	}
	t.Fatalf("tool %q not served for server %q", tool, server)
	return nil
}

func TestUpdateServer_AnnotationOverrides_RefreshStateViewImmediately(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	logger := zap.NewNop()
	ctx := context.Background()

	// Stub upstream: "navigate" carries destructive upstream truth (the
	// browseros shape), "snapshot" is a read-only control with no override.
	stub := mcpserver.NewMCPServer("ao-srv", "0.0.1-test", mcpserver.WithToolCapabilities(true))
	okHandler := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}
	stub.AddTool(mcp.Tool{
		Name: "navigate", Description: "Navigate somewhere",
		InputSchema: mcp.ToolInputSchema{Type: "object"},
		Annotations: mcp.ToolAnnotation{DestructiveHint: boolPtr(true)},
	}, okHandler)
	stub.AddTool(mcp.Tool{
		Name: "snapshot", Description: "Take a snapshot",
		InputSchema: mcp.ToolInputSchema{Type: "object"},
		Annotations: mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)},
	}, okHandler)
	httpSrv := servertest.NewTestStreamableHTTPServer(stub)
	t.Cleanup(httpSrv.Close)

	serverCfg := &config.ServerConfig{Name: "ao-srv", URL: httpSrv.URL, Protocol: "streamable-http", Enabled: true}
	rt, err := runtime.New(&config.Config{
		DataDir: t.TempDir(), Listen: "127.0.0.1:0",
		Servers: []*config.ServerConfig{serverCfg},
	}, "", logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	// UpdateServer resolves the existing record from storage.
	require.NoError(t, rt.StorageManager().SaveUpstreamServer(serverCfg))
	require.NoError(t, rt.UpstreamManager().AddServerConfig("ao-srv", serverCfg))
	require.NoError(t, rt.UpstreamManager().ConnectAll(ctx))
	require.Eventually(t, func() bool {
		client, ok := rt.UpstreamManager().GetClient("ao-srv")
		return ok && client.IsConnected()
	}, 10*time.Second, 50*time.Millisecond, "stub upstream must connect")

	// Seed the served snapshot with upstream truth.
	require.NoError(t, rt.RefreshServerTools(ctx, "ao-srv"))
	require.Equal(t, true, servedAnnotations(t, rt, "ao-srv", "navigate")["destructiveHint"],
		"precondition: upstream truth is destructive")

	mainSrv := &Server{runtime: rt, logger: logger}

	// SET: operator overrides navigate to non-destructive, read-only.
	setUpdates := &config.ServerConfig{Name: "ao-srv", Enabled: true,
		AnnotationOverrides: map[string]*config.ToolAnnotations{
			"navigate": {DestructiveHint: boolPtr(false), ReadOnlyHint: boolPtr(true)},
		},
	}
	require.NoError(t, mainSrv.UpdateServer(ctx, "ao-srv", setUpdates))

	// IMMEDIATE: no sleep, no tick — the next GET must already be effective.
	// Before the fix this still shows upstream destructive:true until the
	// background sweep lands (~one re-list later).
	got := servedAnnotations(t, rt, "ao-srv", "navigate")
	require.Equal(t, false, got["destructiveHint"], "override must be effective on the next GET")
	require.Equal(t, true, got["readOnlyHint"], "override must be effective on the next GET")
	// The un-overridden control tool still serves upstream truth.
	require.Equal(t, true, servedAnnotations(t, rt, "ao-srv", "snapshot")["readOnlyHint"])

	// DELETE: PATCH {"annotation_overrides":{"navigate":null}} with the
	// per-tool remove marker merges to nil; the REST handler persists the
	// empty non-nil sentinel (see handlePatchServer) so the clear survives.
	// Replicate that exact merge here.
	delOpts := config.DefaultMergeOptions().WithRemoveMarker("annotation_overrides.navigate")
	merged := config.MergeAnnotationOverrides(
		setUpdates.AnnotationOverrides,
		map[string]*config.ToolAnnotations{"navigate": nil},
		delOpts,
	)
	require.Nil(t, merged, "merge reports delete-to-empty as nil")
	if merged == nil {
		merged = make(map[string]*config.ToolAnnotations)
	}
	require.NoError(t, mainSrv.UpdateServer(ctx, "ao-srv",
		&config.ServerConfig{Name: "ao-srv", Enabled: true, AnnotationOverrides: merged}))

	// IMMEDIATE: upstream truth is back on the next GET.
	back := servedAnnotations(t, rt, "ao-srv", "navigate")
	require.Equal(t, true, back["destructiveHint"], "deleted override must revert to upstream truth immediately")
	require.Nil(t, back["readOnlyHint"], "deleted override hint must be gone")
}
