package server

import (
	"testing"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// UX audit F16: Settings advertises enable_code_execution as an
// instantly-applied field, but the tool surfaces built their code_execution
// entry — the live tool, or a "disabled" stub whose handler refuses every call
// unconditionally — exactly once, at construction. Flipping the flag on
// therefore left the stub in place on /mcp, so `code_execution` kept answering
// "Code execution is disabled" from a brand-new MCP session until a restart,
// even though the handler-level gate (mcp_code_execution.go) had already gone
// live. These tests pin the rebuild seam that makes the promise true.

func isDisabledCodeExecutionStub(tool mcpserver.ServerTool) bool {
	return tool.Tool.Annotations.Title == "Code Execution (Disabled)"
}

func codeExecutionToolFrom(tools []mcpserver.ServerTool) *mcpserver.ServerTool {
	for i := range tools {
		if tools[i].Tool.Name == "code_execution" {
			return &tools[i]
		}
	}
	return nil
}

// TestBuildCodeExecutionTool_FollowsCurrentConfig: the builder must resolve the
// flag on every call, not capture it. currentConfig() falls back to p.config
// when no runtime is attached, so mutating that snapshot models a hot reload.
func TestBuildCodeExecutionTool_FollowsCurrentConfig(t *testing.T) {
	cfg := &config.Config{EnableCodeExecution: false}
	p := &MCPProxyServer{config: cfg}

	disabled := p.buildCodeExecutionTool()
	require.Len(t, disabled, 1)
	assert.True(t, isDisabledCodeExecutionStub(disabled[0]), "expected the disabled stub while the flag is off")

	cfg.EnableCodeExecution = true
	enabled := p.buildCodeExecutionTool()
	require.Len(t, enabled, 1)
	assert.False(t, isDisabledCodeExecutionStub(enabled[0]), "the live tool must appear once the flag is on")
	assert.Equal(t, codeExecutionToolDescription, enabled[0].Tool.Description)

	cfg.EnableCodeExecution = false
	assert.True(t, isDisabledCodeExecutionStub(p.buildCodeExecutionTool()[0]),
		"turning the flag back off must restore the stub")
}

// TestRefreshCodeExecutionAvailability_SwapsAdvertisedTool: the call-tool
// surface (/mcp in the default retrieve_tools routing mode) and the code-exec
// surface must both re-advertise code_execution after a config reload.
func TestRefreshCodeExecutionAvailability_SwapsAdvertisedTool(t *testing.T) {
	cfg := &config.Config{EnableCodeExecution: false}
	p := &MCPProxyServer{
		config:         cfg,
		logger:         zap.NewNop(),
		callToolServer: mcpserver.NewMCPServer("test", "0.0.0"),
		codeExecServer: mcpserver.NewMCPServer("test", "0.0.0"),
		server:         mcpserver.NewMCPServer("test", "0.0.0"),
	}

	for _, st := range p.buildCallToolModeTools() {
		p.callToolServer.AddTool(st.Tool, st.Handler)
	}
	for _, st := range p.buildCodeExecModeTools() {
		p.codeExecServer.AddTool(st.Tool, st.Handler)
	}

	advertised := func(s *mcpserver.MCPServer) *mcpserver.ServerTool {
		tool, ok := s.ListTools()["code_execution"]
		if !ok {
			return nil
		}
		return tool
	}

	require.NotNil(t, advertised(p.callToolServer))
	assert.True(t, isDisabledCodeExecutionStub(*advertised(p.callToolServer)))
	assert.True(t, isDisabledCodeExecutionStub(*advertised(p.codeExecServer)))

	// The operator flips the toggle; ApplyConfig swaps the live snapshot and
	// emits config.reloaded, which drives this call.
	cfg.EnableCodeExecution = true
	p.RefreshCodeExecutionAvailability()

	require.NotNil(t, advertised(p.callToolServer), "code_execution must still be advertised")
	assert.False(t, isDisabledCodeExecutionStub(*advertised(p.callToolServer)),
		"the call-tool surface must serve the live tool after a hot enable")
	assert.False(t, isDisabledCodeExecutionStub(*advertised(p.codeExecServer)),
		"the code-exec surface must serve the live tool after a hot enable")

	// The default/stdio surface is updated in place with AddTools, which must
	// not disturb the tools registerTools put there.
	require.NotNil(t, advertised(p.server))
	assert.False(t, isDisabledCodeExecutionStub(*advertised(p.server)))

	// And back off again.
	cfg.EnableCodeExecution = false
	p.RefreshCodeExecutionAvailability()
	assert.True(t, isDisabledCodeExecutionStub(*advertised(p.callToolServer)),
		"turning the flag off must restore the refusing stub")
}

// TestRefreshCodeExecutionAvailability_PreservesOtherTools: the default surface
// is refreshed with AddTools rather than SetTools precisely because its tool set
// is assembled once by registerTools; SetTools would drop everything else.
func TestRefreshCodeExecutionAvailability_PreservesOtherTools(t *testing.T) {
	cfg := &config.Config{EnableCodeExecution: false}
	p := &MCPProxyServer{
		config: cfg,
		logger: zap.NewNop(),
		server: mcpserver.NewMCPServer("test", "0.0.0"),
	}

	callToolTools := p.buildCallToolModeTools()
	for _, st := range callToolTools {
		p.server.AddTool(st.Tool, st.Handler)
	}
	before := len(p.server.ListTools())
	require.Greater(t, before, 1)

	cfg.EnableCodeExecution = true
	p.RefreshCodeExecutionAvailability()

	assert.Len(t, p.server.ListTools(), before,
		"refreshing code_execution must not add or drop any other tool")
	assert.NotNil(t, codeExecutionToolFrom(callToolTools), "sanity: the builder emits code_execution")
}

// TestRefreshCodeExecutionAvailability_NilSafe: the refresh runs from the shared
// event listener, which fires before every surface exists in some code paths.
func TestRefreshCodeExecutionAvailability_NilSafe(t *testing.T) {
	p := &MCPProxyServer{config: &config.Config{}, logger: zap.NewNop()}
	assert.NotPanics(t, p.RefreshCodeExecutionAvailability)
}
