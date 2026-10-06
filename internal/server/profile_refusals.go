package server

import (
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// profileDisclosedTo reports whether a caller whose effective profile came
// from source is told that profile's name (Spec 108 D39, narrowing D27).
//
// The caller's own credential (pin, binding) or its own choice (url, session)
// decides its profile, so naming it reveals nothing the caller does not
// already hold. The operator's anonymous_profile is different: naming it would
// hand it to any local process that connects without a credential. "none" has
// no profile to name.
//
// This is the ONE predicate. The tool refusals and retrieve_tools' `profile`
// field both ask it, so refusal and discovery cannot diverge.
func profileDisclosedTo(source profile.Source) bool {
	switch source {
	case profile.SourcePin, profile.SourceBinding, profile.SourceURL, profile.SourceSession:
		return true
	default:
		return false
	}
}

// refusalSubject is the profile a tool refusal may name. Disclose is false for
// every caller that must not learn it; the refusal then keeps the original,
// non-disclosing "this profile" wording.
type refusalSubject struct {
	Slug     string
	Title    string
	Disclose bool
}

// profileRefusalSubject decides what a refusal under res may name. A dangling
// base has no compiled policy (and never reaches a tool refusal), and an empty
// name is the "All servers" base: neither names anything.
func profileRefusalSubject(res ProfileResolution, idx *profileIndex) refusalSubject {
	if res.Policy == nil || res.Name == "" || !profileDisclosedTo(profile.Source(res.Source)) {
		return refusalSubject{}
	}
	subj := refusalSubject{Slug: res.Name, Disclose: true}
	if idx != nil {
		if pc := idx.lookup(res.Name); pc != nil {
			subj.Title = pc.Title
		}
	}
	return subj
}

// label renders the profile as `"<title>" (<slug>)`, or `"<slug>"` alone when
// there is no title or it equals the slug. Both the title and the slug go
// through %q: a title is operator text that lands in an agent's context and in
// activity logs, so it can neither break out of the sentence nor add a line.
func (s refusalSubject) label() string {
	if s.Title == "" || s.Title == s.Slug {
		return fmt.Sprintf("%q", s.Slug)
	}
	return fmt.Sprintf("%q (%s)", s.Title, s.Slug)
}

// profileToolPolicyRefusal renders the refusal text for a tool excluded by a
// compiled profile policy. A caller that may learn its own profile is told
// which one it is (subj.Disclose); every other caller gets the stable,
// non-disclosing text. The two shapes are pinned by
// internal/profile/testdata/contract/tool_refusals.json.
func profileToolPolicyRefusal(reason profile.Reason, tier, cap profile.Tier, server, tool string, subj refusalSubject) (string, profile.BlockReason) {
	switch reason {
	case profile.ReasonAboveTierCap:
		capText := "read and write"
		if cap == profile.TierRead {
			capText = "read"
		}
		if subj.Disclose {
			return fmt.Sprintf("blocked by profile: %s:%s is a %s tool; profile %s allows %s tools only", server, tool, tier, subj.label(), capText), profile.BlockReasonTier
		}
		return fmt.Sprintf("blocked by profile: %s:%s is a %s tool; this profile allows %s tools only", server, tool, tier, capText), profile.BlockReasonTier
	case profile.ReasonDeniedByRule:
		if subj.Disclose {
			return fmt.Sprintf("blocked by profile: %s:%s is denied by a rule in profile %s", server, tool, subj.label()), profile.BlockReasonRule
		}
		return fmt.Sprintf("blocked by profile: %s:%s is denied by a profile rule", server, tool), profile.BlockReasonRule
	case profile.ReasonUnannotatedHidden:
		if subj.Disclose {
			return fmt.Sprintf("blocked by profile: %s:%s has no tier annotation; an operator can classify it in profile %s to allow it", server, tool, subj.label()), profile.BlockReasonUnannotated
		}
		return fmt.Sprintf("blocked by profile: %s:%s has no tier annotation; an operator can classify it in the profile to allow it", server, tool), profile.BlockReasonUnannotated
	default:
		return "blocked by profile: tool is not allowed by the active profile", profile.BlockReasonRule
	}
}
