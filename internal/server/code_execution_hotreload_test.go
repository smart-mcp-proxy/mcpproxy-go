package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Issue #1236: with enable_code_execution=false (the default) the tool surfaces
// used to advertise a "disabled" code_execution stub whose handler refused every
// call. Clients that pick tools from tools/list rather than from descriptions —
// most of them — kept calling it and burning a round trip per attempt. The
// contract is now: disabled → NOT advertised on any surface; a runtime toggle
// (config hot-reload) re-advertises or withdraws the tool and emits
// notifications/tools/list_changed so caching clients refetch. These tests pin
// both halves.
//
// UX audit F16 (the prior shape of this file) established that the builder
// resolves the flag on every call and that RefreshCodeExecutionAvailability is
// the seam config.reloaded drives; those guarantees carry over unchanged.

func codeExecutionToolFrom(tools []mcpserver.ServerTool) *mcpserver.ServerTool {
	for i := range tools {
		if tools[i].Tool.Name == "code_execution" {
			return &tools[i]
		}
	}
	return nil
}

// advertisedCodeExecution returns the registered code_execution entry on a
// server, or nil when the surface does not list it.
func advertisedCodeExecution(s *mcpserver.MCPServer) *mcpserver.ServerTool {
	tool, ok := s.ListTools()["code_execution"]
	if !ok {
		return nil
	}
	return tool
}

// notifyingSession is an initialized mcp-go ClientSession whose notification
// channel is captured, so a test can observe what the server pushed to a
// connected client. It stands in for a real Streamable HTTP session.
type notifyingSession struct {
	id string
	ch chan mcp.JSONRPCNotification
}

func newNotifyingSession(id string) *notifyingSession {
	return &notifyingSession{id: id, ch: make(chan mcp.JSONRPCNotification, 16)}
}

func (s *notifyingSession) Initialize()                                         {}
func (s *notifyingSession) Initialized() bool                                   { return true }
func (s *notifyingSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return s.ch }
func (s *notifyingSession) SessionID() string                                   { return s.id }

// drainListChanged counts the notifications/tools/list_changed pushes queued
// on the session and empties the channel.
func (s *notifyingSession) drainListChanged() int {
	n := 0
	for {
		select {
		case notif := <-s.ch:
			if notif.Method == mcp.MethodNotificationToolsListChanged {
				n++
			}
		default:
			return n
		}
	}
}

// newHotReloadProxy builds a proxy with all three built-in-carrying surfaces
// registered the way production does it — the routing-mode servers through
// initRoutingModeServers (which also seeds their content guards), the default
// surface the way registerTools assembles it — plus one initialized session
// per surface so list_changed pushes are observable.
func newHotReloadProxy(t *testing.T, cfg *config.Config) (*MCPProxyServer, map[string]*notifyingSession) {
	t.Helper()
	p := &MCPProxyServer{
		config: cfg,
		logger: zap.NewNop(),
		server: mcpserver.NewMCPServer("test", "0.0.0", mcpserver.WithToolCapabilities(true)),
	}
	p.initRoutingModeServers()
	require.NotNil(t, p.callToolServer)
	require.NotNil(t, p.codeExecServer)
	// The default surface is assembled by registerTools; the call-tool set is
	// a faithful stand-in (same built-ins, same code_execution gating).
	for _, st := range p.buildCallToolModeTools() {
		p.server.AddTool(st.Tool, st.Handler)
	}

	sessions := map[string]*notifyingSession{}
	for name, s := range map[string]*mcpserver.MCPServer{
		"call_tool": p.callToolServer,
		"code_exec": p.codeExecServer,
		"default":   p.server,
	} {
		sess := newNotifyingSession("sess-" + name)
		require.NoError(t, s.RegisterSession(context.Background(), sess))
		sess.drainListChanged() // registration-time noise, if any
		sessions[name] = sess
	}
	return p, sessions
}

// TestBuildCodeExecutionTool_FollowsCurrentConfig: the builder resolves the
// flag on every call, and a disabled flag yields NO tool — not a stub.
// currentConfig() falls back to p.config when no runtime is attached, so
// mutating that snapshot models a hot reload.
func TestBuildCodeExecutionTool_FollowsCurrentConfig(t *testing.T) {
	cfg := &config.Config{EnableCodeExecution: false}
	p := &MCPProxyServer{config: cfg}

	assert.Empty(t, p.buildCodeExecutionTool(),
		"issue #1236: a disabled code_execution must not be advertised at all")

	cfg.EnableCodeExecution = true
	enabled := p.buildCodeExecutionTool()
	require.Len(t, enabled, 1)
	assert.Equal(t, "code_execution", enabled[0].Tool.Name)
	assert.Equal(t, codeExecutionToolDescription, enabled[0].Tool.Description)
	assert.Equal(t, "Code Execution", enabled[0].Tool.Annotations.Title)

	cfg.EnableCodeExecution = false
	assert.Empty(t, p.buildCodeExecutionTool(),
		"turning the flag back off must withdraw the tool again")
}

// TestRoutingModeSurfaces_OmitDisabledCodeExecution: neither routing-mode
// builder may carry code_execution while the flag is off. The retrieve_tools
// surface is what /mcp serves in the default routing mode, so this is the
// listing the issue reporter saw.
func TestRoutingModeSurfaces_OmitDisabledCodeExecution(t *testing.T) {
	p := &MCPProxyServer{config: &config.Config{EnableCodeExecution: false}, logger: zap.NewNop()}

	assert.Nil(t, codeExecutionToolFrom(p.buildCallToolModeTools()),
		"retrieve_tools mode (/mcp default) must not list a disabled code_execution")
	assert.Nil(t, codeExecutionToolFrom(p.buildCodeExecModeTools()),
		"code_execution mode must not list a disabled code_execution")

	p.config.EnableCodeExecution = true
	assert.NotNil(t, codeExecutionToolFrom(p.buildCallToolModeTools()))
	assert.NotNil(t, codeExecutionToolFrom(p.buildCodeExecModeTools()))
}

