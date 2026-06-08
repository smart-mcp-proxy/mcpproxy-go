package config

import (
	"fmt"
	"regexp"
)

// ProfileConfig is a named, stateless view over a subset of the configured
// upstream servers, addressable at /mcp/p/<name> (Spec 057). The Name is used
// verbatim as the URL slug. Servers references mcpServers[].name; unknown names
// warn-and-skip rather than fail (FR-015).
type ProfileConfig struct {
	Name    string   `json:"name"`    // URL slug, validated
	Servers []string `json:"servers"` // references to mcpServers[].name
}

// profileSlugPattern is the allowed profile-name form (FR-007): lowercase
// alphanumeric start, then up to 62 more of [a-z0-9_-] — max 63 chars total.
var profileSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// reservedProfileSlugs are URL path segments that collide with existing /mcp
// routes (/mcp/code, /mcp/call) or the /mcp/p prefix itself, plus "all"
// reserved for a future explicit all-servers profile (FR-007).
var reservedProfileSlugs = map[string]struct{}{
	"all":  {},
	"code": {},
	"call": {},
	"p":    {},
}

// ValidateProfiles enforces the Spec 057 profile rules (data-model.md). Fatal
// rules (invalid/reserved/duplicate slug) return an error that points at the
// offending entry; soft rules (unknown server, empty server list) return
// human-readable warnings without failing the load. A nil/empty Profiles slice
// is fully valid (returns no warnings, no error) — preserving zero-config
// behaviour (SC-004).
func ValidateProfiles(cfg *Config) (warnings []string, err error) {
	if cfg == nil || len(cfg.Profiles) == 0 {
		return nil, nil
	}

	// Build the set of known server names for membership checks.
	known := make(map[string]struct{}, len(cfg.Servers))
	for _, s := range cfg.Servers {
		if s != nil {
			known[s.Name] = struct{}{}
		}
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

		// Warning: empty server list (legal deny-all).
		if len(p.Servers) == 0 {
			warnings = append(warnings, fmt.Sprintf("profile %q has no servers; it will expose zero tools (deny-all placeholder)", p.Name))
			continue
		}
		// Warning: unknown server references (warn-and-skip).
		for _, srv := range p.Servers {
			if _, ok := known[srv]; !ok {
				warnings = append(warnings, fmt.Sprintf("profile %q references unknown server %q; it will be skipped", p.Name, srv))
			}
		}
	}

	return warnings, nil
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
