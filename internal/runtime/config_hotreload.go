package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
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

// changedHTTPTimeoutFields returns the names of the HTTP server deadline keys
// whose RESOLVED value differs between the two configs (GH #965). Resolving
// first is what makes `nil` and an explicitly-written built-in default compare
// equal, so a config rewrite that materializes the defaults does not report a
// restart-required change.
func changedHTTPTimeoutFields(oldCfg, newCfg *config.Config) []string {
	var changed []string
	if oldCfg.ResolveHTTPReadTimeout() != newCfg.ResolveHTTPReadTimeout() {
		changed = append(changed, "http_read_timeout")
	}
	if oldCfg.ResolveHTTPWriteTimeout() != newCfg.ResolveHTTPWriteTimeout() {
		changed = append(changed, "http_write_timeout")
	}
	if oldCfg.ResolveHTTPIdleTimeout() != newCfg.ResolveHTTPIdleTimeout() {
		changed = append(changed, "http_idle_timeout")
	}
	return changed
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
			oldCfg.TLS.CertsDir != newCfg.TLS.CertsDir ||
			// HSTS wraps the handler chain once, where the HTTP server is built
			// (server.go withHSTS), so it cannot apply hot. Settings already
			// badges tls.hsts as restart-required; this makes the backend agree
			// instead of silently reporting the edit as applied (UX audit F16).
			oldCfg.TLS.HSTS != newCfg.TLS.HSTS {
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

	// 5. HTTP server request deadlines (GH #965). ReadTimeout/WriteTimeout/
	// IdleTimeout are baked into http.Server when it is constructed in
	// startCustomHTTPServer, so changing one only takes effect on a rebind.
	// Compare the RESOLVED values, not the pointers: writing a key out at its
	// built-in default ("120s" read / "120s" write / "180s" idle) is not a
	// real change and must not force a pointless restart.
	if httpTimeoutFields := changedHTTPTimeoutFields(oldCfg, newCfg); len(httpTimeoutFields) > 0 {
		result.ChangedFields = append(result.ChangedFields, httpTimeoutFields...)
		result.RequiresRestart = true
		result.AppliedImmediately = false
		result.RestartReason = "HTTP server timeouts changed - requires restart"
		return result
	}

	// Track hot-reloadable changes

	// Server configuration changes (can be hot-reloaded).
	//
	// Compared via jsonEqual, not reflect.DeepEqual, for the same reason as
	// docker_isolation and trusted_hosts below: PATCH /api/v1/config round-trips
	// the LIVE config through JSON before merging the patch, so newCfg.Servers is
	// a decoded copy while oldCfg.Servers is the in-memory slice. DeepEqual saw
	// those as different for every patch — `Created`/`Updated` carry a monotonic
	// reading and a *time.Location in memory but not after an RFC3339 decode, and
	// `omitempty` collapses empty Args/Env/Headers to nil. That mislabelled
	// changed_fields as ["mcpServers"] on unrelated edits (UX audit F16) AND
	// triggered a spurious full upstream reload/reconnect on every settings save.
	// The length guard keeps nil vs []*ServerConfig{} (`null` vs `[]`) from
	// re-introducing the same false positive when there are no servers at all.
	if len(oldCfg.Servers) != len(newCfg.Servers) ||
		(len(oldCfg.Servers) > 0 && !jsonEqual(oldCfg.Servers, newCfg.Servers)) {
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

	// TOON output (spec 084, FR-001 — hot-reloadable). The call_tool_* encoder
	// seam reads ToonOutput/ToonMinSavingsPct fresh on every call (same pattern
	// as output sanitisation), so applying the change is free; these entries
	// exist so a lone toon edit is acknowledged instead of being reported as
	// "no changes detected". Per-server toon_output overrides are already
	// covered by the Servers DeepEqual above.
	if oldCfg.ToonOutput != newCfg.ToonOutput {
		result.ChangedFields = append(result.ChangedFields, "toon_output")
	}
	if oldCfg.ToonMinSavingsPct != newCfg.ToonMinSavingsPct {
		result.ChangedFields = append(result.ChangedFields, "toon_min_savings_pct")
	}

	// Tool response mode (Spec 085 FR-015 — hot-reloadable, serialization
	// only). Without this clause an API apply that changes only this field
	// computes empty ChangedFields and is swallowed as "no changes detected".
	// The retrieve path reads the live snapshot (p.currentConfig()), so
	// reporting the change is all the propagation needed.
	if oldCfg.ToolResponseMode != newCfg.ToolResponseMode {
		result.ChangedFields = append(result.ChangedFields, "tool_response_mode")
	}

	// Direct-surface serialization (Spec 102 FR-014 — hot-reloadable,
	// serialization only). A SEPARATE clause from the one above, not a shared
	// one: the two axes govern different surfaces, so folding them together
	// would make an operator's edit to retrieve_tools rebuild the direct
	// listing and notify every connected client for nothing.
	//
	// Unlike the retrieve axis, reporting the change is NOT all the propagation
	// needed: the direct listing is registered state, not rendered per request,
	// so listenForRoutingModeRefresh's config.reloaded branch has to rebuild it.
	// This clause is what makes that branch reachable at all — without it the
	// apply computes empty ChangedFields and is swallowed as "no changes
	// detected".
	if oldCfg.DirectToolResponseMode != newCfg.DirectToolResponseMode {
		result.ChangedFields = append(result.ChangedFields, "direct_tool_response_mode")
	}

	// Upstream prompt aggregation (PR #973 — hot-reloadable, opt-in). Without
	// this clause a lone aggregate_upstream_prompts toggle computes empty
	// ChangedFields and is swallowed as "no changes detected", so ApplyConfig
	// never emits config.reloaded and RefreshPrompts (which reads the live
	// snapshot) never re-runs. EnablePrompts is deliberately absent: it gates
	// the prompts CAPABILITY, baked into each server at construction, so
	// toggling it is a restart concern.
	if oldCfg.AggregateUpstreamPrompts != newCfg.AggregateUpstreamPrompts {
		result.ChangedFields = append(result.ChangedFields, "aggregate_upstream_prompts")
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

	// Concurrency limits (spec 093 / GH #955 — hot-reloadable, FR-021). The
	// limiter registry re-publishes one generation from the new snapshot on
	// apply; occupancy is shared across generations, so running calls are never
	// interrupted. These clauses cover the GLOBAL aggregate limiter and the
	// per-server default set — per-server overrides are already covered by the
	// Servers DeepEqual above. Without them a lone limit edit computes empty
	// ChangedFields and is swallowed as "no changes detected".
	if !reflect.DeepEqual(oldCfg.MaxConcurrentRequests, newCfg.MaxConcurrentRequests) {
		result.ChangedFields = append(result.ChangedFields, "max_concurrent_requests")
	}
	if !reflect.DeepEqual(oldCfg.QueueSize, newCfg.QueueSize) {
		result.ChangedFields = append(result.ChangedFields, "queue_size")
	}
	if !reflect.DeepEqual(oldCfg.QueueTimeout, newCfg.QueueTimeout) {
		result.ChangedFields = append(result.ChangedFields, "queue_timeout")
	}
	if !reflect.DeepEqual(oldCfg.ServerConcurrencyDefaults, newCfg.ServerConcurrencyDefaults) {
		result.ChangedFields = append(result.ChangedFields, "server_concurrency_defaults")
	}

	// Code execution settings (Spec 096 FR-004 — hot-reloadable). The
	// code_execution handler resolves timeout / max_tool_calls /
	// max_parallel from the LIVE snapshot (p.currentConfig()) on every
	// execution, so a lone edit here must be reported as a change instead of
	// being swallowed as "no changes detected".
	if oldCfg.CodeExecutionTimeoutMs != newCfg.CodeExecutionTimeoutMs ||
		oldCfg.CodeExecutionMaxToolCalls != newCfg.CodeExecutionMaxToolCalls ||
		oldCfg.CodeExecutionMaxParallel != newCfg.CodeExecutionMaxParallel {
		result.ChangedFields = append(result.ChangedFields, "code_execution")
	}

	// enable_code_execution (UX audit F16 — hot-reloadable). Toggling this
	// changes the advertised tool set, which used to make it a restart concern:
	// the routing-mode surfaces built their code_execution entry (live tool or
	// "disabled" stub) once, from the construction-time config snapshot. Both
	// now build from the LIVE snapshot and are rebuilt on config.reloaded
	// (server.listenForRoutingModeRefresh), and the handler gate already read
	// the live value per call — so the flag applies without a restart and must
	// be reported here, or ApplyConfig swallows a lone toggle as "no changes
	// detected" and never emits config.reloaded.
	if oldCfg.EnableCodeExecution != newCfg.EnableCodeExecution {
		result.ChangedFields = append(result.ChangedFields, "enable_code_execution")
	}

	// The JS runtime pool is sized once at server construction and never
	// resized in-process, so unlike its siblings pool_size cannot apply hot.
	if oldCfg.CodeExecutionPoolSize != newCfg.CodeExecutionPoolSize {
		result.ChangedFields = append(result.ChangedFields, "code_execution_pool_size")
		result.RequiresRestart = true
		if result.RestartReason == "" {
			result.RestartReason = "code_execution_pool_size is sized at startup - requires restart"
		}
	}

	// Logging configuration (can be hot-reloaded)
	if !reflect.DeepEqual(oldCfg.Logging, newCfg.Logging) {
		result.ChangedFields = append(result.ChangedFields, "logging")
	}

	// Docker isolation configuration (can be hot-reloaded for new servers).
	// Compared via jsonEqual, not reflect.DeepEqual: the PATCH /api/v1/config
	// round-trip collapses ExtraArgs []string{} to nil (omitempty), which
	// DeepEqual would spuriously report as a change.
	if !jsonEqual(oldCfg.DockerIsolation, newCfg.DockerIsolation) {
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
	// trusted_hosts (GH #898 — hot-reloadable). hostValidationMiddleware reads
	// the live snapshot per request, so reporting the change is all the
	// propagation needed. slices.Equal, not DeepEqual: the PATCH round-trip
	// collapses []string{} to nil (omitempty), which DeepEqual would
	// spuriously report as a change.
	if !slices.Equal(oldCfg.TrustedHosts, newCfg.TrustedHosts) {
		result.ChangedFields = append(result.ChangedFields, "trusted_hosts")
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

// jsonEqual reports whether a and b marshal to identical JSON. Config
// sub-structs round-trip through JSON on the PATCH /api/v1/config path,
// where `omitempty` collapses empty slices to absent keys; comparing the
// JSON form treats nil and []T{} as the same effective value, unlike
// reflect.DeepEqual (which spuriously reported "docker_isolation" as
// changed because DefaultDockerIsolationConfig's ExtraArgs: []string{}
// round-trips to nil). json.Marshal sorts map keys, so the comparison is
// deterministic.
func jsonEqual(a, b interface{}) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false // conservative: unmarshalable => treat as changed
	}
	return bytes.Equal(ab, bb)
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
