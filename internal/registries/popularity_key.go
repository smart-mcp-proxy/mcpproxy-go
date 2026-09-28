package registries

import (
	"net/url"
	"regexp"
	"strings"
)

// githubOwnerPattern/githubRepoPattern are GitHub's own name rules (FR-003).
var (
	githubOwnerPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	githubRepoPattern  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// GitHubRepoKey normalizes a source-code URL into a lower-cased "owner/repo"
// key when it identifies a github.com repository whose owner and repo both
// pass GitHub's name rules (Spec 110 FR-003). It handles every edge-case
// form spec.md calls out: a `git+` prefix, a `.git` suffix, a monorepo
// subpath (only the first two path segments are taken), a `www.` prefix, a
// trailing slash, and mixed case. Anything else — a non-GitHub host, a gist,
// a malformed URL, or an owner/repo that fails the name rules (including the
// literal repo names "." and "..") — returns ok=false and never causes a
// fetch.
func GitHubRepoKey(sourceCodeURL string) (key string, ok bool) {
	raw := strings.TrimSpace(sourceCodeURL)
	if raw == "" {
		return "", false
	}
	raw = strings.TrimPrefix(raw, "git+")

	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}

	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	if host != "github.com" {
		return "", false
	}

	path := strings.Trim(u.Path, "/")
	if path == "" {
		return "", false
	}
	parts := strings.SplitN(path, "/", 3) // owner, repo, rest (monorepo subpath discarded)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}

	owner := parts[0]
	repo := strings.TrimSuffix(parts[1], ".git")

	if repo == "." || repo == ".." {
		return "", false
	}
	if !githubOwnerPattern.MatchString(owner) || !githubRepoPattern.MatchString(repo) {
		return "", false
	}

	return strings.ToLower(owner + "/" + repo), true
}
