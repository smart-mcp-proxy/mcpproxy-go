package server

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/branding"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// Agents connected through the proxy only see a handful of built-in tools,
// so without help they conclude that GitHub, Jira, … are unavailable and fall
// back to shell CLIs. The proxy therefore tells each caller, in its
// initialize/discover instructions and in the retrieve_tools description,
// which upstream servers IT can reach and which operations IT may perform.
//
// Everything here is derived per caller through the same predicates the
// dispatch gates use — serverInScope (profile ∩ token, with anonymous
// confinement), the token's permission set and the profile's max_tier — so
// the text a client sees never promises a server or an operation the call
// would refuse, and never names a server outside the caller's scope (Spec 105
// semantic disclosure). Operator-authored `instructions` stay verbatim and
// shared (Spec 105 FR-012); only the generated block is per caller.

// maxAdvertisedServers caps the server list so a large fleet cannot bloat the
// context the client injects into its system prompt.
const maxAdvertisedServers = 30

// callerAccess is what one caller can actually reach and do.
type callerAccess struct {
	// Profile is the effective profile slug, "" when none applies.
	Profile string
	// Servers are the reachable upstream servers (enabled, approved,
	// connected, in scope), sorted, capped at maxAdvertisedServers.
	Servers []string
	// More counts reachable servers beyond the cap.
	More int
	// Tiers are the operation tiers a call may use, in ascending order.
	Tiers []profile.Tier
	// CapExempt is true when the profile's max_tier excludes a tier the
	// credential permits AND the profile has tools.allow rules, which
	// CompiledPolicy.Decide admits above the cap.
	CapExempt bool
}

// advertisableServerName is the conservative shape of a server name that may
// be spelled out in instructions and descriptions.
var advertisableServerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// allTiers is the operation ladder, ascending.
var allTiers = []profile.Tier{profile.TierRead, profile.TierWrite, profile.TierDestructive}

// tierPermission maps a tier to the agent-token permission that admits it
// (the same mapping handleCallToolVariant enforces).
func tierPermission(t profile.Tier) string {
	switch t {
	case profile.TierRead:
		return auth.PermRead
	case profile.TierWrite:
		return auth.PermWrite
	case profile.TierDestructive:
		return auth.PermDestructive
	}
	return ""
}

// callerAccessFor resolves the caller's reachable servers and allowed tiers.
// It uses the non-recording profile resolver: it runs inside the initialize
// hook and on every tools/list, and must not count as a session resolution.
func (p *MCPProxyServer) callerAccessFor(ctx context.Context) callerAccess {
	idx, ok := profileRequestIndexFromContext(ctx)
	if !ok {
		idx = p.profileIndexCurrent(ctx)
	}
	res := p.resolveProfileV3(ctx, idx)
	requestAuth := auth.AuthContextFromContext(ctx)
	confinedAnonymous := (requestAuth == nil || requestAuth.Anonymous) && res.anonymousConfinementActive()
	authCtx := auth.ScopedView(requestAuth, confinedAnonymous)
	scoped := auth.IsNonAdmin(authCtx)

	access := callerAccess{}
	// Name the profile only where refusals may name it: never the
	// operator-only anonymous_profile (profileDisclosedTo).
	if profileDisclosedTo(profile.Source(res.Source)) {
		access.Profile = res.Name
	}

	for _, t := range allTiers {
		if scoped && !authCtx.HasPermission(tierPermission(t)) {
			continue
		}
		if res.Policy != nil && res.Policy.Cap != profile.TierUnannotated && t > res.Policy.Cap {
			if res.Policy.HasAllowRules() {
				access.CapExempt = true
			}
			continue
		}
		access.Tiers = append(access.Tiers, t)
	}

	if !p.advertiseUpstreamServers() {
		return access
	}
	names := p.reachableServerNames(func(name string) bool {
		return p.serverInScope(authCtx, res.Scope, name)
	})
	// Only names that are inert as prompt text are spelled out: server names
	// are barely validated (non-empty, no ':') and can come from a registry,
	// and these strings land in the agent's highest-trust context. Others
	// still count toward "+N more" so the total stays honest.
	safe := names[:0:0]
	for _, name := range names {
		if advertisableServerName.MatchString(name) {
			safe = append(safe, name)
		} else {
			access.More++
		}
	}
	names = safe
	if len(names) > maxAdvertisedServers {
		access.More += len(names) - maxAdvertisedServers
		names = names[:maxAdvertisedServers]
	}
	access.Servers = names
	return access
}

// advertiseUpstreamServers reports whether server names may be published to
// clients (config advertise_upstream_servers, default true). Read from the
// live snapshot, so a hot-reload applies on the next list or connect.
func (p *MCPProxyServer) advertiseUpstreamServers() bool {
	return p.currentConfig().AdvertiseUpstreamServersEnabled()
}

