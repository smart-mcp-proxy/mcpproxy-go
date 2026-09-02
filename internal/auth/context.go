package auth

import "context"

// Auth type constants.
const (
	AuthTypeAdmin     = "admin"      // API key admin (personal edition)
	AuthTypeAgent     = "agent"      // Agent token authentication
	AuthTypeUser      = "user"       // Regular OAuth-authenticated user (server edition)
	AuthTypeAdminUser = "admin_user" // OAuth-authenticated admin (server edition)
)

// AuthContext carries authentication identity through request context.
type AuthContext struct {
	Type           string   // "admin", "agent", "user", or "admin_user"
	AgentName      string   // Name of the agent token (empty for admin)
	TokenPrefix    string   // First 12 chars of raw token (empty for admin)
	AllowedServers []string // Servers this token can access (nil = all for admin)
	Permissions    []string // Permission tiers (nil = all for admin)
	ProfilePin     string   // Profile this agent token is pinned to (Profiles v2 T3; empty if unpinned)

	// Multi-user OAuth fields (server edition). Empty for non-user auth types.
	UserID      string // User's unique ULID identifier
	Email       string // User's email from OAuth provider
	DisplayName string // User's display name
	Role        string // "admin" or "user" (empty for API key / agent token auth)
	Provider    string // OAuth provider used (e.g., "google", "github")

	// Anonymous marks a context that was NOT authenticated and only carries
	// admin type for backward compatibility (issue #1148).
	//
	// /mcp is deliberately unprotected by default (require_mcp_auth=false) so
	// MCP clients that cannot send an API key keep working, and the middleware
	// therefore hands an unauthenticated request an admin context. That is
	// fine for the operations those clients need — and it is NOT an identity,
	// so it must not satisfy a check that hands back raw credentials. See
	// CanRevealSecrets.
	Anonymous bool
}

// contextKey is an unexported type used as context key to avoid collisions.
type contextKey struct{}

// authContextKey is the context key for AuthContext values.
var authContextKey = contextKey{}

// WithAuthContext returns a new context with the given AuthContext attached.
func WithAuthContext(ctx context.Context, ac *AuthContext) context.Context {
	return context.WithValue(ctx, authContextKey, ac)
}

// AuthContextFromContext extracts the AuthContext from the context.
// Returns nil if no AuthContext is present.
func AuthContextFromContext(ctx context.Context) *AuthContext {
	ac, _ := ctx.Value(authContextKey).(*AuthContext)
	return ac
}

// IsAdmin returns true if this is an admin authentication context.
// Both API key admins ("admin") and OAuth-authenticated admins ("admin_user") are considered admin.
func (ac *AuthContext) IsAdmin() bool {
	return ac.Type == AuthTypeAdmin || ac.Type == AuthTypeAdminUser
}

// IsUser returns true if this is an OAuth-authenticated user context.
// Both regular users ("user") and admin users ("admin_user") are considered users.
func (ac *AuthContext) IsUser() bool {
	return ac.Type == AuthTypeUser || ac.Type == AuthTypeAdminUser
}

// IsAuthenticated returns true if this context has any authentication type set.
func (ac *AuthContext) IsAuthenticated() bool {
	return ac.Type != ""
}

// GetUserID returns the user's unique identifier, or empty string for non-user auth.
func (ac *AuthContext) GetUserID() string {
	return ac.UserID
}

// CanAccessServer checks whether this context is allowed to access the named server.
// Admin contexts have unrestricted access. Agent contexts check their AllowedServers
// list, where "*" is treated as a wildcard granting access to all servers.
func (ac *AuthContext) CanAccessServer(name string) bool {
	if ac.IsAdmin() {
		return true
	}
	if name == "" {
		return false
	}
	for _, s := range ac.AllowedServers {
		if s == "*" || s == name {
			return true
		}
	}
	return false
}

