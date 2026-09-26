package server

import (
	"context"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// ProfileResolution is the per-request Spec 108-c resolution result
// (data-model.md §4): one call returns the effective profile, WHERE it came
// from, and the (scope, policy) pair to decide against — never a second,
// independent lookup for any of the three.
type ProfileResolution struct {
	// Name is the resolved profile's slug, "" for source "none" or a
	// switchable binding/anonymous confinement whose base is "All servers"
	// (empty pin, legacy any-selectable).
	Name string
	// Source is one of the profile.Source* constants: pin, binding, url,
	// session, anonymous, none.
	Source string
	// Scope is the resolved profile's server scope; nil for "none" and for
	// the empty-pin "All servers" binding/anonymous case.
	Scope *profile.ProfileScope
	// Policy is the resolved profile's compiled policy; nil whenever Scope
	// is nil, and also for a dangling base (a name that no longer resolves).
	Policy *profile.CompiledPolicy
	// Base is the bound or anonymous base profile used for switchable_to
	// admission — "" for sources url/session/none, and for a source
	// pin/binding/anonymous whose base is "All servers" (empty pin).
	Base string
}

// clientCredentialFromContext returns (pin, mode, ok) for a Spec 108-c
// client-credential AuthContext; ok is false for every other context
// (admin, a regular agent token, user, anonymous/no-context).
func clientCredentialFromContext(ctx context.Context) (pin, mode string, ok bool) {
	ac := auth.AuthContextFromContext(ctx)
	if !ac.IsClientCredential() {
		return "", "", false
	}
	return ac.ProfilePin, ac.ProfileMode, true
}

// admittedBySwitchableTo reports whether target is reachable from base
// under basePolicy's switchable_to (FR-022): base itself is always
// admitted (a caller may always return to its own base — `set_profile("")`
// and re-selecting the base explicitly), and so is any name in
// basePolicy.SwitchableTo. A nil SwitchableTo (unset — legacy, or a v3
// profile that leaves the field unset) admits only base itself: "none",
// research D6.
func admittedBySwitchableTo(basePolicy *profile.CompiledPolicy, base, target string) bool {
	if target == base {
		return true
	}
	if basePolicy == nil || basePolicy.SwitchableTo == nil {
		return false
	}
	_, ok := basePolicy.SwitchableTo[target]
	return ok
}

// resolveV3Base computes the FR-020 "base" tiers — pin (a locked client
// credential or a regular agent token's profile_pin), binding (a switchable
// client credential's bound profile) and anonymous (anonymous_profile,
// credential kind "anonymous": no AuthContext, or an unrecognised token
// with require_mcp_auth off) — for idx's snapshot. It returns the base
// name, its Source, and whether that base is DANGLING (a non-empty name
// that no longer resolves in idx — FR-020: dangling bases never fall
// through to a lower tier or admit any URL/session selection).
//
// It never queries the URL or session tiers; ResolveProfileV3 composes
// those on top of what this returns.
func resolveV3Base(ctx context.Context, idx *profileIndex) (name string, source profile.Source, dangling bool) {
	if pin, mode, ok := clientCredentialFromContext(ctx); ok {
		if mode == auth.ProfileModeLocked {
			return pin, profile.SourcePin, pin != "" && idx.position(pin) < 0
		}
		// Switchable: pin (possibly "" = "All servers") is the BOUND base.
		return pin, profile.SourceBinding, pin != "" && idx.position(pin) < 0
	}
	// Regular agent-token pin (existing Profiles v2 rule, unchanged).
	if pin := profilePinFromContext(ctx); pin != "" {
		return pin, profile.SourcePin, idx.position(pin) < 0
	}
	// Anonymous: no AuthContext, or an explicitly anonymous one (unrecognised
	// token with require_mcp_auth off) — never an authenticated admin/user.
	ac := auth.AuthContextFromContext(ctx)
	if ac == nil || ac.Anonymous {
		if idx.cfg != nil && idx.cfg.AnonymousProfile != "" {
			base := idx.cfg.AnonymousProfile
			return base, profile.SourceAnonymous, idx.position(base) < 0
		}
	}
	return "", profile.SourceNone, false
}

// ResolveProfileV3 is the FR-020 resolution: highest wins — pin > url >
// session > binding > anonymous > none — against idx's (index, snapshot)
// pair. It is additive to, and does not replace, resolveActiveProfileFromIndex
// (the live Profiles v2 enforcement path every existing consumer still
// uses): wiring THIS resolution into execution (retrieve_tools, set_profile
// admission, call_tool_*) is 108-d's FR-009a-gated enforcement cutover.
// Exposed now so 108-d's tests can build directly on a resolver whose
// precedence and switchable_to admission are already pinned (T029/T036).
func (p *MCPProxyServer) ResolveProfileV3(ctx context.Context, idx *profileIndex) ProfileResolution {
	base, source, dangling := resolveV3Base(ctx, idx)

	// Tier 1: pin. Authoritative — never falls through, including when
	// dangling (FR-020).
	if source == profile.SourcePin && base != "" {
		if dangling {
			return ProfileResolution{Name: base, Source: string(source), Scope: profile.NewProfileScope(base, nil), Base: base}
		}
		return ProfileResolution{
			Name: base, Source: string(source),
			Scope: profileScopeFromIndex(idx, base), Policy: idx.PolicyFor(base), Base: base,
		}
	}

	// A dangling binding or anonymous base is likewise authoritative deny-all
	// (FR-020): no URL/session selection is admitted (its switchable_to is
	// unknown).
	if (source == profile.SourceBinding || source == profile.SourceAnonymous) && dangling {
		return ProfileResolution{Name: base, Source: string(source), Scope: profile.NewProfileScope(base, nil), Base: base}
	}

	basePolicy := idx.PolicyFor(base) // nil when base == "" (no such profile — expected)

	// Tier 2: URL.
	if urlScope := profile.ProfileScopeFromContext(ctx); urlScope != nil {
		if base == "" || admittedBySwitchableTo(basePolicy, base, urlScope.Name) {
			return ProfileResolution{
				Name: urlScope.Name, Source: string(profile.SourceURL),
				Scope: urlScope, Policy: idx.PolicyFor(urlScope.Name), Base: base,
			}
		}
		// Not admitted under this base: fall through exactly as an
		// unpermitted stored selection does below (FR-022).
	}

	// Tier 3: session (set_profile), re-validated against the CURRENT base
	// on every request; a selection no longer permitted is cleared.
	if p.sessionStore != nil {
		if sid := sessionIDFromContext(ctx); sid != "" {
			if sel := p.sessionStore.GetActiveProfile(sid); sel != "" {
				if base == "" || admittedBySwitchableTo(basePolicy, base, sel) {
					if scope := profileScopeFromIndex(idx, sel); scope != nil {
						return ProfileResolution{
							Name: sel, Source: string(profile.SourceSession),
							Scope: scope, Policy: idx.PolicyFor(sel), Base: base,
						}
					}
				}
				p.sessionStore.SetActiveProfile(sid, "")
			}
		}
	}

	// Tier 4/5: the binding/anonymous base itself, with no override in
	// effect. Empty base ("All servers" switchable binding) is legacy
	// any-selectable and resolves to "no profile" here — it was already
	// admitted above for any URL/session selection.
	if source == profile.SourceBinding || source == profile.SourceAnonymous {
		if base == "" {
			return ProfileResolution{Source: string(source)}
		}
		return ProfileResolution{
			Name: base, Source: string(source),
			Scope: profileScopeFromIndex(idx, base), Policy: basePolicy, Base: base,
		}
	}

	return ProfileResolution{Source: string(profile.SourceNone)}
}
