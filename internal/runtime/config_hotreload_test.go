package runtime

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectConfigChanges(t *testing.T) {
	baseConfig := &config.Config{
		Listen:            "127.0.0.1:8080",
		DataDir:           "/test/data",
		APIKey:            "test-key",
		TopK:              5,
		ToolsLimit:        15,
		ToolResponseLimit: 1000,
		CallToolTimeout:   config.Duration(60 * time.Second),
		Servers:           []*config.ServerConfig{},
		TLS: &config.TLSConfig{
			Enabled: false,
		},
	}

	tests := []struct {
		name                  string
		oldConfig             *config.Config
		newConfig             *config.Config
		expectSuccess         bool
		expectAppliedNow      bool
		expectRequiresRestart bool
		expectRestartReason   string
		expectChangedFields   []string
	}{
		{
			name:                  "no changes",
			oldConfig:             baseConfig,
			newConfig:             baseConfig,
			expectSuccess:         true,
			expectAppliedNow:      false,
			expectRequiresRestart: false,
			expectChangedFields:   []string{},
		},
		{
			name:      "listen address changed",
			oldConfig: baseConfig,
			newConfig: &config.Config{
				Listen:            ":9090", // Changed
				DataDir:           "/test/data",
				APIKey:            "test-key",
				TopK:              5,
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   config.Duration(60 * time.Second),
				Servers:           []*config.ServerConfig{},
			},
			expectSuccess:         true,
			expectAppliedNow:      false,
			expectRequiresRestart: true,
			expectRestartReason:   "Listen address changed",
			expectChangedFields:   []string{"listen"},
		},
		{
			name:      "data directory changed",
			oldConfig: baseConfig,
			newConfig: &config.Config{
				Listen:            "127.0.0.1:8080",
				DataDir:           "/different/data", // Changed
				APIKey:            "test-key",
				TopK:              5,
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   config.Duration(60 * time.Second),
				Servers:           []*config.ServerConfig{},
			},
			expectSuccess:         true,
			expectAppliedNow:      false,
			expectRequiresRestart: true,
			expectRestartReason:   "Data directory changed",
			expectChangedFields:   []string{"data_dir"},
		},
		{
			name:      "API key changed",
			oldConfig: baseConfig,
			newConfig: &config.Config{
				Listen:            "127.0.0.1:8080",
				DataDir:           "/test/data",
				APIKey:            "new-key", // Changed
				TopK:              5,
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   config.Duration(60 * time.Second),
				Servers:           []*config.ServerConfig{},
			},
			expectSuccess:         true,
			expectAppliedNow:      false,
			expectRequiresRestart: true,
			expectRestartReason:   "API key changed",
			expectChangedFields:   []string{"api_key"},
		},
		{
			name:      "TLS configuration changed",
			oldConfig: baseConfig,
			newConfig: &config.Config{
				Listen:            "127.0.0.1:8080",
				DataDir:           "/test/data",
				APIKey:            "test-key",
				TopK:              5,
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   config.Duration(60 * time.Second),
				Servers:           []*config.ServerConfig{},
				TLS: &config.TLSConfig{
					Enabled: true, // Changed
				},
			},
			expectSuccess:         true,
			expectAppliedNow:      false,
			expectRequiresRestart: true,
			expectRestartReason:   "TLS configuration changed",
			expectChangedFields:   []string{"tls"},
		},
		{
			name:      "hot-reloadable: TopK changed",
			oldConfig: baseConfig,
			newConfig: &config.Config{
				Listen:            "127.0.0.1:8080",
				DataDir:           "/test/data",
				APIKey:            "test-key",
				TopK:              10, // Changed
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   config.Duration(60 * time.Second),
				Servers:           []*config.ServerConfig{},
				TLS: &config.TLSConfig{
					Enabled: false,
				},
			},
			expectSuccess:         true,
			expectAppliedNow:      true,
			expectRequiresRestart: false,
			expectChangedFields:   []string{"top_k"},
		},
		{
			name:      "hot-reloadable: ToolsLimit changed",
			oldConfig: baseConfig,
			newConfig: &config.Config{
				Listen:            "127.0.0.1:8080",
				DataDir:           "/test/data",
				APIKey:            "test-key",
				TopK:              5,
				ToolsLimit:        20, // Changed
				ToolResponseLimit: 1000,
				CallToolTimeout:   config.Duration(60 * time.Second),
				Servers:           []*config.ServerConfig{},
				TLS: &config.TLSConfig{
					Enabled: false,
				},
			},
			expectSuccess:         true,
			expectAppliedNow:      true,
			expectRequiresRestart: false,
			expectChangedFields:   []string{"tools_limit"},
		},
		{
			name:      "hot-reloadable: servers changed",
			oldConfig: baseConfig,
			newConfig: &config.Config{
				Listen:            "127.0.0.1:8080",
				DataDir:           "/test/data",
				APIKey:            "test-key",
				TopK:              5,
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   config.Duration(60 * time.Second),
				Servers: []*config.ServerConfig{ // Changed
					{
						Name:     "new-server",
						Protocol: "stdio",
						Command:  "echo",
						Enabled:  true,
					},
				},
				TLS: &config.TLSConfig{
					Enabled: false,
				},
			},
			expectSuccess:         true,
			expectAppliedNow:      true,
			expectRequiresRestart: false,
			expectChangedFields:   []string{"mcpServers"},
		},
		{
			name:      "multiple hot-reloadable changes",
			oldConfig: baseConfig,
			newConfig: &config.Config{
				Listen:            "127.0.0.1:8080",
				DataDir:           "/test/data",
				APIKey:            "test-key",
				TopK:              10,                                 // Changed
				ToolsLimit:        20,                                 // Changed
				ToolResponseLimit: 2000,                               // Changed
				CallToolTimeout:   config.Duration(120 * time.Second), // Changed
				Servers:           []*config.ServerConfig{},
				TLS: &config.TLSConfig{
					Enabled: false,
				},
			},
			expectSuccess:         true,
			expectAppliedNow:      true,
			expectRequiresRestart: false,
			expectChangedFields:   []string{"top_k", "tools_limit", "tool_response_limit", "call_tool_timeout"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectConfigChanges(tt.oldConfig, tt.newConfig)

			require.NotNil(t, result, "Result should not be nil")
			assert.Equal(t, tt.expectSuccess, result.Success, "Success mismatch")
			assert.Equal(t, tt.expectAppliedNow, result.AppliedImmediately, "AppliedImmediately mismatch")
			assert.Equal(t, tt.expectRequiresRestart, result.RequiresRestart, "RequiresRestart mismatch")

			if tt.expectRestartReason != "" {
				assert.Contains(t, result.RestartReason, tt.expectRestartReason, "RestartReason should contain expected text")
			}

			if len(tt.expectChangedFields) > 0 {
				for _, field := range tt.expectChangedFields {
					assert.Contains(t, result.ChangedFields, field, "ChangedFields should contain %s", field)
				}
			} else {
				assert.Empty(t, result.ChangedFields, "ChangedFields should be empty")
			}
		})
	}
}

