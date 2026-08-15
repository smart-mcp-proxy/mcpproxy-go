package preflight

import "sort"

// Tier is the caller's disclosure tier (FR-013). The operator tier sees the
// full diagnosis (including hashes and `server_not_in_scope`); the agent-token
// tier gets scope-silence — an out-of-scope id is byte-indistinguishable from
// an ordinary `not_found`.
type Tier = string

const (
	TierOperator   Tier = "operator"
	TierAgentToken Tier = "agent_token"
)

// Scope is the immutable set of upstream servers an evaluation may see. A nil
// *Scope means unrestricted (operator, no profile, no token scope) — the same
// nil-receiver convention profile.ProfileScope uses.
type Scope struct {
	name    string
	servers map[string]struct{}
}

// NewScope builds a named scope over an explicit server set. An empty (but
// non-nil) set is a legal deny-everything scope.
func NewScope(name string, servers []string) *Scope {
	set := make(map[string]struct{}, len(servers))
	for _, s := range servers {
		if s == "" {
			continue
		}
		set[s] = struct{}{}
	}
	return &Scope{name: name, servers: set}
}

// Allows reports whether the named server is visible under this scope.
func (s *Scope) Allows(serverName string) bool {
	if s == nil {
		return true
	}
	if serverName == "" {
		return false
	}
	_, ok := s.servers[serverName]
	return ok
}

// Name returns the scope's label (a profile slug), or "" for an unnamed or
// unrestricted scope. It is used only in operator-tier `detail` text.
func (s *Scope) Name() string {
	if s == nil {
		return ""
	}
	return s.name
}

// ServerNames returns the sorted member list, or nil for an unrestricted scope.
func (s *Scope) ServerNames() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.servers))
	for name := range s.servers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ScopeInputs are the three independent restrictions that compose an evaluation
// scope (Spec 098 FR-010/FR-013, review finding 11). Each is optional; the
// effective scope is their intersection.
//
// A nil slice means "no restriction from this source". An agent token's
// AllowedServers containing "*" is likewise unrestricted (matching
// auth.AuthContext.CanAccessServer).
type ScopeInputs struct {
	// TokenServers is the agent token's allowed_servers list. nil for operator
	// callers (API key / socket / named pipe).
	TokenServers []string
	// TokenPinName / TokenPinServers describe the profile an agent token is
	// pinned to (auth.AgentToken.ProfilePin). Until this feature the pin was
	// dropped on the REST path; it is now carried into evaluation.
	TokenPinName    string
	TokenPinServers []string
	// RequestedProfileName / RequestedProfileServers describe the profile the
	// caller asked to evaluate under (`profile` in the request body).
	RequestedProfileName    string
	RequestedProfileServers []string
}

// ResolveScope computes the effective evaluation scope as
// token scope ∩ token pin ∩ requested profile.
//
// The scope's name is the most specific label available (requested profile,
// else token pin), which is what the operator-tier `server_not_in_scope` detail
// quotes. When a pinned token requests a DIFFERENT profile, the intersection is
// naturally the overlap of the two profiles — possibly empty, in which case
// every id resolves out of scope (and, at the agent-token tier, to `not_found`).
// The pin can therefore never be widened by naming another profile.
func ResolveScope(in ScopeInputs) *Scope {
	restrictions := make([][]string, 0, 3)
	if servers, restricted := normalizeTokenServers(in.TokenServers); restricted {
		restrictions = append(restrictions, servers)
	}
	if in.TokenPinName != "" {
		restrictions = append(restrictions, in.TokenPinServers)
	}
	if in.RequestedProfileName != "" {
		restrictions = append(restrictions, in.RequestedProfileServers)
	}

	name := in.RequestedProfileName
	if name == "" {
		name = in.TokenPinName
	}

	if len(restrictions) == 0 {
		return nil // unrestricted
	}

	// Intersect: start from the first restriction, keep only members present in
	// every other one.
	members := make(map[string]struct{}, len(restrictions[0]))
	for _, s := range restrictions[0] {
		if s != "" {
			members[s] = struct{}{}
		}
	}
	for _, next := range restrictions[1:] {
		allowed := make(map[string]struct{}, len(next))
		for _, s := range next {
			allowed[s] = struct{}{}
		}
		for s := range members {
			if _, ok := allowed[s]; !ok {
				delete(members, s)
			}
		}
	}

	names := make([]string, 0, len(members))
	for s := range members {
		names = append(names, s)
	}
	return NewScope(name, names)
}

// normalizeTokenServers reports the token's server restriction. A nil/empty
// list or a "*" wildcard entry means the token does not restrict servers.
func normalizeTokenServers(list []string) (servers []string, restricted bool) {
	if len(list) == 0 {
		return nil, false
	}
	for _, s := range list {
		if s == "*" {
			return nil, false
		}
	}
	return list, true
}
