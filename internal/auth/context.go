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

	// Multi-user OAuth fields (server edition). Empty for non-user auth types.
	UserID      string // User's unique ULID identifier
	Email       string // User's email from OAuth provider
	DisplayName string // User's display name
	Role        string // "admin" or "user" (empty for API key / agent token auth)
	Provider    string // OAuth provider used (e.g., "google", "github")
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
