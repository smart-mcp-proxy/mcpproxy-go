package config

import (
	"fmt"
	"regexp"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

// ProfileConfig is a named, stateless view over a subset of the configured
// upstream servers, addressable at /mcp/p/<name> (Spec 057). The Name is used
// verbatim as the URL slug. Servers references mcpServers[].name; unknown names
// warn-and-skip rather than fail (FR-015).
//
// Spec 108 (Profiles v3) adds six optional POLICY fields — MaxTier,
// Unannotated, Tools, CodeExecution, ManagementTools, SwitchableTo — plus two
// display-only fields, Title and Description. A profile that sets none of the
// six policy fields is a "legacy" profile (IsLegacy) and behaves exactly as
// under Specs 057/105: no tier cap, no rules, code_execution/management_tools
// inherit the global gates, switchable_to legacy semantics (FR-002). The
// profile object stays edition-neutral: no user/team/owner field may ever be
// added here (FR-009, pinned by a reflection test over the JSON tags).
type ProfileConfig struct {
	Name        string   `json:"name"`                  // slug (Spec 057 rules unchanged)
	Servers     []string `json:"servers"`               // references to mcpServers[].name
	Title       string   `json:"title,omitempty"`       // display only, <= 80 chars (FR-001)
	Description string   `json:"description,omitempty"` // <= 500 chars (FR-001)

	// MaxTier caps the tier a tool may run at under this profile: "" (no
	// cap) | "read" | "write" | "destructive" (FR-001).
	MaxTier string `json:"max_tier,omitempty"`
	// Unannotated is this profile's handling of a tool whose effective
	// annotations carry no tier hint: "" (unset, see EffectiveUnannotated) |
	// "deny" | "as_write" | "as_read" (FR-001, FR-003).
	Unannotated string `json:"unannotated,omitempty"`
	// Tools holds the allow/deny/classify rule set (FR-001, FR-004, FR-005).
	Tools *ProfileToolRules `json:"tools,omitempty"`
	// CodeExecution: nil = inherit the global enable_code_execution gate;
	// non-nil narrows it (a profile can only narrow, never widen, FR-006).
	CodeExecution *bool `json:"code_execution,omitempty"`
	// ManagementTools: nil = legacy/inherit (FR-016); non-nil sets the
	// profile-level visibility of upstream_servers/quarantine_security.
	ManagementTools *bool `json:"management_tools,omitempty"`
	// SwitchableTo is the list of profile names a session under this
	// profile may `set_profile` into (FR-022). nil = legacy/none (research
	// D6); a non-nil EMPTY list is an explicit "none" and must round-trip as
	// `[]`, never be dropped by omitempty — hence the pointer (see the
	// MarshalJSON note below, and profiles_v3_test.go's switchable_to round
	// trip case).
	SwitchableTo *[]string `json:"switchable_to,omitempty"`
}

// ProfileToolRules is a profile's tool-level policy (FR-001, FR-004, FR-005):
// Allow/Deny are "server:tool" globs ('*' only, anchored, case-sensitive,
// matched against the canonical registration identity — never a flattened
// display name); Classify assigns a tier to specific UNANNOTATED tools
// ("server:tool" -> "read"|"write"|"destructive"). Deny beats allow on
// overlap (internal/profile.CompiledPolicy.Decide, FR-010).
type ProfileToolRules struct {
	Allow    []string          `json:"allow,omitempty"`
	Deny     []string          `json:"deny,omitempty"`
	Classify map[string]string `json:"classify,omitempty"`
}

// IsLegacy reports whether p sets none of the six Spec 108 policy fields.
// Title and Description are DISPLAY ONLY and deliberately excluded — setting
// them never makes a profile non-legacy (spec Definitions; data-model.md
// §1). This is the one predicate that decides both `retrieve_tools`'
// hidden_by_profile/profile presence (FR-011) and ProfileView.is_legacy.
func (p ProfileConfig) IsLegacy() bool {
	return p.MaxTier == "" &&
		p.Unannotated == "" &&
		p.Tools == nil &&
		p.CodeExecution == nil &&
		p.ManagementTools == nil &&
		p.SwitchableTo == nil
}

// EffectiveUnannotated returns this profile's effective handling of an
// unannotated, unclassified tool (FR-003): the explicit value if set, else
// "deny" when MaxTier is "read" or "write" (fail closed), else "as_read"
// (legacy/destructive default).
func (p ProfileConfig) EffectiveUnannotated() string {
	if p.Unannotated != "" {
		return p.Unannotated
	}
	if p.MaxTier == ProfileTierRead || p.MaxTier == ProfileTierWrite {
		return ProfileUnannotatedDeny
	}
	return ProfileUnannotatedAsRead
}

// EffectiveCodeExecution returns this profile's effective code_execution
// permission (FR-003a, data-model.md §1): the explicit value if set, else
// false when MaxTier is "read" or "write" (fail closed — a capped profile
// that leaves the field unset must not still be able to run scripts), else
// true (legacy/destructive: the global enable_code_execution gate decides).
// The global gate is ANDed at the call site (FR-006); this method never
// widens past it, only narrows.
func (p ProfileConfig) EffectiveCodeExecution() bool {
	if p.CodeExecution != nil {
		return *p.CodeExecution
	}
	if p.MaxTier == ProfileTierRead || p.MaxTier == ProfileTierWrite {
		return false
	}
	return true
}

// Profile tier and unannotated-policy wire values (FR-001). Kept here,
// alongside ProfileConfig, rather than in internal/profile: internal/profile
// imports internal/config (for ToolAnnotations), so internal/config cannot
// import internal/profile back without a cycle, and these spellings are
// needed by ValidateProfiles below. internal/profile's own enums
// (internal/profile/contract.go) are defined in terms of these same
// spellings.
const (
	ProfileTierRead        = "read"
	ProfileTierWrite       = "write"
	ProfileTierDestructive = "destructive"

	ProfileUnannotatedDeny    = "deny"
	ProfileUnannotatedAsWrite = "as_write"
	ProfileUnannotatedAsRead  = "as_read"
)

// policyEnforcementReadyBase is the FR-009a rollout gate's compile-time
// value: false from PR 108-a and flips to true in PR 108-d, in the same
// commit that lands the last execution gate — so a release built from any
// commit between 108-a and 108-d rejects every v3 policy field at load time
// and behaves exactly like a pre-108 build for profiles (discovery can never
// hide a tool that execution would still run). It is an unexported const —
// never a package variable — so no import can open the gate by assigning to
// it; the only way to open it outside 108-d is EnablePolicyForTest below.
const policyEnforcementReadyBase = false

// policyEnforcementTestOverride is the FR-009a test-only override's flag
// (zcode review: "the override cannot ship" — it must be unreachable from
// any non-test call site). It is flipped only by EnablePolicyForTest, whose
// own testing.Testing() guard is what keeps it out of production, not the
// atomic type.
var policyEnforcementTestOverride atomic.Bool

// PolicyEnforcementReady reports whether the FR-009a rollout gate is open:
// no build may admit a v3 policy field it cannot yet enforce on every
// dispatch path.
//
// The spec's prose calls this "profile.PolicyEnforcementReady" (FR-009a,
// data-model.md §1); the gate itself lives here, in package config, not
// package profile, because ValidateProfiles (which enforces it) runs at
// config load — before internal/profile even compiles a policy — and
// internal/profile already imports internal/config for ToolAnnotations, so
// a reference the other way round would cycle. internal/profile and every
// other consumer calls config.PolicyEnforcementReady() directly.
func PolicyEnforcementReady() bool {
	return policyEnforcementReadyBase || (policyEnforcementTestOverride.Load() && testing.Testing())
}

// EnablePolicyForTest opens the FR-009a gate for the duration of the
// caller's test only (the 108-a/108-b "test-only override" the spec and
// tasks.md call for). It panics when called outside a test binary
// (testing.Testing() false) — no env var, config field, flag or build tag
// can open the gate — and registers a tb.Cleanup that closes it again when
// the test ends, so callers need no defer/restore bookkeeping of their own.
// It takes testing.TB (not *testing.T) so any package's tests — not only
// internal/config's — can use it.
func EnablePolicyForTest(tb testing.TB) {
	if !testing.Testing() {
		panic("config: EnablePolicyForTest called outside a test binary")
	}
	tb.Helper()
	policyEnforcementTestOverride.Store(true)
	tb.Cleanup(func() { policyEnforcementTestOverride.Store(false) })
}

// profileSlugPattern is the allowed profile-name form (FR-007): lowercase
// alphanumeric start, then up to 62 more of [a-z0-9_-] — max 63 chars total.
var profileSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// IsValidProfileSlug reports whether name is a valid, filesystem-safe profile
// slug (FR-007): a lowercase-alphanumeric start followed by up to 62 more of
// [a-z0-9_-] (1-63 chars total). Callers that map a slug onto a directory path
// (e.g. per-profile Bleve indexes) use this as a defense-in-depth guard against
// path traversal, in addition to ValidateProfiles' load-time enforcement.
func IsValidProfileSlug(name string) bool {
	return profileSlugPattern.MatchString(name)
}

// reservedProfileSlugs are URL path segments that collide with existing /mcp
// routes (/mcp/code, /mcp/call) or the /mcp/p prefix itself, plus "all"
// reserved for a future explicit all-servers profile (FR-007).
var reservedProfileSlugs = map[string]struct{}{
	"all":  {},
	"code": {},
	"call": {},
	"p":    {},
}

// validProfileTiers / validUnannotatedPolicies are the FR-001 enum value
// sets, used by ValidateProfiles' enum checks.
var validProfileTiers = map[string]struct{}{
	ProfileTierRead:        {},
	ProfileTierWrite:       {},
	ProfileTierDestructive: {},
}

var validUnannotatedPolicies = map[string]struct{}{
	ProfileUnannotatedDeny:    {},
	ProfileUnannotatedAsWrite: {},
	ProfileUnannotatedAsRead:  {},
}

// profilePatternCharPattern is the FR-004 allowed character set for a
// "server:tool" rule pattern.
var profilePatternCharPattern = regexp.MustCompile(`^[A-Za-z0-9_.\-/:*]+$`)

// isValidToolPattern reports whether pattern is a syntactically valid
// "server:tool" glob (FR-004): drawn only from the allowed character set,
// with a ':' separating a non-empty server segment from a non-empty tool
// segment (the tool segment itself may contain '*' and further ':' — the
// FIRST ':' is the separator, matching internal/profile's canonical-identity
// convention).
func isValidToolPattern(pattern string) bool {
	if pattern == "" || !profilePatternCharPattern.MatchString(pattern) {
		return false
	}
	idx := indexByte(pattern, ':')
	return idx > 0 && idx < len(pattern)-1
}

// patternServer returns the literal server segment of a "server:tool"
// pattern (the part before the first ':'), or "" when the pattern has no
// ':' or that segment itself contains a wildcard (in which case it cannot be
// resolved to a single server name for the FR-007 outside-servers warning).
func patternServer(pattern string) (server string, literal bool) {
	idx := indexByte(pattern, ':')
	if idx <= 0 {
		return "", false
	}
	server = pattern[:idx]
	if indexByte(server, '*') >= 0 {
		return "", false
	}
	return server, true
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// ValidateProfiles enforces the Spec 057/108 profile rules (data-model.md
// §1). It is the ONE function used by config load, REST, CLI (via REST), MCP
// `profiles` and the editors' live validation (through REST) — FR-007 — so
// every surface returns identical messages. Fatal rules (invalid/reserved/
// duplicate slug, invalid enum/pattern value, switchable_to self-reference,
// title/description too long, an unset-enforcement-ready v3 field) return an
// error that points at the offending entry; soft rules (unknown server,
// empty server list, a rule/classification/switchable_to/anonymous_profile
// naming something outside scope) return human-readable warnings without
// failing the load — the offending entry is saved but ignored (FR-004,
// FR-007). A nil/empty Profiles slice is fully valid (returns no warnings, no
// error) — preserving zero-config behaviour (SC-004).
func ValidateProfiles(cfg *Config) (warnings []string, err error) {
	// FR-009a rollout gate, anonymous_profile row (data-model.md §1): a
	// non-empty anonymous_profile is fatal while the gate is closed,
	// regardless of whether it names a known or unknown profile, and
	// regardless of whether cfg has any profiles at all — checked first so
	// it applies even on the cfg==nil/no-profiles early return below.
	if cfg != nil && cfg.AnonymousProfile != "" && !PolicyEnforcementReady() {
		return nil, fmt.Errorf("anonymous_profile is not supported by this build (Profiles v3 enforcement incomplete)")
	}
	if cfg == nil || len(cfg.Profiles) == 0 {
		return validateAnonymousProfile(cfg, nil), nil
	}

	// Build the set of known server names for membership checks.
	known := make(map[string]struct{}, len(cfg.Servers))
	for _, s := range cfg.Servers {
		if s != nil {
			known[s.Name] = struct{}{}
		}
	}

	profileNames := make(map[string]struct{}, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		profileNames[p.Name] = struct{}{}
	}

	seen := make(map[string]int, len(cfg.Profiles)) // slug -> first index
	for i, p := range cfg.Profiles {
		// Fatal: slug format.
		if !profileSlugPattern.MatchString(p.Name) {
			return warnings, fmt.Errorf("profiles[%d]: invalid profile name %q: must match %s (lowercase alphanumeric, '-'/'_', 1-63 chars)", i, p.Name, profileSlugPattern.String())
		}
		// Fatal: reserved slug.
		if _, reserved := reservedProfileSlugs[p.Name]; reserved {
			return warnings, fmt.Errorf("profiles[%d]: profile name %q is reserved and cannot be used", i, p.Name)
		}
		// Fatal: duplicate name (name both occurrences).
		if first, dup := seen[p.Name]; dup {
			return warnings, fmt.Errorf("profiles[%d]: duplicate profile name %q (already defined at profiles[%d])", i, p.Name, first)
		}
		seen[p.Name] = i

		// FR-009a rollout gate: reject any v3 policy field until execution
		// can enforce it on every dispatch path (108-a..108-d). Checked
		// before the field-level fatal rules below so a build stuck on
		// 108-a/b/c never even reaches value validation for a field it
		// cannot enforce.
		if !PolicyEnforcementReady() {
			if field, set := firstSetPolicyField(p); set {
				return warnings, fmt.Errorf("profiles[%d]: %s is not supported by this build (Profiles v3 enforcement incomplete)", i, field)
			}
		}

		// Fatal: max_tier enum.
		if p.MaxTier != "" {
			if _, ok := validProfileTiers[p.MaxTier]; !ok {
				return warnings, fmt.Errorf("profiles[%d]: invalid max_tier %q: must be one of read, write, destructive", i, p.MaxTier)
			}
		}
		// Fatal: unannotated enum.
		if p.Unannotated != "" {
			if _, ok := validUnannotatedPolicies[p.Unannotated]; !ok {
				return warnings, fmt.Errorf("profiles[%d]: invalid unannotated %q: must be one of deny, as_write, as_read", i, p.Unannotated)
			}
		}
		// Fatal: title / description length (FR-001 limits). Counted in
		// runes, not bytes: a multi-byte character (e.g. Cyrillic, CJK,
		// emoji) must count once against the limit, not once per UTF-8 byte.
		if n := utf8.RuneCountInString(p.Title); n > 80 {
			return warnings, fmt.Errorf("profiles[%d]: title too long (%d chars, max 80)", i, n)
		}
		if n := utf8.RuneCountInString(p.Description); n > 500 {
			return warnings, fmt.Errorf("profiles[%d]: description too long (%d chars, max 500)", i, n)
		}
		// Fatal: switchable_to self-reference.
		if p.SwitchableTo != nil {
			for _, target := range *p.SwitchableTo {
				if target == p.Name {
					return warnings, fmt.Errorf("profiles[%d]: switchable_to cannot include the profile itself", i)
				}
			}
		}

		if p.Tools != nil {
			// Fatal: pattern syntax, then classify value enum.
			for _, pat := range p.Tools.Allow {
				if !isValidToolPattern(pat) {
					return warnings, fmt.Errorf("profiles[%d]: invalid tool pattern %q", i, pat)
				}
			}
			for _, pat := range p.Tools.Deny {
				if !isValidToolPattern(pat) {
					return warnings, fmt.Errorf("profiles[%d]: invalid tool pattern %q", i, pat)
				}
			}
			for pat, tier := range p.Tools.Classify {
				// Classify keys are an EXACT "server:tool" identity, never a
				// glob (data-model.md §1 ProfileToolRules.Classify: "exact
				// server:tool"; FR-005 applies a classification to specific
				// tools, not a pattern of them) — Decide looks a classify
				// entry up by exact map key, so a '*' here would silently
				// never match anything rather than classifying every tool
				// it looks like it should.
				if !isValidToolPattern(pat) || indexByte(pat, '*') >= 0 {
					return warnings, fmt.Errorf("profiles[%d]: invalid tool pattern %q", i, pat)
				}
				if _, ok := validProfileTiers[tier]; !ok {
					return warnings, fmt.Errorf("profiles[%d]: invalid classify tier %q: must be one of read, write, destructive", i, tier)
				}
			}
		}

		// Warning: empty server list (legal deny-all).
		if len(p.Servers) == 0 {
			warnings = append(warnings, fmt.Sprintf("profile %q has no servers; it will expose zero tools (deny-all placeholder)", p.Name))
		}
		// Warning: unknown server references (warn-and-skip).
		for _, srv := range p.Servers {
			if _, ok := known[srv]; !ok {
				warnings = append(warnings, fmt.Sprintf("profile %q references unknown server %q; it will be skipped", p.Name, srv))
			}
		}
		// Warning: rule/classify entries naming a server outside `servers`
		// (FR-004/FR-007) — saved, ignored, never widens the server set.
		if p.Tools != nil {
			inProfile := make(map[string]struct{}, len(p.Servers))
			for _, s := range p.Servers {
				inProfile[s] = struct{}{}
			}
			checkRule := func(pat string) {
				server, literal := patternServer(pat)
				if !literal {
					return
				}
				if _, ok := inProfile[server]; !ok {
					warnings = append(warnings, fmt.Sprintf("profile %q rule %q names server %q outside the profile; ignored", p.Name, pat, server))
				}
			}
			for _, pat := range p.Tools.Allow {
				checkRule(pat)
			}
			for _, pat := range p.Tools.Deny {
				checkRule(pat)
			}
			for pat := range p.Tools.Classify {
				checkRule(pat)
			}
		}
		// Warning: switchable_to names an unknown profile (fatal cases
		// handled above; the target may be defined later in the list, so
		// this is checked against the full profileNames set built up
		// front).
		if p.SwitchableTo != nil {
			for _, target := range *p.SwitchableTo {
				if _, ok := profileNames[target]; !ok {
					warnings = append(warnings, fmt.Sprintf("profile %q switchable_to references unknown profile %q; ignored", p.Name, target))
				}
			}
		}
	}

	warnings = append(warnings, validateAnonymousProfile(cfg, profileNames)...)
	return warnings, nil
}

// firstSetPolicyField returns the FR-001 name of the first v3 policy field
// p sets (in FR-009a message order), used by the FR-009a rollout gate.
func firstSetPolicyField(p ProfileConfig) (field string, set bool) {
	switch {
	case p.MaxTier != "":
		return "max_tier", true
	case p.Unannotated != "":
		return "unannotated", true
	case p.Tools != nil:
		return "tools", true
	case p.CodeExecution != nil:
		return "code_execution", true
	case p.ManagementTools != nil:
		return "management_tools", true
	case p.SwitchableTo != nil:
		return "switchable_to", true
	default:
		return "", false
	}
}

// validateAnonymousProfile returns the FR-008 warning when Config.
// AnonymousProfile names a profile that does not exist. profileNames is nil
// when cfg has no profiles at all (every non-empty AnonymousProfile is then
// unknown).
func validateAnonymousProfile(cfg *Config, profileNames map[string]struct{}) []string {
	if cfg == nil || cfg.AnonymousProfile == "" {
		return nil
	}
	if _, ok := profileNames[cfg.AnonymousProfile]; ok {
		return nil
	}
	return []string{fmt.Sprintf("anonymous_profile %q does not exist; anonymous callers are denied all tools", cfg.AnonymousProfile)}
}

// ProfileWarnings returns the non-fatal profile diagnostics captured by the most
// recent Validate() call (unknown-server and empty-server warnings). The boot
// path logs these via its logger; an empty result means no warnings.
func (c *Config) ProfileWarnings() []string {
	return c.profileWarnings
}

// EffectiveServers returns the profile's server list filtered to those that
// actually exist in cfg (the warn-skip result). Order is preserved and unknown
// names are dropped. Used by the profile middleware to build the request scope.
func (p ProfileConfig) EffectiveServers(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	known := make(map[string]struct{}, len(cfg.Servers))
	for _, s := range cfg.Servers {
		if s != nil {
			known[s.Name] = struct{}{}
		}
	}
	out := make([]string, 0, len(p.Servers))
	for _, srv := range p.Servers {
		if _, ok := known[srv]; ok {
			out = append(out, srv)
		}
	}
	return out
}
