package registries

import (
	"context"
	"encoding/json"
	"mcpproxy-go/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// setupTestRegistries sets up test registries for testing
func setupTestRegistries() {
	cfg := &config.Config{
		Registries: []config.RegistryEntry{
			{
				ID:          "mcprun",
				Name:        "MCP Run",
				Description: "Test MCP Run registry",
				URL:         "https://www.mcp.run/",
				ServersURL:  "https://www.mcp.run/api/servlets",
				Tags:        []string{"verified"},
				Protocol:    "custom/mcprun",
			},
			{
				ID:          "smithery",
				Name:        "Smithery",
				Description: "Test Smithery registry",
				URL:         "https://smithery.ai/",
				ServersURL:  "https://registry.smithery.ai/servers",
				Tags:        []string{"verified"},
				Protocol:    "modelcontextprotocol/registry",
			},
		},
	}
	SetRegistriesFromConfig(cfg)
}

func TestFindRegistry(t *testing.T) {
	setupTestRegistries()

	tests := []struct {
		name     string
		query    string
		expected string
	}{
		{"find by ID", "mcprun", "mcprun"},
		{"find by name", "MCP Run", "mcprun"},
		{"case insensitive ID", "MCPRUN", "mcprun"},
		{"case insensitive name", "mcp run", "mcprun"},
		{"not found", "nonexistent", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := FindRegistry(tt.query)
			if tt.expected == "" {
				if reg != nil {
					t.Errorf("expected nil, got %v", reg)
				}
			} else {
				if reg == nil {
					t.Errorf("expected registry with ID %s, got nil", tt.expected)
				} else if reg.ID != tt.expected {
					t.Errorf("expected registry ID %s, got %s", tt.expected, reg.ID)
				}
			}
		})
	}
}

func TestFilterServers(t *testing.T) {
	servers := []ServerEntry{
		{ID: "weather", Name: "Weather API", Description: "Get weather information"},
		{ID: "news", Name: "News Feed", Description: "Latest news updates"},
		{ID: "weather-pro", Name: "Weather Pro", Description: "Advanced weather forecasting"},
		{ID: "finance", Name: "Finance Tracker", Description: "Track your financial data"},
	}

	tests := []struct {
		name     string
		query    string
		tag      string
		expected []string
	}{
		{"no filter", "", "", []string{"weather", "news", "weather-pro", "finance"}},
		{"search weather", "weather", "", []string{"weather", "weather-pro"}},
		{"search case insensitive", "WEATHER", "", []string{"weather", "weather-pro"}},
		{"search in description", "news", "", []string{"news"}},
		{"search finance", "finance", "", []string{"finance"}},
		{"search pro", "pro", "", []string{"weather-pro"}},
		{"search nonexistent", "nonexistent", "", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := filterServers(servers, tt.tag, tt.query)

			if len(filtered) != len(tt.expected) {
				t.Errorf("expected %d results, got %d", len(tt.expected), len(filtered))
				return
			}

			for i, expectedID := range tt.expected {
				if filtered[i].ID != expectedID {
					t.Errorf("expected result %d to have ID %s, got %s", i, expectedID, filtered[i].ID)
				}
			}
		})
	}
}

func TestParseOpenAPIRegistry(t *testing.T) {
	tests := []struct {
		name     string
		data     interface{}
		expected int
	}{
		{
			"servers field",
			map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{"id": "test1", "name": "Test 1"},
					map[string]interface{}{"id": "test2", "name": "Test 2"},
				},
			},
			2,
		},
		{
			"data field",
			map[string]interface{}{
				"data": []interface{}{
					map[string]interface{}{"id": "test1", "name": "Test 1"},
				},
			},
			1,
		},
		{
			"direct array",
			[]interface{}{
				map[string]interface{}{"id": "test1", "name": "Test 1"},
			},
			1,
		},
		{
			"empty",
			map[string]interface{}{},
			0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			servers := parseOpenAPIRegistry(tt.data)
			if len(servers) != tt.expected {
				t.Errorf("expected %d servers, got %d", tt.expected, len(servers))
			}
		})
	}
}

