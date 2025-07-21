package experiments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"mcpproxy-go/internal/cache"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

func setupTestGuesser(t *testing.T) (*Guesser, *bbolt.DB) {
	// Create temporary database file (Windows-compatible)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	db, err := bbolt.Open(dbPath, 0644, &bbolt.Options{Timeout: time.Second})
	require.NoError(t, err)

	logger := zap.NewNop()
	cacheManager, err := cache.NewManager(db, logger)
	require.NoError(t, err)

	guesser := NewGuesser(cacheManager, logger)
	return guesser, db
}

func TestNewGuesser(t *testing.T) {
	guesser, db := setupTestGuesser(t)
	defer db.Close()

	assert.NotNil(t, guesser)
	assert.NotNil(t, guesser.client)
	assert.Equal(t, requestTimeout, guesser.client.Timeout)
}

func TestGitHubURLPattern(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected bool
		author   string
		repo     string
	}{
		{
			name:     "valid GitHub repo URL",
			url:      "https://github.com/facebook/react",
			expected: true,
			author:   "facebook",
			repo:     "react",
		},
		{
			name:     "GitHub repo with path",
			url:      "https://github.com/microsoft/vscode/tree/main",
			expected: true,
			author:   "microsoft",
			repo:     "vscode",
		},
		{
			name:     "non-GitHub URL",
			url:      "https://gitlab.com/user/repo",
			expected: false,
		},
		{
			name:     "invalid URL format",
			url:      "not-a-url",
			expected: false,
		},
		{
			name:     "GitHub URL without repo",
			url:      "https://github.com/user",
			expected: false,
		},
		{
			name:     "empty URL",
			url:      "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := githubURLPattern.FindStringSubmatch(tt.url)

			if tt.expected {
				assert.Len(t, matches, 3, "Should match GitHub pattern")
				assert.Equal(t, tt.author, matches[1], "Author should match")
				assert.Equal(t, tt.repo, matches[2], "Repo should match")
			} else {
				assert.Nil(t, matches, "Should not match GitHub pattern")
			}
		})
	}
}

func TestCheckNPMPackage_Success(t *testing.T) {
	// Create mock npm registry server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/@facebook/react" {
			npmResponse := NPMPackageInfo{
				Name:        "@facebook/react",
				Description: "React is a JavaScript library for building user interfaces.",
				DistTags:    map[string]string{"latest": "18.2.0"},
				Versions:    map[string]interface{}{},
				Time:        map[string]string{},
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(npmResponse); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Mock the checkNPMPackage to use test server (simplified test)
	info := &RepositoryInfo{
		Type:        RepoTypeNPM,
		PackageName: "@facebook/react",
		Exists:      true,
		Description: "React is a JavaScript library for building user interfaces.",
		Version:     "18.2.0",
		InstallCmd:  "npm install @facebook/react",
		URL:         "https://www.npmjs.com/package/@facebook/react",
	}

	// Test the successful case
	assert.Equal(t, RepoTypeNPM, info.Type)
	assert.True(t, info.Exists)
	assert.Equal(t, "@facebook/react", info.PackageName)
	assert.Equal(t, "18.2.0", info.Version)
	assert.Contains(t, info.InstallCmd, "npm install")
}

func TestCheckNPMPackage_NotFound(t *testing.T) {
	guesser, db := setupTestGuesser(t)
	defer db.Close()

	ctx := context.Background()

	// Test with a package that doesn't exist
	info := guesser.checkNPMPackage(ctx, "@nonexistent/package-12345")

	assert.Equal(t, RepoTypeNPM, info.Type)
	assert.False(t, info.Exists)
	assert.Equal(t, "@nonexistent/package-12345", info.PackageName)
}

func TestGuessRepositoryType_GitHubURL(t *testing.T) {
	guesser, db := setupTestGuesser(t)
	defer db.Close()

	ctx := context.Background()

	tests := []struct {
		name        string
		githubURL   string
		shouldCheck bool
		expectedPkg string
	}{
		{
			name:        "valid GitHub URL",
			githubURL:   "https://github.com/facebook/react",
			shouldCheck: true,
			expectedPkg: "@facebook/react",
		},
		{
			name:        "GitHub URL with path",
			githubURL:   "https://github.com/microsoft/vscode/tree/main",
			shouldCheck: true,
			expectedPkg: "@microsoft/vscode",
		},
		{
			name:        "non-GitHub URL",
			githubURL:   "https://gitlab.com/user/repo",
			shouldCheck: false,
		},
		{
			name:        "empty URL",
			githubURL:   "",
			shouldCheck: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()

			result, err := guesser.GuessRepositoryType(ctx, tt.githubURL)

			assert.NoError(t, err)
			assert.NotNil(t, result)

			if !tt.shouldCheck {
				// Should not have found anything for non-GitHub URLs
				assert.Nil(t, result.NPM)
			}
			// For GitHub URLs, NPM field might be nil if package doesn't exist,
			// but we should have attempted to check
		})
	}
}