func TestDetectConfigChangesNilConfigs(t *testing.T) {
	result := DetectConfigChanges(nil, nil)
	require.NotNil(t, result)
	assert.False(t, result.Success)

	cfg := &config.Config{
		Listen: ":8080",
	}

	result = DetectConfigChanges(cfg, nil)
	require.NotNil(t, result)
	assert.False(t, result.Success)

	result = DetectConfigChanges(nil, cfg)
	require.NotNil(t, result)
	assert.False(t, result.Success)
}

func TestFormatChangedFields(t *testing.T) {
	tests := []struct {
		name           string
		changedFields  []string
		expectedOutput string
	}{
		{
			name:           "no fields",
			changedFields:  []string{},
			expectedOutput: "none",
		},
		{
			name:           "one field",
			changedFields:  []string{"listen"},
			expectedOutput: "listen",
		},
		{
			name:           "two fields",
			changedFields:  []string{"listen", "api_key"},
			expectedOutput: "listen and api_key",
		},
		{
			name:           "three fields",
			changedFields:  []string{"listen", "api_key", "top_k"},
			expectedOutput: "listen, api_key, and 1 others",
		},
		{
			name:           "five fields",
			changedFields:  []string{"listen", "api_key", "top_k", "tools_limit", "logging"},
			expectedOutput: "listen, api_key, and 3 others",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &ConfigApplyResult{
				ChangedFields: tt.changedFields,
			}
			output := result.FormatChangedFields()
			assert.Equal(t, tt.expectedOutput, output)
		})
	}
}