func TestParseMCPRun(t *testing.T) {
	testData := []interface{}{
		map[string]interface{}{
			"slug": "weather-api",
			"meta": map[string]interface{}{
				"description": "Weather service",
			},
			"created_at": "2025-01-01T00:00:00Z",
			"updated_at": "2025-01-01T12:00:00Z",
		},
		map[string]interface{}{
			"slug": "news-feed",
		},
	}

	servers := parseMCPRun(testData)

	if len(servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(servers))
	}

	if servers[0].ID != "weather-api" {
		t.Errorf("expected ID 'weather-api', got '%s'", servers[0].ID)
	}
	if servers[0].Description != "Weather service" {
		t.Errorf("expected description 'Weather service', got '%s'", servers[0].Description)
	}
}

func TestParsePulse(t *testing.T) {
	testData := map[string]interface{}{
		"servers": []interface{}{
			map[string]interface{}{
				"name":                                  "Taskwarrior",
				"short_description":                     "Task management with Taskwarrior",
				"EXPERIMENTAL_ai_generated_description": "AI generated description",
				"package_registry":                      "npm",
				"package_name":                          "@0xbeedao/mcp-taskwarrior",
			},
			map[string]interface{}{
				"name":                                  "Ethereum RPC",
				"EXPERIMENTAL_ai_generated_description": strings.Repeat("a", 400), // Long description
				"package_registry":                      "docker",
				"package_name":                          "mcp/evm-mcp-tools",
			},
			map[string]interface{}{
				"name":              "Remote Service",
				"short_description": "Service with remote connection",
				"package_registry":  "pypi",
				"package_name":      "some-python-package",
				"remotes": []interface{}{
					map[string]interface{}{
						"url_direct": "https://api.example.com/mcp",
					},
				},
			},
		},
	}

	servers := parsePulse(testData)

	if len(servers) != 3 {
		t.Errorf("expected 3 servers, got %d", len(servers))
	}

	// Test first server - npm package
	if servers[0].ID != "Taskwarrior" {
		t.Errorf("expected ID 'Taskwarrior', got '%s'", servers[0].ID)
	}
	if servers[0].Description != "Task management with Taskwarrior" {
		t.Errorf("expected description 'Task management with Taskwarrior', got '%s'", servers[0].Description)
	}
	if servers[0].InstallCmd != "npx -y @0xbeedao/mcp-taskwarrior" {
		t.Errorf("expected InstallCmd 'npx -y @0xbeedao/mcp-taskwarrior', got '%s'", servers[0].InstallCmd)
	}

	// Test second server - docker package with truncation
	if servers[1].ID != "Ethereum RPC" {
		t.Errorf("expected ID 'Ethereum RPC', got '%s'", servers[1].ID)
	}
	if len(servers[1].Description) != 300 {
		t.Errorf("expected description to be truncated to 300 chars, got %d", len(servers[1].Description))
	}
	if servers[1].InstallCmd != "docker run -i --rm mcp/evm-mcp-tools" {
		t.Errorf("expected InstallCmd 'docker run -i --rm mcp/evm-mcp-tools', got '%s'", servers[1].InstallCmd)
	}

	// Test third server - pypi package with remote connection
	if servers[2].ID != "Remote Service" {
		t.Errorf("expected ID 'Remote Service', got '%s'", servers[2].ID)
	}
	if servers[2].InstallCmd != "pipx run some-python-package" {
		t.Errorf("expected InstallCmd 'pipx run some-python-package', got '%s'", servers[2].InstallCmd)
	}
	if servers[2].ConnectURL != "https://api.example.com/mcp" {
		t.Errorf("expected ConnectURL 'https://api.example.com/mcp', got '%s'", servers[2].ConnectURL)
	}
}

func TestParseDocker(t *testing.T) {
	testData := map[string]interface{}{
		"results": []interface{}{
			map[string]interface{}{
				"name": "mcp-weather",
				"images": []interface{}{
					map[string]interface{}{
						"description": "Weather MCP server",
					},
				},
				"last_updated": "2025-01-01T12:00:00Z",
			},
		},
	}

	servers := parseDocker(testData)

	if len(servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(servers))
	}

	if servers[0].ID != "mcp-weather" {
		t.Errorf("expected ID 'mcp-weather', got '%s'", servers[0].ID)
	}
	if servers[0].Description != "Weather MCP server" {
		t.Errorf("expected description 'Weather MCP server', got '%s'", servers[0].Description)
	}
}

