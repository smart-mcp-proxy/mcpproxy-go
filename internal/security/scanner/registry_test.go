package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestRegistryListBundledScanners(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	r := NewRegistry(dir, logger)

	scanners := r.List()
	if len(scanners) != len(bundledScanners) {
		t.Errorf("expected %d bundled scanners, got %d", len(bundledScanners), len(scanners))
	}

	// Docker-backed scanners start "available"; in-process scanners (no image
	// to pull) start "installed" so they always run (MCP-2082).
	for _, s := range scanners {
		want := ScannerStatusAvailable
		if s.InProcess {
			want = ScannerStatusInstalled
		}
		if s.Status != want {
			t.Errorf("scanner %s: expected status %q, got %q", s.ID, want, s.Status)
		}
	}
}

// TestRampartsV08Invariants guards the v0.8.x URL/stdio scanning contract
// (MCP-2422). Ramparts dropped directory scanning, so the registry entry must
// run via the entrypoint (Command nil) and declare both source and mcp_connection
// inputs. Network is required because the container may need to reach a live
// HTTP/SSE endpoint when MCP_SERVER_URL is set (the stdio replay shim path
// remains offline — it's the container's network mode, not a per-invocation toggle).
func TestRampartsV08Invariants(t *testing.T) {
	r := NewRegistry(t.TempDir(), zap.NewNop())

	s, err := r.Get("ramparts")
	if err != nil {
		t.Fatalf("Get ramparts: %v", err)
	}
	if s.Command != nil {
		t.Errorf("ramparts Command should be nil (entrypoint-driven), got %v", s.Command)
	}
	if !s.NetworkReq {
		t.Errorf("ramparts requires network for live URL scanning (MCP_SERVER_URL mode)")
	}
	if s.InProcess {
		t.Errorf("ramparts is a Docker-backed scanner, should not be InProcess")
	}
	if s.DockerImage == "" {
		t.Errorf("ramparts must declare a Docker image")
	}
	hasMCPConnection := false
	hasSource := false
	for _, in := range s.Inputs {
		if in == "mcp_connection" {
			hasMCPConnection = true
		}
		if in == "source" {
			hasSource = true
		}
	}
	if !hasMCPConnection {
		t.Errorf("ramparts must declare mcp_connection input for live URL scanning")
	}
	if !hasSource {
		t.Errorf("ramparts must declare source input for stdio replay scanning")
	}
}

func TestRegistryInProcessScannerInstalledByDefault(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, zap.NewNop())

	s, err := r.Get(inProcessTPAScannerID)
	if err != nil {
		t.Fatalf("Get %s: %v", inProcessTPAScannerID, err)
	}
	if !s.InProcess {
		t.Errorf("scanner %s should be marked InProcess", inProcessTPAScannerID)
	}
	if s.Status != ScannerStatusInstalled {
		t.Errorf("in-process scanner status = %q, want %q", s.Status, ScannerStatusInstalled)
	}
	if s.DockerImage != "" {
		t.Errorf("in-process scanner should have no Docker image, got %q", s.DockerImage)
	}
}

func TestRegistryGetScanner(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	r := NewRegistry(dir, logger)

	s, err := r.Get("mcp-scan")
	if err != nil {
		t.Fatalf("Get mcp-scan: %v", err)
	}
	if s.Vendor != "Snyk (Invariant Labs)" {
		t.Errorf("expected vendor 'Snyk (Invariant Labs)', got %q", s.Vendor)
	}
}

func TestRegistryGetNotFound(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	r := NewRegistry(dir, logger)

	_, err := r.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent scanner")
	}
}

func TestRegistryRegisterCustomScanner(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	r := NewRegistry(dir, logger)

	custom := &ScannerPlugin{
		ID:          "custom-scanner",
		Name:        "Custom Scanner",
		DockerImage: "myorg/scanner:v1",
		Inputs:      []string{"source"},
		Command:     []string{"scan"},
		Timeout:     "60s",
	}

	if err := r.Register(custom); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Should be in list
	s, err := r.Get("custom-scanner")
	if err != nil {
		t.Fatalf("Get custom-scanner: %v", err)
	}
	if !s.Custom {
		t.Error("expected Custom=true")
	}

	// Should be persisted to file
	path := filepath.Join(dir, "scanner-registry.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("user registry file should exist after Register")
	}
}

func TestRegistryUnregisterCustomOnly(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	r := NewRegistry(dir, logger)

	// Cannot unregister bundled
	if err := r.Unregister("mcp-scan"); err == nil {
		t.Error("expected error when unregistering bundled scanner")
	}

	// Register and unregister custom
	custom := &ScannerPlugin{
		ID:          "custom",
		Name:        "Custom",
		DockerImage: "x/y:z",
		Inputs:      []string{"source"},
		Command:     []string{"scan"},
	}
	if err := r.Register(custom); err != nil {
		t.Fatalf("Register custom: %v", err)
	}
	if err := r.Unregister("custom"); err != nil {
		t.Fatalf("Unregister custom: %v", err)
	}
	if _, err := r.Get("custom"); err == nil {
		t.Error("expected error after unregister")
	}
}

func TestRegistryUserOverride(t *testing.T) {
	dir := t.TempDir()

	// Write user registry that overrides a bundled scanner
	userJSON := `[{"id":"mcp-scan","name":"My Custom MCP Scan","docker_image":"myorg/mcp-scan:v2","inputs":["source"],"command":["scan"],"custom":true}]`
	if err := os.WriteFile(filepath.Join(dir, "scanner-registry.json"), []byte(userJSON), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	logger := zap.NewNop()
	r := NewRegistry(dir, logger)

	s, _ := r.Get("mcp-scan")
	if s.Name != "My Custom MCP Scan" {
		t.Errorf("user override should win, got name %q", s.Name)
	}
}

func TestValidateManifest(t *testing.T) {
	tests := []struct {
		name    string
		scanner *ScannerPlugin
		wantErr bool
	}{
		{"valid", &ScannerPlugin{ID: "x", Name: "X", DockerImage: "x:1", Inputs: []string{"source"}, Command: []string{"scan"}}, false},
		{"missing ID", &ScannerPlugin{Name: "X", DockerImage: "x:1", Inputs: []string{"source"}, Command: []string{"scan"}}, true},
		{"missing name", &ScannerPlugin{ID: "x", DockerImage: "x:1", Inputs: []string{"source"}, Command: []string{"scan"}}, true},
		{"missing image", &ScannerPlugin{ID: "x", Name: "X", Inputs: []string{"source"}, Command: []string{"scan"}}, true},
		{"no inputs", &ScannerPlugin{ID: "x", Name: "X", DockerImage: "x:1", Command: []string{"scan"}}, true},
		{"invalid input", &ScannerPlugin{ID: "x", Name: "X", DockerImage: "x:1", Inputs: []string{"bad"}, Command: []string{"scan"}}, true},
		{"no command", &ScannerPlugin{ID: "x", Name: "X", DockerImage: "x:1", Inputs: []string{"source"}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateManifest(tt.scanner)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateManifest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
