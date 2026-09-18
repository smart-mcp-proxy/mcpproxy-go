package cache

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

// callerKindCodes is the one-byte encoding of each kind in the fixed frame
// header a stored record carries (see recordHeader). Code 0 is reserved for
// "no producer" (an unstamped record); a code this table does not name is
// provenance this binary does not recognise. Never renumber: the codes are
// persisted.
var callerKindCodes = map[string]uint8{
	CallerKindAdmin:     1,
	CallerKindAdminUser: 2,
	CallerKindAnonymous: 3,
	CallerKindAgent:     4,
	CallerKindUser:      5,
	CallerKindInternal:  6,
}

var callerKindNames = func() map[uint8]string {
	names := make(map[uint8]string, len(callerKindCodes))
	for kind, code := range callerKindCodes {
		names[code] = kind
	}
	return names
}()

// IsKnownCallerKind reports whether kind is one this binary stamps and
// gates on. A record carrying any other kind has provenance this binary does
// not recognise (see Record.HasCurrentProvenance).
func IsKnownCallerKind(kind string) bool {
	_, ok := callerKindCodes[kind]
	return ok
}

// callerKindCode is the frame-header code for kind; 0 for the empty kind
// (an unstamped record), which HasCurrentProvenance refuses.
func callerKindCode(kind string) uint8 {
	return callerKindCodes[kind]
}

// callerKindFromCode is the inverse of callerKindCode; "" for a code this
// binary does not know (0 included).
func callerKindFromCode(code uint8) string {
	return callerKindNames[code]
}

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

// ErrEntryUnreadable is returned by a gated read to a reader the header
// ADMITTED whose entry then proved unreadable: a body this binary cannot
// decode, or one that disagrees with the header it sits behind. It is not an
// ErrUnauthorizedRead — the reader was entitled to the entry, and what it
// learns (its own entry is corrupt) discloses nothing about another subject
// — and it is not a miss: the refusal shape is decided on the fixed header
// only, so an admitted reader never receives it (codex round 6). The entry
// has been invalidated by the time the caller sees the error.
var ErrEntryUnreadable = errors.New("cache entry is unreadable and has been invalidated")

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
	// nil means unrestricted for administrator kinds; for an agent it is an
	// empty grant, which the dispatch gates (auth.CanAccessServer) and the
	// read gate alike treat as deny-all.
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
	return kindIsAdministrator(a.CallerKind)
}

// IsScoped reports whether the caller kind is bounded by the dispatch gates
// — auth.CanAccessServer, HasPermission and the effective profile — rather
// than admitted as an administrator: agent tokens and server-edition OAuth
// users. A user is allowlist-scoped exactly like an agent at every dispatch
// gate (call_tool_*, direct dispatch, retrieve_tools/describe_tool
// visibility, set_profile), so the read gate bounds it the same way
// (codex round 4).
func (a Authorization) IsScoped() bool {
	return kindIsScoped(a.CallerKind)
}

// DenyAll reports whether a SCOPED snapshot could have authorized no tool
// call at all: an empty server grant (deny-all on every dispatch gate — an
// agent or user AuthContext with no AllowedServers reaches nothing), or a
// binding to an empty effective profile (an empty profile, or the scope a
// stale pin resolves to). As a producer snapshot it could not have authorized
// the entry it is stamped on, so no scoped reader qualifies for it however
// broad; as a reader it could not have produced ANY entry, its own
// deny-all-stamped one included. Administrator kinds are never deny-all here:
// the read gate admits them on kind alone. The frame header records this bit
// so the gated read can refuse a scoped reader without loading the snapshot.
func (a Authorization) DenyAll() bool {
	if !a.IsScoped() {
		return false
	}
	return len(a.AllowedServers) == 0 || (a.ProfileScoped && len(a.ProfileServers) == 0)
}