// reachableCacheTTL bounds how stale the cached fleet-wide reachable set may
// be. retrieve_tools' tool filter also runs on mcp-go's call-time
// re-evaluation of every retrieve_tools call, so the storage scan and client
// probes are cached; servers.changed invalidates the cache immediately.
const reachableCacheTTL = 2 * time.Second

// reachableServerNames lists enabled, non-quarantined, connected upstream
// servers that inScope admits, sorted. A nil inScope admits every server.
func (p *MCPProxyServer) reachableServerNames(inScope func(string) bool) []string {
	all := p.cachedReachableServers()
	if inScope == nil {
		return all
	}
	names := make([]string, 0, len(all))
	for _, name := range all {
		if inScope(name) {
			names = append(names, name)
		}
	}
	return names
}

// cachedReachableServers returns the fleet-wide reachable set, rescanning
// at most once per reachableCacheTTL. The returned slice is shared: callers
// must not modify it.
func (p *MCPProxyServer) cachedReachableServers() []string {
	p.reachableMu.Lock()
	defer p.reachableMu.Unlock()
	if p.reachableAt.IsZero() || time.Since(p.reachableAt) > reachableCacheTTL {
		p.reachable = p.scanReachableServers()
		p.reachableAt = time.Now()
	}
	return p.reachable
}

// invalidateReachableServers drops the cached set (servers.changed).
func (p *MCPProxyServer) invalidateReachableServers() {
	p.reachableMu.Lock()
	p.reachableAt = time.Time{}
	p.reachableMu.Unlock()
}

// scanReachableServers reads storage and live client state.
func (p *MCPProxyServer) scanReachableServers() []string {
	if p.storage == nil || p.upstreamManager == nil {
		return nil
	}
	servers, err := p.storage.ListUpstreamServers()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(servers))
	for _, s := range servers {
		if s == nil || !s.Enabled || s.Quarantined {
			continue
		}
		client, ok := p.upstreamManager.GetClient(s.Name)
		if !ok || client == nil || !client.IsConnected() {
			continue
		}
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names
}

// serverListText renders "a, b, c (+N more)".
func (a callerAccess) serverListText() string {
	text := strings.Join(a.Servers, ", ")
	if a.More > 0 {
		if text != "" {
			text += ", "
		}
		text += "+" + strconv.Itoa(a.More) + " more"
	}
	return text
}

// accessBlock renders the per-caller ACCESS paragraph appended to the
// instructions. visible is the set of built-in tool names this caller sees
// on the surface (nil on the direct surface, which lists upstream tools).
func (a callerAccess) accessBlock(visible map[string]bool, advertise bool) string {
	var b strings.Builder
	b.WriteString("YOUR ACCESS (this connection):")
	if a.Profile != "" {
		b.WriteString(" profile '" + a.Profile + "' is active — servers outside it are not reachable.")
	}
	if advertise {
		if len(a.Servers) == 0 && a.More == 0 {
			b.WriteString(" No upstream servers are connected and reachable for this connection right now.")
		} else {
			b.WriteString(" Connected upstream servers you can use: " + a.serverListText() + ".")
			if visible != nil && visible["retrieve_tools"] {
				b.WriteString(" Find their tools with 'retrieve_tools' (query: the server name plus the task).")
			}
		}
	}
	switch len(a.Tiers) {
	case len(allTiers):
		// Unrestricted: say nothing rather than add noise.
	case 0:
		if a.CapExempt {
			b.WriteString(" Tool calls are refused except for tools the profile explicitly allows.")
		} else {
			b.WriteString(" Tool calls are not permitted for this connection; discovery only.")
		}
	default:
		allowed := make([]string, 0, len(a.Tiers))
		for _, t := range a.Tiers {
			allowed = append(allowed, t.String())
		}
		refused := make([]string, 0, len(allTiers))
		for _, t := range allTiers {
			if !containsTier(a.Tiers, t) {
				refused = append(refused, t.String())
			}
		}
		exempt := ""
		if a.CapExempt {
			exempt = ", except tools the profile explicitly allows"
		}
		b.WriteString(" Allowed operations: " + strings.Join(allowed, ", ") + " — " + strings.Join(refused, " and ") + " tool calls will be refused" + exempt + " (individual tools may also be restricted).")
	}
	if visible != nil {
		var unavailable []string
		for _, name := range []string{"code_execution", "upstream_servers", "quarantine_security"} {
			if !visible[name] {
				unavailable = append(unavailable, "'"+name+"'")
			}
		}
		if len(unavailable) > 0 {
			b.WriteString(" Not available here: " + strings.Join(unavailable, ", ") + ".")
		}
	}
	if b.Len() == len("YOUR ACCESS (this connection):") {
		return ""
	}
	return b.String()
}

