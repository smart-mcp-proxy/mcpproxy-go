package profile

import "testing"

// TestGlobMatcher pins FR-004: '*' is the only wildcard, matching is
// anchored to the full "server:tool" identity and case-sensitive; the
// canonical identity is built without re-splitting a tool name that itself
// contains '/' or "__".
func TestGlobMatcher(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		id      string
		want    bool
	}{
		{"exact match", "github:list_issues", "github:list_issues", true},
		{"exact mismatch", "github:list_issues", "github:create_issue", false},
		{"trailing star", "github:delete*", "github:delete_repo", true},
		{"leading and trailing star", "github:*secret*", "github:get_secret_scanning_alert", true},
		{"star matches nothing in between", "github:a*b", "github:ab", true},
		{"star does not cross server boundary implicitly", "github:*", "notion:update_page", false},
		{"anchored: no partial prefix match", "github:list", "github:list_issues", false},
		{"anchored: no partial suffix match", "github:issues", "github:list_issues", false},
		{"case-sensitive", "github:List_Issues", "github:list_issues", false},
		{"tool name containing a slash", "filesystem:read_text_file", "filesystem:read_text_file", true},
		{"tool name containing a slash, glob", "ns:sub/*", "ns:sub/erase", true},
		{"tool name containing double underscore (direct-surface alias shape)", "github:list__issues", "github:list__issues", true},
		{"tool identity with an embedded colon (namespaced raw name)", "a:ns:erase", "a:ns:erase", true},
		{"tool identity with an embedded colon, mismatch", "a:ns:erase", "a:ns:wipe", false},
		{"literal dot is not a regex any-char wildcard", "github:v1.0-sync", "github:v1.0-sync", true},
		{"literal dot near-miss: dot must match exactly, not any char", "github:v1.0-sync", "github:v1x0-sync", false},
		{"literal hyphen", "github:v1.0-sync", "github:v1.0_sync", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := compileGlob(tc.pattern)
			if got := m.match(tc.id); got != tc.want {
				t.Errorf("compileGlob(%q).match(%q) = %v, want %v", tc.pattern, tc.id, got, tc.want)
			}
		})
	}
}

func TestCanonicalIdentity(t *testing.T) {
	if got := canonicalIdentity("github", "list_issues"); got != "github:list_issues" {
		t.Errorf("canonicalIdentity = %q", got)
	}
	// A raw tool name that itself contains ':' (e.g. an upstream-namespaced
	// name "ns:erase" on server "a") must pass through untouched — the
	// identity is exactly server + ":" + tool, never re-split.
	if got := canonicalIdentity("a", "ns:erase"); got != "a:ns:erase" {
		t.Errorf("canonicalIdentity = %q", got)
	}
}

func TestPatternServer(t *testing.T) {
	cases := []struct {
		pattern    string
		wantServer string
		wantOK     bool
	}{
		{"github:list_issues", "github", true},
		{"github:*secret*", "github", true},
		{"*:tool", "", false},
		{"no-colon", "", false},
		{"a:ns:erase", "a", true},
	}
	for _, tc := range cases {
		server, ok := patternServer(tc.pattern)
		if server != tc.wantServer || ok != tc.wantOK {
			t.Errorf("patternServer(%q) = (%q, %v), want (%q, %v)", tc.pattern, server, ok, tc.wantServer, tc.wantOK)
		}
	}
}
