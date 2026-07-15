package runtime

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDetectConfigChanges_Observability (MCP-835 / Codex finding #3): changing
// the observability usage cadence must be detected as a hot-reloadable change so
// ApplyConfig can push the new persist interval to the running ActivityService.
// SetUsagePersistInterval advertises hot-reload; the detector must back it.
func TestDetectConfigChanges_Observability(t *testing.T) {
	base := &config.Config{
		Listen: "127.0.0.1:8080", DataDir: "/d", TLS: &config.TLSConfig{},
		Observability: &config.ObservabilityConfig{
			UsageCacheTTL:        config.Duration(5 * time.Second),
			UsagePersistInterval: config.Duration(30 * time.Second),
		},
	}
	changed := &config.Config{
		Listen: "127.0.0.1:8080", DataDir: "/d", TLS: &config.TLSConfig{},
		Observability: &config.ObservabilityConfig{
			UsageCacheTTL:        config.Duration(5 * time.Second),
			UsagePersistInterval: config.Duration(10 * time.Second),
		},
	}

	result := DetectConfigChanges(base, changed)
	require.True(t, result.Success)
	assert.Contains(t, result.ChangedFields, "observability")
	assert.False(t, result.RequiresRestart, "cadence change is hot-reloadable")
}

// TestDetectConfigChanges_DiscoveryHealthIntervals (MCP-1189 / Codex finding #2):
// a global health_check_interval / tool_discovery_interval edit must be detected
// as a hot-reloadable change so ApplyConfig propagates the new cadence to the
// running upstream manager + managed clients. Without this, a lone interval edit
// would be reported as "no changes detected" (FR-012/SC-002).
func TestDetectConfigChanges_DiscoveryHealthIntervals(t *testing.T) {
	mk := func(health, discovery *config.Duration) *config.Config {
		return &config.Config{
			Listen: "127.0.0.1:8080", DataDir: "/d", TLS: &config.TLSConfig{},
			HealthCheckInterval:   health,
			ToolDiscoveryInterval: discovery,
		}
	}

	t.Run("health_check_interval change detected", func(t *testing.T) {
		old45 := config.Duration(45 * time.Second)
		new10 := config.Duration(10 * time.Second)
		result := DetectConfigChanges(mk(&old45, nil), mk(&new10, nil))
		require.True(t, result.Success)
		assert.Contains(t, result.ChangedFields, "health_check_interval")
		assert.False(t, result.RequiresRestart, "interval change is hot-reloadable")
	})

	t.Run("tool_discovery_interval change detected", func(t *testing.T) {
		old5m := config.Duration(5 * time.Minute)
		new1m := config.Duration(1 * time.Minute)
		result := DetectConfigChanges(mk(nil, &old5m), mk(nil, &new1m))
		require.True(t, result.Success)
		assert.Contains(t, result.ChangedFields, "tool_discovery_interval")
		assert.False(t, result.RequiresRestart)
	})

	t.Run("setting from unset (nil -> value) detected", func(t *testing.T) {
		val := config.Duration(0) // disabling the loop
		result := DetectConfigChanges(mk(nil, nil), mk(&val, nil))
		require.True(t, result.Success)
		assert.Contains(t, result.ChangedFields, "health_check_interval")
	})

	t.Run("unchanged intervals not reported", func(t *testing.T) {
		same := config.Duration(45 * time.Second)
		other := config.Duration(45 * time.Second)
		result := DetectConfigChanges(mk(&same, nil), mk(&other, nil))
		require.True(t, result.Success)
		assert.NotContains(t, result.ChangedFields, "health_check_interval")
	})
}

// TestDetectConfigChanges_DeepScanSecurity (Spec 077 US3 / Codex finding #1):
// a lone security.deep_scan.* edit must be detected as a hot-reloadable change so
// ApplyConfig emits config.reloaded and the scanner service is re-gated without a
// restart. Without this the change fell through as "no changes detected".
func TestDetectConfigChanges_DeepScanSecurity(t *testing.T) {
	mk := func(enabled bool) *config.Config {
		return &config.Config{
			Listen: "127.0.0.1:8080", DataDir: "/d", TLS: &config.TLSConfig{},
			Security: &config.SecurityConfig{
				DeepScan: &config.DeepScanConfig{Enabled: enabled},
			},
		}
	}

	t.Run("deep_scan.enabled toggle detected", func(t *testing.T) {
		result := DetectConfigChanges(mk(false), mk(true))
		require.True(t, result.Success)
		assert.Contains(t, result.ChangedFields, "security")
		assert.False(t, result.RequiresRestart, "deep-scan toggle is hot-reloadable")
		assert.True(t, result.AppliedImmediately)
	})

	t.Run("unchanged security not reported", func(t *testing.T) {
		result := DetectConfigChanges(mk(true), mk(true))
		require.True(t, result.Success)
		assert.NotContains(t, result.ChangedFields, "security")
	})

	t.Run("scanners allow-list change detected", func(t *testing.T) {
		old := mk(true)
		next := mk(true)
		next.Security.DeepScan.Scanners = []string{"trivy"}
		result := DetectConfigChanges(old, next)
		require.True(t, result.Success)
		assert.Contains(t, result.ChangedFields, "security")
	})
}

func TestDetectConfigChanges(t *testing.T) {
	baseConfig := &config.Config{
		Listen:            "127.0.0.1:8080",
		DataDir:           "/test/data",
		APIKey:            "test-key",
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
			name:      "hot-reloadable: ToolsLimit changed",
			oldConfig: baseConfig,
			newConfig: &config.Config{
				Listen:            "127.0.0.1:8080",
				DataDir:           "/test/data",
				APIKey:            "test-key",
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
			expectChangedFields:   []string{"tools_limit", "tool_response_limit", "call_tool_timeout"},
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

// TestDetectConfigChanges_UpdateCheck (Spec 079 FR-012): an update_check
// {enabled,channel} edit must be detected as a hot-reloadable change so
// ApplyConfig re-gates the running updatecheck.Checker without a restart —
// otherwise a lone update_check edit reports "No configuration changes
// detected" and only takes effect on restart.
func TestDetectConfigChanges_UpdateCheck(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	mk := func(uc *config.UpdateCheckConfig) *config.Config {
		return &config.Config{
			Listen: "127.0.0.1:8080", DataDir: "/d", TLS: &config.TLSConfig{},
			UpdateCheck: uc,
		}
	}

	t.Run("enabled flip detected", func(t *testing.T) {
		result := DetectConfigChanges(
			mk(nil),
			mk(&config.UpdateCheckConfig{Enabled: boolPtr(false)}),
		)
		require.True(t, result.Success)
		assert.Contains(t, result.ChangedFields, "update_check")
		assert.False(t, result.RequiresRestart, "update_check change is hot-reloadable")
	})

	t.Run("channel switch detected", func(t *testing.T) {
		result := DetectConfigChanges(
			mk(&config.UpdateCheckConfig{Channel: config.UpdateChannelStable}),
			mk(&config.UpdateCheckConfig{Channel: config.UpdateChannelRC}),
		)
		require.True(t, result.Success)
		assert.Contains(t, result.ChangedFields, "update_check")
	})

	t.Run("no change not reported", func(t *testing.T) {
		result := DetectConfigChanges(mk(nil), mk(nil))
		require.True(t, result.Success)
		assert.NotContains(t, result.ChangedFields, "update_check")
	})
}

// TestDetectConfigChanges_ToonOutput (spec 084 T021, FR-001): a lone
// toon_output / toon_min_savings_pct edit must be reported as a hot-reloadable
// change — the encoder reads the config fresh on every call, so the entries
// exist to acknowledge the reload instead of logging "no changes detected".
func TestDetectConfigChanges_ToonOutput(t *testing.T) {
	mk := func(mode string, pct int) *config.Config {
		return &config.Config{
			Listen: "127.0.0.1:8080", DataDir: "/d", TLS: &config.TLSConfig{},
			ToonOutput: mode, ToonMinSavingsPct: pct,
		}
	}

	t.Run("toon_output change detected", func(t *testing.T) {
		result := DetectConfigChanges(mk("off", 15), mk("adaptive", 15))
		require.True(t, result.Success)
		assert.Contains(t, result.ChangedFields, "toon_output")
		assert.False(t, result.RequiresRestart, "toon_output is hot-reloadable")
	})

	t.Run("toon_min_savings_pct change detected", func(t *testing.T) {
		result := DetectConfigChanges(mk("adaptive", 15), mk("adaptive", 30))
		require.True(t, result.Success)
		assert.Contains(t, result.ChangedFields, "toon_min_savings_pct")
		assert.False(t, result.RequiresRestart)
	})

	t.Run("no toon change means not reported", func(t *testing.T) {
		result := DetectConfigChanges(mk("adaptive", 15), mk("adaptive", 15))
		assert.NotContains(t, result.ChangedFields, "toon_output")
		assert.NotContains(t, result.ChangedFields, "toon_min_savings_pct")
	})
}
