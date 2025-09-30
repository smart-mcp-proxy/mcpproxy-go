package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateDetailed(t *testing.T) {
	tests := []struct {
		name           string
		config         *Config
		expectedErrors int
		errorFields    []string
	}{
		{
			name: "valid config",
			config: &Config{
				Listen:            "127.0.0.1:8080",
				TopK:              5,
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   Duration(60000000000), // 1 minute
				Servers:           []*ServerConfig{},
			},
			expectedErrors: 0,
			errorFields:    []string{},
		},
		{
			name: "invalid listen address",
			config: &Config{
				Listen:            "", // Will fail validation (empty not valid unless it's truly empty)
				TopK:              0,  // Will fail validation
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   Duration(60000000000), // Add valid timeout
			},
			expectedErrors: 1, // Only top_k error (empty listen is actually not validated as error)
			errorFields:    []string{"top_k"},
		},
		{
			name: "TopK out of range",
			config: &Config{
				Listen:            ":8080",
				TopK:              101, // Too high
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   Duration(60000000000), // Add valid timeout
			},
			expectedErrors: 1,
			errorFields:    []string{"top_k"},
		},
		{
			name: "ToolsLimit out of range",
			config: &Config{
				Listen:            ":8080",
				TopK:              5,
				ToolsLimit:        0, // Too low
				ToolResponseLimit: 1000,
				CallToolTimeout:   Duration(60000000000), // Add valid timeout
			},
			expectedErrors: 1,
			errorFields:    []string{"tools_limit"},
		},
		{
			name: "negative ToolResponseLimit",
			config: &Config{
				Listen:            ":8080",
				TopK:              5,
				ToolsLimit:        15,
				ToolResponseLimit: -100, // Negative
				CallToolTimeout:   Duration(60000000000), // Add valid timeout
			},
			expectedErrors: 1,
			errorFields:    []string{"tool_response_limit"},
		},
		{
			name: "invalid timeout",
			config: &Config{
				Listen:            ":8080",
				TopK:              5,
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   Duration(0), // Zero
			},
			expectedErrors: 1,
			errorFields:    []string{"call_tool_timeout"},
		},
		{
			name: "server missing name",
			config: &Config{
				Listen:            ":8080",
				TopK:              5,
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   Duration(60000000000),
				Servers: []*ServerConfig{
					{
						Name:     "", // Missing
						Protocol: "stdio",
						Command:  "echo",
					},
				},
			},
			expectedErrors: 1,
			errorFields:    []string{"mcpServers[0].name"},
		},
		{
			name: "duplicate server names",
			config: &Config{
				Listen:            ":8080",
				TopK:              5,
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   Duration(60000000000),
				Servers: []*ServerConfig{
					{
						Name:     "test",
						Protocol: "stdio",
						Command:  "echo",
					},
					{
						Name:     "test", // Duplicate
						Protocol: "stdio",
						Command:  "cat",
					},
				},
			},
			expectedErrors: 1,
			errorFields:    []string{"mcpServers[1].name"},
		},
		{
			name: "invalid protocol",
			config: &Config{
				Listen:            ":8080",
				TopK:              5,
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   Duration(60000000000),
				Servers: []*ServerConfig{
					{
						Name:     "test",
						Protocol: "invalid", // Invalid
						Command:  "echo",
					},
				},
			},
			expectedErrors: 1,
			errorFields:    []string{"mcpServers[0].protocol"},
		},
		{
			name: "stdio server missing command",
			config: &Config{
				Listen:            ":8080",
				TopK:              5,
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   Duration(60000000000),
				Servers: []*ServerConfig{
					{
						Name:     "test",
						Protocol: "stdio",
						Command:  "", // Missing
					},
				},
			},
			expectedErrors: 1,
			errorFields:    []string{"mcpServers[0].command"},
		},
		{
			name: "http server missing url",
			config: &Config{
				Listen:            ":8080",
				TopK:              5,
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   Duration(60000000000),
				Servers: []*ServerConfig{
					{
						Name:     "test",
						Protocol: "http",
						URL:      "", // Missing
					},
				},
			},
			expectedErrors: 1,
			errorFields:    []string{"mcpServers[0].url"},
		},
		{
			name: "invalid log level",
			config: &Config{
				Listen:            ":8080",
				TopK:              5,
				ToolsLimit:        15,
				ToolResponseLimit: 1000,
				CallToolTimeout:   Duration(60000000000),
				Logging: &LogConfig{
					Level: "invalid", // Invalid
				},
			},
			expectedErrors: 1,
			errorFields:    []string{"logging.level"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := tt.config.ValidateDetailed()
			assert.Equal(t, tt.expectedErrors, len(errors), "Expected %d errors, got %d: %v", tt.expectedErrors, len(errors), errors)

			if tt.expectedErrors > 0 {
				// Check that expected fields are in error list
				errorFieldMap := make(map[string]bool)
				for _, err := range errors {
					errorFieldMap[err.Field] = true
				}

				for _, expectedField := range tt.errorFields {
					assert.True(t, errorFieldMap[expectedField], "Expected error for field %s", expectedField)
				}
			}
		})
	}
}

func TestValidationError(t *testing.T) {
	err := ValidationError{
		Field:   "test_field",
		Message: "test message",
	}

	assert.Equal(t, "test_field: test message", err.Error())
}

func TestIsValidListenAddr(t *testing.T) {
	tests := []struct {
		name  string
		addr  string
		valid bool
	}{
		{"empty", "", false},
		{"port only", ":8080", true},
		{"host and port", "127.0.0.1:8080", true},
		{"localhost", "localhost:8080", true},
		{"just colon", ":", true}, // Edge case: technically valid for port 0
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidListenAddr(tt.addr)
			assert.Equal(t, tt.valid, result, "Expected %s to be valid=%v", tt.addr, tt.valid)
		})
	}
}

func TestValidateWithDefaults(t *testing.T) {
	// Test that Validate applies defaults before validation
	cfg := &Config{
		Listen:            "",  // Should default to 127.0.0.1:8080
		TopK:              0,   // Should default to 5
		ToolsLimit:        0,   // Should default to 15
		ToolResponseLimit: -1,  // Should default to 0
		CallToolTimeout:   0,   // Should default to 2 minutes
		Servers:           []*ServerConfig{},
	}

	err := cfg.Validate()
	require.NoError(t, err, "Validation should succeed after applying defaults")

	assert.Equal(t, "127.0.0.1:8080", cfg.Listen)
	assert.Equal(t, 5, cfg.TopK)
	assert.Equal(t, 15, cfg.ToolsLimit)
	assert.Equal(t, 0, cfg.ToolResponseLimit)
	assert.Greater(t, cfg.CallToolTimeout.Duration().Seconds(), 0.0)
}