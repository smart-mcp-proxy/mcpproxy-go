package runtime

import (
	"fmt"
	"sort"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// FR-008a binding guard: contract types and the refusal. A client credential
// bound to a named profile stays confined only while an anonymous caller
// cannot simply omit the credential and reach more. Every path that could
// create that condition asks the guard for the delta over its WHOLE candidate
// state (never a per-route field check) and refuses with
// *BindingGuardError when the delta is non-empty.
//
// The evaluator itself lives in internal/server (profile_binding_guard.go): it
// needs the unexported profile index and the published tool snapshot, and the
// runtime cannot import server. It is injected through Runtime.SetBindingGuard.

// BindingRef names one client binding in a guard refusal (the `bindings[]`
// of the 409 body).
type BindingRef struct {
	ClientID  string `json:"client_id"`
	TokenName string `json:"token_name"`
	Profile   string `json:"profile"`
	Mode      string `json:"mode"`
}

// GuardFix is one remediation offered by a guard refusal (`fixes[]`).
// Kind is profile.GuardFixRequireMCPAuth or profile.GuardFixSetAnonymousProfile;
// Target is the profile name for the latter and is present only when that
// profile itself would not leave any binding bypassable.
type GuardFix struct {
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
}

// GuardState is one whole state the guard compares: a config plus the client
// credential records that would exist with it.
type GuardState struct {
	Config *config.Config
	Tokens []auth.AgentToken
}

// BindingGuard evaluates FR-008a. Implementations must be safe for concurrent
// use and must fail closed: when the condition cannot be evaluated they
// report the binding as bypassable.
type BindingGuard interface {
	// BindingGuardDelta returns the bindings that are bypassable in candidate
	// and were not already bypassable in current, sorted by client id.
	BindingGuardDelta(current, candidate GuardState) []BindingRef
	// BindingGuardFixes returns the fixes that would make delta empty for
	// candidate: require_mcp_auth always, plus set_anonymous_profile (with a
	// target when one is available).
	BindingGuardFixes(candidate GuardState, delta []BindingRef) []GuardFix
	// BindingGuardActiveBindings returns the bindings that are bypassable
	// right now (config and token store as they stand). It drives the
	// anonymous_denied_by_binding_guard warning.
	BindingGuardActiveBindings() []BindingRef
}

// BindingGuardError is the refusal returned when a write would leave a named
// client binding bypassable without auth. REST maps it to
// `409 binding_bypassable_without_auth`, the CLI prints it and exits 1.
type BindingGuardError struct {
	Bindings []BindingRef
	Fixes    []GuardFix
}

// Error returns the byte-stable refusal text (contracts/refusals.md).
func (e *BindingGuardError) Error() string {
	name := ""
	if len(e.Bindings) > 0 {
		name = e.Bindings[0].Profile
	}
	return fmt.Sprintf("a client bound to profile %s could escape it by omitting its credential while require_mcp_auth is off", name)
}

// Code is the wire `code` of the refusal.
func (e *BindingGuardError) Code() string { return profile.ErrorCodeBindingBypassable }

// CheckBindingGuard runs the guard over (current -> candidate) and returns a
// *BindingGuardError when the delta is non-empty, nil otherwise. A nil guard
// is replaced by ConservativeBindingGuard, so an unwired runtime can never
// silently skip the check.
func CheckBindingGuard(g BindingGuard, current, candidate GuardState) error {
	if g == nil {
		g = ConservativeBindingGuard{}
	}
	delta := g.BindingGuardDelta(current, candidate)
	if len(delta) == 0 {
		return nil
	}
	return &BindingGuardError{Bindings: delta, Fixes: g.BindingGuardFixes(candidate, delta)}
}

// ConservativeBindingGuard is the guard for callers that cannot evaluate the
// tool-dependent reachability comparison (the runtime before the server wires
// the real evaluator, and the offline CLI without a running core). It treats
// every active named binding as bypassable while require_mcp_auth is off, so
// it is only ever stricter than the real evaluator.
type ConservativeBindingGuard struct{}

func conservativeBypassable(state GuardState, t *auth.AgentToken, now time.Time) bool {
	return !config.EffectiveRequireMCPAuth(state.Config) && ActiveNamedBinding(t, now)
}

// ActiveNamedBinding reports whether t is an active client credential bound
// to a named profile (locked or switchable).
func ActiveNamedBinding(t *auth.AgentToken, now time.Time) bool {
	return t != nil && t.Kind == auth.KindClient && !t.Revoked &&
		t.ExpiresAt.After(now) && t.ProfilePin != "" &&
		(t.ProfileMode == auth.ProfileModeLocked || t.ProfileMode == auth.ProfileModeSwitchable)
}

// BindingRefOf projects a client credential record to its BindingRef.
func BindingRefOf(t *auth.AgentToken) BindingRef {
	return BindingRef{ClientID: t.ClientID, TokenName: t.Name, Profile: t.ProfilePin, Mode: t.ProfileMode}
}

// BindingGuardDelta implements BindingGuard.
func (ConservativeBindingGuard) BindingGuardDelta(current, candidate GuardState) []BindingRef {
	now := time.Now()
	already := make(map[string]bool)
	for i := range current.Tokens {
		t := &current.Tokens[i]
		if conservativeBypassable(current, t, now) {
			already[t.ClientID] = true
		}
	}
	var out []BindingRef
	for i := range candidate.Tokens {
		t := &candidate.Tokens[i]
		if conservativeBypassable(candidate, t, now) && !already[t.ClientID] {
			out = append(out, BindingRefOf(t))
		}
	}
	SortBindingRefs(out)
	return out
}

// BindingGuardFixes implements BindingGuard: with no evaluator, only turning
// auth on is a fix that is known to work.
func (ConservativeBindingGuard) BindingGuardFixes(GuardState, []BindingRef) []GuardFix {
	return []GuardFix{{Kind: profile.GuardFixRequireMCPAuth}}
}

// BindingGuardActiveBindings implements BindingGuard.
func (ConservativeBindingGuard) BindingGuardActiveBindings() []BindingRef { return nil }

// StrictOfflineBindingGuard is the guard of the offline CLI connect (no daemon,
// no published tool snapshot). It cannot evaluate reach, so it exempts only an
// UNCHANGED binding: the identity of a binding is (client_id, profile_pin,
// profile_mode), and any candidate bypassable binding without an identical one
// among the current bypassable bindings is refused. A reconnect that keeps the
// recorded binding passes; a re-point or mode change of an already-bound client
// does not, which ConservativeBindingGuard (whose re-point allowance other
// callers rely on) would let through.
type StrictOfflineBindingGuard struct{}

type bindingIdentity struct{ client, pin, mode string }

// BindingGuardDelta implements BindingGuard.
func (StrictOfflineBindingGuard) BindingGuardDelta(current, candidate GuardState) []BindingRef {
	now := time.Now()
	already := make(map[bindingIdentity]bool)
	for i := range current.Tokens {
		t := &current.Tokens[i]
		if conservativeBypassable(current, t, now) {
			already[bindingIdentity{t.ClientID, t.ProfilePin, t.ProfileMode}] = true
		}
	}
	var out []BindingRef
	for i := range candidate.Tokens {
		t := &candidate.Tokens[i]
		if conservativeBypassable(candidate, t, now) && !already[bindingIdentity{t.ClientID, t.ProfilePin, t.ProfileMode}] {
			out = append(out, BindingRefOf(t))
		}
	}
	SortBindingRefs(out)
	return out
}

// BindingGuardFixes implements BindingGuard.
func (StrictOfflineBindingGuard) BindingGuardFixes(st GuardState, delta []BindingRef) []GuardFix {
	return ConservativeBindingGuard{}.BindingGuardFixes(st, delta)
}

// BindingGuardActiveBindings implements BindingGuard.
func (StrictOfflineBindingGuard) BindingGuardActiveBindings() []BindingRef { return nil }

// SortBindingRefs orders refs by client id (the refusal's stable order).
func SortBindingRefs(refs []BindingRef) {
	sort.Slice(refs, func(i, j int) bool { return refs[i].ClientID < refs[j].ClientID })
}
