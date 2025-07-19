package experiments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mcpproxy-go/internal/cache"

	"go.uber.org/zap"
)

const (
	npmRegistryURL = "https://registry.npmjs.org"
	pypiJSONAPIURL = "https://pypi.org/pypi"
	requestTimeout = 10 * time.Second
	userAgent      = "mcpproxy-go/1.0"
)

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

// GuessRepositoryType attempts to determine repository type from a URL or name
func (g *Guesser) GuessRepositoryType(ctx context.Context, serverURL, serverName string) (*GuessResult, error) {
	// Extract potential package names from URL or name
	packageNames := g.extractPackageNames(serverURL, serverName)

	result := &GuessResult{}

	// Try to detect npm and PyPI packages in parallel
	npmChan := make(chan *RepositoryInfo, 1)
	pypiChan := make(chan *RepositoryInfo, 1)

	for _, packageName := range packageNames {
		go func(pkg string) {
			npmChan <- g.checkNPMPackage(ctx, pkg)
		}(packageName)

		go func(pkg string) {
			pypiChan <- g.checkPyPIPackage(ctx, pkg)
		}(packageName)

		// Use first successful detection
		select {
		case npmInfo := <-npmChan:
			if npmInfo.Exists {
				result.NPM = npmInfo
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		select {
		case pypiInfo := <-pypiChan:
			if pypiInfo.Exists {
				result.PyPI = pypiInfo
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		// If we found something, we can stop
		if result.NPM != nil || result.PyPI != nil {
			break
		}
	}

	return result, nil
}

// extractPackageNames extracts potential package names from URL and server name
func (g *Guesser) extractPackageNames(serverURL, serverName string) []string {
	var names []string

	// Add server name as-is
	if serverName != "" {
		names = append(names, serverName)
	}

	// Extract from URL
	if serverURL != "" {
		if parsed, err := url.Parse(serverURL); err == nil {
			// Extract from path
			pathParts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
			for _, part := range pathParts {
				if part != "" && !strings.Contains(part, ".") { // Skip file extensions
					names = append(names, part)
				}
			}

			// Extract from hostname
			hostParts := strings.Split(parsed.Hostname(), ".")
			for _, part := range hostParts {
				if part != "" && part != "www" && part != "api" && !strings.Contains(part, "http") {
					names = append(names, part)
				}
			}
		}
	}

	// Clean and deduplicate names
	seen := make(map[string]bool)
	var cleanNames []string
	for _, name := range names {
		cleaned := g.cleanPackageName(name)
		if cleaned != "" && !seen[cleaned] {
			cleanNames = append(cleanNames, cleaned)
			seen[cleaned] = true
		}
	}

	return cleanNames
}

// cleanPackageName cleans a package name for API lookups
func (g *Guesser) cleanPackageName(name string) string {
	// Remove common prefixes/suffixes
	name = strings.TrimPrefix(name, "mcp-")
	name = strings.TrimPrefix(name, "mcp_")
	name = strings.TrimSuffix(name, "-mcp")
	name = strings.TrimSuffix(name, "_mcp")
	name = strings.TrimSuffix(name, "-server")
	name = strings.TrimSuffix(name, "_server")

	// Remove invalid characters for package names
	name = strings.ToLower(name)

	return name
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

	url := fmt.Sprintf("%s/%s", npmRegistryURL, encodedName)

	req, err := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
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

// checkPyPIPackage checks if a package exists on PyPI
func (g *Guesser) checkPyPIPackage(ctx context.Context, packageName string) *RepositoryInfo {
	// Check cache first
	cacheKey := "pypi:" + packageName
	if g.cacheManager != nil {
		if cached, err := g.cacheManager.Get(cacheKey); err == nil {
			var info RepositoryInfo
			if err := json.Unmarshal([]byte(cached.FullContent), &info); err == nil {
				g.logger.Debug("Found PyPI package in cache", zap.String("package", packageName))
				return &info
			}
		}
	}

	info := &RepositoryInfo{
		Type:        RepoTypePyPI,
		PackageName: packageName,
		Exists:      false,
	}

	url := fmt.Sprintf("%s/%s/json", pypiJSONAPIURL, packageName)

	req, err := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
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

	var pypiInfo PyPIPackageInfo
	if err := json.NewDecoder(resp.Body).Decode(&pypiInfo); err != nil {
		info.Error = fmt.Sprintf("Failed to parse response: %v", err)
		g.cacheInfo(cacheKey, info)
		return info
	}

	// Package exists
	info.Exists = true
	info.PackageName = pypiInfo.Info.Name
	info.Description = pypiInfo.Info.Summary
	info.Version = pypiInfo.Info.Version
	info.URL = fmt.Sprintf("https://pypi.org/project/%s/", pypiInfo.Info.Name)

	// Generate install command
	info.InstallCmd = fmt.Sprintf("pip install %s", pypiInfo.Info.Name)

	g.logger.Debug("Found PyPI package",
		zap.String("package", packageName),
		zap.String("name", pypiInfo.Info.Name),
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
