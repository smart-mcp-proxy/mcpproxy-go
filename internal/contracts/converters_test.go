package contracts

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// telemetryEnabledFromContract marshals the contract representation of cfg and
// extracts the resolved telemetry.enabled value (or reports it absent).
func telemetryEnabledFromContract(t *testing.T, cfg *config.Config) (enabled, present bool) {
	t.Helper()
	raw, err := json.Marshal(ConvertConfigToContract(cfg))
	require.NoError(t, err)

	var decoded struct {
		Telemetry *struct {
			Enabled *bool `json:"enabled"`
		} `json:"telemetry"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	if decoded.Telemetry == nil || decoded.Telemetry.Enabled == nil {
		return false, false
	}
	return *decoded.Telemetry.Enabled, true
}

// TestConvertConfigToContract_TelemetryMaterializedOnFreshDefault asserts that
// /api/v1/config exposes the resolved telemetry.enabled value (true) on a fresh
// install where Telemetry is nil, instead of omitting the key (which clients
// coerce to false). Regression test for MCP-2477.
func TestConvertConfigToContract_TelemetryMaterializedOnFreshDefault(t *testing.T) {
	t.Setenv("MCPPROXY_TELEMETRY", "") // ensure no env override
	cfg := config.DefaultConfig()
	require.Nil(t, cfg.Telemetry, "precondition: DefaultConfig leaves Telemetry nil")

	enabled, present := telemetryEnabledFromContract(t, cfg)
	assert.True(t, present, "telemetry.enabled must be present in the API response")
	assert.True(t, enabled, "telemetry.enabled must serialize as true on a fresh default install")

	// Materialization must not mutate the shared config.
	assert.Nil(t, cfg.Telemetry, "ConvertConfigToContract must not mutate the source config")
}

// TestConvertConfigToContract_TelemetryEnabledNilMaterialized covers the case
// where a TelemetryConfig exists but Enabled is nil — it must resolve to true.
func TestConvertConfigToContract_TelemetryEnabledNilMaterialized(t *testing.T) {
	t.Setenv("MCPPROXY_TELEMETRY", "")
	cfg := config.DefaultConfig()
	cfg.Telemetry = &config.TelemetryConfig{AnonymousID: "abc"} // Enabled nil

	enabled, present := telemetryEnabledFromContract(t, cfg)
	assert.True(t, present, "telemetry.enabled must be present when Enabled is nil")
	assert.True(t, enabled, "telemetry.enabled must resolve to true when Enabled is nil")
	assert.Nil(t, cfg.Telemetry.Enabled, "source TelemetryConfig.Enabled must remain nil")
}

// TestConvertConfigToContract_TelemetryExplicitFalsePreserved asserts an
// explicit opt-out is faithfully serialized (not overwritten by the default).
func TestConvertConfigToContract_TelemetryExplicitFalsePreserved(t *testing.T) {
	t.Setenv("MCPPROXY_TELEMETRY", "")
	disabled := false
	cfg := config.DefaultConfig()
	cfg.Telemetry = &config.TelemetryConfig{Enabled: &disabled}

	enabled, present := telemetryEnabledFromContract(t, cfg)
	assert.True(t, present, "telemetry.enabled must be present when explicitly set")
	assert.False(t, enabled, "explicit telemetry opt-out must be preserved")
}

// TestConvertGenericServersToTyped_OAuth verifies OAuth config is properly extracted
func TestConvertGenericServersToTyped_OAuth(t *testing.T) {
	// Simulate the map structure returned from the management service
	genericServers := []map[string]interface{}{
		{
			"id":            "sentry",
			"name":          "sentry",
			"url":           "https://mcp.sentry.dev/mcp",
			"protocol":      "http",
			"enabled":       true,
			"quarantined":   false,
			"connected":     false,
			"status":        "connecting",
			"authenticated": false,
			"tool_count":    0,
			"created":       time.Date(2025, 11, 29, 15, 49, 25, 0, time.UTC),
			"updated":       time.Date(2025, 11, 29, 15, 49, 25, 0, time.UTC),
			"oauth": map[string]interface{}{
				"auth_url":  "https://mcp.sentry.dev/oauth/authorize",
				"token_url": "https://mcp.sentry.dev/oauth/token",
				"client_id": "test-client-id",
				"scopes":    []interface{}{"read", "write"},
				"extra_params": map[string]interface{}{
					"resource": "https://mcp.sentry.dev/mcp",
					"audience": "sentry-api",
				},
				"redirect_port": 8080,
			},
			"last_error": "OAuth authorization required",
		},
	}

	servers := ConvertGenericServersToTyped(genericServers)

	require.Len(t, servers, 1, "Should convert exactly one server")

	server := servers[0]
	assert.Equal(t, "sentry", server.Name)
	assert.Equal(t, "https://mcp.sentry.dev/mcp", server.URL)
	assert.Equal(t, false, server.Authenticated)

	// The critical assertions: OAuth config should be extracted
	require.NotNil(t, server.OAuth, "OAuth config should not be nil")
	assert.Equal(t, "https://mcp.sentry.dev/oauth/authorize", server.OAuth.AuthURL)
	assert.Equal(t, "https://mcp.sentry.dev/oauth/token", server.OAuth.TokenURL)
	assert.Equal(t, "test-client-id", server.OAuth.ClientID)
	assert.Equal(t, []string{"read", "write"}, server.OAuth.Scopes)
	assert.Equal(t, 8080, server.OAuth.RedirectPort)

	require.NotNil(t, server.OAuth.ExtraParams, "ExtraParams should not be nil")
	assert.Equal(t, "https://mcp.sentry.dev/mcp", server.OAuth.ExtraParams["resource"])
	assert.Equal(t, "sentry-api", server.OAuth.ExtraParams["audience"])
}

// TestConvertGenericServersToTyped_EmptyOAuth verifies empty OAuth config creates non-nil OAuth struct
func TestConvertGenericServersToTyped_EmptyOAuth(t *testing.T) {
	genericServers := []map[string]interface{}{
		{
			"id":            "test-server",
			"name":          "test-server",
			"enabled":       true,
			"connected":     false,
			"authenticated": false,
			"tool_count":    0,
			"oauth":         map[string]interface{}{}, // Empty OAuth config
		},
	}

	servers := ConvertGenericServersToTyped(genericServers)

	require.Len(t, servers, 1)
	require.NotNil(t, servers[0].OAuth, "Even empty oauth map should create non-nil OAuth config")
	assert.Empty(t, servers[0].OAuth.AuthURL)
	assert.Empty(t, servers[0].OAuth.ClientID)
}

// TestConvertGenericServersToTyped_SourceRegistry verifies registry provenance
// (MCP-901) is carried through the generic-map fallback projection so the
// approval/quarantine view can show a server's origin.
func TestConvertGenericServersToTyped_SourceRegistry(t *testing.T) {
	genericServers := []map[string]interface{}{
		{
			"id":                         "everything",
			"name":                       "everything",
			"enabled":                    true,
			"source_registry_id":         "modelcontextprotocol",
			"source_registry_provenance": "custom",
		},
		{
			// Manually-configured server: both fields absent → empty.
			"id":      "manual",
			"name":    "manual",
			"enabled": true,
		},
	}

	servers := ConvertGenericServersToTyped(genericServers)
	require.Len(t, servers, 2)

	assert.Equal(t, "modelcontextprotocol", servers[0].SourceRegistryID)
	assert.Equal(t, "custom", servers[0].SourceRegistryProvenance)

	assert.Empty(t, servers[1].SourceRegistryID, "manual server carries no registry id")
	assert.Empty(t, servers[1].SourceRegistryProvenance)
}

// TestConvertServerConfig_SourceRegistry verifies the direct config→contracts
// mapper populates registry provenance (MCP-901).
func TestConvertServerConfig_SourceRegistry(t *testing.T) {
	cfg := &config.ServerConfig{
		Name:                     "everything",
		Protocol:                 "stdio",
		Enabled:                  true,
		SourceRegistryID:         "modelcontextprotocol",
		SourceRegistryProvenance: config.RegistryProvenanceCustom,
	}

	server := ConvertServerConfig(cfg, "ready", true, 3, false)
	require.NotNil(t, server)
	assert.Equal(t, "modelcontextprotocol", server.SourceRegistryID)
	assert.Equal(t, config.RegistryProvenanceCustom, server.SourceRegistryProvenance)

	// Manual server (no source registry) leaves both empty.
	manual := ConvertServerConfig(&config.ServerConfig{Name: "manual", Enabled: true}, "ready", true, 0, false)
	assert.Empty(t, manual.SourceRegistryID)
	assert.Empty(t, manual.SourceRegistryProvenance)
}

// TestConvertGenericServersToTyped_AutoApproveToolChanges verifies the
// per-server auto_approve_tool_changes flag (MCP-2940) survives the
// generic-map fallback projection so the Web UI toggle can read it. A server
// that never set the flag must leave the pointer nil (tri-state), not coerce
// to false.
func TestConvertGenericServersToTyped_AutoApproveToolChanges(t *testing.T) {
	genericServers := []map[string]interface{}{
		{"id": "on", "name": "on", "enabled": true, "auto_approve_tool_changes": true},
		{"id": "off", "name": "off", "enabled": true, "auto_approve_tool_changes": false},
		{"id": "unset", "name": "unset", "enabled": true},
	}

	servers := ConvertGenericServersToTyped(genericServers)
	require.Len(t, servers, 3)

	require.NotNil(t, servers[0].AutoApproveToolChanges)
	assert.True(t, *servers[0].AutoApproveToolChanges)

	require.NotNil(t, servers[1].AutoApproveToolChanges)
	assert.False(t, *servers[1].AutoApproveToolChanges)

	assert.Nil(t, servers[2].AutoApproveToolChanges, "unset flag must stay nil, not coerce to false")
}

// TestConvertServerConfig_AutoApproveToolChanges verifies the direct
// config→contracts mapper carries the auto_approve_tool_changes pointer.
func TestConvertServerConfig_AutoApproveToolChanges(t *testing.T) {
	on := true
	cfg := &config.ServerConfig{Name: "on", Enabled: true, AutoApproveToolChanges: &on}
	server := ConvertServerConfig(cfg, "ready", true, 0, false)
	require.NotNil(t, server.AutoApproveToolChanges)
	assert.True(t, *server.AutoApproveToolChanges)

	unset := ConvertServerConfig(&config.ServerConfig{Name: "unset", Enabled: true}, "ready", true, 0, false)
	assert.Nil(t, unset.AutoApproveToolChanges)
}

// TestConvertGenericServersToTyped_NoOAuth verifies servers without OAuth have nil OAuth field
func TestConvertGenericServersToTyped_NoOAuth(t *testing.T) {
	genericServers := []map[string]interface{}{
		{
			"id":            "test-server",
			"name":          "test-server",
			"enabled":       true,
			"connected":     true,
			"authenticated": false,
			"tool_count":    5,
			// No oauth field at all
		},
	}

	servers := ConvertGenericServersToTyped(genericServers)

	require.Len(t, servers, 1)
	assert.Nil(t, servers[0].OAuth, "Servers without OAuth config should have nil OAuth field")
}

// TestConvertGenericToolsToTyped_PreservesInputSchema asserts the management
// tool-list conversion keeps the upstream input schema. Every producer of the
// generic tool map emits the schema under the "inputSchema" key (e.g.
// internal/runtime/runtime.go:2141 GetServerTools, internal/server/server.go:2367),
// so a converter that only reads "schema" silently drops every schema on the
// /api/v1/tools response. Regression guard for MCP-3132/MCP-3167: without real
// schemas the live benchmark baseline is no longer a full-schema token count.
func TestConvertGenericToolsToTyped_PreservesInputSchema(t *testing.T) {
	inputSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{"type": "string"},
		},
	}
	generic := []map[string]interface{}{
		{
			"name":        "read_file",
			"server_name": "fs",
			"description": "Read a file",
			"inputSchema": inputSchema, // key the runtime/server map builders actually emit
		},
	}

	typed := ConvertGenericToolsToTyped(generic)

	require.Len(t, typed, 1)
	assert.Equal(t, inputSchema, typed[0].Schema, "input schema must survive conversion to the /api/v1/tools response")
}

// TestConvertGenericToolsToTyped_SchemaLegacyFallback keeps the historical
// "schema" key working so any in-process caller that still emits it is not
// regressed by the inputSchema fix.
func TestConvertGenericToolsToTyped_SchemaLegacyFallback(t *testing.T) {
	schema := map[string]interface{}{"type": "object"}
	generic := []map[string]interface{}{
		{"name": "t", "server_name": "s", "description": "d", "schema": schema},
	}

	typed := ConvertGenericToolsToTyped(generic)

	require.Len(t, typed, 1)
	assert.Equal(t, schema, typed[0].Schema, "legacy schema key must still be honored")
}
