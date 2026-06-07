// Package profile carries request-scoped in-proxy profile filtering (Spec 057).
//
// A profile is a named, stateless view over a subset of the configured upstream
// servers, addressable at /mcp/p/<slug>. A ProfileScope is resolved once by the
// profile middleware from the request URL and injected into the request context;
// the scope filters which servers a request may see/call. It is an independent,
// auth-type-agnostic primitive that composes with (but does not depend on) the
// Spec 028 agent-token scope — an unauthenticated /mcp/p/<slug> connection runs
// as an admin AuthContext yet must still be profile-filtered.
package profile

import "context"

// ProfileScope is the immutable, request-scoped set of servers a profile exposes.
// It is resolved by profileMiddleware from the /mcp/p/<slug> URL and never mutated
// for the lifetime of a request.
type ProfileScope struct {
	// Name is the resolved profile slug, used in rejection messages and activity
	// metadata (FR-012).
	Name string
	// servers is the effective set after unknown-server warn-skip. A non-nil but
	// empty set is a legal "deny everything" profile.
	servers map[string]struct{}
}

// NewProfileScope builds a scope for the named profile over the given effective
// server set. The returned scope is always non-nil (an empty/nil server list is
// a legal profile that allows nothing).
func NewProfileScope(name string, servers []string) *ProfileScope {
	set := make(map[string]struct{}, len(servers))
	for _, s := range servers {
		set[s] = struct{}{}
	}
	return &ProfileScope{Name: name, servers: set}
}

// Allows reports whether the named server is visible under this scope.
//
// A nil receiver means the request did not enter via /mcp/p/<slug> (it used /mcp,
// /mcp/code, or /mcp/call) and therefore is not profile-filtered — every server
// is allowed (FR-010). A non-nil scope allows only servers in its set; the empty
// server name is never allowed for a real scope.
func (p *ProfileScope) Allows(serverName string) bool {
	if p == nil {
		return true
	}
	if serverName == "" {
		return false
	}
	_, ok := p.servers[serverName]
	return ok
}

// profileScopeKey is an unexported context key avoiding cross-package collisions.
type profileScopeKey struct{}

// WithProfileScope returns a context carrying the given ProfileScope.
func WithProfileScope(ctx context.Context, p *ProfileScope) context.Context {
	return context.WithValue(ctx, profileScopeKey{}, p)
}

// ProfileScopeFromContext extracts the ProfileScope, or nil when the request did
// not enter via a profile URL (no filtering).
func ProfileScopeFromContext(ctx context.Context) *ProfileScope {
	p, _ := ctx.Value(profileScopeKey{}).(*ProfileScope)
	return p
}
