package server

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/mark3labs/mcp-go/mcp"

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
	name, scope := p.resolveActiveProfile(ctx)
	return p.cacheAuthorizationWith(ctx, name, scope)
}

// cacheAuthorizationWith is cacheAuthorization for a handler that has already
// resolved the request's effective profile — the same (name, scope) pair it
// authorized the call against. Handlers capture the producer stamp HERE, before
// the upstream call, not when the response comes back to be truncated: a
// profile deleted or narrowed while the call is in flight must not re-stamp a
// response that was authorized under the wider scope.
func (p *MCPProxyServer) cacheAuthorizationWith(ctx context.Context, profileName string, scope *profile.ProfileScope) cache.Authorization {
	a := cache.Authorization{CallerKind: cache.CallerKindAnonymous}
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
		a.ProfileServers = scope.AllowedServerNames()
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

// childPageProducer is the snapshot a recursively re-truncated read_cache
// page is stamped with: the parent entry's own producer (Spec 105 FR-001,
// monotone recursive provenance). GetRecordsAs refuses legacy provenance
// before a page exists, so a paged entry always carries a producer; the
// redeemer is the fallback only for a page that somehow arrives without one.
func childPageProducer(page *cache.ReadCacheResponse, redeemer cache.Authorization) cache.Authorization {
	if page != nil && page.Producer != nil {
		return *page.Producer
	}
	return redeemer
}

// readCacheRefusal renders a failed gated read as the read_cache tool error.
//
// For a scoped caller — anything but an administrator kind — an entry that
// exists but was produced under an authorization the caller does not hold,
// an entry that expired, an internal entry and a key that never existed all
// answer with ONE body, the not-found one, so the refusal is not an existence
// oracle (Spec 105 FR-001 "refusal is non-disclosing", FR-010(1)). The body
// keeps the "cache key not found" substring agents already handle, and the
// cache commits every refusal the way it commits a miss, so the timing class
// matches too. Storage failures (a bbolt error) stay distinct: they are
// operational faults, not answers about the key. A record the cache cannot
// decode is not one of them — the gated read treats it as unrecognised
// provenance (legacy: refused for every caller, invalidated).
//
// Administrators get the reason: legacy provenance (invalidated), an internal
// entry, or — for the anonymous /mcp caller — an authenticated
// administrator's entry.
func readCacheRefusal(err error, reader cache.Authorization) *mcp.CallToolResult {
	if !reader.IsAdministrator() && (errors.Is(err, cache.ErrUnauthorizedRead) || errors.Is(err, cache.ErrKeyExpired)) {
		err = cache.ErrKeyNotFound
	}
	switch {
	case errors.Is(err, cache.ErrLegacyProvenance):
		return mcp.NewToolResultError("Cache entry is not readable: it predates provenance stamping and has been invalidated. Re-run the original tool call to obtain a new cache key.")
	case errors.Is(err, cache.ErrInternalEntry):
		return mcp.NewToolResultError("Cache entry is not readable: it is internal to mcpproxy (registry or repository metadata) and cannot be paged through read_cache.")
	case errors.Is(err, cache.ErrUnauthorizedRead):
		return mcp.NewToolResultError("Cache entry is not readable with this credential: it was produced under a broader authorization (server scope, permission tier or profile) than this request holds. Re-run the original tool call with this credential to obtain your own cache key.")
	}
	return mcp.NewToolResultError(fmt.Sprintf("Failed to retrieve cached data: %v", err))
}
