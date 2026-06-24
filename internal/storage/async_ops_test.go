package storage

import (
	"os"
	"reflect"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestSaveServerSyncPreservesAllFields verifies that saveServerSync copies all ServerConfig fields.
// This test guards against regression where new fields are added to ServerConfig but not copied.
// Related issues: #239, #240
func TestSaveServerSyncPreservesAllFields(t *testing.T) {
	// Create a temp database using the Manager pattern
	tmpDir, err := os.MkdirTemp("", "async_ops_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := zaptest.NewLogger(t).Sugar()
	manager, err := NewManager(tmpDir, logger)
	if err != nil {
		t.Fatalf("Failed to create storage manager: %v", err)
	}
	defer manager.Close()

	// Access the BoltDB directly for async manager testing
	am := NewAsyncManager(manager.db, logger)
	am.Start()
	defer am.Stop()

	// Create a ServerConfig with ALL fields populated
	created := time.Now().Add(-24 * time.Hour)
	serverConfig := &config.ServerConfig{
		Name:        "test-server",
		URL:         "https://example.com/mcp",
		Protocol:    "http",
		Command:     "npx",
		Args:        []string{"--verbose", "--config", "/path/to/config"},
		WorkingDir:  "/tmp/workdir",
		Env:         map[string]string{"API_KEY": "secret", "DEBUG": "true"},
		Headers:     map[string]string{"Authorization": "Bearer token", "X-Custom": "value"},
		Enabled:     true,
		Quarantined: false,
		Created:     created,
		Updated:     time.Now(),
		Isolation: &config.IsolationConfig{
			Enabled:     config.BoolPtr(true),
			Image:       "python:3.11",
			NetworkMode: "bridge",
			ExtraArgs:   []string{"-v", "/host:/container"},
			WorkingDir:  "/app",
			LogDriver:   "json-file",
			LogMaxSize:  "100m",
			LogMaxFiles: "3",
		},
		OAuth: &config.OAuthConfig{
			ClientID:     "test-client-id",
			ClientSecret: "test-client-secret",
			RedirectURI:  "http://localhost:8080/callback",
			Scopes:       []string{"read", "write"},
			PKCEEnabled:  true,
			ExtraParams:  map[string]string{"audience": "api.example.com"},
		},
	}

	// Save the server
	err = am.saveServerSync(serverConfig)
	if err != nil {
		t.Fatalf("Failed to save server: %v", err)
	}

	// Retrieve the server from storage
	record, err := manager.db.GetUpstream(serverConfig.Name)
	if err != nil {
		t.Fatalf("Failed to retrieve server: %v", err)
	}

	// Verify all fields are preserved
	if record.Name != serverConfig.Name {
		t.Errorf("Name mismatch: got %s, want %s", record.Name, serverConfig.Name)
	}
	if record.URL != serverConfig.URL {
		t.Errorf("URL mismatch: got %s, want %s", record.URL, serverConfig.URL)
	}
	if record.Protocol != serverConfig.Protocol {
		t.Errorf("Protocol mismatch: got %s, want %s", record.Protocol, serverConfig.Protocol)
	}
	if record.Command != serverConfig.Command {
		t.Errorf("Command mismatch: got %s, want %s", record.Command, serverConfig.Command)
	}
	if !reflect.DeepEqual(record.Args, serverConfig.Args) {
		t.Errorf("Args mismatch: got %v, want %v", record.Args, serverConfig.Args)
	}
	if record.WorkingDir != serverConfig.WorkingDir {
		t.Errorf("WorkingDir mismatch: got %s, want %s", record.WorkingDir, serverConfig.WorkingDir)
	}
	if !reflect.DeepEqual(record.Env, serverConfig.Env) {
		t.Errorf("Env mismatch: got %v, want %v", record.Env, serverConfig.Env)
	}
	if !reflect.DeepEqual(record.Headers, serverConfig.Headers) {
		t.Errorf("Headers mismatch: got %v, want %v", record.Headers, serverConfig.Headers)
	}
	if record.Enabled != serverConfig.Enabled {
		t.Errorf("Enabled mismatch: got %v, want %v", record.Enabled, serverConfig.Enabled)
	}
	if record.Quarantined != serverConfig.Quarantined {
		t.Errorf("Quarantined mismatch: got %v, want %v", record.Quarantined, serverConfig.Quarantined)
	}

	// Verify Isolation config is preserved (critical - issue #239, #240)
	if record.Isolation == nil {
		t.Fatal("Isolation config is nil - data loss detected!")
	}
	if record.Isolation.IsEnabled() != serverConfig.Isolation.IsEnabled() {
		t.Errorf("Isolation.Enabled mismatch: got %v, want %v", record.Isolation.IsEnabled(), serverConfig.Isolation.IsEnabled())
	}
	if record.Isolation.Image != serverConfig.Isolation.Image {
		t.Errorf("Isolation.Image mismatch: got %s, want %s", record.Isolation.Image, serverConfig.Isolation.Image)
	}
	if record.Isolation.NetworkMode != serverConfig.Isolation.NetworkMode {
		t.Errorf("Isolation.NetworkMode mismatch: got %s, want %s", record.Isolation.NetworkMode, serverConfig.Isolation.NetworkMode)
	}
	if !reflect.DeepEqual(record.Isolation.ExtraArgs, serverConfig.Isolation.ExtraArgs) {
		t.Errorf("Isolation.ExtraArgs mismatch: got %v, want %v", record.Isolation.ExtraArgs, serverConfig.Isolation.ExtraArgs)
	}
	if record.Isolation.WorkingDir != serverConfig.Isolation.WorkingDir {
		t.Errorf("Isolation.WorkingDir mismatch: got %s, want %s", record.Isolation.WorkingDir, serverConfig.Isolation.WorkingDir)
	}
	if record.Isolation.LogDriver != serverConfig.Isolation.LogDriver {
		t.Errorf("Isolation.LogDriver mismatch: got %s, want %s", record.Isolation.LogDriver, serverConfig.Isolation.LogDriver)
	}
	if record.Isolation.LogMaxSize != serverConfig.Isolation.LogMaxSize {
		t.Errorf("Isolation.LogMaxSize mismatch: got %s, want %s", record.Isolation.LogMaxSize, serverConfig.Isolation.LogMaxSize)
	}
	if record.Isolation.LogMaxFiles != serverConfig.Isolation.LogMaxFiles {
		t.Errorf("Isolation.LogMaxFiles mismatch: got %s, want %s", record.Isolation.LogMaxFiles, serverConfig.Isolation.LogMaxFiles)
	}

	// Verify OAuth config is preserved
	if record.OAuth == nil {
		t.Fatal("OAuth config is nil - data loss detected!")
	}
	if record.OAuth.ClientID != serverConfig.OAuth.ClientID {
		t.Errorf("OAuth.ClientID mismatch: got %s, want %s", record.OAuth.ClientID, serverConfig.OAuth.ClientID)
	}
	if record.OAuth.ClientSecret != serverConfig.OAuth.ClientSecret {
		t.Errorf("OAuth.ClientSecret mismatch: got %s, want %s", record.OAuth.ClientSecret, serverConfig.OAuth.ClientSecret)
	}
	if record.OAuth.RedirectURI != serverConfig.OAuth.RedirectURI {
		t.Errorf("OAuth.RedirectURI mismatch: got %s, want %s", record.OAuth.RedirectURI, serverConfig.OAuth.RedirectURI)
	}
	if !reflect.DeepEqual(record.OAuth.Scopes, serverConfig.OAuth.Scopes) {
		t.Errorf("OAuth.Scopes mismatch: got %v, want %v", record.OAuth.Scopes, serverConfig.OAuth.Scopes)
	}
	if record.OAuth.PKCEEnabled != serverConfig.OAuth.PKCEEnabled {
		t.Errorf("OAuth.PKCEEnabled mismatch: got %v, want %v", record.OAuth.PKCEEnabled, serverConfig.OAuth.PKCEEnabled)
	}
	if !reflect.DeepEqual(record.OAuth.ExtraParams, serverConfig.OAuth.ExtraParams) {
		t.Errorf("OAuth.ExtraParams mismatch: got %v, want %v", record.OAuth.ExtraParams, serverConfig.OAuth.ExtraParams)
	}

	t.Log("All ServerConfig fields are correctly preserved in saveServerSync")
}

// TestAutoApproveToolChangesRoundTrip verifies the per-server
// auto_approve_tool_changes flag (MCP-2940) survives a Save → Get / List
// cycle through BBolt. This is the persistence half of the feature: without
// it, SaveConfiguration (which rebuilds the JSON config from these records)
// would wipe a REST/UI-set toggle on the next mutation. Tri-state *bool — an
// unset flag must stay nil, an explicit false must round-trip as false.
func TestAutoApproveToolChangesRoundTrip(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "async_ops_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := zaptest.NewLogger(t).Sugar()
	manager, err := NewManager(tmpDir, logger)
	if err != nil {
		t.Fatalf("Failed to create storage manager: %v", err)
	}
	defer manager.Close()

	boolPtr := func(b bool) *bool { return &b }
	cases := []struct {
		name string
		flag *bool
	}{
		{"auto-on", boolPtr(true)},
		{"auto-off", boolPtr(false)},
		{"unset", nil},
	}

	for _, tc := range cases {
		sc := &config.ServerConfig{
			Name:                   tc.name,
			URL:                    "https://example.com/mcp",
			Protocol:               "http",
			Enabled:                true,
			Created:                time.Now(),
			AutoApproveToolChanges: tc.flag,
		}
		if err := manager.SaveUpstreamServer(sc); err != nil {
			t.Fatalf("[%s] SaveUpstreamServer: %v", tc.name, err)
		}
	}

	for _, tc := range cases {
		got, err := manager.GetUpstreamServer(tc.name)
		if err != nil {
			t.Fatalf("[%s] GetUpstreamServer: %v", tc.name, err)
		}
		if !reflect.DeepEqual(got.AutoApproveToolChanges, tc.flag) {
			t.Errorf("[%s] Get: AutoApproveToolChanges = %v, want %v",
				tc.name, derefBool(got.AutoApproveToolChanges), derefBool(tc.flag))
		}
	}

	listed, err := manager.ListUpstreamServers()
	if err != nil {
		t.Fatalf("ListUpstreamServers: %v", err)
	}
	byName := map[string]*config.ServerConfig{}
	for _, s := range listed {
		byName[s.Name] = s
	}
	for _, tc := range cases {
		s, ok := byName[tc.name]
		if !ok {
			t.Fatalf("[%s] missing from ListUpstreamServers", tc.name)
		}
		if !reflect.DeepEqual(s.AutoApproveToolChanges, tc.flag) {
			t.Errorf("[%s] List: AutoApproveToolChanges = %v, want %v",
				tc.name, derefBool(s.AutoApproveToolChanges), derefBool(tc.flag))
		}
	}
}

func derefBool(b *bool) interface{} {
	if b == nil {
		return nil
	}
	return *b
}

// TestSaveServerSyncPreservesNilFields verifies that nil nested configs remain nil after save.
func TestSaveServerSyncPreservesNilFields(t *testing.T) {
	// Create a temp database using the Manager pattern
	tmpDir, err := os.MkdirTemp("", "async_ops_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := zaptest.NewLogger(t).Sugar()
	manager, err := NewManager(tmpDir, logger)
	if err != nil {
		t.Fatalf("Failed to create storage manager: %v", err)
	}
	defer manager.Close()

	// Access the BoltDB directly for async manager testing
	am := NewAsyncManager(manager.db, logger)
	am.Start()
	defer am.Stop()

	// Create a minimal ServerConfig with nil nested configs
	serverConfig := &config.ServerConfig{
		Name:      "minimal-server",
		URL:       "https://example.com/mcp",
		Protocol:  "http",
		Enabled:   true,
		Created:   time.Now(),
		Isolation: nil, // Explicitly nil
		OAuth:     nil, // Explicitly nil
	}

	// Save the server
	err = am.saveServerSync(serverConfig)
	if err != nil {
		t.Fatalf("Failed to save server: %v", err)
	}

	// Retrieve the server from storage
	record, err := manager.db.GetUpstream(serverConfig.Name)
	if err != nil {
		t.Fatalf("Failed to retrieve server: %v", err)
	}

	// Verify nil fields remain nil (not empty structs)
	if record.Isolation != nil {
		t.Errorf("Isolation should be nil, got %+v", record.Isolation)
	}
	if record.OAuth != nil {
		t.Errorf("OAuth should be nil, got %+v", record.OAuth)
	}

	t.Log("Nil nested configs are correctly preserved")
}

// TestSaveServerSyncFieldCoverage uses reflection to verify all ServerConfig fields
// are handled in the conversion to UpstreamRecord.
func TestSaveServerSyncFieldCoverage(t *testing.T) {
	// List of ServerConfig fields that ARE expected to be copied
	expectedFields := map[string]bool{
		"Name":           true,
		"URL":            true,
		"Protocol":       true,
		"Command":        true,
		"Args":           true,
		"WorkingDir":     true,
		"Env":            true,
		"Headers":        true,
		"OAuth":          true,
		"Enabled":        true,
		"Quarantined":    true,
		"Created":        true,
		"Updated":        true, // Updated is set by saveServerSync, not copied
		"Isolation":      true,
		"Shared":         true, // Server-edition-only: persisted in JSON config, not in BBolt
		"SkipQuarantine": true, // Spec 032: runtime-only field, not persisted to BBolt
		// MCP-2930/MCP-2940: successor to SkipQuarantine; persisted to BBolt
		// because SaveConfiguration rebuilds the JSON config's server list from
		// these records — without it the REST/UI toggle would be wiped on save.
		"AutoApproveToolChanges": true,
		"ReconnectOnUse":         true, // Spec 354: persisted to BBolt for on-demand reconnection
		"LauncherWaitTimeout":    true, // Spec 046: persisted to BBolt so REST-API-added launcher servers survive restarts
		"EnabledTools":           true, // feat/config-tool-allowlist: persisted to BBolt
		"DisabledTools":          true, // feat/config-tool-allowlist: persisted to BBolt
		// MCP-866: persisted to BBolt so a server's registry origin/provenance
		// (and the custom-origin skip_quarantine guard) survive a restart.
		"SourceRegistryID":         true,
		"SourceRegistryProvenance": true,
		// Spec 074: server-edition per-upstream broker config; lives in the JSON
		// config (like Shared), not persisted to the BBolt UpstreamRecord.
		"AuthBroker": true,
		// Spec 074: per-server discovery/health-check overrides; round-tripped
		// through UpstreamRecord so REST/UI-set overrides survive a restart.
		"HealthCheckInterval":   true,
		"ToolDiscoveryInterval": true,
		// MCP-3322: per-server init_timeout override; round-tripped through
		// UpstreamRecord so REST/UI/CLI-set deadlines survive a restart.
		"InitTimeout": true,
	}

	// Get all fields from ServerConfig
	serverConfigType := reflect.TypeOf(config.ServerConfig{})
	for i := 0; i < serverConfigType.NumField(); i++ {
		field := serverConfigType.Field(i)
		if !expectedFields[field.Name] {
			t.Errorf("ServerConfig field %q is not handled in saveServerSync. "+
				"Add it to expectedFields if intentionally excluded, or add it to the UpstreamRecord conversion.",
				field.Name)
		}
	}

	// Get all fields from UpstreamRecord
	upstreamRecordType := reflect.TypeOf(UpstreamRecord{})
	upstreamFields := make(map[string]bool)
	for i := 0; i < upstreamRecordType.NumField(); i++ {
		field := upstreamRecordType.Field(i)
		upstreamFields[field.Name] = true
	}

	// Verify expected fields exist in UpstreamRecord (except ID which is derived from Name)
	for fieldName := range expectedFields {
		if fieldName == "Name" {
			// Name maps to both ID and Name in UpstreamRecord
			continue
		}
		if fieldName == "Shared" {
			// Server-edition-only field, persisted in JSON config not BBolt
			continue
		}
		if fieldName == "SkipQuarantine" {
			// Spec 032: runtime-only field, not persisted to BBolt
			continue
		}
		if fieldName == "AuthBroker" {
			// Spec 074: server-edition JSON-config field, not persisted to BBolt
			continue
		}
		if !upstreamFields[fieldName] {
			t.Errorf("Expected field %q in UpstreamRecord but not found", fieldName)
		}
	}

	t.Logf("ServerConfig has %d fields, all mapped to UpstreamRecord", serverConfigType.NumField())
}