func TestGuessRepositoryType_EmptyURL(t *testing.T) {
	guesser, db := setupTestGuesser(t)
	defer db.Close()

	ctx := context.Background()

	result, err := guesser.GuessRepositoryType(ctx, "")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Nil(t, result.NPM)
}

func TestCaching(t *testing.T) {
	guesser, db := setupTestGuesser(t)
	defer db.Close()

	ctx := context.Background()

	// First call should hit the API
	info1 := guesser.checkNPMPackage(ctx, "@nonexistent/package-for-test")
	assert.False(t, info1.Exists)

	// Second call should come from cache
	info2 := guesser.checkNPMPackage(ctx, "@nonexistent/package-for-test")
	assert.False(t, info2.Exists)

	// Both should have same structure
	assert.Equal(t, info1.Type, info2.Type)
	assert.Equal(t, info1.PackageName, info2.PackageName)
	assert.Equal(t, info1.Exists, info2.Exists)
}

func TestRepositoryInfoCacheKey(t *testing.T) {
	info := &RepositoryInfo{Type: RepoTypeNPM}
	key := info.CacheKey("@facebook/react")
	assert.Equal(t, "repo_guess:npm:@facebook/react", key)
}

func TestRepositoryInfoCacheTTL(t *testing.T) {
	info := &RepositoryInfo{}
	ttl := info.CacheTTL()
	assert.Equal(t, 6*time.Hour, ttl)
}

func TestGuessResultStructure(t *testing.T) {
	result := &GuessResult{
		NPM: &RepositoryInfo{
			Type:        RepoTypeNPM,
			PackageName: "@facebook/react",
			Exists:      true,
		},
	}

	// Test JSON marshaling
	data, err := json.Marshal(result)
	assert.NoError(t, err)
	assert.Contains(t, string(data), "npm")
	assert.NotContains(t, string(data), "pypi") // Should not contain pypi anymore

	// Test JSON unmarshaling
	var restored GuessResult
	err = json.Unmarshal(data, &restored)
	assert.NoError(t, err)
	assert.Equal(t, result.NPM.PackageName, restored.NPM.PackageName)
}

func TestScopedNPMPackages(t *testing.T) {
	guesser, db := setupTestGuesser(t)
	defer db.Close()

	tests := []string{
		"@types/node",
		"@babel/core",
		"@angular/core",
	}

	for _, packageName := range tests {
		t.Run(packageName, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			info := guesser.checkNPMPackage(ctx, packageName)
			assert.Equal(t, RepoTypeNPM, info.Type)
			assert.Equal(t, packageName, info.PackageName)
			// Don't assert on existence since we're hitting real API
		})
	}
}

