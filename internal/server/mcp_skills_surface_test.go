package server

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 125 PR-3 (T039, FR-023b, FR-026, SC-005): the three skill tools follow
// the live enable_skills switch on every surface, and nowhere else.

var skillTools = []string{toolRetrieveSkills, toolLoadSkill, toolReadSkillFile}

// listSurface returns the tools/list answer of an mcp-go server for ctx, as the
// JSON bytes (for byte-identity checks) and the names.
func listSurface(t *testing.T, srv *mcpserver.MCPServer, ctx context.Context) ([]byte, []string) {
	t.Helper()
	raw, err := json.Marshal(srv.HandleMessage(withDirectRequestKindBox(ctx),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	require.NoError(t, err)
	var listed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(raw, &listed), string(raw))
	names := make([]string, 0, len(listed.Result.Tools))
	for _, tool := range listed.Result.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return raw, names
}

func hasAll(names []string, want ...string) bool {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func hasAny(names []string, want ...string) bool {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	for _, w := range want {
		if set[w] {
			return true
		}
	}
	return false
}

func skillSurfaces(p *MCPProxyServer) map[string]*mcpserver.MCPServer {
	return map[string]*mcpserver.MCPServer{
		"default":   p.server,
		"call_tool": p.callToolServer,
		"code_exec": p.codeExecServer,
		"direct":    p.directServer,
	}
}

func TestSkillTools_FollowTheSwitchOnEverySurface(t *testing.T) {
	p := createTestMCPProxyServer(t)
	ctx := auth.WithAuthContext(context.Background(), auth.AdminContext())

	baseline := map[string][]byte{}
	for name, srv := range skillSurfaces(p) {
		raw, names := listSurface(t, srv, ctx)
		baseline[name] = raw
		assert.False(t, hasAny(names, skillTools...), "%s: switch off, no skill tool", name)
	}

	// off -> on
	p.config.EnableSkills = true
	p.RefreshSkillsAvailability()
	for name, srv := range skillSurfaces(p) {
		_, names := listSurface(t, srv, ctx)
		assert.True(t, hasAll(names, skillTools...), "%s: switch on, all three tools: %v", name, names)
	}

	// on -> off: the listing is byte-identical to what it was before.
	p.config.EnableSkills = false
	p.RefreshSkillsAvailability()
	for name, srv := range skillSurfaces(p) {
		raw, names := listSurface(t, srv, ctx)
		assert.False(t, hasAny(names, skillTools...), "%s: switch off again", name)
		assert.Equal(t, string(baseline[name]), string(raw), "%s: with the switch off the tool list is byte-identical to today's", name)
	}
}

func TestSkillTools_NotOnSingleServerSurfaces(t *testing.T) {
	p := newDirectDescribeProxy(t)
	p.config.EnableSkills = true
	p.RefreshSkillsAvailability()
	// The refresh rebuilt the direct catalog from the (empty) upstream manager;
	// put the fixture catalog back so the surface has tools of its own.
	p.publishDirectCatalog(buildDirectCatalog(describeDirectFixture(), nil))
	s := newSingleServerSurfaceForTest(t, p, "github")

	tools := listSingleServerTools(t, s, singleServerAdminCtx())
	for _, name := range skillTools {
		assert.NotContains(t, tools, name, "/mcp/s/<server> never carries the skill tools")
	}
	// Control: the surface lists its own tools.
	assert.Contains(t, tools, "read_file")

	// Off and on again: still none.
	p.config.EnableSkills = false
	p.RefreshSkillsAvailability()
	tools = listSingleServerTools(t, s, singleServerAdminCtx())
	for _, name := range skillTools {
		assert.NotContains(t, tools, name)
	}
}

func TestSkillTools_NoListChangedUnlessTheSwitchMoved(t *testing.T) {
	cfg := config.DefaultConfig()
	p, sessions := newHotReloadProxy(t, cfg)
	// newHotReloadProxy's default surface stands in with the call-tool set and
	// the direct server is part of initRoutingModeServers; observe it too.
	direct := newNotifyingSession("sess-direct")
	require.NoError(t, p.directServer.RegisterSession(context.Background(), direct))
	direct.drainListChanged()
	sessions["direct"] = direct

	drain := func() map[string]int {
		out := map[string]int{}
		for name, s := range sessions {
			out[name] = s.drainListChanged()
		}
		return out
	}
	zero := map[string]int{"call_tool": 0, "code_exec": 0, "default": 0, "direct": 0}
	one := map[string]int{"call_tool": 1, "code_exec": 1, "default": 1, "direct": 1}

	p.RefreshSkillsAvailability()
	p.RefreshSkillsAvailability()
	assert.Equal(t, zero, drain(), "switch off and already off: nothing is announced")

	cfg.EnableSkills = true
	p.RefreshSkillsAvailability()
	assert.Equal(t, one, drain(), "off -> on: one announcement per surface")
	p.RefreshSkillsAvailability()
	assert.Equal(t, zero, drain(), "on and already on: nothing")

	cfg.EnableSkills = false
	p.RefreshSkillsAvailability()
	assert.Equal(t, one, drain(), "on -> off: one announcement per surface")
}

func TestSkillTools_RefreshIsNilSafe(t *testing.T) {
	p := &MCPProxyServer{config: config.DefaultConfig(), logger: createTestMCPProxyServer(t).logger}
	assert.NotPanics(t, p.RefreshSkillsAvailability)
}

func TestSkillToolNames_AreBuiltinsOnTheDirectSurfaceWhetherOrNotTheSwitchIsOn(t *testing.T) {
	p := newDirectDescribeProxy(t)
	for _, enabled := range []bool{false, true} {
		p.config.EnableSkills = enabled
		for _, name := range skillTools {
			assert.Contains(t, builtinDirectToolNames, name)
			entry, decision := p.resolveDirectTool(name)
			assert.Nil(t, entry, "%s resolves to no upstream entry (enabled=%v)", name, enabled)
			assert.Equal(t, directResolveBuiltin, decision, "%s (enabled=%v)", name, enabled)
		}
	}
}

func TestSkillToolNames_CannotBeShadowedByUpstreamTools(t *testing.T) {
	// An upstream whose tool would display as a skill tool's name is refused
	// admission by the direct catalog (a built-in name is never an upstream).
	for _, name := range skillTools {
		_, isBuiltin := builtinDirectToolNames[name]
		require.True(t, isBuiltin)
	}
	// The sandbox and the REST switch resolve names to built-ins first; an
	// upstream server named like a skill tool does not change that.
	p := newDirectDescribeProxy(t)
	p.config.EnableSkills = true
	for _, server := range skillTools {
		entry, decision := p.resolveDirectTool(server)
		assert.Nil(t, entry)
		assert.Equal(t, directResolveBuiltin, decision, "name %s is a built-in, whichever upstream is configured", server)
	}
}

func TestSkillTools_VisibleToEveryCallerThatSeesRetrieveTools(t *testing.T) {
	p := createTestMCPProxyServer(t)
	p.config.EnableSkills = true
	p.RefreshSkillsAvailability()

	callers := map[string]context.Context{
		"admin":      auth.WithAuthContext(context.Background(), auth.AdminContext()),
		"no auth":    context.Background(),
		"read-only":  auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAgent, AgentName: "ro", AllowedServers: []string{"github"}, Permissions: []string{auth.PermRead}}),
		"full tier":  auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAgent, AgentName: "full", AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}}),
		"no servers": auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAgent, AgentName: "none", AllowedServers: nil, Permissions: []string{auth.PermRead}}),
	}
	for label, ctx := range callers {
		for _, surface := range []string{"call_tool", "code_exec", "default"} {
			_, names := listSurface(t, skillSurfaces(p)[surface], ctx)
			assert.True(t, hasAll(names, "retrieve_tools"), "%s/%s: control, retrieve_tools is listed", label, surface)
			assert.True(t, hasAll(names, skillTools...), "%s/%s: skill tools are listed wherever retrieve_tools is", label, surface)
		}
		_, names := listSurface(t, p.directServer, ctx)
		assert.True(t, hasAll(names, "describe_tool"), "%s/direct: control", label)
		assert.True(t, hasAll(names, skillTools...), "%s/direct", label)
	}
}

