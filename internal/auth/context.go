package auth

import "context"

// Auth type constants.
const (
	AuthTypeAdmin = "admin"
	AuthTypeAgent = "agent"
)

// AuthContext carries authentication identity through request context.
type AuthContext struct {
	Type           string   // "admin" or "agent"
	AgentName      string   // Name of the agent token (empty for admin)
	TokenPrefix    string   // First 12 chars of raw token (empty for admin)
	AllowedServers []string // Servers this token can access (nil = all for admin)
	Permissions    []string // Permission tiers (nil = all for admin)
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
func (ac *AuthContext) IsAdmin() bool {
	return ac.Type == AuthTypeAdmin
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

// AdminContext returns an AuthContext representing full admin access.
func AdminContext() *AuthContext {
	return &AuthContext{
		Type: AuthTypeAdmin,
	}
}