func TestParseFleur(t *testing.T) {
	testData := []interface{}{
		map[string]interface{}{
			"appId":       "weather-app",
			"name":        "Weather Application",
			"description": "Weather forecast app",
			"config": map[string]interface{}{
				"mcpKey":  "github",
				"runtime": "npx",
				"args":    []interface{}{"-y", "@modelcontextprotocol/server-github"},
			},
		},
		map[string]interface{}{
			"appId": "news-app",
			"name":  "News Reader",
			"config": map[string]interface{}{
				"mcpKey":  "news",
				"runtime": "docker",
				"args":    []interface{}{"news-mcp-server"},
			},
		},
		map[string]interface{}{
			"name": "non-mcp-app", // No config section, should be skipped
		},
		map[string]interface{}{
			"name": "empty-mcp-app",
			"config": map[string]interface{}{
				"mcpKey": "", // Empty mcpKey, should be skipped
			},
		},
	}

	servers := parseFleur(testData)

	if len(servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(servers))
	}

	// Test first server
	if servers[0].ID != "github" {
		t.Errorf("expected ID 'github', got '%s'", servers[0].ID)
	}
	if servers[0].Name != "github" {
		t.Errorf("expected Name 'github', got '%s'", servers[0].Name)
	}
	if servers[0].Description != "Weather forecast app" {
		t.Errorf("expected Description 'Weather forecast app', got '%s'", servers[0].Description)
	}
	if servers[0].InstallCmd != "npx -y @modelcontextprotocol/server-github" {
		t.Errorf("expected InstallCmd 'npx -y @modelcontextprotocol/server-github', got '%s'", servers[0].InstallCmd)
	}

	// Test second server
	if servers[1].ID != "news" { // Should use mcpKey
		t.Errorf("expected ID 'news', got '%s'", servers[1].ID)
	}
	if servers[1].InstallCmd != "docker news-mcp-server" {
		t.Errorf("expected InstallCmd 'docker news-mcp-server', got '%s'", servers[1].InstallCmd)
	}
}

func TestBuildFleurInstallCmd(t *testing.T) {
	tests := []struct {
		name     string
		runtime  string
		args     []string
		expected string
	}{
		{
			"npx runtime",
			"npx",
			[]string{"-y", "@modelcontextprotocol/server-github"},
			"npx -y @modelcontextprotocol/server-github",
		},
		{
			"docker runtime",
			"docker",
			[]string{"run", "-i", "mcp-server"},
			"docker run -i mcp-server",
		},
		{
			"uvx runtime",
			"uvx",
			[]string{"some-package"},
			"uvx some-package",
		},
		{
			"stdio runtime",
			"stdio",
			[]string{"python", "-m", "server"},
			"python -m server",
		},
		{
			"unknown runtime",
			"custom-runner",
			[]string{"arg1", "arg2"},
			"custom-runner arg1 arg2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildFleurInstallCmd(tt.runtime, tt.args)
			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestDerivePulseServerDetails(t *testing.T) {
	tests := []struct {
		name            string
		itemMap         map[string]interface{}
		expectedCmd     string
		expectedConnURL string
	}{
		{
			"npm package",
			map[string]interface{}{
				"package_registry": "npm",
				"package_name":     "@example/package",
			},
			"npx -y @example/package",
			"",
		},
		{
			"docker package",
			map[string]interface{}{
				"package_registry": "docker",
				"package_name":     "example/image",
			},
			"docker run -i --rm example/image",
			"",
		},
		{
			"pypi package",
			map[string]interface{}{
				"package_registry": "pypi",
				"package_name":     "example-package",
			},
			"pipx run example-package",
			"",
		},
		{
			"with remote connection",
			map[string]interface{}{
				"package_registry": "npm",
				"package_name":     "example",
				"remotes": []interface{}{
					map[string]interface{}{
						"url_direct": "https://api.example.com/mcp",
					},
				},
			},
			"npx -y example",
			"https://api.example.com/mcp",
		},
		{
			"unknown registry",
			map[string]interface{}{
				"package_registry": "unknown",
				"package_name":     "example",
			},
			"",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, url := derivePulseServerDetails(tt.itemMap)
			if cmd != tt.expectedCmd {
				t.Errorf("expected cmd '%s', got '%s'", tt.expectedCmd, cmd)
			}
			if url != tt.expectedConnURL {
				t.Errorf("expected url '%s', got '%s'", tt.expectedConnURL, url)
			}
		})
	}
}

func TestParseAPITracker(t *testing.T) {
	tests := []struct {
		name     string
		data     interface{}
		expected int
	}{
		{
			"servers field",
			map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{"id": "api1", "name": "API 1"},
				},
			},
			1,
		},
		{
			"items field",
			map[string]interface{}{
				"items": []interface{}{
					map[string]interface{}{"id": "api1", "name": "API 1"},
					map[string]interface{}{"id": "api2", "name": "API 2"},
				},
			},
			2,
		},
		{
			"direct array",
			[]interface{}{
				map[string]interface{}{"id": "api1", "name": "API 1"},
			},
			1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			servers := parseAPITracker(tt.data)
			if len(servers) != tt.expected {
				t.Errorf("expected %d servers, got %d", tt.expected, len(servers))
			}
		})
	}
}