func TestSkillTools_NotCallableFromCodeExecution(t *testing.T) {
	proxy, _ := createTestProxyWithRuntimeCfg(t, []*config.ServerConfig{{Name: "hf", Enabled: true}}, func(cfg *config.Config) {
		cfg.EnableSkills = true
		cfg.EnableCodeExecution = true
	})
	cat := newFakeSkillsCatalog()
	cat.add("hf", "cli", "cli", "d", "sk1-a", map[string]string{"SKILL.md": "body"})
	proxy.skillsCatalogOverride = cat
	require.True(t, proxy.skillsEnabled(), "fixture: skills are on, so the only thing in the way is the sandbox")

	// A script names the skill tools as (server, tool) in every spelling that
	// could reach a built-in: none resolves to one.
	for _, c := range [][2]string{
		{"mcpproxy", "load_skill"},
		{"", "load_skill"},
		{"hf", "load_skill"},
		{"hf", "read_skill_file"},
		{"hf", "retrieve_skills"},
		{"load_skill", "SKILL.md"},
		{"retrieve_skills", "retrieve_skills"},
		{"hf:cli", "load_skill"},
	} {
		call := runSandboxCallTool(t, proxy, adminCtx(), c[0], c[1])
		assert.False(t, call.OK, "call_tool(%q, %q) must not run a skill tool: %s", c[0], c[1], call.Message)
	}
	assert.Zero(t, cat.calls, "no skill tool handler ran from inside the sandbox")
}