// HasPermission checks whether this context includes the given permission.
// Admin contexts have all permissions. Agent contexts check their Permissions list.
func (ac *AuthContext) HasPermission(perm string) bool {
	if ac.IsAdmin() {
		return true
	}
	for _, p := range ac.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// AdminContext returns an AuthContext representing full admin access (API key auth).
func AdminContext() *AuthContext {
	return &AuthContext{
		Type: AuthTypeAdmin,
	}
}

// AnonymousContext returns the back-compat admin context for an
// UNAUTHENTICATED MCP request (issue #1148).
//
// Type stays AuthTypeAdmin on purpose: every existing IsAdmin()-based
// allowance — ordinary tool calls, retrieve_tools, server add/update/patch,
// quarantine approvals — behaves exactly as it did, so no MCP client that
// works today stops working. The only thing the Anonymous bit changes is
// CanRevealSecrets.
func AnonymousContext() *AuthContext {
	return &AuthContext{
		Type:      AuthTypeAdmin,
		Anonymous: true,
	}
}

// CanRevealSecrets reports whether this caller may be handed RAW credential
// values (issue #1148) — today, `upstream_servers list` under the opt-in
// `reveal_secret_headers` flag.
//
// It is deliberately stricter than IsAdmin and deliberately nil-safe:
//
//   - an unauthenticated /mcp caller is admin for back-compat but has proved no
//     identity, so it gets masked values like everyone else;
//   - a nil context (the stdio transport before it installs one, an in-process
//     caller, a test) is unprivileged rather than admin-by-absence — the
//     opposite of the `authCtx != nil && !authCtx.IsAdmin()` shape, which lets
//     the ABSENCE of a token pass a gate that a scoped token fails.
//
// Authenticated admins — the API key, a tray/socket connection, stdio, an
// OAuth admin user in the server edition — are unaffected.
func (ac *AuthContext) CanRevealSecrets() bool {
	return ac != nil && ac.IsAdmin() && !ac.Anonymous
}

// UserContext returns an AuthContext for a regular OAuth-authenticated user.
func UserContext(userID, email, displayName, provider string) *AuthContext {
	return &AuthContext{
		Type:        AuthTypeUser,
		UserID:      userID,
		Email:       email,
		DisplayName: displayName,
		Role:        "user",
		Provider:    provider,
	}
}

// AdminUserContext returns an AuthContext for an OAuth-authenticated admin user.
func AdminUserContext(userID, email, displayName, provider string) *AuthContext {
	return &AuthContext{
		Type:        AuthTypeAdminUser,
		UserID:      userID,
		Email:       email,
		DisplayName: displayName,
		Role:        "admin",
		Provider:    provider,
	}
}

// RevealSecretsAllowed is THE shared predicate for "may this caller be handed
// raw credential values?" (issues #1148 and #1167).
//
// Both halves are required and neither is sufficient:
//
//   - flagEnabled — the operator's `reveal_secret_headers` opt-in, and
//   - CanRevealSecrets() — an authenticated, non-anonymous admin identity.
//
// #1167: the MCP door already ANDed the two; the REST list, the whole-config
// read, both doctor doors and the per-server diagnostics door checked the flag
// ALONE, so a read-only agent token scoped to one server received every other
// server's Authorization header, URL query credential, argv secret and env
// secret in plaintext the moment an operator turned the flag on. Every
// request-scoped door now reads the answer from here so they cannot drift
// apart again.
//
// Fails CLOSED on absence: a nil ctx (an in-process or future background
// caller) and a ctx with no AuthContext both yield false. A payload with no
// caller has no identity to check, so it gets masked values.
func RevealSecretsAllowed(ctx context.Context, flagEnabled bool) bool {
	if !flagEnabled || ctx == nil {
		return false
	}
	return AuthContextFromContext(ctx).CanRevealSecrets()
}

// CanEnumerateServer is THE shared predicate for "may this caller SEE that a
// server named `name` exists?" (issue #1166).
//
// The MCP door has filtered upstream enumeration through
// AuthContext.CanAccessServer since Spec 028; the REST doors never did, so a
// token scoped to `allowed_servers: ["alpha"]` still enumerated every
// configured server on GET /api/v1/servers, /api/v1/config, /api/v1/tools,
// /api/v1/status and the /events stream. Reads on both surfaces now answer
// from one place.
//
// Fails OPEN on absence, deliberately and in exact mirror of
// AuthorizeServerOp: apiKeyAuthMiddleware forwards a request with NO
// AuthContext whenever the controller has no usable config (the testing /
// bootstrap passthrough), and treating that as "sees nothing" would blank the
// server list for every such caller. Absence of a token must not be stricter
// than a scoped token — it must be treated exactly as today's unrestricted
// behaviour. The unauthenticated /mcp back-compat context (AnonymousContext)
// is admin-typed and likewise unrestricted here; hiding a server's NAME from
// it is not the boundary this predicate defends, revealing its CREDENTIALS is,
// and that is what RevealSecretsAllowed refuses it.
func CanEnumerateServer(ctx context.Context, name string) bool {
	if ctx == nil {
		return true
	}
	ac := AuthContextFromContext(ctx)
	if ac == nil || ac.IsAdmin() {
		return true
	}
	return ac.CanAccessServer(name)
}

// IsScopedCaller reports whether ctx carries a non-admin identity whose
// visibility is limited by AllowedServers. It is the cheap short-circuit for
// handlers that would otherwise copy a payload just to filter it: an admin (or
// an absent context, per CanEnumerateServer) sees everything, so the payload
// can be forwarded untouched.
func IsScopedCaller(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	ac := AuthContextFromContext(ctx)
	return ac != nil && !ac.IsAdmin()
}