func TestParseApify(t *testing.T) {
	testData := map[string]interface{}{
		"data": map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{
					"name":        "web-scraper",
					"title":       "Web Scraper Tool",
					"description": "Scrape web data",
					"stats": map[string]interface{}{
						"lastRunStartedAt": "2025-01-01T10:00:00Z",
					},
				},
				map[string]interface{}{
					"name":  "data-processor",
					"title": "Data Processor",
				},
			},
		},
	}

	servers := parseApify(testData)

	if len(servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(servers))
	}

	if servers[0].ID != "web-scraper" {
		t.Errorf("expected ID 'web-scraper', got '%s'", servers[0].ID)
	}
	if servers[0].Name != "Web Scraper Tool" {
		t.Errorf("expected name 'Web Scraper Tool', got '%s'", servers[0].Name)
	}
	if servers[0].UpdatedAt != "2025-01-01T10:00:00Z" {
		t.Errorf("expected updatedAt '2025-01-01T10:00:00Z', got '%s'", servers[0].UpdatedAt)
	}
}

func TestCreateServerEntry(t *testing.T) {
	testData := map[string]interface{}{
		"id":          "test-server",
		"name":        "Test Server",
		"description": "A test server",
		"createdAt":   "2025-01-01T00:00:00Z",
		"updated_at":  "2025-01-01T12:00:00Z",
		"url":         "https://test.example.com",
	}

	server := createServerEntry(testData)

	if server.ID != "test-server" {
		t.Errorf("expected ID 'test-server', got '%s'", server.ID)
	}
	if server.Name != "Test Server" {
		t.Errorf("expected name 'Test Server', got '%s'", server.Name)
	}
	if server.Description != "A test server" {
		t.Errorf("expected description 'A test server', got '%s'", server.Description)
	}
	if server.URL != "https://test.example.com" {
		t.Errorf("expected URL 'https://test.example.com', got '%s'", server.URL)
	}
}

func TestSearchServersUnknownRegistry(t *testing.T) {
	ctx := context.Background()
	_, err := SearchServers(ctx, "unknown-registry", "", "")

	if err == nil {
		t.Error("expected error for unknown registry, got nil")
	}

	expectedMsg := "registry 'unknown-registry' not found"
	if err.Error() != expectedMsg {
		t.Errorf("expected error message '%s', got '%s'", expectedMsg, err.Error())
	}
}

