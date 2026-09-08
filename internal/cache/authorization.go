package cache

import "errors"

// Caller kinds recorded on a cache entry. They mirror the auth context types
// the MCP layer hands out; the cache package keeps its own copy so it does not
// depend on internal/auth.
const (
	CallerKindAdmin     = "admin"      // API-key admin: unrestricted
	CallerKindAdminUser = "admin_user" // OAuth admin (server edition): unrestricted
	CallerKindAnonymous = "anonymous"  // unauthenticated /mcp caller (back-compat admin): unrestricted
	CallerKindAgent     = "agent"      // agent token: bounded by AllowedServers/Permissions/ProfilePin
	CallerKindUser      = "user"       // OAuth user (server edition): bounded to its own identity
)

// ErrUnauthorizedRead is returned when a reader's authorization could not have
// produced the entry it asks for (Spec 104 FR-016a).
var ErrUnauthorizedRead = errors.New("cache entry was produced under an authorization this request does not hold")

// Authorization is the authorization a cache entry was produced under, and the
// authorization a read_cache request presents. A cache key is a hash, not a
// credential: without this record a narrower token on the same MCP session
// could page a payload a broader token generated.
type Authorization struct {
	CallerKind string `json:"caller_kind"`
	// Principal identifies the caller within its kind: agent name for agent
	// tokens, user id for OAuth users. Empty for unrestricted kinds.
	Principal string `json:"principal,omitempty"`
	// AllowedServers is the agent token's server scope ("*" = every server).
	// nil means unrestricted (admin kinds).
	AllowedServers []string `json:"allowed_servers,omitempty"`
	// Permissions is the agent token's permission tier list. nil means
	// unrestricted (admin kinds).
	Permissions []string `json:"permissions,omitempty"`
	// ProfilePin is the agent token's pinned profile ("" = unpinned).
	ProfilePin string `json:"profile_pin,omitempty"`
	// Profile is the effective profile the request was bounded to (token pin >
	// URL profile > session set_profile), "" when unscoped.
	Profile string `json:"profile,omitempty"`
	// ProfileScoped is true when a profile bounded the request. It is kept
	// separate from ProfileServers because a deny-all scope (an empty profile,
	// or the scope a stale pin resolves to) is scoped with NO servers, which
	// omitempty could not tell apart from unscoped.
	ProfileScoped bool `json:"profile_scoped,omitempty"`
	// ProfileServers is the effective server set of that profile at the time
	// of the request. The read gate compares server sets, not names: deleting
	// or narrowing a profile must revoke cached access, and a stale pin keeps
	// its name while resolving to deny-all.
	ProfileServers []string `json:"profile_servers,omitempty"`
}

// Unrestricted reports whether the caller kind carries no server/permission
// bound of its own.
func (a Authorization) Unrestricted() bool {
	switch a.CallerKind {
	case CallerKindAdmin, CallerKindAdminUser, CallerKindAnonymous:
		return true
	}
	return false
}

// CouldHaveProduced reports whether reader is at least as broad as the
// producing authorization a in every dimension — i.e. whether the reader could
// have generated the entry itself. That is the read gate for read_cache: a
// reader never sees a payload it could not have obtained by calling the tool.
//
// Profile scope is compared first and for every kind: a request bounded to a
// profile (by pin, URL or set_profile) is narrower than an unscoped one, and a
// scoped reader must currently cover every server the producer's profile
// exposed — compared as server sets, so a profile that was deleted (stale pin:
// same name, deny-all scope) or narrowed since the entry was produced no
// longer reads it.
//
// The anonymous kind is unrestricted for tool calls but is not an identity
// (auth.AnonymousContext), so it ranks below an authenticated admin: it may
// read anonymous, agent and user entries, never an authenticated admin's.
func (a Authorization) CouldHaveProduced(reader Authorization) bool {
	if reader.ProfileScoped {
		// A deny-all scope (empty profile, or a stale pin) can call no tool,
		// so it could not have produced ANY entry — including one that was
		// stamped deny-all because the profile vanished while the upstream
		// call was in flight.
		if len(reader.ProfileServers) == 0 {
			return false
		}
		if !a.ProfileScoped || !coversAll(reader.ProfileServers, a.ProfileServers) {
			return false
		}
	}
	if reader.Unrestricted() {
		if reader.CallerKind == CallerKindAnonymous {
			return a.CallerKind != CallerKindAdmin && a.CallerKind != CallerKindAdminUser
		}
		return true
	}
	if a.Unrestricted() {
		return false
	}
	if reader.CallerKind != a.CallerKind {
		return false
	}
	switch a.CallerKind {
	case CallerKindUser:
		return reader.Principal != "" && reader.Principal == a.Principal
	case CallerKindAgent:
		if reader.ProfilePin != "" && reader.ProfilePin != a.ProfilePin {
			return false
		}
		return coversServers(reader.AllowedServers, a.AllowedServers) &&
			coversAll(reader.Permissions, a.Permissions)
	}
	return false
}

// coversServers reports whether the reader's server scope includes every
// server in the producer's scope. A "*" wildcard covers everything; only a
// wildcard covers a wildcard.
func coversServers(reader, producer []string) bool {
	readerAll := false
	for _, s := range reader {
		if s == "*" {
			readerAll = true
			break
		}
	}
	if readerAll {
		return true
	}
	for _, s := range producer {
		if s == "*" {
			return false
		}
	}
	return coversAll(reader, producer)
}

// coversAll reports whether every element of want is present in have.
func coversAll(have, want []string) bool {
	set := make(map[string]struct{}, len(have))
	for _, s := range have {
		set[s] = struct{}{}
	}
	for _, s := range want {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}
