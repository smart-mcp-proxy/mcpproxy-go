package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 105 shared test fixtures (tasks T001/T002). Every scope-hardening test
// builds its callers and its zero-upstream-call oracle from here so the
// agent-token rules are encoded once:
//
//   - AllowedServers: an EMPTY list is DENY-ALL under CanAccessServer /
//     serverInScope / callerVisibleServers (nothing matches). The unrestricted
//     token is spelled ["*"] — the wildcard entry is what every real token
//     carries — so a fixture that means "no server restriction" must pass
//     []string{"*"}, never nil. (preflight.ResolveScope alone treats an empty
//     list as unrestricted, which is exactly why fixtures must not rely on it.)
//   - Permissions: HasPermission is exact membership — "destructive" does not
//     imply "write" or "read"; pass every tier the token holds.
//   - ProfilePin: empty = unpinned. A pin naming a deleted profile resolves to
//     deny-all (profile_resolver.go), so a pinned fixture must create the
//     profile it pins.

// agentCtx returns a context authenticated as an agent token with the given
// allowed-server list, permission set and profile pin. Names are derived from
// the inputs so failure messages identify the fixture.
func agentCtx(allowed []string, perms []string, pin string) context.Context {
	return auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      fmt.Sprintf("agent(%v,%v,pin=%q)", allowed, perms, pin),
		TokenPrefix:    "mcp_agt_fix",
		AllowedServers: allowed,
		Permissions:    perms,
		ProfilePin:     pin,
	})
}

// adminCtx returns a context authenticated as an API-key administrator — the
// SC-005 parity control for every scoped computation (never the anonymous,
// admin-shaped context; use context.Background() for the nil-auth cell).
func adminCtx() context.Context {
	return auth.WithAuthContext(context.Background(), auth.AdminContext())
}

// toolSpec describes one tool a counting upstream exposes: its raw name (a
// namespaced "ns:erase" is passed through uncollapsed), its annotations (the
// tier oracle) and the approval record seeded for it in storage.
type toolSpec struct {
	Name        string
	Description string
	Annotations *config.ToolAnnotations
	// Approval is the storage.ToolApprovalStatus* seeded for the tool. The
	// zero value seeds an approved record (what startCountingTargetTierUpstream
	// always did); set NoRecord to seed nothing at all (the FR-009 "no record
	// under an active quarantine gate" cell).
	Approval string
	NoRecord bool
}

// readSpec / writeSpec / destructiveSpec build the three target tiers.
func readSpec(name string) toolSpec {
	return toolSpec{Name: name, Description: "Read " + name, Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}}
}

func writeSpec(name string) toolSpec {
	return toolSpec{Name: name, Description: "Write " + name, Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(false), DestructiveHint: boolPtr(false)}}
}

func destructiveSpec(name string) toolSpec {
	return toolSpec{Name: name, Description: "Destroy " + name, Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)}}
}

func (s toolSpec) info() stateview.ToolInfo {
	return stateview.ToolInfo{Name: s.Name, Description: s.Description, Annotations: s.Annotations}
}

// countingUpstream is a real in-process streamable-HTTP upstream wired into
// the proxy under Server, plus the invocation witness for every call that
// reaches it. Refusals are proven by count == 0, admissions by the count AND
// the raw name the call arrived under (dispatched()).
type countingUpstream struct {
	*upstreamCalls
	Server string
	URL    string
	Tools  []stateview.ToolInfo
	mcpSrv *mcpserver.MCPServer
}

// serve registers one more tool on the running stub upstream, for tests
// whose upstream lists a tool on a LATER discovery pass than the first (the
// Spec 105 "bare erase on pass 1, ns:erase on pass 2" cells). The next
// runtime discovery lists it; storage records are never seeded here.
func (u *countingUpstream) serve(spec toolSpec) {
	u.Tools = append(u.Tools, spec.info())
	u.mcpSrv.AddTool(mcp.Tool{Name: spec.Name, Description: spec.Description, InputSchema: mcp.ToolInputSchema{Type: "object"}},
		func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			u.record(request.Params.Name)
			return mcp.NewToolResultText("ok"), nil
		})
}

