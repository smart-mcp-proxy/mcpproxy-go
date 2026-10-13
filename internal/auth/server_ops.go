package auth

// Server-mutating operation identifiers.
//
// These name the write operations on upstream MCP servers that are exposed on
// BOTH the MCP `upstream_servers` / `quarantine_security` tools and the REST
// `/api/v1/servers/...` surface. They are the single source of truth for which
// operations agent tokens (non-admin) may NOT perform, so the two surfaces can
// never drift apart again (issues #877/#878).
//
// The string values for the operations that also exist on the MCP
// `upstream_servers` tool intentionally match that tool's operation names
// (see internal/server/mcp.go: operationAdd/operationRemove and the literal
// "update"/"patch"/"enable"/"disable"/"restart"/"refresh"/"add_from_registry"
// dispatch), so the MCP denylist can consume this policy without changing its
// observable behavior. REST-only operations (quarantine, login, …) use their
// own identifiers; the MCP surface never passes those strings here, so listing
// them is a no-op for MCP and closes the REST gap.
const (
	ServerOpAdd             = "add"
	ServerOpRemove          = "remove"
	ServerOpUpdate          = "update"
	ServerOpPatch           = "patch"
	ServerOpEnable          = "enable"
	ServerOpDisable         = "disable"
	ServerOpRestart         = "restart"
	ServerOpRefresh         = "refresh"
	ServerOpDiscoverTools   = "discover_tools"
	ServerOpQuarantine      = "quarantine"
	ServerOpUnquarantine    = "unquarantine"
	ServerOpAddFromRegistry = "add_from_registry"
	ServerOpLogin           = "login"
	ServerOpLogout          = "logout"
	ServerOpConfigToSecret  = "config_to_secret"
	ServerOpApproveTools    = "approve_tools"
	ServerOpBlockTools      = "block_tools"

	// REST-only mutating operations that reach server/security/config state
	// through routes OTHER than /api/v1/servers/{id}. They are denied to agents
	// so the /servers gate cannot be bypassed via a sibling endpoint
	// (config apply rewrites mcpServers; registry add creates a server; the
	// security scanner mutates a server's approval state).
	ServerOpConfigWrite     = "config_write"
	ServerOpScan            = "scan"
	ServerOpSecurityApprove = "security_approve"
	ServerOpSecurityReject  = "security_reject"
	ServerOpSecretWrite     = "secret_write"
	ServerOpDiagnosticsFix  = "diagnostics_fix"
)

// agentDeniedServerOps is the canonical set of server/tool-mutating operations
// that agent tokens are NOT permitted to perform, on any surface. Admin API
// keys (and OS-authenticated socket connections, which authenticate as admin)
// are never subject to this denylist.
//
// Read/observability operations (upstream_servers "list"/"tail_log", the
// index search, GET diagnostics, …) are deliberately absent — they stay
// available to scoped agent tokens.
var agentDeniedServerOps = map[string]struct{}{
	ServerOpAdd:             {},
	ServerOpRemove:          {},
	ServerOpUpdate:          {},
	ServerOpPatch:           {},
	ServerOpEnable:          {},
	ServerOpDisable:         {},
	ServerOpRestart:         {},
	ServerOpRefresh:         {},
	ServerOpDiscoverTools:   {},
	ServerOpQuarantine:      {},
	ServerOpUnquarantine:    {},
	ServerOpAddFromRegistry: {},
	ServerOpLogin:           {},
	ServerOpLogout:          {},
	ServerOpConfigToSecret:  {},
	ServerOpApproveTools:    {},
	ServerOpBlockTools:      {},
	ServerOpConfigWrite:     {},
	ServerOpScan:            {},
	ServerOpSecurityApprove: {},
	ServerOpSecurityReject:  {},
	ServerOpSecretWrite:     {},
	ServerOpDiagnosticsFix:  {},
}

// AgentDeniedServerOp reports whether the named operation is forbidden to agent
// tokens (non-admin auth contexts). It is case-sensitive: callers pass the
// canonical ServerOp* identifiers, never user-controlled free text.
func AgentDeniedServerOp(op string) bool {
	_, denied := agentDeniedServerOps[op]
	return denied
}

// AuthorizeServerOp is the shared gate both surfaces use. It returns true when
// the caller is permitted to perform op. An admin context (API key or
// OS-authenticated socket) is always allowed. A nil context means the request
// arrived without an AuthContext — the auth middleware only leaves it nil in
// test/no-config passthrough scenarios that never occur once a real
// *config.Config is loaded, so it is treated as allowed here (the middleware,
// not this policy, is responsible for rejecting unauthenticated production
// requests). Any present non-admin context (agent token) is denied when op is
// on the denylist.
func AuthorizeServerOp(ac *AuthContext, op string) bool {
	if ac == nil || ac.IsAdmin() {
		return true
	}
	return !AgentDeniedServerOp(op)
}
