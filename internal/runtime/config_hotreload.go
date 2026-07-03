package runtime

import (
	"fmt"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"reflect"
)

// ConfigApplyResult represents the result of applying a configuration
type ConfigApplyResult struct {
	Success            bool                     `json:"success"`
	AppliedImmediately bool                     `json:"applied_immediately"`
	RequiresRestart    bool                     `json:"requires_restart"`
	RestartReason      string                   `json:"restart_reason,omitempty"`
	ChangedFields      []string                 `json:"changed_fields,omitempty"`
	ValidationErrors   []config.ValidationError `json:"validation_errors,omitempty"`
}

// DetectConfigChanges compares old and new configurations to determine what changed
// and whether a restart is required
func DetectConfigChanges(oldCfg, newCfg *config.Config) *ConfigApplyResult {
	result := &ConfigApplyResult{
		Success:            true,
		AppliedImmediately: true,
		RequiresRestart:    false,
		ChangedFields:      []string{},
	}

	if oldCfg == nil || newCfg == nil {
		result.Success = false
		return result
	}

	// Check for changes that require restart

	// 1. Listen address change (requires HTTP server rebind)
	if oldCfg.Listen != newCfg.Listen {
		result.ChangedFields = append(result.ChangedFields, "listen")
		result.RequiresRestart = true
		result.AppliedImmediately = false
		result.RestartReason = "Listen address changed - requires HTTP server restart"
		return result
	}

	// 2. Data directory change (requires database reconnection)
	if oldCfg.DataDir != newCfg.DataDir {
		result.ChangedFields = append(result.ChangedFields, "data_dir")
		result.RequiresRestart = true
		result.AppliedImmediately = false
		result.RestartReason = "Data directory changed - requires database restart"
		return result
	}

	// 3. API key change (affects authentication middleware)
	if oldCfg.APIKey != newCfg.APIKey {
		result.ChangedFields = append(result.ChangedFields, "api_key")
		result.RequiresRestart = true
		result.AppliedImmediately = false
		result.RestartReason = "API key changed - requires middleware reconfiguration"
		return result
	}

	// 4. TLS configuration changes
	if !reflect.DeepEqual(oldCfg.TLS, newCfg.TLS) {
		tlsChanged := false
		if oldCfg.TLS == nil || newCfg.TLS == nil {
			tlsChanged = true
		} else if oldCfg.TLS.Enabled != newCfg.TLS.Enabled ||
			oldCfg.TLS.RequireClientCert != newCfg.TLS.RequireClientCert ||
			oldCfg.TLS.CertsDir != newCfg.TLS.CertsDir {
			tlsChanged = true
		}

		if tlsChanged {
			result.ChangedFields = append(result.ChangedFields, "tls")
			result.RequiresRestart = true
			result.AppliedImmediately = false
			result.RestartReason = "TLS configuration changed - requires HTTP server restart"
			return result
		}
	}

	// Track hot-reloadable changes

	// Server configuration changes (can be hot-reloaded)
	if !reflect.DeepEqual(oldCfg.Servers, newCfg.Servers) {
		result.ChangedFields = append(result.ChangedFields, "mcpServers")
		// These will be applied by triggering server reconnection
	}

	// Tool limits (can be hot-reloaded)
	if oldCfg.ToolsLimit != newCfg.ToolsLimit {
		result.ChangedFields = append(result.ChangedFields, "tools_limit")
	}
	if oldCfg.ToolResponseLimit != newCfg.ToolResponseLimit {
		result.ChangedFields = append(result.ChangedFields, "tool_response_limit")
	}
	if oldCfg.CallToolTimeout != newCfg.CallToolTimeout {
		result.ChangedFields = append(result.ChangedFields, "call_tool_timeout")
	}

	// Discovery & health-check cadence (spec 074 — hot-reloadable). The health
	// loop (managed client) and indexing loop (runtime) re-resolve their interval
	// each cycle, and ApplyConfig propagates the new global config to the upstream
	// manager + managed clients, so a global edit takes effect without a restart
	// (FR-012/SC-002). Tracking these keeps a lone interval edit from being
	// reported as "no changes detected".
	if !reflect.DeepEqual(oldCfg.HealthCheckInterval, newCfg.HealthCheckInterval) {
		result.ChangedFields = append(result.ChangedFields, "health_check_interval")
	}
	if !reflect.DeepEqual(oldCfg.ToolDiscoveryInterval, newCfg.ToolDiscoveryInterval) {
		result.ChangedFields = append(result.ChangedFields, "tool_discovery_interval")
	}

	// Logging configuration (can be hot-reloaded)
	if !reflect.DeepEqual(oldCfg.Logging, newCfg.Logging) {
		result.ChangedFields = append(result.ChangedFields, "logging")
	}

	// Docker isolation configuration (can be hot-reloaded for new servers)
	if !reflect.DeepEqual(oldCfg.DockerIsolation, newCfg.DockerIsolation) {
		result.ChangedFields = append(result.ChangedFields, "docker_isolation")
	}

	// Registries (can be hot-reloaded)
	if !reflect.DeepEqual(oldCfg.Registries, newCfg.Registries) {
		result.ChangedFields = append(result.ChangedFields, "registries")
	}

	// Security settings (can be hot-reloaded)
	if oldCfg.ReadOnlyMode != newCfg.ReadOnlyMode {
		result.ChangedFields = append(result.ChangedFields, "read_only_mode")
	}
	if oldCfg.DisableManagement != newCfg.DisableManagement {
		result.ChangedFields = append(result.ChangedFields, "disable_management")
	}
	if oldCfg.AllowServerAdd != newCfg.AllowServerAdd {
		result.ChangedFields = append(result.ChangedFields, "allow_server_add")
	}
	if oldCfg.AllowServerRemove != newCfg.AllowServerRemove {
		result.ChangedFields = append(result.ChangedFields, "allow_server_remove")
	}

	// Environment configuration (can be hot-reloaded)
	if !reflect.DeepEqual(oldCfg.Environment, newCfg.Environment) {
		result.ChangedFields = append(result.ChangedFields, "environment")
	}

	// Observability cadence (Spec 069 A2 — can be hot-reloaded; the usage flush
	// loop re-reads the interval each cycle, so applying it is just a setter).
	if !reflect.DeepEqual(oldCfg.Observability, newCfg.Observability) {
		result.ChangedFields = append(result.ChangedFields, "observability")
	}

	// Security scanner settings, incl. the opt-in deep-scan layer (Spec 077 US3
	// — hot-reloadable). The scanner service is (re)configured from cfg.Security
	// on the config.reloaded event, so a lone security.deep_scan.* edit MUST be
	// reported as a change (not "No configuration changes detected") and drive
	// that re-apply — otherwise toggling deep scan via config edit / API apply
	// only takes effect on restart. Deep compare covers deep_scan.{enabled,
	// fetch_package_source,disable_no_new_privileges,scanners} plus the deprecated
	// top-level scanner_* keys.
	if !reflect.DeepEqual(oldCfg.Security, newCfg.Security) {
		result.ChangedFields = append(result.ChangedFields, "security")
	}

	// Update-check settings (Spec 079 FR-012 — hot-reloadable). ApplyConfig
	// re-gates the running updatecheck.Checker when this field is reported,
	// so an update_check.{enabled,channel} edit takes effect without a
	// restart (and is not swallowed as "No configuration changes detected").
	if !reflect.DeepEqual(oldCfg.UpdateCheck, newCfg.UpdateCheck) {
		result.ChangedFields = append(result.ChangedFields, "update_check")
	}

	// If no changes detected
	if len(result.ChangedFields) == 0 {
		result.AppliedImmediately = false
		result.RestartReason = "No configuration changes detected"
	}

	return result
}

// FormatChangedFields returns a human-readable string of changed fields
func (r *ConfigApplyResult) FormatChangedFields() string {
	if len(r.ChangedFields) == 0 {
		return "none"
	}
	if len(r.ChangedFields) == 1 {
		return r.ChangedFields[0]
	}
	if len(r.ChangedFields) == 2 {
		return fmt.Sprintf("%s and %s", r.ChangedFields[0], r.ChangedFields[1])
	}
	// For 3+ fields, show "field1, field2, and N others"
	return fmt.Sprintf("%s, %s, and %d others", r.ChangedFields[0], r.ChangedFields[1], len(r.ChangedFields)-2)
}