// startCountingUpstream publishes an approved, connected server into the live
// StateView and storage (the same lookup path production uses) and connects
// the proxy's upstream manager to a stub that exposes every tool and records
// each call it receives. It generalises startCountingTargetTierUpstream so any
// scope test can assert zero upstream calls; the older helper delegates here.
func startCountingUpstream(t *testing.T, proxy *MCPProxyServer, rt *runtime.Runtime, server string, tools ...toolSpec) *countingUpstream {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	mcpSrv := mcpserver.NewMCPServer(server, "1.0.0-test", mcpserver.WithToolCapabilities(true))
	up := &countingUpstream{upstreamCalls: &upstreamCalls{}, Server: server, mcpSrv: mcpSrv}
	for _, tool := range tools {
		up.serve(tool)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	httpSrv := &http.Server{Handler: mcpserver.NewStreamableHTTPServer(mcpSrv), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() { _ = httpSrv.Shutdown(context.Background()) })
	up.URL = fmt.Sprintf("http://%s", ln.Addr().String())

	serverCfg := &config.ServerConfig{Name: server, URL: up.URL, Protocol: "streamable-http", Enabled: true}
	require.NoError(t, proxy.storage.SaveUpstreamServer(serverCfg))
	rt.Supervisor().StateView().UpdateServer(server, func(s *stateview.ServerStatus) {
		s.Name = server
		s.Enabled = true
		s.Connected = true
		s.ToolsDiscovered = true
		s.Tools = up.Tools
	})
	for _, tool := range tools {
		if tool.NoRecord {
			continue
		}
		status := tool.Approval
		if status == "" {
			status = storage.ToolApprovalStatusApproved
		}
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: server, ToolName: tool.Name, Status: status,
		}))
	}
	require.NoError(t, proxy.upstreamManager.AddServerConfig(server, serverCfg))
	require.NoError(t, proxy.upstreamManager.ConnectAll(context.Background()))
	require.Eventually(t, func() bool {
		client, ok := proxy.upstreamManager.GetClient(server)
		return ok && client.IsConnected()
	}, 10*time.Second, 50*time.Millisecond, "stub upstream %q must connect", server)
	return up
}

// TestScopeFixture_Contract pins the fixture rules above in executable form
// so a later test cannot silently rely on the wrong empty-list semantics.
// It exercises the fixtures, not a Spec 105 gap, and is expected to pass on
// the merge base.
func TestScopeFixture_Contract(t *testing.T) {
	t.Run("agentCtx: empty allowed is deny-all, [\"*\"] is unrestricted, permissions exact", func(t *testing.T) {
		denyAll := auth.AuthContextFromContext(agentCtx(nil, []string{auth.PermRead}, ""))
		require.NotNil(t, denyAll)
		require.Equal(t, auth.AuthTypeAgent, denyAll.Type)
		require.False(t, denyAll.CanAccessServer("a"), "an empty AllowedServers list must deny every server")

		wildcard := auth.AuthContextFromContext(agentCtx([]string{"*"}, []string{auth.PermDestructive}, "research"))
		require.True(t, wildcard.CanAccessServer("a"))
		require.True(t, wildcard.CanAccessServer("anything-else"))
		require.Equal(t, "research", wildcard.ProfilePin)
		require.True(t, wildcard.HasPermission(auth.PermDestructive))
		require.False(t, wildcard.HasPermission(auth.PermWrite), "destructive must not imply write")
		require.False(t, wildcard.HasPermission(auth.PermRead), "destructive must not imply read")

		narrow := auth.AuthContextFromContext(agentCtx([]string{"a"}, []string{auth.PermRead}, ""))
		require.True(t, narrow.CanAccessServer("a"))
		require.False(t, narrow.CanAccessServer("b"))
		require.True(t, auth.IsScopedCaller(agentCtx([]string{"a"}, nil, "")))
	})

	t.Run("adminCtx: administrator, not anonymous, not scoped", func(t *testing.T) {
		ac := auth.AuthContextFromContext(adminCtx())
		require.NotNil(t, ac)
		require.True(t, ac.IsAdmin())
		require.False(t, ac.Anonymous)
		require.False(t, auth.IsScopedCaller(adminCtx()))
		require.True(t, ac.CanAccessServer("anything"))
	})

	t.Run("startCountingUpstream: seeds StateView + approvals, counts exact raw names", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		up := startCountingUpstream(t, proxy, rt, "a",
			readSpec("erase"),
			writeSpec("ns:erase"),
			destructiveSpec("delete_repo"),
			toolSpec{Name: "pending_one", Approval: storage.ToolApprovalStatusPending},
			toolSpec{Name: "no_record", NoRecord: true},
		)
		require.Equal(t, "a", up.Server)
		require.NotEmpty(t, up.URL)
		require.Len(t, up.Tools, 5)
		require.Equal(t, int64(0), up.count.Load(), "nothing has been dispatched yet")
		require.Empty(t, up.dispatched())

		snapshot, ok := rt.Supervisor().StateView().GetServer("a")
		require.True(t, ok)
		require.Len(t, snapshot.Tools, 5)

		rec, err := proxy.storage.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		require.Equal(t, storage.ToolApprovalStatusApproved, rec.Status, "the zero-value Approval seeds an approved record")
		rec, err = proxy.storage.GetToolApproval("a", "pending_one")
		require.NoError(t, err)
		require.Equal(t, storage.ToolApprovalStatusPending, rec.Status)
		_, err = proxy.storage.GetToolApproval("a", "no_record")
		require.Error(t, err, "NoRecord must seed nothing")

		// Admitted control: an administrator reaches the upstream under the
		// exact raw name, namespaced ones uncollapsed.
		client, ok := proxy.upstreamManager.GetClient("a")
		require.True(t, ok)
		_, err = client.CallTool(context.Background(), "ns:erase", map[string]interface{}{})
		require.NoError(t, err)
		require.Equal(t, int64(1), up.count.Load())
		require.Equal(t, []string{"ns:erase"}, up.dispatched())
	})
}