// TestRefreshCodeExecutionAvailability_TogglesAdvertisedTool: a hot enable
// adds code_execution to every surface that carries it and a hot disable
// withdraws it again — including the default surface, which is updated in
// place (AddTools / DeleteTools) rather than rebuilt.
func TestRefreshCodeExecutionAvailability_TogglesAdvertisedTool(t *testing.T) {
	cfg := &config.Config{EnableCodeExecution: false}
	p, _ := newHotReloadProxy(t, cfg)

	require.Nil(t, advertisedCodeExecution(p.callToolServer), "precondition: disabled at startup")
	require.Nil(t, advertisedCodeExecution(p.codeExecServer))
	require.Nil(t, advertisedCodeExecution(p.server))

	// The operator flips the toggle; ApplyConfig swaps the live snapshot and
	// emits config.reloaded, which drives this call.
	cfg.EnableCodeExecution = true
	p.RefreshCodeExecutionAvailability()

	for name, s := range map[string]*mcpserver.MCPServer{
		"call_tool": p.callToolServer, "code_exec": p.codeExecServer, "default": p.server,
	} {
		tool := advertisedCodeExecution(s)
		require.NotNil(t, tool, "%s surface must advertise code_execution after a hot enable", name)
		assert.Equal(t, codeExecutionToolDescription, tool.Tool.Description,
			"%s surface must serve the live tool, never a stub", name)
	}

	// And back off again: the tool disappears from every surface.
	cfg.EnableCodeExecution = false
	p.RefreshCodeExecutionAvailability()

	assert.Nil(t, advertisedCodeExecution(p.callToolServer),
		"turning the flag off must withdraw code_execution from the retrieve_tools surface")
	assert.Nil(t, advertisedCodeExecution(p.codeExecServer),
		"turning the flag off must withdraw code_execution from the code-exec surface")
	assert.Nil(t, advertisedCodeExecution(p.server),
		"turning the flag off must withdraw code_execution from the default surface")
}

// TestRefreshCodeExecutionAvailability_EmitsListChangedOnlyOnAFlip: a runtime
// toggle must push notifications/tools/list_changed to connected sessions on
// every affected surface (the issue's second expectation), and an unrelated
// config reload — which also drives this refresh — must push nothing, or every
// config edit looks to a client exactly like the tool set changing.
func TestRefreshCodeExecutionAvailability_EmitsListChangedOnlyOnAFlip(t *testing.T) {
	cfg := &config.Config{EnableCodeExecution: false}
	p, sessions := newHotReloadProxy(t, cfg)

	// Unrelated reload: same flag value, nothing to announce.
	p.RefreshCodeExecutionAvailability()
	p.RefreshCodeExecutionAvailability()
	for name, sess := range sessions {
		assert.Zero(t, sess.drainListChanged(),
			"%s surface: a reload that changes nothing must not emit list_changed", name)
	}

	// Enable: exactly one list_changed per surface.
	cfg.EnableCodeExecution = true
	p.RefreshCodeExecutionAvailability()
	for name, sess := range sessions {
		assert.Equal(t, 1, sess.drainListChanged(),
			"%s surface: a hot enable must emit exactly one list_changed", name)
	}

	// Steady state again: silence.
	p.RefreshCodeExecutionAvailability()
	for name, sess := range sessions {
		assert.Zero(t, sess.drainListChanged(),
			"%s surface: a reload after the flip must not re-announce it", name)
	}

	// Disable: exactly one list_changed per surface.
	cfg.EnableCodeExecution = false
	p.RefreshCodeExecutionAvailability()
	for name, sess := range sessions {
		assert.Equal(t, 1, sess.drainListChanged(),
			"%s surface: a hot disable must emit exactly one list_changed", name)
	}
}

// TestRefreshCodeExecutionAvailability_PreservesOtherTools: the default surface
// is refreshed in place precisely because its tool set is assembled once by
// registerTools; a rebuild would drop everything else. Neither the add nor the
// remove may disturb the other registrations.
func TestRefreshCodeExecutionAvailability_PreservesOtherTools(t *testing.T) {
	cfg := &config.Config{EnableCodeExecution: false}
	p, _ := newHotReloadProxy(t, cfg)

	before := len(p.server.ListTools())
	require.Greater(t, before, 1)

	cfg.EnableCodeExecution = true
	p.RefreshCodeExecutionAvailability()
	assert.Len(t, p.server.ListTools(), before+1,
		"a hot enable adds code_execution and nothing else")

	cfg.EnableCodeExecution = false
	p.RefreshCodeExecutionAvailability()
	assert.Len(t, p.server.ListTools(), before,
		"a hot disable removes code_execution and nothing else")
}

// TestRefreshCodeExecutionAvailability_NilSafe: the refresh runs from the shared
// event listener, which fires before every surface exists in some code paths.
func TestRefreshCodeExecutionAvailability_NilSafe(t *testing.T) {
	p := &MCPProxyServer{config: &config.Config{}, logger: zap.NewNop()}
	assert.NotPanics(t, p.RefreshCodeExecutionAvailability)
}