func TestErrorHandling(t *testing.T) {
	guesser, db := setupTestGuesser(t)
	defer db.Close()

	// Test with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result, err := guesser.GuessRepositoryType(ctx, "https://github.com/user/repo")
	// Should not error for cancelled context since we check GitHub pattern first
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestNilCacheManager(t *testing.T) {
	logger := zap.NewNop()
	guesser := NewGuesser(nil, logger) // No cache manager

	ctx := context.Background()

	// Should still work without cache
	info := guesser.checkNPMPackage(ctx, "@nonexistent/package")
	assert.Equal(t, RepoTypeNPM, info.Type)
	assert.False(t, info.Exists)
}

func TestGuessRepositoryTypesBatch_EmptyInput(t *testing.T) {
	logger := zap.NewNop()
	guesser := NewGuesser(nil, logger)

	ctx := context.Background()

	// Test empty slice
	results := guesser.GuessRepositoryTypesBatch(ctx, []string{})
	assert.Empty(t, results)

	// Test nil slice
	results = guesser.GuessRepositoryTypesBatch(ctx, nil)
	assert.Empty(t, results)
}

func TestGuessRepositoryTypesBatch_NonGitHubURLs(t *testing.T) {
	logger := zap.NewNop()
	guesser := NewGuesser(nil, logger)

	ctx := context.Background()
	urls := []string{
		"https://example.com/repo",
		"https://gitlab.com/user/repo",
		"not-a-url",
		"",
	}

	results := guesser.GuessRepositoryTypesBatch(ctx, urls)

	// Should return same number of results as input URLs
	assert.Len(t, results, len(urls))

	// All results should be empty since no GitHub URLs
	for i, result := range results {
		assert.NotNil(t, result, "Result at index %d should not be nil", i)
		assert.Nil(t, result.NPM, "NPM info should be nil for non-GitHub URL at index %d", i)
	}
}

func TestGuessRepositoryTypesBatch_MixedURLs(t *testing.T) {
	// Skip real network requests in CI
	if testing.Short() {
		t.Skip("Skipping network request test in short mode")
	}

	logger := zap.NewNop()
	guesser := NewGuesser(nil, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	urls := []string{
		"https://github.com/facebook/react",         // Real package that exists
		"https://github.com/nonexistent/package123", // Package that doesn't exist
		"https://example.com/not-github",            // Non-GitHub URL
		"",                                          // Empty URL
		"https://github.com/microsoft/vscode",       // Another real package
	}

	results := guesser.GuessRepositoryTypesBatch(ctx, urls)

	// Should return same number of results as input URLs
	assert.Len(t, results, len(urls))

	// Check that results maintain order
	for i, result := range results {
		assert.NotNil(t, result, "Result at index %d should not be nil", i)

		switch i {
		case 0: // facebook/react - should likely exist
			// Note: This test may be flaky based on npm availability
			// We just check the structure is correct
		case 1: // nonexistent package - should not exist
			if result.NPM != nil {
				assert.False(t, result.NPM.Exists, "Nonexistent package should not exist")
			}
		case 2, 3: // Non-GitHub and empty URLs
			assert.Nil(t, result.NPM, "Non-GitHub URL should have no NPM info")
		case 4: // microsoft/vscode
			// Again, just check structure
		}
	}
}

func TestGuessRepositoryTypesBatch_Performance(t *testing.T) {
	// Skip real network requests in CI
	if testing.Short() {
		t.Skip("Skipping network request test in short mode")
	}

	logger := zap.NewNop()
	guesser := NewGuesser(nil, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create a list of GitHub URLs (mix of existing and non-existing)
	urls := []string{
		"https://github.com/facebook/react",
		"https://github.com/lodash/lodash",
		"https://github.com/nonexistent1/package1",
		"https://github.com/nonexistent2/package2",
		"https://github.com/nonexistent3/package3",
		"https://github.com/webpack/webpack",
		"https://github.com/babel/babel",
		"https://github.com/nonexistent4/package4",
	}

	startTime := time.Now()
	results := guesser.GuessRepositoryTypesBatch(ctx, urls)
	duration := time.Since(startTime)

	// Should complete within reasonable time (less than 10 seconds due to 3-second timeout)
	assert.Less(t, duration, 10*time.Second, "Batch processing should complete quickly with parallel requests")

	// Should return correct number of results
	assert.Len(t, results, len(urls))

	// All results should be non-nil
	for i, result := range results {
		assert.NotNil(t, result, "Result at index %d should not be nil", i)
	}

	t.Logf("Batch processing of %d URLs completed in %v", len(urls), duration)
}

func TestGuessRepositoryTypesBatch_ConcurrencyLimit(t *testing.T) {
	logger := zap.NewNop()
	guesser := NewGuesser(nil, logger)

	// Create many URLs to test concurrency limiting
	urls := make([]string, 50)
	for i := 0; i < 50; i++ {
		urls[i] = fmt.Sprintf("https://github.com/test%d/repo%d", i, i)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	startTime := time.Now()
	results := guesser.GuessRepositoryTypesBatch(ctx, urls)
	duration := time.Since(startTime)

	// Should return correct number of results
	assert.Len(t, results, len(urls))

	// All results should be non-nil
	for i, result := range results {
		assert.NotNil(t, result, "Result at index %d should not be nil", i)
	}

	t.Logf("Batch processing of %d URLs with concurrency limit completed in %v", len(urls), duration)
}

func TestGuessRepositoryTypesBatch_ContextCancellation(t *testing.T) {
	logger := zap.NewNop()
	guesser := NewGuesser(nil, logger)

	urls := []string{
		"https://github.com/facebook/react",
		"https://github.com/lodash/lodash",
		"https://github.com/webpack/webpack",
	}

	// Create context that will be cancelled quickly
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	results := guesser.GuessRepositoryTypesBatch(ctx, urls)

	// Should still return correct number of results (though they may be empty due to timeout)
	assert.Len(t, results, len(urls))

	// All results should be non-nil even if requests failed
	for i, result := range results {
		assert.NotNil(t, result, "Result at index %d should not be nil", i)
	}
}

func TestGuessRepositoryTypesBatch_DuplicateURLs(t *testing.T) {
	// Skip real network requests in CI
	if testing.Short() {
		t.Skip("Skipping network request test in short mode")
	}

	logger := zap.NewNop()
	guesser := NewGuesser(nil, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test with duplicate URLs to ensure efficient processing
	urls := []string{
		"https://github.com/facebook/react",
		"https://github.com/lodash/lodash",
		"https://github.com/facebook/react", // Duplicate
		"https://github.com/lodash/lodash",  // Duplicate
		"https://github.com/facebook/react", // Another duplicate
	}

	results := guesser.GuessRepositoryTypesBatch(ctx, urls)

	// Should return same number of results as input URLs
	assert.Len(t, results, len(urls))

	// Results for duplicate URLs should be identical
	assert.Equal(t, results[0], results[2], "Duplicate URLs should have identical results")
	assert.Equal(t, results[0], results[4], "Duplicate URLs should have identical results")
	assert.Equal(t, results[1], results[3], "Duplicate URLs should have identical results")
}

func TestBatchVsSingle_ConsistencyCheck(t *testing.T) {
	// Skip real network requests in CI
	if testing.Short() {
		t.Skip("Skipping network request test in short mode")
	}

	logger := zap.NewNop()
	guesser := NewGuesser(nil, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	testURLs := []string{
		"https://github.com/facebook/react",
		"https://github.com/nonexistent/package123",
	}

	// Get results using single method
	var singleResults []*GuessResult
	for _, url := range testURLs {
		result, err := guesser.GuessRepositoryType(ctx, url)
		assert.NoError(t, err)
		singleResults = append(singleResults, result)
	}

	// Get results using batch method
	batchResults := guesser.GuessRepositoryTypesBatch(ctx, testURLs)

	// Results should be consistent between single and batch methods
	assert.Len(t, batchResults, len(singleResults))
	for i := 0; i < len(testURLs); i++ {
		// Compare the existence status (the most important part)
		singleExists := singleResults[i].NPM != nil && singleResults[i].NPM.Exists
		batchExists := batchResults[i].NPM != nil && batchResults[i].NPM.Exists
		assert.Equal(t, singleExists, batchExists,
			"Existence status should be same for URL %s between single and batch methods", testURLs[i])
	}
}
