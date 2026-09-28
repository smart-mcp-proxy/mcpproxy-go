package profile

import (
	"regexp"
	"strings"
)

// globMatcher is an anchored, case-sensitive matcher over the canonical
// "server:tool" tool identity, compiled from a Spec 108 profile rule pattern
// whose only wildcard is '*' (FR-004): it matches zero or more characters at
// that position; every other character in the pattern must match literally.
// There is no '?' or character-class wildcard, and matching is always
// anchored to the full identity string (never a substring match).
type globMatcher struct {
	pattern string
	re      *regexp.Regexp
}

// compileGlob compiles a "server:tool" rule pattern into an anchored
// matcher. The pattern is assumed already validated (config.ValidateProfiles,
// FR-004) — an unparsable pattern here would be a validator bug, not a
// runtime condition, so this never errors.
func compileGlob(pattern string) *globMatcher {
	return &globMatcher{pattern: pattern, re: regexp.MustCompile("^" + globToRegexp(pattern) + "$")}
}

// globToRegexp renders pattern as an anchor-free regexp fragment: '*'
// becomes ".*", every other rune is escaped literally so regexp metachars in
// a tool name (e.g. the '.' in some upstream tool names) never act as
// wildcards.
func globToRegexp(pattern string) string {
	var sb strings.Builder
	for _, r := range pattern {
		if r == '*' {
			sb.WriteString(".*")
			continue
		}
		sb.WriteString(regexp.QuoteMeta(string(r)))
	}
	return sb.String()
}

// match reports whether the canonical "server:tool" identity matches this
// pattern. A nil matcher never matches (defensive; compileGlob never
// returns nil).
func (g *globMatcher) match(identity string) bool {
	if g == nil {
		return false
	}
	return g.re.MatchString(identity)
}

// matchAny reports whether identity matches any of matchers.
func matchAny(matchers []*globMatcher, identity string) bool {
	for _, m := range matchers {
		if m.match(identity) {
			return true
		}
	}
	return false
}

// canonicalIdentity builds the "server:tool" string a rule pattern is
// matched against — the Spec 105 registration identity, never a flattened
// display name. A tool's own raw name may itself contain ':' or '/' (e.g.
// "ns:erase", "ns/erase"); canonicalIdentity never re-splits or re-joins
// beyond this one ':' concatenation, so such names pass through exactly as
// they were discovered.
func canonicalIdentity(server, tool string) string {
	return server + ":" + tool
}

// patternServer returns the literal server segment of a "server:tool"
// pattern — the substring before the FIRST ':' — and true when that segment
// contains no wildcard (so it names exactly one server). It returns
// ok=false when the pattern has no ':' or its server segment itself
// contains '*' (validation and the FR-007 "names a server outside the
// profile" warning then cannot resolve it to a single name).
func patternServer(pattern string) (server string, ok bool) {
	idx := strings.IndexByte(pattern, ':')
	if idx <= 0 {
		return "", false
	}
	server = pattern[:idx]
	if strings.ContainsRune(server, '*') {
		return "", false
	}
	return server, true
}
