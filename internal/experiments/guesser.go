package experiments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"mcpproxy-go/internal/cache"

	"go.uber.org/zap"
)

const (
	npmRegistryURL = "https://registry.npmjs.org"
	requestTimeout = 10 * time.Second
	userAgent      = "mcpproxy-go/1.0"
)

// GitHub URL pattern for matching https://github.com/<author|org>/<repo>
var githubURLPattern = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)(?:/.*)?$`)

// Guesser handles repository type detection using external APIs
type Guesser struct {
	client       *http.Client
	cacheManager *cache.Manager
	logger       *zap.Logger
}

// NewGuesser creates a new repository type guesser
func NewGuesser(cacheManager *cache.Manager, logger *zap.Logger) *Guesser {
	return &Guesser{
		client: &http.Client{
			Timeout: requestTimeout,
		},
		cacheManager: cacheManager,
		logger:       logger,
	}
}

// GuessRepositoryType attempts to determine repository type from a GitHub URL
// Only handles GitHub URLs matching https://github.com/<author|org>/<repo>
// Only checks npm packages with @<author|org>/<repo> format
func (g *Guesser) GuessRepositoryType(ctx context.Context, githubURL string) (*GuessResult, error) {
	if githubURL == "" {
		return &GuessResult{}, nil
	}

	// Check if URL matches GitHub pattern
	matches := githubURLPattern.FindStringSubmatch(githubURL)
	if len(matches) != 3 {
		g.logger.Debug("URL does not match GitHub pattern", zap.String("url", githubURL))
		return &GuessResult{}, nil
	}

	author := matches[1]
	repo := matches[2]

	// Create npm package name in format @author/repo
	packageName := fmt.Sprintf("@%s/%s", author, repo)

	g.logger.Debug("Checking npm package for GitHub repo",
		zap.String("github_url", githubURL),
		zap.String("author", author),
		zap.String("repo", repo),
		zap.String("package_name", packageName))

	// Check npm package
	npmInfo := g.checkNPMPackage(ctx, packageName)

	result := &GuessResult{}
	if npmInfo.Exists {
		result.NPM = npmInfo
	}

	return result, nil
}

// checkNPMPackage checks if a package exists on npm registry
func (g *Guesser) checkNPMPackage(ctx context.Context, packageName string) *RepositoryInfo {
	// Check cache first
	cacheKey := "npm:" + packageName
	if g.cacheManager != nil {
		if cached, err := g.cacheManager.Get(cacheKey); err == nil {
			var info RepositoryInfo
			if err := json.Unmarshal([]byte(cached.FullContent), &info); err == nil {
				g.logger.Debug("Found npm package in cache", zap.String("package", packageName))
				return &info
			}
		}
	}

	info := &RepositoryInfo{
		Type:        RepoTypeNPM,
		PackageName: packageName,
		Exists:      false,
	}

	// Handle scoped packages - encode @ and / for URL
	encodedName := url.PathEscape(packageName)

	npmURL := fmt.Sprintf("%s/%s", npmRegistryURL, encodedName)

	req, err := http.NewRequestWithContext(ctx, "GET", npmURL, http.NoBody)
	if err != nil {
		info.Error = fmt.Sprintf("Failed to create request: %v", err)
		g.cacheInfo(cacheKey, info)
		return info
	}

	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		info.Error = fmt.Sprintf("Request failed: %v", err)
		g.cacheInfo(cacheKey, info)
		return info
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		// Package doesn't exist
		g.cacheInfo(cacheKey, info)
		return info
	}

	if resp.StatusCode != 200 {
		info.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status)
		g.cacheInfo(cacheKey, info)
		return info
	}

	var npmInfo NPMPackageInfo
	if err := json.NewDecoder(resp.Body).Decode(&npmInfo); err != nil {
		info.Error = fmt.Sprintf("Failed to parse response: %v", err)
		g.cacheInfo(cacheKey, info)
		return info
	}

	// Package exists
	info.Exists = true
	info.PackageName = npmInfo.Name
	info.Description = npmInfo.Description
	if latest, ok := npmInfo.DistTags["latest"]; ok {
		info.Version = latest
	}
	info.URL = fmt.Sprintf("https://www.npmjs.com/package/%s", npmInfo.Name)

	// Generate install command
	info.InstallCmd = fmt.Sprintf("npm install %s", npmInfo.Name)

	g.logger.Debug("Found npm package",
		zap.String("package", packageName),
		zap.String("name", npmInfo.Name),
		zap.String("version", info.Version))

	g.cacheInfo(cacheKey, info)
	return info
}

// cacheInfo caches repository information
func (g *Guesser) cacheInfo(cacheKey string, info *RepositoryInfo) {
	if g.cacheManager == nil {
		return
	}

	data, err := json.Marshal(info)
	if err != nil {
		g.logger.Warn("Failed to marshal repo info for cache", zap.Error(err))
		return
	}

	// Cache for 6 hours
	if err := g.cacheManager.Store(cacheKey, "repo_guess", map[string]interface{}{
		"package_name": info.PackageName,
		"type":         string(info.Type),
	}, string(data), "", 1); err != nil {
		g.logger.Warn("Failed to cache repo info", zap.Error(err))
	}
}