func TestSkillTools_RESTToolCallRoutesThemOnlyWhileTheSwitchIsOn(t *testing.T) {
	p := createTestMCPProxyServer(t)
	cat := newFakeSkillsCatalog()
	cat.add("hf", "cli", "cli", "d", "sk1-a", map[string]string{"SKILL.md": "body"})
	p.skillsCatalogOverride = cat

	call := func(name string, args map[string]any) (interface{}, error) {
		req := mcp.CallToolRequest{}
		req.Params.Name = name
		req.Params.Arguments = args
		return p.CallToolDirectResult(context.Background(), req)
	}

	_, err := call("load_skill", map[string]any{"id": "hf:cli"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown tool", "switch off: not routable")

	p.config.EnableSkills = true
	res, err := call("load_skill", map[string]any{"id": "hf:cli"})
	require.NoError(t, err)
	out, ok := res.(*mcp.CallToolResult)
	require.True(t, ok)
	assert.False(t, out.IsError)
	assert.Contains(t, skillResultText(t, out), "body")

	res, err = call("retrieve_skills", map[string]any{"query": "cli"})
	require.NoError(t, err)
	assert.Contains(t, skillResultText(t, res.(*mcp.CallToolResult)), `"total":1`)

	res, err = call("read_skill_file", map[string]any{"id": "hf:cli", "path": "SKILL.md"})
	require.NoError(t, err)
	assert.Contains(t, skillResultText(t, res.(*mcp.CallToolResult)), "body")
}

// T038 (FR-023, FR-023a): reach is the caller's server reach through the same
// chain retrieve_tools uses; tool policy (tier cap, per-tool rules) does not
// apply to skills.
func TestSkillTools_ReachFollowsTokenProfileAndPin_ToolPolicyIsNotConsulted(t *testing.T) {
	proxy, _ := newProfilesV3FixtureWithConfig(t, func(cfg *config.Config) { cfg.EnableSkills = true })
	cat := newFakeSkillsCatalog()
	for _, server := range []string{"github", "notion", "filesystem"} {
		cat.add(server, "helper", "helper", "shared helper skill", "sk1-"+server, map[string]string{"SKILL.md": "text of " + server})
	}
	proxy.skillsCatalogOverride = cat
	fx := &skillFixture{proxy: proxy, cat: cat}

	loads := func(ctx context.Context, server string) bool {
		res := fx.load(t, ctx, map[string]any{"id": server + ":helper"})
		if res.IsError {
			require.Equal(t, "skill not found: "+server+":helper", skillResultText(t, res))
			return false
		}
		return true
	}
	total := func(ctx context.Context) int {
		_, out := fx.search(t, ctx, map[string]any{"query": "helper"})
		return out.Total
	}

	t.Run("URL profile: only the profile's servers, although its tier cap is read and github:* secrets are denied", func(t *testing.T) {
		ctx := urlProfileCtx(proxy, "work-readonly")
		assert.True(t, loads(ctx, "github"))
		assert.True(t, loads(ctx, "notion"), "notion's only tool is a write tool the cap would refuse; the skill is not a tool call")
		assert.False(t, loads(ctx, "filesystem"))
		assert.Equal(t, 2, total(ctx))
	})

	t.Run("agent token scoped to github", func(t *testing.T) {
		ctx := skillAgentCtx("github")
		assert.True(t, loads(ctx, "github"))
		assert.False(t, loads(ctx, "notion"))
		assert.Equal(t, 1, total(ctx))
	})

	t.Run("token pinned to a profile: grant intersected with the pin", func(t *testing.T) {
		ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type: auth.AuthTypeAgent, AgentName: "pinned", ProfilePin: "work-readonly",
			AllowedServers: []string{"github", "filesystem"}, Permissions: []string{auth.PermRead},
		})
		assert.True(t, loads(ctx, "github"))
		assert.False(t, loads(ctx, "notion"), "in the pin, not in the token's grant")
		assert.False(t, loads(ctx, "filesystem"), "in the token's grant, not in the pin")
		assert.Equal(t, 1, total(ctx))
	})

	t.Run("token pinned to a profile that no longer exists: nothing", func(t *testing.T) {
		ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type: auth.AuthTypeAgent, AgentName: "stale", ProfilePin: "deleted-profile",
			AllowedServers: []string{"*"}, Permissions: []string{auth.PermRead},
		})
		assert.False(t, loads(ctx, "github"))
		assert.Equal(t, 0, total(ctx))
	})

	t.Run("administrator without a profile: every server", func(t *testing.T) {
		ctx := auth.WithAuthContext(context.Background(), auth.AdminContext())
		assert.True(t, loads(ctx, "filesystem"))
		assert.Equal(t, 3, total(ctx))
	})
}
