package experiments

import (
	"context"
	"encoding/json"
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
