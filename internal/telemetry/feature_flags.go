package telemetry

import (
	"strings"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// FeatureFlagSnapshot captures the boolean / enum feature flags reported in
// the daily heartbeat. Spec 042 User Story 4.
type FeatureFlagSnapshot struct {
	EnableSocket                  bool     `json:"enable_socket"`
	EnableWebUI                   bool     `json:"enable_web_ui"`
	EnablePrompts                 bool     `json:"enable_prompts"`
	RequireMCPAuth                bool     `json:"require_mcp_auth"`
	EnableCodeExecution           bool     `json:"enable_code_execution"`
	QuarantineEnabled             bool     `json:"quarantine_enabled"`
	SensitiveDataDetectionEnabled bool     `json:"sensitive_data_detection_enabled"`
	OAuthProviderTypes            []string `json:"oauth_provider_types"`

	// Schema v3: DockerAvailable reports whether the host has a reachable
	// Docker daemon, as observed by the runtime's checkDockerDaemon probe.
	// Populated by the telemetry service at heartbeat time (not by
	// BuildFeatureFlagSnapshot) so the snapshot helper stays side-effect-free
	// and doesn't shell out to `docker info`.
	DockerAvailable bool `json:"docker_available"`
}

// protocolKeys is the canonical fixed-enum set of protocol labels emitted by
// the telemetry payload. Dashboard queries can rely on these keys always
// being present (even with a zero count) in the map. This deliberately does
// NOT include the raw "streamable-http" (dashed) form — we normalize to the
// underscored form so the JSON map uses idiomatic identifier-safe keys.
var protocolKeys = []string{"stdio", "http", "sse", "streamable_http", "auto"}

// buildServerProtocolCounts counts configured upstream servers grouped by
// Protocol. Keys are fixed to protocolKeys; unknown or empty values bucket
// into "auto". Never emits server names, URLs, or unknown keys — keeps
// cardinality bounded.
func buildServerProtocolCounts(cfg *config.Config) map[string]int {
	return buildServerProtocolCountsWithLogger(cfg, nil)
}

// buildServerProtocolCountsWithLogger is the internal form used by the
// telemetry service. It records unknown protocol values at debug level so
// operators can spot misconfigurations without inflating metric cardinality.
// Pass nil for no-op logging (unit tests).
func buildServerProtocolCountsWithLogger(cfg *config.Config, logger *zap.Logger) map[string]int {
	counts := make(map[string]int, len(protocolKeys))
	for _, k := range protocolKeys {
		counts[k] = 0
	}
	if cfg == nil {
		return counts
	}
	for _, srv := range cfg.Servers {
		if srv == nil {
			continue
		}
		key := normalizeProtocolKey(srv.Protocol)
		if key == "" {
			// Unknown value — bucket into "auto" and log at debug.
			if logger != nil {
				logger.Debug("telemetry: unknown server protocol bucketed into auto",
					zap.String("protocol", srv.Protocol))
			}
			key = "auto"
		}
		counts[key]++
	}
	return counts
}

// normalizeProtocolKey maps a raw config protocol string to one of the
// canonical keys in protocolKeys. Returns "" for unrecognized values so the
// caller can log and bucket them explicitly.
func normalizeProtocolKey(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "stdio":
		return "stdio"
	case "http":
		return "http"
	case "sse":
		return "sse"
	case "streamable-http", "streamable_http", "streamablehttp":
		return "streamable_http"
	case "", "auto":
		return "auto"
	default:
		return ""
	}
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
		EnablePrompts:       cfg.EnablePrompts,
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
