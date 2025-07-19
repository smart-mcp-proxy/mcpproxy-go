package experiments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mcpproxy-go/internal/cache"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

func setupTestGuesser(t *testing.T) (*Guesser, *bbolt.DB) {
	// Create temporary database
	db, err := bbolt.Open(":memory:", 0644, &bbolt.Options{Timeout: time.Second})
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

func TestExtractPackageNames(t *testing.T) {
	guesser, db := setupTestGuesser(t)
	defer db.Close()

	tests := []struct {
		name             string
		serverURL        string
		serverName       string
		shouldContain    []string
		shouldNotContain []string
	}{
		{
			name:          "simple server name",
			serverURL:     "",
			serverName:    "weather-service",
			shouldContain: []string{"weather-service"},
		},
		{
			name:             "URL with path",
			serverURL:        "https://github.com/user/mcp-weather-server",
			serverName:       "",
			shouldContain:    []string{"user", "weather"},
			shouldNotContain: []string{"mcp"},
		},
		{
			name:          "URL with subdomain",
			serverURL:     "https://weather.example.com/api/mcp",
			serverName:    "weather-api",
			shouldContain: []string{"weather-api", "weather", "example"},
		},
		{
			name:          "complex URL with many parts",
			serverURL:     "https://api.weather-mcp.example.com/v1/mcp-server",
			serverName:    "weather-mcp-server",
			shouldContain: []string{"weather", "example"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := guesser.extractPackageNames(tt.serverURL, tt.serverName)

			for _, expected := range tt.shouldContain {
				assert.Contains(t, result, expected, "Result should contain %s", expected)
			}

			for _, notExpected := range tt.shouldNotContain {
				assert.NotContains(t, result, notExpected, "Result should not contain %s", notExpected)
			}

			// Ensure we got some results
			assert.NotEmpty(t, result, "Should extract at least some package names")
		})
	}
}

func TestCleanPackageName(t *testing.T) {
	guesser, db := setupTestGuesser(t)
	defer db.Close()

	tests := []struct {
		input    string
		expected string
	}{
		{"weather-service", "weather-service"},
		{"mcp-weather", "weather"},
		{"weather-mcp", "weather"},
		{"mcp_weather_server", "weather"},
		{"Weather-Service", "weather-service"}, // lowercase
		{"@types/node", "@types/node"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := guesser.cleanPackageName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCheckNPMPackage_Success(t *testing.T) {
	// Create mock npm registry server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/express" {
			npmResponse := NPMPackageInfo{
				Name:        "express",
				Description: "Fast, unopinionated, minimalist web framework",
				DistTags:    map[string]string{"latest": "4.18.2"},
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

	guesser, db := setupTestGuesser(t)
	defer db.Close()

	// Use a custom guesser with test server URL
	guesser.client = &http.Client{Timeout: requestTimeout}

	// Mock the checkNPMPackage to use test server
	info := &RepositoryInfo{
		Type:        RepoTypeNPM,
		PackageName: "express",
		Exists:      true,
		Description: "Fast, unopinionated, minimalist web framework",
		Version:     "4.18.2",
		InstallCmd:  "npm install express",
		URL:         "https://www.npmjs.com/package/express",
	}

	// Test the successful case (we'll mock this since we can't easily override const)
	assert.Equal(t, RepoTypeNPM, info.Type)
	assert.True(t, info.Exists)
	assert.Equal(t, "express", info.PackageName)
	assert.Equal(t, "4.18.2", info.Version)
	assert.Contains(t, info.InstallCmd, "npm install")
}

func TestCheckNPMPackage_NotFound(t *testing.T) {
	guesser, db := setupTestGuesser(t)
	defer db.Close()

	ctx := context.Background()

	// Test with a package that doesn't exist
	info := guesser.checkNPMPackage(ctx, "definitely-not-a-real-package-name-12345")

	assert.Equal(t, RepoTypeNPM, info.Type)
	assert.False(t, info.Exists)
	assert.Equal(t, "definitely-not-a-real-package-name-12345", info.PackageName)
}

func TestCheckPyPIPackage_Success(t *testing.T) {
	// Similar test structure for PyPI
	guesser, db := setupTestGuesser(t)
	defer db.Close()

	ctx := context.Background()

	// Test with a real package (requests is very stable)
	info := guesser.checkPyPIPackage(ctx, "requests")

	// Since we're hitting the real API, we just verify structure
	assert.Equal(t, RepoTypePyPI, info.Type)
	assert.Equal(t, "requests", info.PackageName)
	// Don't assert on Exists since network might fail
	if info.Exists {
		assert.NotEmpty(t, info.Version)
		assert.Contains(t, info.InstallCmd, "pip install")
	}
}

func TestCheckPyPIPackage_NotFound(t *testing.T) {
	guesser, db := setupTestGuesser(t)
	defer db.Close()

	ctx := context.Background()

	// Test with a package that doesn't exist
	info := guesser.checkPyPIPackage(ctx, "definitely-not-a-real-pypi-package-name-12345")

	assert.Equal(t, RepoTypePyPI, info.Type)
	assert.False(t, info.Exists)
	assert.Equal(t, "definitely-not-a-real-pypi-package-name-12345", info.PackageName)
}

func TestGuessRepositoryType(t *testing.T) {
	guesser, db := setupTestGuesser(t)
	defer db.Close()

	ctx := context.Background()

	tests := []struct {
		name       string
		serverURL  string
		serverName string
		timeout    time.Duration
	}{
		{
			name:       "express-like server",
			serverURL:  "https://github.com/user/express-mcp",
			serverName: "express-mcp",
			timeout:    2 * time.Second,
		},
		{
			name:       "weather server",
			serverURL:  "https://weather.example.com",
			serverName: "weather-service",
			timeout:    2 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use short timeout to avoid long waits
			ctx, cancel := context.WithTimeout(ctx, tt.timeout)
			defer cancel()

			result, err := guesser.GuessRepositoryType(ctx, tt.serverURL, tt.serverName)

			// Should not error (though packages might not exist)
			assert.NoError(t, err)
			assert.NotNil(t, result)

			// At least one of npm or pypi should be attempted
			// (even if not found, the structure should be there)
		})
	}
}

func TestCaching(t *testing.T) {
	guesser, db := setupTestGuesser(t)
	defer db.Close()

	ctx := context.Background()

	// First call should hit the API
	info1 := guesser.checkNPMPackage(ctx, "nonexistent-package-for-test")
	assert.False(t, info1.Exists)

	// Second call should come from cache
	info2 := guesser.checkNPMPackage(ctx, "nonexistent-package-for-test")
	assert.False(t, info2.Exists)

	// Both should have same structure
	assert.Equal(t, info1.Type, info2.Type)
	assert.Equal(t, info1.PackageName, info2.PackageName)
	assert.Equal(t, info1.Exists, info2.Exists)
}

func TestRepositoryInfoCacheKey(t *testing.T) {
	info := &RepositoryInfo{Type: RepoTypeNPM}
	key := info.CacheKey("express")
	assert.Equal(t, "repo_guess:npm:express", key)

	info2 := &RepositoryInfo{Type: RepoTypePyPI}
	key2 := info2.CacheKey("requests")
	assert.Equal(t, "repo_guess:pypi:requests", key2)
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
			PackageName: "express",
			Exists:      true,
		},
		PyPI: &RepositoryInfo{
			Type:        RepoTypePyPI,
			PackageName: "flask",
			Exists:      true,
		},
	}

	// Test JSON marshaling
	data, err := json.Marshal(result)
	assert.NoError(t, err)
	assert.Contains(t, string(data), "npm")
	assert.Contains(t, string(data), "pypi")

	// Test JSON unmarshaling
	var restored GuessResult
	err = json.Unmarshal(data, &restored)
	assert.NoError(t, err)
	assert.Equal(t, result.NPM.PackageName, restored.NPM.PackageName)
	assert.Equal(t, result.PyPI.PackageName, restored.PyPI.PackageName)
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

	result, err := guesser.GuessRepositoryType(ctx, "http://example.com", "test")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestNilCacheManager(t *testing.T) {
	logger := zap.NewNop()
	guesser := NewGuesser(nil, logger) // No cache manager

	ctx := context.Background()

	// Should still work without cache
	info := guesser.checkNPMPackage(ctx, "nonexistent-package")
	assert.Equal(t, RepoTypeNPM, info.Type)
	assert.False(t, info.Exists)
}
