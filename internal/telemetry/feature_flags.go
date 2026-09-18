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
	AggregateUpstreamPrompts      bool     `json:"aggregate_upstream_prompts"`
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

	// Schema v5 (MCP-2745): DockerIsolationEnabled is the global
	// docker_isolation.enabled flag. Set by BuildFeatureFlagSnapshot (pure,
	// config-only). Together with DockerCLISource it lets the dashboard tell
	// "isolation on, 0 matching servers" apart from "isolation off".
	DockerIsolationEnabled bool `json:"docker_isolation_enabled"`

	// Schema v5 (MCP-2745): DockerCLISource is the coarse, fixed-enum branch
	// that resolved the `docker` CLI — one of "path" | "bundled" |
	// "login_shell" | "absent". This is the direct #696 fleet signal (docker
	// installed but not on the spawn PATH). NEVER the path string itself.
	// Populated by the telemetry service at heartbeat time (the resolution is a
	// runtime concern) — mirrors DockerAvailable.
	DockerCLISource string `json:"docker_cli_source,omitempty"`

	// Schema v8: DeepScanEnabled is the opt-in deep-scan master switch
	// (security.deep_scan.enabled). Set by BuildFeatureFlagSnapshot (pure,
	// config-only) like DockerIsolationEnabled. It lets the dashboard read
	// tpa_scanner scan volume against the population that actually turned the
	// deep-scan layer on.
	DeepScanEnabled bool `json:"deep_scan_enabled"`

	// Schema v13 (Spec 107 US7): ServerEditionEnabled reports whether the
	// server_edition block is present and enabled. Read through the
	// build-tagged config.ServerEditionEnabled accessor, so the personal
	// build — where the block is an opaque, uninterpreted carrier — always
	// reports false.
	ServerEditionEnabled bool `json:"server_edition_enabled"`

	// Schema v13 (Spec 107 US7): IdPProvider is the configured identity
	// provider FAMILY as a closed enum — one of "google" | "github" |
	// "microsoft" | "oidc" | "none". It is the provider kind only: NEVER the
	// issuer URL, tenant id, client id or display name. "none" when the block
	// is disabled, absent, has no oauth section, or (personal build) cannot be
	// interpreted. Set by BuildFeatureFlagSnapshot (pure, config-only).
	IdPProvider string `json:"idp_provider"`
}

// IdP provider families reported in feature_flags.idp_provider (schema v13).
// The vocabulary is closed: idpProviderEnum clamps anything else to
// IdPProviderNone so a misconfigured or pre-validation provider string can
// never widen the enum on the wire.
const (
	IdPProviderGoogle    = "google"
	IdPProviderGitHub    = "github"
	IdPProviderMicrosoft = "microsoft"
	IdPProviderOIDC      = "oidc"
	IdPProviderNone      = "none"
)

// idpProviderEnum maps the accessor's raw provider family to the closed
// telemetry enum. enabled=false short-circuits to "none" regardless of what
// the block says (contract: "none when disabled/unset").
func idpProviderEnum(enabled bool, family string) string {
	if !enabled {
		return IdPProviderNone
	}
	switch strings.ToLower(strings.TrimSpace(family)) {
	case IdPProviderGoogle:
		return IdPProviderGoogle
	case IdPProviderGitHub:
		return IdPProviderGitHub
	case IdPProviderMicrosoft:
		return IdPProviderMicrosoft
	case IdPProviderOIDC:
		return IdPProviderOIDC
	default:
		return IdPProviderNone
	}
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
		return &FeatureFlagSnapshot{OAuthProviderTypes: []string{}, IdPProvider: IdPProviderNone}
	}

	snap := &FeatureFlagSnapshot{
		EnableSocket:             cfg.EnableSocket,
		EnablePrompts:            cfg.EnablePrompts,
		AggregateUpstreamPrompts: cfg.AggregateUpstreamPrompts,
		RequireMCPAuth:           cfg.RequireMCPAuth,
		EnableCodeExecution:      cfg.EnableCodeExecution,
		QuarantineEnabled:        cfg.IsQuarantineEnabled(),
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

	// Schema v5 (MCP-2745): global docker isolation toggle. Nil-guarded so a
	// config without the block reports false rather than panicking.
	if cfg.DockerIsolation != nil {
		snap.DockerIsolationEnabled = cfg.DockerIsolation.Enabled
	}

	// Schema v8: deep-scan master switch. IsDeepScanEnabled is nil-safe on
	// both the SecurityConfig and its DeepScan block, so a config without the
	// security block reports false.
	snap.DeepScanEnabled = cfg.Security.IsDeepScanEnabled()

	// Schema v13 (Spec 107 US7): server-edition presence and IdP family, read
	// only through the build-tagged accessors so the personal build never
	// interprets the block. The family is clamped to the closed enum; the
	// issuer is never consulted.
	snap.ServerEditionEnabled = config.ServerEditionEnabled(cfg)
	snap.IdPProvider = idpProviderEnum(snap.ServerEditionEnabled, config.IdPProviderFamily(cfg))

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
