package config

import "fmt"

// FeatureFlags represents feature toggles for mcpproxy functionality
type FeatureFlags struct {
	// Runtime features
	EnableRuntime     bool `json:"enable_runtime" mapstructure:"enable_runtime"`
	EnableEventBus    bool `json:"enable_event_bus" mapstructure:"enable_event_bus"`
	EnableSSE         bool `json:"enable_sse" mapstructure:"enable_sse"`

	// Observability features
	EnableObservability bool `json:"enable_observability" mapstructure:"enable_observability"`
	EnableHealthChecks  bool `json:"enable_health_checks" mapstructure:"enable_health_checks"`
	EnableMetrics       bool `json:"enable_metrics" mapstructure:"enable_metrics"`
	EnableTracing       bool `json:"enable_tracing" mapstructure:"enable_tracing"`

	// Security features
	EnableOAuth         bool `json:"enable_oauth" mapstructure:"enable_oauth"`
	EnableQuarantine    bool `json:"enable_quarantine" mapstructure:"enable_quarantine"`
	EnableDockerIsolation bool `json:"enable_docker_isolation" mapstructure:"enable_docker_isolation"`

	// Storage features
	EnableSearch        bool `json:"enable_search" mapstructure:"enable_search"`
	EnableCaching       bool `json:"enable_caching" mapstructure:"enable_caching"`
	EnableAsyncStorage  bool `json:"enable_async_storage" mapstructure:"enable_async_storage"`

	// UI features
	EnableWebUI         bool `json:"enable_web_ui" mapstructure:"enable_web_ui"`
	EnableTray          bool `json:"enable_tray" mapstructure:"enable_tray"`

	// Development features
	EnableDebugLogging  bool `json:"enable_debug_logging" mapstructure:"enable_debug_logging"`
	EnableContractTests bool `json:"enable_contract_tests" mapstructure:"enable_contract_tests"`
}

// DefaultFeatureFlags returns the default feature flag configuration
func DefaultFeatureFlags() FeatureFlags {
	return FeatureFlags{
		// Runtime features (core functionality)
		EnableRuntime:     true,
		EnableEventBus:    true,
		EnableSSE:         true,

		// Observability features
		EnableObservability: true,
		EnableHealthChecks:  true,
		EnableMetrics:       true,
		EnableTracing:       false, // Disabled by default for performance

		// Security features
		EnableOAuth:           true,
		EnableQuarantine:      true,
		EnableDockerIsolation: false, // Disabled by default, requires Docker

		// Storage features
		EnableSearch:       true,
		EnableCaching:      true,
		EnableAsyncStorage: true,

		// UI features
		EnableWebUI: true,
		EnableTray:  true,

		// Development features
		EnableDebugLogging:  false,
		EnableContractTests: false,
	}
}

// IsFeatureEnabled checks if a specific feature is enabled
func (ff *FeatureFlags) IsFeatureEnabled(feature string) bool {
	switch feature {
	case "runtime":
		return ff.EnableRuntime
	case "event_bus":
		return ff.EnableEventBus
	case "sse":
		return ff.EnableSSE
	case "observability":
		return ff.EnableObservability
	case "health_checks":
		return ff.EnableHealthChecks
	case "metrics":
		return ff.EnableMetrics
	case "tracing":
		return ff.EnableTracing
	case "oauth":
		return ff.EnableOAuth
	case "quarantine":
		return ff.EnableQuarantine
	case "docker_isolation":
		return ff.EnableDockerIsolation
	case "search":
		return ff.EnableSearch
	case "caching":
		return ff.EnableCaching
	case "async_storage":
		return ff.EnableAsyncStorage
	case "web_ui":
		return ff.EnableWebUI
	case "tray":
		return ff.EnableTray
	case "debug_logging":
		return ff.EnableDebugLogging
	case "contract_tests":
		return ff.EnableContractTests
	default:
		return false
	}
}

// ValidateFeatureFlags validates feature flag dependencies
func (ff *FeatureFlags) ValidateFeatureFlags() error {
	// Observability features require observability to be enabled
	if (ff.EnableHealthChecks || ff.EnableMetrics || ff.EnableTracing) && !ff.EnableObservability {
		return fmt.Errorf("observability features require enable_observability=true")
	}

	// SSE requires event bus
	if ff.EnableSSE && !ff.EnableEventBus {
		return fmt.Errorf("SSE requires enable_event_bus=true")
	}

	// Event bus requires runtime
	if ff.EnableEventBus && !ff.EnableRuntime {
		return fmt.Errorf("event bus requires enable_runtime=true")
	}

	return nil
}