// Permission tiers, mirrored from internal/auth (which this package does not
// import). The frame header records a producer's tier set as bits so an
// unrestricted reader's coverage of it is decided on the header alone; a
// tier this table does not name sets permBitOther, which no header-only
// coverage check can cover — only digest equality (or an administrator)
// admits such a producer.
const (
	permRead        = "read"
	permWrite       = "write"
	permDestructive = "destructive"
)

const (
	permBitRead uint8 = 1 << iota
	permBitWrite
	permBitDestructive
	permBitOther uint8 = 1 << 7
	// permBitKnown is every bit permissionBits can set; a header tier byte
	// with any other bit was not written by this binary (decodeHeaderBytes).
	permBitKnown = permBitRead | permBitWrite | permBitDestructive | permBitOther
)

// permissionBits encodes a permission tier list as header bits.
func permissionBits(perms []string) uint8 {
	var bits uint8
	for _, p := range perms {
		switch p {
		case permRead:
			bits |= permBitRead
		case permWrite:
			bits |= permBitWrite
		case permDestructive:
			bits |= permBitDestructive
		default:
			bits |= permBitOther
		}
	}
	return bits
}

// canonicalAuthorization is the shape the digest hashes: the effective
// authorization with every list sorted and deduplicated, and WITHOUT the
// profile name — the gate compares server sets, not names (a renamed profile
// with the same servers is the same authorization; a stale pin keeps its
// name while resolving to deny-all). Principal is included: for a user it is
// the identity the gate requires, and for an agent it makes "digest-equal"
// mean the same credential.
type canonicalAuthorization struct {
	CallerKind     string   `json:"k"`
	Principal      string   `json:"p,omitempty"`
	AllowedServers []string `json:"s,omitempty"`
	Permissions    []string `json:"t,omitempty"`
	ProfilePin     string   `json:"pin,omitempty"`
	ProfileScoped  bool     `json:"ps,omitempty"`
	ProfileServers []string `json:"pss,omitempty"`
}

