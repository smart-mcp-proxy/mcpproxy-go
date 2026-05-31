package registries

import (
	"regexp"
	"sort"
	"strings"
)

// placeholderPattern matches shell-style env placeholders in an install command
// or URL: ${VAR}, ${VAR:-default}, or a bare $VAR. The captured group is the
// variable name (uppercase letters, digits, and underscores, not starting with
// a digit — the conventional env-var shape).
var placeholderPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::[^}]*)?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// DetectRequiredInputs returns the env vars / keys a server needs before it can
// run (FR-003 plumbing). It is best-effort and combines two sources:
//
//	(a) any RequiredInputs already declared on the entry (e.g. from a registry
//	    payload that surfaced them explicitly), and
//	(b) a heuristic scan of the install command and URL for ${VAR} / $VAR
//	    placeholders (decision O1 — no rich per-registry schema in this spec).
//
// Results are de-duplicated by Name and returned in a stable (sorted) order so
// the same entry always yields the same list across surfaces (CN-004).
func DetectRequiredInputs(entry *ServerEntry) []RequiredInput {
	if entry == nil {
		return nil
	}

	byName := make(map[string]RequiredInput)
	order := []string{}
	add := func(in RequiredInput) {
		if in.Name == "" {
			return
		}
		if existing, ok := byName[in.Name]; ok {
			// Prefer the richer declaration (keep a description / secret flag
			// if the explicit entry provided one).
			if existing.Description == "" && in.Description != "" {
				existing.Description = in.Description
			}
			existing.Secret = existing.Secret || in.Secret
			byName[in.Name] = existing
			return
		}
		byName[in.Name] = in
		order = append(order, in.Name)
	}

	// (a) explicit declarations win first so their metadata is preserved.
	for _, in := range entry.RequiredInputs {
		add(in)
	}

	// (b) heuristic placeholder scan over install command and URL.
	for _, src := range []string{entry.InstallCmd, entry.URL, entry.ConnectURL} {
		for _, m := range placeholderPattern.FindAllStringSubmatch(src, -1) {
			name := m[1]
			if name == "" {
				name = m[2]
			}
			add(RequiredInput{
				Name:   name,
				Secret: looksSecret(name),
			})
		}
	}

	if len(order) == 0 {
		return nil
	}

	sort.Strings(order)
	out := make([]RequiredInput, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out
}

// looksSecret guesses whether an env var holds a credential, so surfaces can
// mask it. Conservative substring match on the conventional secret-ish words.
func looksSecret(name string) bool {
	upper := strings.ToUpper(name)
	for _, kw := range []string{"TOKEN", "KEY", "SECRET", "PASSWORD", "PASS", "CREDENTIAL", "AUTH"} {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}