func TestSearchServersWithMockServer(t *testing.T) {
	// Create mock server data
	mockData := map[string]interface{}{
		"servers": []interface{}{
			map[string]interface{}{
				"id":          "test-server",
				"name":        "Test Server",
				"description": "A test MCP server",
				"url":         "https://test.example.com/mcp",
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(mockData); err != nil {
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	// Temporarily modify the smithery registry to use our mock server
	originalList := registryList
	registryList = []RegistryEntry{
		{
			ID:         "test",
			Name:       "Test Registry",
			ServersURL: server.URL,
			Protocol:   "modelcontextprotocol/registry",
		},
	}
	defer func() { registryList = originalList }()

	ctx := context.Background()
	servers, err := SearchServers(ctx, "test", "", "")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
		return
	}

	if len(servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(servers))
		return
	}

	server1 := servers[0]
	if server1.ID != "test-server" {
		t.Errorf("expected server ID 'test-server', got '%s'", server1.ID)
	}
	if server1.Name != "Test Server" {
		t.Errorf("expected server name 'Test Server', got '%s'", server1.Name)
	}
	if server1.Registry != "Test Registry" {
		t.Errorf("expected registry 'Test Registry', got '%s'", server1.Registry)
	}
}

func TestSearchServersWithSearch(t *testing.T) {
	// Create mock HTTP server with multiple servers
	mockData := map[string]interface{}{
		"servers": []interface{}{
			map[string]interface{}{
				"id":          "weather-api",
				"name":        "Weather API",
				"description": "Get current weather data",
			},
			map[string]interface{}{
				"id":          "news-feed",
				"name":        "News Feed",
				"description": "Latest news updates",
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(mockData); err != nil {
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	// Temporarily modify registry
	originalList := registryList
	registryList = []RegistryEntry{
		{
			ID:         "test",
			Name:       "Test Registry",
			ServersURL: server.URL,
			Protocol:   "modelcontextprotocol/registry",
		},
	}
	defer func() { registryList = originalList }()

	ctx := context.Background()

	// Test search for "weather"
	servers, err := SearchServers(ctx, "test", "", "weather")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
		return
	}

	if len(servers) != 1 {
		t.Errorf("expected 1 server matching 'weather', got %d", len(servers))
		return
	}

	if servers[0].ID != "weather-api" {
		t.Errorf("expected 'weather-api', got '%s'", servers[0].ID)
	}
}

func TestConstructServerURL(t *testing.T) {
	tests := []struct {
		name     string
		server   ServerEntry
		registry RegistryEntry
		expected string
	}{
		{
			"already has URL",
			ServerEntry{ID: "test", URL: "https://existing.com"},
			RegistryEntry{Protocol: "custom/mcprun"},
			"https://existing.com",
		},
		{
			"mcprun protocol",
			ServerEntry{ID: "weather", URL: ""},
			RegistryEntry{Protocol: "custom/mcprun"},
			"https://weather.mcp.run/mcp/",
		},
		{
			"mcprun protocol with slash in ID",
			ServerEntry{ID: "G4Vi/weather-service", URL: ""},
			RegistryEntry{Protocol: "custom/mcprun"},
			"https://G4Vi-weather-service.mcp.run/mcp/",
		},
		{
			"mcprun protocol with multiple slashes in ID",
			ServerEntry{ID: "owner/namespace/server", URL: ""},
			RegistryEntry{Protocol: "custom/mcprun"},
			"https://owner-namespace-server.mcp.run/mcp/",
		},
		{
			"mcpstore protocol",
			ServerEntry{ID: "news", URL: ""},
			RegistryEntry{Protocol: "custom/mcpstore"},
			"https://api.mcpstore.co/servers/news/mcp",
		},
		{
			"docker protocol",
			ServerEntry{ID: "scraper", URL: ""},
			RegistryEntry{Protocol: "custom/docker"},
			"docker://mcp/scraper",
		},
		{
			"fleur protocol",
			ServerEntry{ID: "app1", URL: ""},
			RegistryEntry{Protocol: "custom/fleur"},
			"https://api.fleurmcp.com/apps/app1/mcp",
		},
		{
			"unknown protocol",
			ServerEntry{ID: "test", URL: ""},
			RegistryEntry{Protocol: "unknown"},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := constructServerURL(&tt.server, &tt.registry)
			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

// Helper function for testing
func TestProtocolParsersWithMissingData(t *testing.T) {
	// Test all parsers with null/invalid data
	tests := []struct {
		name   string
		parser func(interface{}) []ServerEntry
	}{
		{"parseMCPRun", parseMCPRun},
		{"parsePulse", parsePulse},
		{"parseMCPStore", parseMCPStore},
		{"parseDocker", parseDocker},
		{"parseFleur", parseFleur},
		{"parseAPITracker", parseAPITracker},
		{"parseApify", parseApify},
		{"parseDefault", parseDefault},
	}

	invalidInputs := []interface{}{
		nil,
		"not an object",
		123,
		map[string]interface{}{},
		[]interface{}{},
	}

	for _, tt := range tests {
		for _, input := range invalidInputs {
			t.Run(strings.ReplaceAll(tt.name, "parse", "parse_with_invalid_input"), func(t *testing.T) {
				result := tt.parser(input)
				if result == nil {
					t.Errorf("parser %s returned nil instead of empty slice", tt.name)
				}
			})
		}
	}
}
