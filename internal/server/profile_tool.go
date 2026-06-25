package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// buildSetProfileTool constructs the set_profile MCP tool definition (Profiles
// v2 T2). Factored out so it can be registered on the default server and every
// routing-mode server (call-tool / code-exec) from one source of truth.
func buildSetProfileTool() mcp.Tool {
	return mcp.NewTool("set_profile",
		mcp.WithDescription("Switch the active profile for THIS session. A profile scopes tool discovery "+
			"(retrieve_tools) and tool calls to a named subset of upstream servers — useful to focus an agent "+
			"on one task domain (e.g. 'research', 'deploy'). The selection persists for the lifetime of the "+
			"current MCP session and applies to subsequent retrieve_tools / call_tool_* / code_execution calls "+
			"on the base /mcp endpoint without re-indexing. Pass an empty string to clear the selection and go "+
			"back to all servers. Note: an explicit /mcp/p/<slug> URL still overrides the session profile for "+
			"that request, and a profile-pinned agent token cannot switch away from its pinned profile."),
		mcp.WithTitleAnnotation("Set Profile"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
		mcp.WithString("profile",
			mcp.Description("Profile slug to activate for this session (e.g. 'research'). Pass \"\" (empty) to "+
				"clear the active profile and return to all servers."),
		),
	)
}

// handleSetProfile implements the set_profile tool. It validates the requested
// slug against live config, records it on the session (mutex-guarded, cleared
// on session close), and returns the resolved {active_profile, servers}.
func (p *MCPProxyServer) handleSetProfile(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slug := strings.TrimSpace(request.GetString("profile", ""))

	sessionID := sessionIDFromContext(ctx)
	if sessionID == "" {
		return mcp.NewToolResultError("set_profile requires an active MCP session; no session id is bound to this request"), nil
	}

	cfg := p.currentConfig()

	// Profiles v2 T3 hook: a profile-pinned agent token may not switch away from
	// its pinned profile. Inert until T3 populates the pin (always "" for now).
	if pin := profilePinFromContext(ctx); pin != "" && slug != "" && slug != pin {
		return mcp.NewToolResultError(fmt.Sprintf("agent token is pinned to profile '%s' and cannot switch to '%s'", pin, slug)), nil
	}

	// Empty slug clears the session selection (back to all servers).
	if slug == "" {
		p.sessionStore.SetActiveProfile(sessionID, "")
		return setProfileResult("", allServerNames(cfg))
	}

	// Validate the slug matches a configured profile.
	var match *config.ProfileConfig
	if cfg != nil {
		for i := range cfg.Profiles {
			if cfg.Profiles[i].Name == slug {
				match = &cfg.Profiles[i]
				break
			}
		}
	}
	if match == nil {
		return mcp.NewToolResultError(fmt.Sprintf("unknown profile '%s' (available: %s)", slug, strings.Join(profileNames(cfg), ", "))), nil
	}

	p.sessionStore.SetActiveProfile(sessionID, slug)
	p.logger.Info("set_profile: session profile updated",
		zap.String("session_id", sessionID),
		zap.String("profile", slug),
	)
	return setProfileResult(slug, match.EffectiveServers(cfg))
}

// setProfileResult renders the standard set_profile success payload.
func setProfileResult(activeProfile string, servers []string) (*mcp.CallToolResult, error) {
	if servers == nil {
		servers = []string{}
	}
	payload := map[string]interface{}{
		"active_profile": activeProfile,
		"servers":        servers,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode set_profile result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

// profileNames returns all configured profile slugs (for error messages).
func profileNames(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Profiles))
	for i := range cfg.Profiles {
		names = append(names, cfg.Profiles[i].Name)
	}
	return names
}

// allServerNames returns the names of every configured server (the "all
// servers" set returned when a profile selection is cleared).
func allServerNames(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		if s != nil {
			names = append(names, s.Name)
		}
	}
	return names
}

// setProfileServerTool wraps buildSetProfileTool as a ServerTool for routing-mode registration.
func (p *MCPProxyServer) setProfileServerTool() mcpserver.ServerTool {
	return mcpserver.ServerTool{Tool: buildSetProfileTool(), Handler: p.handleSetProfile}
}
