package server

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/headerfwd"
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
	var callerAllowed []string
	callerBounded := false
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
			callerAllowed = ac.AllowedServers
			callerBounded = true
		case ac.Type == auth.AuthTypeUser:
			// A server-edition user is bounded by the SAME dispatch gates
			// as an agent token — CanAccessServer, HasPermission and the
			// effective profile all apply to any non-admin context — so its
			// snapshot carries the same dimensions and the read gate holds
			// the user to them, identity included, through the header
			// digest (codex round 4: a snapshot of the user id alone let a
			// user narrowed to {b} redeem the {a} entry it produced
			// earlier; research D16: digest equality is the only user
			// admission). Spec 107 PR-C (IdP-group server grants, #1293,
			// already merged) gives a plain OAuth user its own restricted
			// AllowedServers via CanAccessServer's exact rule (nil/empty is
			// deny-all, same as an agent token) — so a User is caller-
			// bounded exactly like an Agent, not "no AllowedServers of its
			// own" as the ProfileServers comment below used to assume
			// (cross-model review, PR D: that assumption was already false
			// for this type, reopening round 17's cache side-channel for a
			// restricted OAuth user).
			a.CallerKind = cache.CallerKindUser
			a.Principal = ac.UserID
			a.AllowedServers = append([]string(nil), ac.AllowedServers...)
			a.Permissions = append([]string(nil), ac.Permissions...)
			a.ProfilePin = ac.ProfilePin
			callerAllowed = ac.AllowedServers
			callerBounded = true
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
		// Spec 105 PR D review round 17 MUST-FIX (widened by cross-model
		// review to cover AuthTypeUser, not only AuthTypeAgent — see the
		// AuthTypeUser case above): a caller-bounded token's stamp is the
		// CALLER-INTERSECTED profile membership — the same
		// EffectiveServersFor helper handleSetProfile's own scoped-visible
		// path already renders through (profile_tool.go), O(len(callerAllowed))
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
		// (SC-005-class disclosure). Only truly unbounded scoped callers
		// (admin/anonymous reading through a profile URL, which carry no
		// AllowedServers of their own to intersect against) keep the
		// resolver's full profile membership — exactly resolveActiveProfileIn's
		// documented wildcard/profile's-own-membership semantic, untouched
		// here.
		if callerBounded && idx != nil {
			a.ProfileServers = idx.EffectiveServersFor(profileName, callerAllowed)
		} else {
			a.ProfileServers = scope.AllowedServerNames()
		}
		sort.Strings(a.ProfileServers)

		// Spec 108 FR-027: the tool-policy dimensions of the stamp. A
		// profile with no compiled policy (idx nil, or the name resolved to
		// no ProfileConfig — a dangling pin) leaves PolicyFingerprint
		// empty, matching a legacy profile's own zero-value fingerprint
		// (Compile hashes only the six FR-001 fields, all unset either
		// way) — the read gate still bounds correctly on ProfileServers
		// alone in that case.
		if idx != nil {
			if pol := idx.PolicyFor(profileName); pol != nil {
				a.PolicyFingerprint = hex.EncodeToString(pol.Fingerprint[:])
			}
		}
		if p.mainServer != nil && p.mainServer.runtime != nil {
			if sup := p.mainServer.runtime.Supervisor(); sup != nil {
				a.ToolTierGeneration = sup.ToolTierGeneration()
			}
		}
	}
	return a
}

// producerCacheStore is the CacheStore the truncation helpers write through.
// It carries the producing request's authorization so every entry lands
// stamped, without the helpers (which have no ctx) needing to know about auth.
type producerCacheStore struct {
	store    *cache.Manager
	producer cache.Authorization
	// fwdDigest / fwdServer (Spec 112 FR-017) are set only when the producing
	// call forwarded client headers: the entry is then stamped with the digest
	// of that set so it is redeemable only by a request that forwards the same.
	fwdDigest string
	fwdServer string
}

