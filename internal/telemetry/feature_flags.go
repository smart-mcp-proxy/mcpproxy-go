package telemetry

import (
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// FeatureFlagSnapshot captures the boolean / enum feature flags reported in
// the daily heartbeat. Spec 042 User Story 4.
type FeatureFlagSnapshot struct {
	EnableSocket                  bool     `json:"enable_socket"`
	EnableWebUI                   bool     `json:"enable_web_ui"`
	RequireMCPAuth                bool     `json:"require_mcp_auth"`
	EnableCodeExecution           bool     `json:"enable_code_execution"`
	QuarantineEnabled             bool     `json:"quarantine_enabled"`
	SensitiveDataDetectionEnabled bool     `json:"sensitive_data_detection_enabled"`
	OAuthProviderTypes            []string `json:"oauth_provider_types"`
}

// BuildFeatureFlagSnapshot returns a snapshot of the current feature flag
// state. It records boolean flags and a sorted, deduplicated list of OAuth
// provider TYPES (not URLs, client IDs, or tenant identifiers). The empty list
// is returned if no upstream servers have OAuth configured.
func BuildFeatureFlagSnapshot(cfg *config.Config) *FeatureFlagSnapshot {
	if cfg == nil {
		return &FeatureFlagSnapshot{OAuthProviderTypes: []string{}}
	}

	snap := &FeatureFlagSnapshot{
		EnableSocket:        cfg.EnableSocket,
		RequireMCPAuth:      cfg.RequireMCPAuth,
		EnableCodeExecution: cfg.EnableCodeExecution,
		QuarantineEnabled:   cfg.IsQuarantineEnabled(),
	}
	// Read EnableWebUI from the legacy Features block. The Features struct is
	// flagged as deprecated for runtime purposes, but it is still the canonical
	// source for user-facing UI toggles and remains the only field that maps
	// to the heartbeat-v2 `enable_web_ui` signal. Nil-guarded so telemetry
	// gracefully reports `false` when Features is unset.
	if cfg.Features != nil { //nolint:staticcheck // SA1019: telemetry reads deprecated Features for the web UI signal.
		snap.EnableWebUI = cfg.Features.EnableWebUI //nolint:staticcheck // SA1019: see above.
	}

	if cfg.SensitiveDataDetection != nil {
		snap.SensitiveDataDetectionEnabled = cfg.SensitiveDataDetection.IsEnabled()
	}

	// Derive OAuth provider types from upstream server URLs.
	var providerTypes []string
	for _, srv := range cfg.Servers {
		if srv == nil || srv.OAuth == nil {
			continue
		}
		// OAuth is configured for this server. Classify by URL host.
		providerTypes = append(providerTypes, classifyOAuthProvider(srv.URL))
	}
	snap.OAuthProviderTypes = SortedOAuthProviderTypes(providerTypes)
	return snap
}

// classifyOAuthProvider maps an upstream server URL to one of the four OAuth
// provider type buckets. Defaults to "generic" for anything we don't
// recognize. NEVER includes the URL itself in the output.
func classifyOAuthProvider(serverURL string) string {
	host := strings.ToLower(serverURL)
	switch {
	case strings.Contains(host, "google.com") ||
		strings.Contains(host, "googleapis.com") ||
		strings.Contains(host, "googleusercontent.com"):
		return "google"
	case strings.Contains(host, "github.com") ||
		strings.Contains(host, "githubusercontent.com"):
		return "github"
	case strings.Contains(host, "microsoftonline.com") ||
		strings.Contains(host, "microsoft.com") ||
		strings.Contains(host, "azurewebsites.net") ||
		strings.Contains(host, "azure.com"):
		return "microsoft"
	default:
		return "generic"
	}
}
