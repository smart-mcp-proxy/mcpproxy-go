package auth

// ScopedView returns the request's existing authorization identity unless it
// is an anonymous MCP caller confined by a named profile. Such callers retain
// the historical admin-shaped AnonymousContext everywhere else; management
// handlers use this non-admin view so their operation gates match a client
// credential without granting secret-reveal or administrator behavior.
func ScopedView(ac *AuthContext, confinedAnonymous bool) *AuthContext {
	if !confinedAnonymous {
		return ac
	}
	var view AuthContext
	if ac != nil {
		view = *ac
	}
	view.Type = AuthTypeAgent
	view.Anonymous = true
	view.CredentialKind = CredentialKindAnonymous
	view.AllowedServers = []string{"*"}
	view.Permissions = []string{PermRead, PermWrite, PermDestructive}
	return &view
}

// IsAdminOrAbsent keeps the historical admin-shaped management view for an
// unconfined anonymous MCP request while allowing ScopedView to narrow a
// confined anonymous request first.
func IsAdminOrAbsent(ac *AuthContext) bool { return ac == nil || ac.IsAdmin() }

// IsNonAdmin is the nil-safe counterpart used by handlers after ScopedView.
func IsNonAdmin(ac *AuthContext) bool { return ac != nil && !ac.IsAdmin() }
