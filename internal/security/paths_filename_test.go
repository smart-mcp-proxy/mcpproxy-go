package security

import "testing"

// Issue #795 / Codex PR-#977 finding 1: the *.kdbx / *.pgpass globs added to
// GetFilePathPatterns are only reachable when ExtractPaths actually extracts a
// bare filename with those extensions — the fileNamePattern extension list must
// carry them, or "opens vault.kdbx" never reaches glob matching at runtime.
func TestExtractPaths_NewSensitiveFilenames(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"opens vault.kdbx to read entries", "vault.kdbx"},
		{"reads prod.pgpass for connection auth", "prod.pgpass"},
		{"loads server.pem from disk", "server.pem"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			paths := ExtractPaths(tc.text)
			found := false
			for _, p := range paths {
				if p == tc.want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("ExtractPaths(%q) = %v — expected to extract %q", tc.text, paths, tc.want)
			}
		})
	}
}

// The extracted filenames must then actually match the paths.go globs.
func TestMatchesPathPattern_NewGlobs(t *testing.T) {
	cases := []struct {
		path    string
		pattern string
	}{
		{"vault.kdbx", "*.kdbx"},
		{"prod.pgpass", "*.pgpass"},
		{"~/.netrc", "~/.netrc"},
	}
	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			if !MatchesPathPattern(tc.path, tc.pattern) {
				t.Errorf("MatchesPathPattern(%q, %q) = false, want true", tc.path, tc.pattern)
			}
		})
	}
}
