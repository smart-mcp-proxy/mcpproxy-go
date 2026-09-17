package server

import (
	"context"
	"sort"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// cacheAuthorization derives the authorization a request acts under, in the
// shape the cache stamps on entries and checks on reads (Spec 104 FR-016a):
// caller kind, principal, agent server scope, permission tier, profile pin and
// the effective profile (token pin > URL > session set_profile).
//
// A request with no auth context at all is the unauthenticated /mcp path,
// which the auth middleware would otherwise have handed an anonymous admin
// context — so it is recorded as such rather than as an agent with nothing.
func (p *MCPProxyServer) cacheAuthorization(ctx context.Context) cache.Authorization {
	name, scope, idx := p.resolveActiveProfileWithIndex(ctx)
	return p.cacheAuthorizationWith(ctx, name, scope, idx)
}

// cacheAuthorizationWith is cacheAuthorization for a handler that has already
// resolved the request's effective profile — the same (name, scope, idx)
// triple it authorized the call against (idx is the (index, snapshot) pair
// resolveActiveProfileWithIndex resolved that (name, scope) pair from — see
// its doc comment). Handlers capture the producer stamp HERE, before the
// upstream call, not when the response comes back to be truncated: a profile
// deleted or narrowed while the call is in flight must not re-stamp a
// response that was authorized under the wider scope.
func (p *MCPProxyServer) cacheAuthorizationWith(ctx context.Context, profileName string, scope *profile.ProfileScope, idx *profileIndex) cache.Authorization {
	a := cache.Authorization{CallerKind: cache.CallerKindAnonymous}
	var agentAllowed []string
	isAgent := false
	if ac := auth.AuthContextFromContext(ctx); ac != nil {
		switch {
		case ac.Anonymous:
			a.CallerKind = cache.CallerKindAnonymous
		case ac.Type == auth.AuthTypeAgent:
			a.CallerKind = cache.CallerKindAgent
			a.Principal = ac.AgentName
			a.AllowedServers = append([]string(nil), ac.AllowedServers...)
			a.Permissions = append([]string(nil), ac.Permissions...)
			a.ProfilePin = ac.ProfilePin
			agentAllowed = ac.AllowedServers
			isAgent = true
		case ac.Type == auth.AuthTypeUser:
			a.CallerKind = cache.CallerKindUser
			a.Principal = ac.UserID
		case ac.Type == auth.AuthTypeAdminUser:
			a.CallerKind = cache.CallerKindAdminUser
			a.Principal = ac.UserID
		default:
			a.CallerKind = cache.CallerKindAdmin
		}
	}
	a.Profile = profileName
	if scope != nil {
		a.ProfileScoped = true
		// Spec 105 PR D review round 17 MUST-FIX: an agent token's stamp is
		// the CALLER-INTERSECTED profile membership — the same
		// EffectiveServersFor helper handleSetProfile's own scoped-visible
		// path already renders through (profile_tool.go), O(len(agentAllowed))
		// via idx's precomputed serverPos/members data, never a fleet- or
		// profile-declared-size walk. A profile member entirely outside the
		// token's own grant (the token never had, and never will have,
		// access to it) must not appear in the stamp: left in, its later
		// removal narrows what THIS token's cache read resolves to on the
		// next request and fails the redemption set-covering comparison
		// (internal/cache/authorization.go CouldHaveProduced/coversAll) for
		// an entry produced from a server the token remains fully authorized
		// for — an unrelated, never-authorized server's continued existence
		// becoming an observable side-channel through the cache layer
		// (SC-005-class disclosure). Non-agent scoped callers (admin/user
		// reading through a profile URL) carry no AllowedServers of their own
		// to intersect against, so they keep the resolver's full profile
		// membership — exactly resolveActiveProfileIn's documented
		// wildcard/profile's-own-membership semantic, untouched here.
		if isAgent && idx != nil {
			a.ProfileServers = idx.EffectiveServersFor(profileName, agentAllowed)
		} else {
			a.ProfileServers = scope.AllowedServerNames()
		}
		sort.Strings(a.ProfileServers)
	}
	return a
}

// producerCacheStore is the CacheStore the truncation helpers write through.
// It carries the producing request's authorization so every entry lands
// stamped, without the helpers (which have no ctx) needing to know about auth.
type producerCacheStore struct {
	store    *cache.Manager
	producer cache.Authorization
}

func (s producerCacheStore) Store(key, toolName string, args map[string]interface{}, content, recordPath string, totalRecords int) error {
	return s.store.StoreAs(key, toolName, args, content, recordPath, totalRecords, s.producer)
}

// cacheStoreAs returns the CacheStore a handler must write truncated payloads
// through, stamping every entry with producer — the authorization the handler
// captured when it authorized the call (never re-sampled at truncation time:
// read_cache gates the read and re-caches an oversize page, and a concurrent
// set_profile or profile deletion must not stamp the page under a different
// scope than the one that passed the gate). Returns an untyped nil when there
// is no cache manager so the helpers' `cacheStore != nil` short-circuit holds.
func (p *MCPProxyServer) cacheStoreAs(producer cache.Authorization) CacheStore {
	if p.cacheManager == nil {
		return nil
	}
	return producerCacheStore{store: p.cacheManager, producer: producer}
}
