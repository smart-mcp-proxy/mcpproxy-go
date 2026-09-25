package profile

import (
	"crypto/sha256"
	"encoding/json"
	"sort"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Tier is a tool's capability classification under a profile: none (an
// unannotated tool with no cap-relevant classification) < read < write <
// destructive. It is an int, unlike contracts.Tier (a string), because
// CompiledPolicy.Decide compares it against a numeric cap (data-model.md
// §2).
type Tier int

const (
	TierUnannotated Tier = iota // 0: no tier hint and no classification/policy resolved one
	TierRead
	TierWrite
	TierDestructive
)

// String returns the contracts.Tier spelling, so every surface that prints a
// profile tier prints Spec 109's word (data-model.md §2).
func (t Tier) String() string {
	switch t {
	case TierRead:
		return string(contracts.TierRead)
	case TierWrite:
		return string(contracts.TierWrite)
	case TierDestructive:
		return string(contracts.TierDestructive)
	default:
		return string(contracts.TierUnannotated)
	}
}

// IntrinsicTier derives a tool's intrinsic tier from its effective
// annotations, through Spec 109-a's contracts.AnnotationTier (research D30)
// — the ONE place the annotation→tier rule is computed; this is an
// EXHAUSTIVE adapter over every contracts.Tier value, never a second
// implementation of the rule. found=false (the tool's identity could not be
// resolved, e.g. a stale/unknown server:tool pair) fails closed to
// TierDestructive regardless of a (FR-011).
func IntrinsicTier(a *config.ToolAnnotations, found bool) Tier {
	if !found {
		return TierDestructive
	}
	switch contracts.AnnotationTier(a) {
	case contracts.TierRead:
		return TierRead
	case contracts.TierWrite:
		return TierWrite
	case contracts.TierDestructive:
		return TierDestructive
	case contracts.TierUnannotated:
		return TierUnannotated
	default:
		// contracts.TierUnknown or any future value: fail closed.
		return TierDestructive
	}
}

// tierFromString maps an FR-001 tier spelling ("read"|"write"|"destructive")
// to Tier; "" or an unrecognised value returns TierUnannotated (0 = "no
// cap" when used as CompiledPolicy.Cap — callers that need a real value have
// already validated it via config.ValidateProfiles).
func tierFromString(s string) Tier {
	switch s {
	case config.ProfileTierRead:
		return TierRead
	case config.ProfileTierWrite:
		return TierWrite
	case config.ProfileTierDestructive:
		return TierDestructive
	default:
		return TierUnannotated
	}
}

// CompiledPolicy is the compiled, request-ready form of one profile's Spec
// 108 policy (data-model.md §2), built once per published config snapshot by
// Compile and cached inside profileIndex (internal/server) alongside its
// server-scope data, taken with the snapshot as one immutable pair (Spec 105
// D17 pattern) — never recompiled per request.
type CompiledPolicy struct {
	Name, Title string
	Servers     map[string]struct{}

	// Cap is the tier ceiling (FR-001 max_tier); TierUnannotated (0) means
	// "no cap" (legacy, or max_tier unset).
	Cap Tier
	// Unannotated is the effective handling of an unclassified, unannotated
	// tool (config.ProfileConfig.EffectiveUnannotated: "deny"|"as_write"|
	// "as_read").
	Unannotated string

	allow, deny []*globMatcher
	classify    map[string]Tier

	CodeExecution   *bool
	ManagementTools *bool
	// SwitchableTo is nil for "legacy/none" (FR-022); a non-nil, possibly
	// empty, set means "the profiles this one may set_profile into".
	SwitchableTo map[string]struct{}

	// Fingerprint is a sha256 over a canonical JSON encoding of exactly the
	// six FR-001 POLICY fields (never Name/Title, which are display-only,
	// and never Servers, which Spec 108 FR-027 tracks separately from the
	// policy fingerprint) — stable regardless of Go map iteration order
	// (encoding/json sorts string map keys) or the caller's slice order
	// (allow/deny are sorted before hashing), and changes whenever any one
	// of those six fields changes.
	Fingerprint [32]byte
}

// fingerprintFields is the canonical, ordered projection of exactly the
// FR-001 policy fields hashed into CompiledPolicy.Fingerprint.
type fingerprintFields struct {
	MaxTier         string            `json:"max_tier"`
	Unannotated     string            `json:"unannotated"`
	Allow           []string          `json:"allow,omitempty"`
	Deny            []string          `json:"deny,omitempty"`
	Classify        map[string]string `json:"classify,omitempty"`
	CodeExecution   *bool             `json:"code_execution,omitempty"`
	ManagementTools *bool             `json:"management_tools,omitempty"`
	SwitchableTo    *[]string         `json:"switchable_to,omitempty"`
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

// fingerprintOf computes the Fingerprint for pc's raw (uncompiled) policy
// fields, called by Compile.
func fingerprintOf(pc *config.ProfileConfig) [32]byte {
	f := fingerprintFields{
		MaxTier:         pc.MaxTier,
		Unannotated:     pc.Unannotated,
		CodeExecution:   pc.CodeExecution,
		ManagementTools: pc.ManagementTools,
	}
	if pc.Tools != nil {
		f.Allow = sortedCopy(pc.Tools.Allow)
		f.Deny = sortedCopy(pc.Tools.Deny)
		f.Classify = pc.Tools.Classify
	}
	if pc.SwitchableTo != nil {
		sorted := sortedCopy(*pc.SwitchableTo)
		if sorted == nil {
			sorted = []string{}
		}
		f.SwitchableTo = &sorted
	}
	b, err := json.Marshal(f)
	if err != nil {
		// f contains only strings, maps and slices of strings/bools —
		// Marshal cannot fail on this shape.
		panic("profile: fingerprintOf: " + err.Error())
	}
	return sha256.Sum256(b)
}

// Compile builds the request-ready CompiledPolicy for one profile (data-model
// §2). It is deliberately cheap for a legacy profile (pc.IsLegacy(): Tools is
// nil, so the allow/deny/classify loops below do zero work and Cap/
// SwitchableTo/CodeExecution/ManagementTools stay at their zero values) —
// the "fast path" a legacy snapshot takes through the profileIndex that
// caches this per snapshot (internal/server/profile_tool.go).
//
// Rule/classify entries naming a server outside pc.Servers were already
// warned-and-ignored at validation time (config.ValidateProfiles, FR-004/
// FR-007); Compile re-applies that same filter so a stale or hand-edited
// config can never let such an entry widen anything here either.
func Compile(pc *config.ProfileConfig) *CompiledPolicy {
	cp := &CompiledPolicy{
		Name:            pc.Name,
		Title:           pc.Title,
		Servers:         make(map[string]struct{}, len(pc.Servers)),
		Cap:             tierFromString(pc.MaxTier),
		Unannotated:     pc.EffectiveUnannotated(),
		CodeExecution:   pc.CodeExecution,
		ManagementTools: pc.ManagementTools,
		Fingerprint:     fingerprintOf(pc),
	}
	for _, s := range pc.Servers {
		cp.Servers[s] = struct{}{}
	}

	inProfile := func(server string) bool {
		_, ok := cp.Servers[server]
		return ok
	}
	keep := func(pattern string) bool {
		server, literal := patternServer(pattern)
		if !literal {
			// A wildcard server segment can't be resolved to one name at
			// validation time either, so it was never warned/ignored there
			// — keep it.
			return true
		}
		return inProfile(server)
	}

	if pc.Tools != nil {
		for _, pat := range pc.Tools.Allow {
			if keep(pat) {
				cp.allow = append(cp.allow, compileGlob(pat))
			}
		}
		for _, pat := range pc.Tools.Deny {
			if keep(pat) {
				cp.deny = append(cp.deny, compileGlob(pat))
			}
		}
		if len(pc.Tools.Classify) > 0 {
			cp.classify = make(map[string]Tier, len(pc.Tools.Classify))
			for pat, tier := range pc.Tools.Classify {
				if !keep(pat) {
					continue
				}
				cp.classify[pat] = tierFromString(tier)
			}
		}
	}

	if pc.SwitchableTo != nil {
		cp.SwitchableTo = make(map[string]struct{}, len(*pc.SwitchableTo))
		for _, name := range *pc.SwitchableTo {
			cp.SwitchableTo[name] = struct{}{}
		}
	}

	return cp
}

// effectiveTier is the "profile tier" of a tool under p (spec Definitions):
// its intrinsic tier when annotated; when unannotated, the profile's
// classification for that identity if one exists (FR-005: classify applies
// only to unannotated tools), else the profile's effective unannotated
// policy (deny -> stays TierUnannotated for reporting, the caller excludes
// it; as_write -> TierWrite; as_read -> TierRead).
func (p *CompiledPolicy) effectiveTier(identity string, intrinsic Tier) Tier {
	if intrinsic != TierUnannotated {
		return intrinsic
	}
	if p == nil {
		return TierRead
	}
	if t, ok := p.classify[identity]; ok {
		return t
	}
	switch p.Unannotated {
	case string(UnannotatedAsWrite):
		return TierWrite
	case string(UnannotatedAsRead):
		return TierRead
	default: // "" (unset, never happens post-EffectiveUnannotated) or "deny"
		return TierUnannotated
	}
}

// Decide computes the FR-010 profile decision for (server, tool) with its
// caller-supplied intrinsic tier (from IntrinsicTier). Order (highest
// priority first, matching FR-010 and contracts/enforcement-matrix.md
// exactly): (1) server not in the profile's servers -> excluded,
// server_not_in_profile; (2) a deny rule matches -> excluded,
// denied_by_rule; (3) an allow rule matches -> ADMITTED (an explicit allow
// bypasses the tier cap and unannotated policy entirely — deny beats allow
// on overlap because step 2 runs first); (4) unannotated and unclassified
// with policy "deny" -> excluded, unannotated_hidden; (5) the effective
// profile tier exceeds the cap -> excluded, above_tier_cap; else admitted.
//
// p == nil is treated as "no policy" (defensive; every real caller only
// calls Decide when it already holds a non-nil CompiledPolicy for the
// caller's effective profile — data-model.md §4 ProfileResolution.Policy is
// nil only for source "none", which never reaches Decide).
func (p *CompiledPolicy) Decide(server, tool string, intrinsic Tier) (admitted bool, reason Reason, profileTier Tier) {
	identity := canonicalIdentity(server, tool)
	profileTier = p.effectiveTier(identity, intrinsic)

	if p == nil {
		return true, ReasonNone, profileTier
	}
	if _, ok := p.Servers[server]; !ok {
		return false, ReasonServerNotInProfile, profileTier
	}
	if matchAny(p.deny, identity) {
		return false, ReasonDeniedByRule, profileTier
	}
	if matchAny(p.allow, identity) {
		return true, ReasonNone, profileTier
	}
	if intrinsic == TierUnannotated {
		if _, classified := p.classify[identity]; !classified && p.Unannotated == string(UnannotatedDeny) {
			return false, ReasonUnannotatedHidden, profileTier
		}
	}
	if p.Cap != TierUnannotated && profileTier > p.Cap {
		return false, ReasonAboveTierCap, profileTier
	}
	return true, ReasonNone, profileTier
}
