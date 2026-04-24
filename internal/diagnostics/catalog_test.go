package diagnostics

import (
	"regexp"
	"strings"
	"testing"
)

var codeRegex = regexp.MustCompile(`^MCPX_(OAUTH|STDIO|HTTP|DOCKER|CONFIG|QUARANTINE|NETWORK|UNKNOWN)_[A-Z0-9_]+$`)

// TestCatalog_Completeness enforces FR-003: every registered code has a
// non-empty message, at least one fix step, and a docs URL.
func TestCatalog_Completeness(t *testing.T) {
	if len(registry) == 0 {
		t.Fatalf("registry is empty — seed functions did not run")
	}

	for code, entry := range registry {
		t.Run(string(code), func(t *testing.T) {
			if !codeRegex.MatchString(string(entry.Code)) {
				t.Errorf("code %q does not match MCPX_<DOMAIN>_<SPECIFIC> pattern", entry.Code)
			}
			if entry.Code != code {
				t.Errorf("registry key %q does not match entry.Code %q", code, entry.Code)
			}
			if entry.UserMessage == "" {
				t.Errorf("code %q has empty UserMessage", code)
			}
			if len(entry.FixSteps) < 1 {
				t.Errorf("code %q has no fix steps", code)
			}
			if entry.DocsURL == "" {
				t.Errorf("code %q has empty DocsURL", code)
			}
			if entry.Severity != SeverityInfo && entry.Severity != SeverityWarn && entry.Severity != SeverityError {
				t.Errorf("code %q has invalid severity %q", code, entry.Severity)
			}
			if entry.Deprecated && entry.ReplacedBy == "" {
				t.Errorf("code %q is deprecated but has no ReplacedBy", code)
			}
			if entry.Deprecated && entry.ReplacedBy != "" {
				if _, ok := registry[entry.ReplacedBy]; !ok {
					t.Errorf("code %q deprecated → %q, but replacement is not registered", code, entry.ReplacedBy)
				}
			}
			// Every fix_step must have a label.
			for i, s := range entry.FixSteps {
				if s.Label == "" {
					t.Errorf("code %q fix_steps[%d] has empty label", code, i)
				}
				switch s.Type {
				case FixStepLink:
					if s.URL == "" {
						t.Errorf("code %q fix_steps[%d] is Link but URL is empty", code, i)
					}
				case FixStepCommand:
					if s.Command == "" {
						t.Errorf("code %q fix_steps[%d] is Command but Command is empty", code, i)
					}
				case FixStepButton:
					if s.FixerKey == "" {
						t.Errorf("code %q fix_steps[%d] is Button but FixerKey is empty", code, i)
					}
				default:
					t.Errorf("code %q fix_steps[%d] has invalid Type %q", code, i, s.Type)
				}
			}
		})
	}
}

// TestCatalog_MinimumDomainCoverage — FR-005: every domain has at least one
// registered code so classifiers have something to emit.
func TestCatalog_MinimumDomainCoverage(t *testing.T) {
	domains := map[string]int{
		"STDIO":      0,
		"OAUTH":      0,
		"HTTP":       0,
		"DOCKER":     0,
		"CONFIG":     0,
		"QUARANTINE": 0,
		"NETWORK":    0,
		"UNKNOWN":    0,
	}
	for code := range registry {
		for d := range domains {
			if strings.HasPrefix(string(code), "MCPX_"+d+"_") {
				domains[d]++
			}
		}
	}
	for d, n := range domains {
		if n == 0 {
			t.Errorf("domain %q has zero registered codes", d)
		}
	}
}

// TestCatalog_All_Stable — All() returns a deterministic order.
func TestCatalog_All_Stable(t *testing.T) {
	a := All()
	b := All()
	if len(a) != len(b) {
		t.Fatalf("All() length differs between calls: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Code != b[i].Code {
			t.Errorf("All() order unstable at index %d: %q vs %q", i, a[i].Code, b[i].Code)
		}
	}
}

// TestCatalog_DocsURL_MatchesCode — when DocsURL is relative, it must follow
// the docs/errors/<CODE>.md convention.
func TestCatalog_DocsURL_MatchesCode(t *testing.T) {
	for code, entry := range registry {
		if strings.HasPrefix(entry.DocsURL, "http://") || strings.HasPrefix(entry.DocsURL, "https://") {
			continue
		}
		expected := "docs/errors/" + string(code) + ".md"
		if entry.DocsURL != expected {
			t.Errorf("code %q has DocsURL %q, expected %q (or an absolute https:// URL)", code, entry.DocsURL, expected)
		}
	}
}
