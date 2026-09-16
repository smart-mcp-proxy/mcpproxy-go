package cache

import (
	"errors"
	"fmt"
)

// Caller kinds recorded on a cache entry. They mirror the auth context types
// the MCP layer hands out; the cache package keeps its own copy so it does not
// depend on internal/auth.
const (
	CallerKindAdmin     = "admin"      // API-key admin: administrator
	CallerKindAdminUser = "admin_user" // OAuth admin (server edition): administrator
	CallerKindAnonymous = "anonymous"  // unauthenticated /mcp caller (back-compat admin): administrator-shaped
	CallerKindAgent     = "agent"      // agent token: bounded by AllowedServers/Permissions/ProfilePin
	CallerKindUser      = "user"       // OAuth user (server edition): bounded to its own identity
	// CallerKindInternal marks an entry the proxy wrote for ITSELF — the
	// registry search cache and the repository guesser cache. No request
	// produces such an entry, so no read_cache caller can redeem it, the
	// administrator included (Spec 105 FR-002, SC-005 named exception). Its
	// legitimate readers are the ungated Peek/Get paths of its writers.
	CallerKindInternal = "internal"
)

// ErrUnauthorizedRead is returned when a reader's authorization could not have
// produced the entry it asks for (Spec 104 FR-016a), and for the entries no
// request could have produced: legacy provenance and internal entries (Spec
// 105 FR-002). The handler surfaces one non-disclosing body for scoped callers.
var ErrUnauthorizedRead = errors.New("cache entry was produced under an authorization this request does not hold")

// ErrLegacyProvenance is the ErrUnauthorizedRead a gated read returns for an
// entry with absent, legacy or unrecognised provenance (Spec 105 FR-002). The
// entry has been invalidated by the time the caller sees it. errors.Is(err,
// ErrUnauthorizedRead) holds.
var ErrLegacyProvenance = fmt.Errorf("%w: entry predates provenance stamping and has been invalidated", ErrUnauthorizedRead)

// ErrInternalEntry is the ErrUnauthorizedRead a gated read returns for an
// internal (registry/guesser) entry. The entry is kept: its keys are
// guessable, and evicting on refusal would let any caller purge what the
// proxy's own readers depend on. errors.Is(err, ErrUnauthorizedRead) holds.
var ErrInternalEntry = fmt.Errorf("%w: entry is internal to the proxy", ErrUnauthorizedRead)

// Authorization is the authorization a cache entry was produced under, and the
// authorization a read_cache request presents. A cache key is a hash, not a
// credential: without this record a narrower token on the same MCP session
// could page a payload a broader token generated.
type Authorization struct {
	CallerKind string `json:"caller_kind"`
	// Principal identifies the caller within its kind: agent name for agent
	// tokens, user id for OAuth users. Empty for administrator kinds.
	Principal string `json:"principal,omitempty"`
	// AllowedServers is the agent token's server scope ("*" = every server).
	// nil means unrestricted (administrator kinds).
	AllowedServers []string `json:"allowed_servers,omitempty"`
	// Permissions is the agent token's permission tier list. nil means
	// unrestricted (administrator kinds).
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

// IsAdministrator reports whether the caller kind is an administrator kind:
// the API-key admin, the OAuth admin of the server edition, and the
// administrator-shaped anonymous /mcp caller. The name is deliberately about
// KIND, not reach — an administrator request can still be bounded to a
// profile, and the read gate ignores that binding (Spec 105 FR-001, D5).
func (a Authorization) IsAdministrator() bool {
	switch a.CallerKind {
	case CallerKindAdmin, CallerKindAdminUser, CallerKindAnonymous:
		return true
	}
	return false
}

// CouldHaveProduced reports whether reader is at least as broad as the
// producing authorization a — i.e. whether the reader could have generated
// the entry itself. That is the read gate for read_cache: a reader never sees
// a payload it could not have obtained by calling the tool.
//
// Superset is ordered by CALLER KIND FIRST (Spec 105 FR-001, research D5):
//
//   - An administrator reader qualifies for any snapshot, whatever its own
//     profile binding — unscoped, narrower, wider, empty, or a profile deleted
//     since. The anonymous kind is administrator-shaped for tool calls but is
//     not an identity (auth.AnonymousContext), so it ranks below an
//     authenticated administrator: it reads anonymous, agent and user entries,
//     never an authenticated administrator's.
//   - A non-administrator reader never qualifies for an administrator snapshot,
//     however broad its own grant, and never for a snapshot of another kind.
//   - Between agent snapshots every dimension must contain the snapshot's: the
//     deny-all guard (a reader bounded to an empty effective profile — an empty
//     profile, or the scope a stale pin resolves to — can call no tool and so
//     could not have produced ANY entry, its own deny-all-stamped one included),
//     then effective profile scope compared as server sets (a request bounded
//     to a profile is narrower than an unscoped one; a scoped reader must
//     currently cover every server the producer's profile exposed, so a profile
//     deleted or narrowed since no longer reads), pin equality, allowed-server
//     set and permission set.
//   - Internal entries (CallerKindInternal) were produced by no request and
//     match no reader (Spec 105 FR-002).
func (a Authorization) CouldHaveProduced(reader Authorization) bool {
	if a.CallerKind == CallerKindInternal {
		return false
	}
	if reader.IsAdministrator() {
		if reader.CallerKind == CallerKindAnonymous {
			return a.CallerKind != CallerKindAdmin && a.CallerKind != CallerKindAdminUser
		}
		return true
	}
	if a.IsAdministrator() || reader.CallerKind != a.CallerKind {
		return false
	}
	switch a.CallerKind {
	case CallerKindUser:
		return reader.Principal != "" && reader.Principal == a.Principal
	case CallerKindAgent:
		if reader.ProfileScoped {
			if len(reader.ProfileServers) == 0 {
				return false
			}
			if !a.ProfileScoped || !coversAll(reader.ProfileServers, a.ProfileServers) {
				return false
			}
		}
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