func sortedSet(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

// digest is the content address of an effective authorization: the SHA-256
// of its canonical encoding. It is what the frame header stores for the
// producer and what a reader is compared by, so the gate's same-kind verdict
// is one 32-byte comparison whatever the snapshot names (research D16).
func (a Authorization) digest() [sha256.Size]byte {
	data, err := json.Marshal(canonicalAuthorization{
		CallerKind:     a.CallerKind,
		Principal:      a.Principal,
		AllowedServers: sortedSet(a.AllowedServers),
		Permissions:    sortedSet(a.Permissions),
		ProfilePin:     a.ProfilePin,
		ProfileScoped:  a.ProfileScoped,
		ProfileServers: sortedSet(a.ProfileServers),
	})
	if err != nil {
		// Strings, string slices and a bool: json.Marshal cannot fail.
		panic(fmt.Sprintf("cache: marshal canonical authorization: %v", err))
	}
	return sha256.Sum256(data)
}

// unrestricted reports whether an AGENT reader is a superset of every agent
// snapshot on the server and profile dimensions: a wildcard grant, no pin
// and no effective profile. Tier coverage is checked separately, on the
// header's bits. A user is never unrestricted here: the header carries no
// identity, and a user reader must be the same user.
func (a Authorization) unrestricted() bool {
	return a.CallerKind == CallerKindAgent && !a.ProfileScoped && a.ProfilePin == "" &&
		slices.Contains(a.AllowedServers, "*")
}

// producerFacts is everything the gate knows about a producer: exactly the
// fields the fixed frame header carries. Derived from a stored header on the
// gated door and from the Authorization itself in CouldHaveProduced, so the
// two cannot disagree.
type producerFacts struct {
	Kind    string
	DenyAll bool
	Perms   uint8
	Digest  [sha256.Size]byte
}

func (a Authorization) facts() producerFacts {
	return producerFacts{Kind: a.CallerKind, DenyAll: a.DenyAll(), Perms: permissionBits(a.Permissions), Digest: a.digest()}
}

// readerFacts is the reader's side of the verdict, computed ONCE per request
// from the reader's own authorization — before the transaction, so a miss
// and a refusal do the same work — and compared against any number of
// headers in O(1).
type readerFacts struct {
	Kind         string
	DenyAll      bool
	Unrestricted bool
	Perms        uint8
	Digest       [sha256.Size]byte
}

func newReaderFacts(reader Authorization) readerFacts {
	return readerFacts{
		Kind:         reader.CallerKind,
		DenyAll:      reader.DenyAll(),
		Unrestricted: reader.unrestricted(),
		Perms:        permissionBits(reader.Permissions),
		Digest:       reader.digest(),
	}
}

func kindIsAdministrator(kind string) bool {
	switch kind {
	case CallerKindAdmin, CallerKindAdminUser, CallerKindAnonymous:
		return true
	}
	return false
}

func kindIsScoped(kind string) bool {
	return kind == CallerKindAgent || kind == CallerKindUser
}

// kindVerdict is the CALLER-KIND-FIRST part of the read gate, decided on
// the two kinds alone (Spec 105 FR-001, research D5). decided is false only
// when both sides are the same scoped kind, where the header's remaining
// facts decide.
func kindVerdict(producerKind, readerKind string) (admit, decided bool) {
	if producerKind == CallerKindInternal {
		return false, true
	}
	if kindIsAdministrator(readerKind) {
		if readerKind == CallerKindAnonymous {
			return producerKind != CallerKindAdmin && producerKind != CallerKindAdminUser, true
		}
		return true, true
	}
	if !kindIsScoped(readerKind) || readerKind != producerKind {
		return false, true
	}
	return false, false
}

// admits is THE read gate (research D16): a verdict from the producer's
// fixed header facts and the reader's precomputed facts, in O(1) — it never
// loads a producer snapshot, so a refusal does no work proportional to what
// it refuses, and a nonexistent key is indistinguishable from it in timing
// class (Spec 105 Definitions, non-disclosing refusal). In order:
//
//   - caller kind first (FR-001, D5): an administrator reader is admitted to
//     any request-kind snapshot (the anonymous kind never to an authenticated
//     administrator's); a reader of another kind, or an internal producer,
//     is refused;
//   - the deny-all bits, on both sides: a scoped snapshot that could have
//     authorized no call is no producer, and a deny-all reader could have
//     produced nothing;
//   - digest equality: the reader IS the effective authorization the entry
//     was produced under — kind, identity, server grant, tier set, pin and
//     profile server set, compared as sets;
//   - else, for an agent snapshot only, an UNRESTRICTED agent reader
//     (wildcard grant, no pin, no profile) whose tier set covers the
//     producer's — a superset of anything an agent could have produced.
//
// A reader that is strictly wider than the producer but bounded (a {a,b}
// grant over an {a} entry, an unscoped session over its own profiled entry,
// a user whose grant grew) is REFUSED: FR-001 obliges the door to refuse
// non-supersets; it does not oblige it to admit every superset, and deciding
// that shape would mean loading the snapshot. Fail-closed by design.
func admits(p producerFacts, r readerFacts) bool {
	if admit, decided := kindVerdict(p.Kind, r.Kind); decided {
		return admit
	}
	if p.DenyAll || r.DenyAll {
		return false
	}
	if p.Digest == r.Digest {
		return true
	}
	return p.Kind == CallerKindAgent && r.Unrestricted &&
		p.Perms&permBitOther == 0 && p.Perms&^r.Perms == 0
}

// CouldHaveProduced reports whether reader may redeem an entry produced
// under a: it is admits over the facts a stored header carries for a, so the
// predicate and the gated door (Manager.GetRecordsAs) return one verdict.
// See admits for the ordering.
func (a Authorization) CouldHaveProduced(reader Authorization) bool {
	return admits(a.facts(), newReaderFacts(reader))
}
