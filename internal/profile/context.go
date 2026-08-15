// Package profile carries request-scoped in-proxy profile filtering (Spec 057).
//
// A profile is a named, stateless view over a subset of the configured upstream
// servers, addressable at /mcp/p/<slug>. A ProfileScope is resolved once per
// request and filters which servers that request may see/call. The URL tier is
// only one of its sources: the server package's resolver (Profiles v2) also
// builds a scope from an agent token's profile_pin and from a session's
// set_profile selection, in that precedence order. The scope stays an
// independent, auth-type-agnostic primitive that composes with (but does not
// depend on) the Spec 028 agent-token scope — an unauthenticated /mcp/p/<slug>
// connection runs as an admin AuthContext yet must still be profile-filtered.
package profile

import "context"

// ProfileScope is the immutable, request-scoped set of servers a profile
// exposes. It is resolved once — from a token pin, a /mcp/p/<slug> URL, or a
// session selection — and never mutated for the lifetime of a request.
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
// A nil receiver means no profile is in effect for this request — no URL slug,
// no session selection, no token pin — so it is not profile-filtered and every
// server is allowed (FR-010). A non-nil scope allows only servers in its set;
// the empty server name is never allowed for a real scope.
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

// DeniesAll reports whether this scope allows nothing at all — an empty
// profile, or the deny-all scope a stale agent-token pin resolves to. A nil
// receiver is "no profile filtering" and therefore never denies all.
//
// It exists so callers can distinguish "filtered to nothing" from "not
// filtered" without allocating an AllowedServerNames slice, notably to avoid
// lazily creating a per-profile Bleve index for a scope that can return no
// results anyway.
func (p *ProfileScope) DeniesAll() bool {
	return p != nil && len(p.servers) == 0
}

// AllowedServerNames returns the list of server names in this profile scope.
// Returns nil for a nil receiver (allow-all — no restriction list).
// Returns an empty slice for a non-nil scope with no servers (deny-all).
func (p *ProfileScope) AllowedServerNames() []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.servers))
	for s := range p.servers {
		out = append(out, s)
	}
	return out
}

// profileScopeKey is an unexported context key avoiding cross-package collisions.
type profileScopeKey struct{}

// WithProfileScope returns a context carrying the given ProfileScope.
func WithProfileScope(ctx context.Context, p *ProfileScope) context.Context {
	return context.WithValue(ctx, profileScopeKey{}, p)
}

// ProfileScopeFromContext extracts the URL-injected ProfileScope, or nil when
// the request did not enter via a profile URL.
//
// This is the URL TIER ONLY. Enforcement code must not call it directly: a
// request can carry a higher-precedence token pin or a session selection that
// never touches the context. Resolve the effective scope through the server
// package's resolveActiveProfile instead — reading this alone is how the
// code_execution sandbox once ran outside a pinned token's profile.
func ProfileScopeFromContext(ctx context.Context) *ProfileScope {
	p, _ := ctx.Value(profileScopeKey{}).(*ProfileScope)
	return p
}
