package registries

import (
	"context"
	"encoding/json"
	"mcpproxy-go/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSearchServersIntegration tests the complete search flow with a mock HTTP server
func TestSearchServersIntegration(t *testing.T) {
	// Create mock server data
	mockServers := map[string]interface{}{
		"servers": []interface{}{
			map[string]interface{}{
				"id":          "weather-api",
				"name":        "Weather API",
				"description": "Get current weather data",
				"url":         "https://weather.example.com/mcp",
				"updatedAt":   "2025-01-01T12:00:00Z",
			},
			map[string]interface{}{
				"id":          "news-feed",
				"name":        "News Feed",
				"description": "Latest news updates",
				"url":         "https://news.example.com/mcp",
				"updatedAt":   "2025-01-01T10:00:00Z",
			},
			map[string]interface{}{
				"id":          "crypto-tracker",
				"name":        "Crypto Tracker",
				"description": "Track cryptocurrency prices",
				"url":         "https://crypto.example.com/mcp",
				"updatedAt":   "2025-01-01T08:00:00Z",
			},
		},
	}

	// Create mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(mockServers); err != nil {
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	// Set up test registries using config
	cfg := &config.Config{
		Registries: []config.RegistryEntry{
			{
				ID:         "test-registry",
				Name:       "Test Registry",
				ServersURL: server.URL,
				Protocol:   "modelcontextprotocol/registry",
			},
		},
	}

	// Store original list and restore after test
	originalList := registryList
	defer func() { registryList = originalList }()

	// Set test registries
	SetRegistriesFromConfig(cfg)

	ctx := context.Background()

	t.Run("search all servers", func(t *testing.T) {
		servers, err := SearchServers(ctx, "test-registry", "", "", 10, nil)
		if err != nil {
			t.Fatalf("SearchServers failed: %v", err)
		}

		if len(servers) != 3 {
			t.Errorf("expected 3 servers, got %d", len(servers))
		}

		// Verify first server
		if servers[0].ID != "weather-api" {
			t.Errorf("expected first server ID 'weather-api', got '%s'", servers[0].ID)
		}
		if servers[0].Registry != "Test Registry" {
			t.Errorf("expected registry 'Test Registry', got '%s'", servers[0].Registry)
		}
		if servers[0].URL != "https://weather.example.com/mcp" {
			t.Errorf("expected URL 'https://weather.example.com/mcp', got '%s'", servers[0].URL)
		}
	})

	t.Run("search with filter", func(t *testing.T) {
		servers, err := SearchServers(ctx, "test-registry", "", "weather", 10, nil)
		if err != nil {
			t.Fatalf("SearchServers failed: %v", err)
		}

		if len(servers) != 1 {
			t.Errorf("expected 1 server matching 'weather', got %d", len(servers))
		}

		if servers[0].ID != "weather-api" {
			t.Errorf("expected server ID 'weather-api', got '%s'", servers[0].ID)
		}
	})

	t.Run("search no results", func(t *testing.T) {
		servers, err := SearchServers(ctx, "test-registry", "", "nonexistent", 10, nil)
		if err != nil {
			t.Fatalf("SearchServers failed: %v", err)
		}

		if len(servers) != 0 {
			t.Errorf("expected 0 servers, got %d", len(servers))
		}
	})

	t.Run("unknown registry", func(t *testing.T) {
		_, err := SearchServers(ctx, "unknown-registry", "", "", 10, nil)
		if err == nil {
			t.Error("expected error for unknown registry, got nil")
		}

		expectedMsg := "registry 'unknown-registry' not found"
		if err.Error() != expectedMsg {
			t.Errorf("expected error message '%s', got '%s'", expectedMsg, err.Error())
		}
	})
}

// TestCompleteWorkflow demonstrates the intended usage pattern
func TestCompleteWorkflow(t *testing.T) {
	// Set up default registries (as per user requirements: pulse, docker-mcp-catalog, fleur)
	cfg := config.DefaultConfig()
	SetRegistriesFromConfig(cfg)

	// This test demonstrates how the search_servers feature would be used:
	// 1. List available registries
	// 2. Search for specific servers
	// 3. Use the results to add upstream servers

	t.Run("list registries", func(t *testing.T) {
		registries := ListRegistries()
		if len(registries) == 0 {
			t.Error("expected default registries, got none")
		}

		// Verify we have the expected default registries (pulse, docker-mcp-catalog, fleur)
		expectedIDs := []string{"pulse", "docker-mcp-catalog", "fleur"}
		found := make(map[string]bool)

		for _, reg := range registries {
			found[reg.ID] = true
		}

		for _, expectedID := range expectedIDs {
			if !found[expectedID] {
				t.Errorf("expected default registry '%s' not found", expectedID)
			}
		}
	})

	t.Run("find registry by name", func(t *testing.T) {
		reg := FindRegistry("Pulse MCP")
		if reg == nil {
			t.Error("expected to find Pulse MCP registry")
		} else if reg.ID != "pulse" {
			t.Errorf("expected ID 'pulse', got '%s'", reg.ID)
		}
	})

	t.Run("validate registry data", func(t *testing.T) {
		for _, reg := range ListRegistries() {
			if reg.ID == "" {
				t.Errorf("registry missing ID: %+v", reg)
			}
			if reg.Name == "" {
				t.Errorf("registry missing Name: %+v", reg)
			}
			// ServersURL can be empty for some registries
			if reg.Protocol == "" {
				t.Errorf("registry missing Protocol: %+v", reg)
			}
		}
	})
}