func containsTier(ts []profile.Tier, t profile.Tier) bool {
	for _, x := range ts {
		if x == t {
			return true
		}
	}
	return false
}

// visibleBuiltins returns the names of the tools srv lists to this caller,
// through the same request-scoped filter p.server, callToolServer and
// codeExecServer apply on tools/list. It does not round-trip a tools/list
// message: that would re-enter the hooks (beforeAny claims the workspace-
// roots fetch on the first non-initialize request, which must not happen
// while the initialize response is still unwritten).
func (p *MCPProxyServer) visibleBuiltins(ctx context.Context, srv *mcpserver.MCPServer) map[string]bool {
	registered := srv.ListTools()
	tools := make([]mcp.Tool, 0, len(registered))
	for _, st := range registered {
		if st != nil {
			tools = append(tools, st.Tool)
		}
	}
	tools = p.filterProfileV3Tools(context.WithValue(ctx, noResolutionRecordKey{}, true), tools)
	visible := make(map[string]bool, len(tools))
	for _, t := range tools {
		visible[t.Name] = true
	}
	return visible
}

// instructionsForCaller returns the initialize/discover instructions for this
// caller on srv. static is the server's construction-time text.
//
//   - Operator-authored instructions are kept verbatim (FR-012), as is the
//     direct surface's own text and legend.
//   - Otherwise the default is recomposed from the built-ins this caller can
//     actually see, so it never points at a tool its profile hides.
//
// The per-caller ACCESS block is then appended.
func (p *MCPProxyServer) instructionsForCaller(ctx context.Context, srv *mcpserver.MCPServer, static string) string {
	if srv == nil {
		return static
	}
	access := p.callerAccessFor(ctx)
	advertise := p.advertiseUpstreamServers()

	base := static
	var visible map[string]bool
	if srv != p.directServer {
		visible = p.visibleBuiltins(ctx, srv)
		if directCustomInstructions(p.config) == "" {
			base = composeDefaultInstructions(visible)
		}
	}
	block := access.accessBlock(visible, advertise)
	if block == "" {
		return base
	}
	if base == "" {
		return block
	}
	return base + "\n\n" + block
}

// retrieveToolsServersSuffix is appended to retrieve_tools' description so a
// client that defers MCP tools behind its own keyword search (Claude Code's
// ToolSearch) matches "github", "jira", … to this tool.
func retrieveToolsServersSuffix(access callerAccess) string {
	if len(access.Servers) == 0 && access.More == 0 {
		return ""
	}
	return " CONNECTED SERVERS searchable here: " + access.serverListText() + "."
}

// filterAdvertiseServersInRetrieveTools is the tool filter that appends the
// caller's reachable servers to retrieve_tools' description. It never drops a
// tool, so it is neutral on mcp-go's call-time re-evaluation; the inventory
// is resolved only when retrieve_tools is in the list.
func (p *MCPProxyServer) filterAdvertiseServersInRetrieveTools(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	if !p.advertiseUpstreamServers() {
		return tools
	}
	at := -1
	for i := range tools {
		if tools[i].Name == "retrieve_tools" {
			at = i
			break
		}
	}
	if at < 0 {
		return tools
	}
	suffix := retrieveToolsServersSuffix(p.callerAccessFor(ctx))
	if suffix == "" {
		return tools
	}
	out := make([]mcp.Tool, len(tools))
	copy(out, tools)
	out[at].Description += suffix
	return out
}

// installCallerInstructionsHooks rewrites the instructions of every
// initialize and server/discover response for the calling session.
func installCallerInstructionsHooks(hooks *mcpserver.Hooks, proxy func() *MCPProxyServer) {
	hooks.AddAfterInitialize(func(ctx context.Context, _ any, _ *mcp.InitializeRequest, result *mcp.InitializeResult) {
		if p := proxy(); p != nil && result != nil {
			result.Instructions = p.instructionsForCaller(ctx, mcpserver.ServerFromContext(ctx), result.Instructions)
		}
	})
	hooks.AddAfterDiscover(func(ctx context.Context, _ any, _ *mcp.DiscoverRequest, result *mcp.DiscoverResult) {
		if p := proxy(); p != nil && result != nil {
			result.Instructions = p.instructionsForCaller(ctx, mcpserver.ServerFromContext(ctx), result.Instructions)
		}
	})
}

