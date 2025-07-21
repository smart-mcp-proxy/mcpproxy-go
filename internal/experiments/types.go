package experiments

import "time"

// RepositoryType represents the type of repository detected
type RepositoryType string

const (
	RepoTypeUnknown RepositoryType = "unknown"
	RepoTypeNPM     RepositoryType = "npm"
)

// RepositoryInfo contains information about a detected repository
type RepositoryInfo struct {
	Type        RepositoryType `json:"type"`
	PackageName string         `json:"package_name,omitempty"`
	Version     string         `json:"version,omitempty"`
	Description string         `json:"description,omitempty"`
	InstallCmd  string         `json:"install_cmd,omitempty"`
	URL         string         `json:"url,omitempty"`
	Exists      bool           `json:"exists"`
	Error       string         `json:"error,omitempty"`
}

// NPMPackageInfo represents npm package information from npm registry API
type NPMPackageInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	DistTags    map[string]string      `json:"dist-tags"`
	Versions    map[string]interface{} `json:"versions"`
	Time        map[string]string      `json:"time"`
}

// GuessResult contains the result of repository type guessing
// Only supports npm packages for GitHub repositories
type GuessResult struct {
	NPM *RepositoryInfo `json:"npm,omitempty"`
}

// CacheKey generates a cache key for repository guessing
func (r *RepositoryInfo) CacheKey(packageName string) string {
	return "repo_guess:" + string(r.Type) + ":" + packageName
}

// CacheTTL returns the cache TTL for repository information
func (r *RepositoryInfo) CacheTTL() time.Duration {
	// Cache repository info for 6 hours
	return 6 * time.Hour
}
