package headerfwd

import (
	"fmt"
	"strings"
)

func isTokenChar(c byte) bool {
	switch {
	case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

func validToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isTokenChar(s[i]) {
			return false
		}
	}
	return true
}

// nameProblem returns a reason a single name is unusable, or "".
func nameProblem(name string) string {
	switch {
	case strings.Contains(name, "*"):
		return "wildcards are not allowed"
	case !validToken(name):
		return "not a valid HTTP header name"
	case Denied(name):
		return "header is on the never-forward list"
	}
	return ""
}

// ValidateNames returns one error per problem in an allowlist (FR-005a). Errors
// mention header names only, never values. static is the server's static
// headers, used for the case-insensitive collision check.
func ValidateNames(names []string, static map[string]string) []error {
	var errs []error
	if len(names) > MaxAllowNames {
		errs = append(errs, fmt.Errorf("forward_headers has %d entries, maximum is %d", len(names), MaxAllowNames))
	}
	seen := map[string]struct{}{}
	for _, n := range names {
		if p := nameProblem(n); p != "" {
			errs = append(errs, fmt.Errorf("forward_headers entry %q: %s", n, p))
			continue
		}
		l := strings.ToLower(n)
		if _, dup := seen[l]; dup {
			errs = append(errs, fmt.Errorf("forward_headers entry %q is a duplicate", n))
			continue
		}
		seen[l] = struct{}{}
		for k := range static {
			if strings.EqualFold(k, n) {
				errs = append(errs, fmt.Errorf("forward_headers entry %q collides with a static header of the same name", n))
				break
			}
		}
	}
	return errs
}

// NormalizeNames is the lenient load-time counterpart of ValidateNames
// (FR-005b): it returns the usable names in canonical form and the names it
// dropped (invalid, wildcard, denied, duplicate, or beyond the 32-entry cap).
func NormalizeNames(names []string) (kept []string, dropped []string) {
	seen := map[string]struct{}{}
	for _, n := range names {
		if nameProblem(n) != "" {
			dropped = append(dropped, n)
			continue
		}
		l := strings.ToLower(n)
		if _, dup := seen[l]; dup || len(kept) >= MaxAllowNames {
			dropped = append(dropped, n)
			continue
		}
		seen[l] = struct{}{}
		kept = append(kept, canon(n))
	}
	return kept, dropped
}