func (s producerCacheStore) Store(key, toolName string, args map[string]interface{}, content, recordPath string, totalRecords int) error {
	if s.fwdDigest != "" {
		return s.store.StoreAsForwarded(key, toolName, args, content, recordPath, totalRecords, s.producer, s.fwdDigest, s.fwdServer)
	}
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

// cacheStoreAsForwarded is cacheStoreAs for a result produced by a call that
// forwarded the client headers in out to upstream `server` (Spec 112 FR-017):
// the entry is stamped with Digest(out) and the server. An empty out is
// exactly cacheStoreAs. The unscrubbed result is what is cached, isolated by
// that digest, because only a request forwarding the same set can redeem it.
func (p *MCPProxyServer) cacheStoreAsForwarded(producer cache.Authorization, out headerfwd.Snapshot, server string) CacheStore {
	if p.cacheManager == nil {
		return nil
	}
	return producerCacheStore{store: p.cacheManager, producer: producer, fwdDigest: headerfwd.Digest(out), fwdServer: server}
}

// cacheStoreAsChildPage is cacheStoreAs for a recursively re-truncated
// read_cache page: the child inherits its PARENT's forwarded-set fact along
// with the parent's producer snapshot, so a child page is redeemable by
// exactly the parent's readers (FR-017 stays monotone down the chain).
func (p *MCPProxyServer) cacheStoreAsChildPage(producer cache.Authorization, parent *cache.ReadCacheResponse) CacheStore {
	if p.cacheManager == nil {
		return nil
	}
	s := producerCacheStore{store: p.cacheManager, producer: producer}
	if parent != nil {
		s.fwdDigest, s.fwdServer = parent.ForwardedDigest, parent.ForwardedServer
	}
	return s
}

// forwardedEntryRedeem enforces the FR-017 digest fact for one gated
// read_cache page: an entry with no fact is unaffected; an entry produced
// under forwarded headers is redeemable only when this request's forwarded set
// for the same upstream digests equal (a different set, or none, is refused).
// The digest is compared and dropped; it is never logged or returned.
// It also returns the outbound set the
// redeeming request forwards to the entry's upstream, so the caller can scrub
// the forwarded values out of the activity copy of the page (FR-015b: the
// cache store holds the unscrubbed payload, the activity log must not).
// The set is empty for an entry with no forwarded fact.
func (p *MCPProxyServer) forwardedEntryRedeem(ctx context.Context, page *cache.ReadCacheResponse) (bool, headerfwd.Snapshot) {
	if page == nil || page.ForwardedDigest == "" {
		return true, headerfwd.Snapshot{}
	}
	snap, ok := headerfwd.SnapshotFrom(ctx)
	if !ok {
		return false, headerfwd.Snapshot{}
	}
	policy, ok := p.forwardPolicyFor(page.ForwardedServer)
	if !ok {
		return false, headerfwd.Snapshot{}
	}
	out := headerfwd.Outbound(snap, policy)
	got := headerfwd.Digest(out)
	if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(page.ForwardedDigest)) != 1 {
		return false, headerfwd.Snapshot{}
	}
	return true, out
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
// operational faults, not answers about the key. So is an entry the header
// ADMITTED the caller to and that then proved unreadable (an undecodable or
// header-disagreeing body): the caller was entitled to it, the refusal shape
// is decided on the fixed header only, and every caller kind is told the
// entry is unreadable and has been invalidated (cache.ErrEntryUnreadable;
// codex round 6). A frame the header itself cannot vouch for is unrecognised
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
	case errors.Is(err, cache.ErrEntryUnreadable):
		return mcp.NewToolResultError("Cache entry is unreadable: its stored record could not be decoded and it has been invalidated. Re-run the original tool call to obtain a new cache key.")
	case errors.Is(err, cache.ErrLegacyProvenance):
		return mcp.NewToolResultError("Cache entry is not readable: it predates provenance stamping and has been invalidated. Re-run the original tool call to obtain a new cache key.")
	case errors.Is(err, cache.ErrInternalEntry):
		return mcp.NewToolResultError("Cache entry is not readable: it is internal to mcpproxy (registry or repository metadata) and cannot be paged through read_cache.")
	case errors.Is(err, cache.ErrForwardedMismatch):
		return mcp.NewToolResultError("Cache entry is not readable with this request: it was produced for a different set of forwarded client headers (Spec 112). Re-run the original tool call from this client to obtain your own cache key.")
	case errors.Is(err, cache.ErrUnauthorizedRead):
		return mcp.NewToolResultError("Cache entry is not readable with this credential: it was produced under a broader authorization (server scope, permission tier or profile) than this request holds. Re-run the original tool call with this credential to obtain your own cache key.")
	}
	return mcp.NewToolResultError(fmt.Sprintf("Failed to retrieve cached data: %v", err))
}
