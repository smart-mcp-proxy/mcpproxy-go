package registries

import "testing"

// TestGitHubRepoKey covers every edge-case form spec.md calls out (FR-003).
func TestGitHubRepoKey(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantKey string
		wantOK  bool
	}{
		{"plain https", "https://github.com/o/r", "o/r", true},
		{"git suffix", "https://github.com/o/r.git", "o/r", true},
		{"monorepo subpath", "https://github.com/o/r/tree/main/src/x", "o/r", true},
		{"git+https prefix", "git+https://github.com/o/r", "o/r", true},
		{"git+https with .git", "git+https://github.com/o/r.git", "o/r", true},
		{"http scheme", "http://github.com/o/r", "o/r", true},
		{"www prefix", "https://www.github.com/o/r", "o/r", true},
		{"trailing slash", "https://github.com/o/r/", "o/r", true},
		{"mixed case", "https://GitHub.com/Owner/Repo", "owner/repo", true},
		{"reference monorepo", "https://github.com/modelcontextprotocol/servers/tree/main/src/fetch", "modelcontextprotocol/servers", true},

		{"empty", "", "", false},
		{"non-github host", "https://gitlab.com/o/r", "", false},
		{"gist host", "https://gist.github.com/o/r", "", false},
		{"owner only, no repo", "https://github.com/o", "", false},
		{"malformed url", "://not a url", "", false},
		{"repo is dot", "https://github.com/o/.", "", false},
		{"repo is dotdot", "https://github.com/o/..", "", false},
		{"owner fails name rule (leading dash)", "https://github.com/-o/r", "", false},
		{"repo fails name rule (space)", "https://github.com/o/r r", "", false},
		{"ssh-style (no scheme)", "git@github.com:o/r.git", "", false},
		{"non-http scheme", "ftp://github.com/o/r", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, ok := GitHubRepoKey(tt.url)
			if ok != tt.wantOK {
				t.Fatalf("GitHubRepoKey(%q) ok = %v, want %v (key=%q)", tt.url, ok, tt.wantOK, key)
			}
			if ok && key != tt.wantKey {
				t.Fatalf("GitHubRepoKey(%q) = %q, want %q", tt.url, key, tt.wantKey)
			}
		})
	}
}