// NotifyUpstreamInventoryChanged pushes tools/list_changed to every session
// on the retrieve_tools-shaped surfaces when the set of reachable servers
// changed, so clients re-list and pick up the new retrieve_tools description.
// Guarded on a real change: an unconditional push would make every
// servers.changed event (tool reindex, health flap) look like a new tool set.
// The advertise flag is part of the key, so flipping it re-lists too.
func (p *MCPProxyServer) NotifyUpstreamInventoryChanged() {
	p.invalidateReachableServers()
	// Computed under the lock so two overlapping events cannot store an
	// older key last.
	p.inventoryMu.Lock()
	key := p.inventoryKey()
	changed := key != p.lastInventoryKey
	p.lastInventoryKey = key
	p.inventoryMu.Unlock()
	if !changed {
		return
	}
	for _, srv := range []*mcpserver.MCPServer{p.server, p.callToolServer, p.codeExecServer} {
		if srv != nil {
			srv.SendNotificationToAllClients(mcp.MethodNotificationToolsListChanged, nil)
		}
	}
}

// inventoryKey identifies what retrieve_tools' description advertises
// fleet-wide: the advertise flag and the reachable server set.
func (p *MCPProxyServer) inventoryKey() string {
	if !p.advertiseUpstreamServers() {
		return "off"
	}
	// A fresh scan, not the cache: events are rare, and seeding the cache at
	// construction would pin "nothing connected" for a TTL.
	return "on\x00" + strings.Join(p.scanReachableServers(), "\x00")
}

// seedInventoryKey records the construction-time inventory as the baseline,
// so the first servers.changed that leaves it unchanged sends nothing.
func (p *MCPProxyServer) seedInventoryKey() {
	p.inventoryMu.Lock()
	p.lastInventoryKey = p.inventoryKey()
	p.inventoryMu.Unlock()
}

// retrieveToolsReachNote is shared by every retrieve_tools description: the
// upstream tools are not listed, so the agent must search before improvising.
const retrieveToolsReachNote = " Upstream servers' tools are reachable ONLY through this tool — search here before using a shell CLI (gh, aws, kubectl, curl) or a raw HTTP API for an external service."

// The default instructions, one clause per constant, so the per-caller
// composition below and the static defaultInstructions share their wording.
const (
	instrIntro         = "This is mcpproxy-go, an MCP aggregator proxy that connects multiple upstream MCP servers and exposes their tools. "
	instrNotListed     = "IMPORTANT: the upstream servers' tools (e.g. GitHub, Jira, databases, cloud APIs) are NOT listed individually — they are reachable only through this proxy. "
	instrDiscovery     = "DISCOVERY: Use 'retrieve_tools' to search for tools by description across all connected upstream servers — do this before assuming a capability is unavailable. "
	instrCLIFallback   = "Before falling back to a shell CLI (gh, aws, kubectl, curl, ...) or a raw HTTP API for an external service, first call 'retrieve_tools' with the task and the service name (e.g. 'create github issue'), and prefer the MCP tool it finds. "
	instrRetry         = "If a first query returns nothing relevant, retry with different wording or just the service name before concluding the tool does not exist. "
	instrCalling       = "CALLING: When 'call_tool_read', 'call_tool_write', and 'call_tool_destructive' are exposed, call the variant named by the 'call_with' field of each retrieve_tools result. "
	instrCodeExecution = "When 'code_execution' is exposed, you may instead orchestrate several discovered tools in a single step with JavaScript. "
	instrDirect        = "When upstream tools are listed directly (named 'server__tool'), just call them by name. "
	instrSearchServers = "Do NOT use 'search_servers' to find existing tools — it searches EXTERNAL registries for adding NEW servers only. "
	instrUpstreamList  = "Use 'upstream_servers' with operation 'list' to see currently connected servers and their status. "
	// Discussion #948: carry the project links at the protocol level so an
	// agent (and anyone reading its logs) can always find the project.
	instrAbout = "ABOUT: MCPProxy homepage " + branding.Homepage + ", source " + branding.Repo + ", docs " + branding.Docs + "."
)

// composeDefaultInstructions builds the default instructions from the
// built-ins the caller actually sees, so a clause never names a tool the
// caller's profile hides. The direct-listing clause is omitted: this is used
// only on retrieve_tools-shaped surfaces, where upstream tools are not listed.
func composeDefaultInstructions(visible map[string]bool) string {
	var b strings.Builder
	b.WriteString(instrIntro + instrNotListed)
	if visible["retrieve_tools"] {
		b.WriteString(instrDiscovery + instrCLIFallback + instrRetry)
	}
	if visible["call_tool_read"] || visible["call_tool_write"] || visible["call_tool_destructive"] {
		b.WriteString(instrCalling)
	}
	if visible["code_execution"] {
		b.WriteString(instrCodeExecution)
	}
	if visible["search_servers"] {
		b.WriteString(instrSearchServers)
	}
	if visible["upstream_servers"] {
		b.WriteString(instrUpstreamList)
	}
	b.WriteString(instrAbout)
	return b.String()
}
