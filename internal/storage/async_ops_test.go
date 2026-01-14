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
		"Name":        true,
		"URL":         true,
		"Protocol":    true,
		"Command":     true,
		"Args":        true,
		"WorkingDir":  true,
		"Env":         true,
		"Headers":     true,
		"OAuth":       true,
		"Enabled":     true,
		"Quarantined": true,
		"Created":     true,
		"Updated":     true, // Updated is set by saveServerSync, not copied
		"Isolation":   true,
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
		if !upstreamFields[fieldName] {
			t.Errorf("Expected field %q in UpstreamRecord but not found", fieldName)
		}
	}

	t.Logf("ServerConfig has %d fields, all mapped to UpstreamRecord", serverConfigType.NumField())
}
