package experiments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"

	"mcpproxy-go/internal/cache"

	"go.uber.org/zap"
)

const (
	npmRegistryURL        = "https://registry.npmjs.org"
	requestTimeout        = 10 * time.Second
	batchRequestTimeout   = 3 * time.Second // Short timeout for batch operations
	userAgent             = "mcpproxy-go/1.0"
	maxConcurrentRequests = 10 // Limit concurrent requests
)

// GitHub URL pattern for matching https://github.com/<author|org>/<repo>
var githubURLPattern = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)(?:/.*)?$`)

// BatchGuessRequest represents a single repository URL to check
type BatchGuessRequest struct {
	URL   string // GitHub URL
	Index int    // Original index for result mapping
}

// BatchGuessResult represents the result of a batch guess operation
type BatchGuessResult struct {
	Index  int          // Original index
	Result *GuessResult // Guess result (nil if failed)
	Error  error        // Error if the guess failed
}

// Guesser handles repository type detection using external APIs
type Guesser struct {
	client       *http.Client
	batchClient  *http.Client // Separate client for batch operations with shorter timeout
	cacheManager *cache.Manager
	logger       *zap.Logger
	semaphore    chan struct{} // Semaphore for controlling concurrency
}

// NewGuesser creates a new repository type guesser
func NewGuesser(cacheManager *cache.Manager, logger *zap.Logger) *Guesser {
	return &Guesser{
		client: &http.Client{
			Timeout: requestTimeout,
		},
		batchClient: &http.Client{
			Timeout: batchRequestTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     30 * time.Second,
			},
		},
		cacheManager: cacheManager,
		logger:       logger,
		semaphore:    make(chan struct{}, maxConcurrentRequests),
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

// GuessRepositoryTypesBatch processes multiple GitHub URLs in parallel with connection pooling
// Returns results in the same order as input URLs
func (g *Guesser) GuessRepositoryTypesBatch(ctx context.Context, githubURLs []string) []*GuessResult {
	if len(githubURLs) == 0 {
		return []*GuessResult{}
	}

	g.logger.Debug("Starting batch repository type guessing",
		zap.Int("urls_count", len(githubURLs)))

	// Prepare requests and filter valid GitHub URLs
	var requests []BatchGuessRequest
	for i, url := range githubURLs {
		if url != "" && githubURLPattern.MatchString(url) {
			requests = append(requests, BatchGuessRequest{
				URL:   url,
				Index: i,
			})
		}
	}

	if len(requests) == 0 {
		// Return empty results for all URLs
		results := make([]*GuessResult, len(githubURLs))
		for i := range results {
			results[i] = &GuessResult{}
		}
		return results
	}

	// Process requests in parallel
	resultChan := make(chan BatchGuessResult, len(requests))
	var wg sync.WaitGroup

	for _, req := range requests {
		wg.Add(1)
		go func(request BatchGuessRequest) {
			defer wg.Done()

			// Acquire semaphore slot
			g.semaphore <- struct{}{}
			defer func() { <-g.semaphore }()

			// Process single URL with batch timeout
			result, err := g.guessRepositoryTypeBatch(ctx, request.URL)
			resultChan <- BatchGuessResult{
				Index:  request.Index,
				Result: result,
				Error:  err,
			}
		}(req)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(resultChan)

	// Collect results and map them back to original order
	results := make([]*GuessResult, len(githubURLs))
	for i := range results {
		results[i] = &GuessResult{} // Default empty result
	}

	processedCount := 0
	errorCount := 0

	for batchResult := range resultChan {
		processedCount++
		if batchResult.Error != nil {
			errorCount++
			g.logger.Debug("Failed to guess repository type",
				zap.String("url", githubURLs[batchResult.Index]),
				zap.Error(batchResult.Error))
			// Keep empty result for failed URLs
		} else {
			results[batchResult.Index] = batchResult.Result
		}
	}

	g.logger.Debug("Batch repository type guessing completed",
		zap.Int("total_urls", len(githubURLs)),
		zap.Int("processed", processedCount),
		zap.Int("errors", errorCount))

	return results
}

// guessRepositoryTypeBatch is the internal batch version that uses the batch client
func (g *Guesser) guessRepositoryTypeBatch(ctx context.Context, githubURL string) (*GuessResult, error) {
	// Check if URL matches GitHub pattern
	matches := githubURLPattern.FindStringSubmatch(githubURL)
	if len(matches) != 3 {
		return &GuessResult{}, nil
	}

	author := matches[1]
	repo := matches[2]

	// Create npm package name in format @author/repo
	packageName := fmt.Sprintf("@%s/%s", author, repo)

	// Check npm package with batch client (shorter timeout)
	npmInfo := g.checkNPMPackageBatch(ctx, packageName)

	result := &GuessResult{}
	if npmInfo.Exists {
		result.NPM = npmInfo
	}

	return result, nil
}

// checkNPMPackage checks if a package exists on npm registry
func (g *Guesser) checkNPMPackage(ctx context.Context, packageName string) *RepositoryInfo {
	return g.checkNPMPackageWithClient(ctx, packageName, g.client)
}

// checkNPMPackageBatch checks if a package exists on npm registry with batch client
func (g *Guesser) checkNPMPackageBatch(ctx context.Context, packageName string) *RepositoryInfo {
	return g.checkNPMPackageWithClient(ctx, packageName, g.batchClient)
}

// checkNPMPackageWithClient checks if a package exists on npm registry with specified client
func (g *Guesser) checkNPMPackageWithClient(ctx context.Context, packageName string, client *http.Client) *RepositoryInfo {
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

	resp, err := client.Do(req)
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
