package connect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// helper to create a service pointing at a temp home directory
func testService(t *testing.T) (*Service, string) {
	t.Helper()
	homeDir := t.TempDir()
	svc := NewServiceWithHome("127.0.0.1:8080", "", homeDir)
	return svc, homeDir
}

func testServiceWithKey(t *testing.T) (*Service, string) {
	t.Helper()
	homeDir := t.TempDir()
	svc := NewServiceWithHome("127.0.0.1:8080", "test-key-123", homeDir)
	return svc, homeDir
}

// ---------- JSON client tests ----------

func TestConnect_ClaudeCode_NewFile(t *testing.T) {
	svc, _ := testService(t)

	result, err := svc.Connect("claude-code", "", false)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected success, got: %s", result.Message)
	}
	if result.Action != "created" {
		t.Errorf("Expected action=created, got %s", result.Action)
	}
	if result.ServerName != "mcpproxy" {
		t.Errorf("Expected serverName=mcpproxy, got %s", result.ServerName)
	}
	if result.BackupPath != "" {
		t.Errorf("Expected no backup for new file, got %s", result.BackupPath)
	}

	// Verify the file was written correctly
	raw, err := os.ReadFile(result.ConfigPath)
	if err != nil {
		t.Fatalf("Read config failed: %v", err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("Parse config failed: %v", err)
	}

	servers, ok := data["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatal("Missing mcpServers key")
	}

	entry, ok := servers["mcpproxy"].(map[string]interface{})
	if !ok {
		t.Fatal("Missing mcpproxy entry")
	}

	if entry["type"] != "http" {
		t.Errorf("Expected type=http, got %v", entry["type"])
	}
	if entry["url"] != "http://127.0.0.1:8080/mcp" {
		t.Errorf("Expected url=http://127.0.0.1:8080/mcp, got %v", entry["url"])
	}
}

func TestConnect_ClaudeCode_WithAPIKey(t *testing.T) {
	svc, _ := testServiceWithKey(t)

	result, err := svc.Connect("claude-code", "", false)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected success, got: %s", result.Message)
	}

	raw, err := os.ReadFile(result.ConfigPath)
	if err != nil {
		t.Fatalf("Read config failed: %v", err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("Parse config failed: %v", err)
	}

	servers := data["mcpServers"].(map[string]interface{})
	entry := servers["mcpproxy"].(map[string]interface{})

	expectedURL := "http://127.0.0.1:8080/mcp?apikey=test-key-123"
	if entry["url"] != expectedURL {
		t.Errorf("Expected url=%s, got %v", expectedURL, entry["url"])
	}
}

func TestConnect_ExistingFile_PreservesOtherEntries(t *testing.T) {
	svc, homeDir := testService(t)

	// Create an existing config with another server
	cfgPath := filepath.Join(homeDir, ".claude.json")
	existingConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"github": map[string]interface{}{
				"type": "http",
				"url":  "https://api.github.com/mcp",
			},
		},
		"someOtherKey": "preserved",
	}
	data, _ := json.MarshalIndent(existingConfig, "", "  ")
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Connect("claude-code", "", false)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected success, got: %s", result.Message)
	}

	// Verify both entries exist and other keys are preserved
	raw, _ := os.ReadFile(cfgPath)
	var config map[string]interface{}
	json.Unmarshal(raw, &config)

	servers := config["mcpServers"].(map[string]interface{})
	if _, ok := servers["github"]; !ok {
		t.Error("github entry was lost")
	}
	if _, ok := servers["mcpproxy"]; !ok {
		t.Error("mcpproxy entry was not added")
	}
	if config["someOtherKey"] != "preserved" {
		t.Error("someOtherKey was not preserved")
	}
}

func TestConnect_AlreadyExists_NoForce(t *testing.T) {
	svc, _ := testService(t)

	// First connect succeeds
	_, err := svc.Connect("claude-code", "", false)
	if err != nil {
		t.Fatalf("First connect failed: %v", err)
	}

	// Second connect without force returns already_exists
	result, err := svc.Connect("claude-code", "", false)
	if err != nil {
		t.Fatalf("Second connect failed unexpectedly: %v", err)
	}
	if result.Success {
		t.Error("Expected failure for duplicate entry without force")
	}
	if result.Action != "already_exists" {
		t.Errorf("Expected action=already_exists, got %s", result.Action)
	}
}

func TestConnect_AlreadyExists_WithForce(t *testing.T) {
	svc, _ := testService(t)

	// First connect
	_, err := svc.Connect("claude-code", "", false)
	if err != nil {
		t.Fatalf("First connect failed: %v", err)
	}

	// Second connect with force updates
	result, err := svc.Connect("claude-code", "", true)
	if err != nil {
		t.Fatalf("Force connect failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected success for force connect, got: %s", result.Message)
	}
	if result.Action != "updated" {
		t.Errorf("Expected action=updated, got %s", result.Action)
	}
	if result.BackupPath == "" {
		t.Error("Expected backup path for update of existing file")
	}
}

func TestConnect_Backup_Created(t *testing.T) {
	svc, homeDir := testService(t)

	// Create an existing config
	cfgPath := filepath.Join(homeDir, ".claude.json")
	if err := os.WriteFile(cfgPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Connect("claude-code", "", false)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected success, got: %s", result.Message)
	}
	if result.BackupPath == "" {
		t.Fatal("Expected backup path")
	}

	// Verify backup file exists
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Errorf("Backup file does not exist: %s", result.BackupPath)
	}

	// Verify backup has the original content
	backupData, _ := os.ReadFile(result.BackupPath)
	if string(backupData) != `{"mcpServers":{}}` {
		t.Errorf("Backup content mismatch: %s", string(backupData))
	}
}

func TestConnect_VSCode_ServersKey(t *testing.T) {
	svc, homeDir := testService(t)

	// For VS Code, we need to create the directory structure
	// ConfigPath returns OS-specific path; let's directly test the connect logic
	cfgPath := ConfigPath("vscode", homeDir)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Connect("vscode", "", false)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected success, got: %s", result.Message)
	}

	// Verify uses "servers" key (not "mcpServers")
	raw, _ := os.ReadFile(result.ConfigPath)
	var data map[string]interface{}
	json.Unmarshal(raw, &data)

	if _, ok := data["servers"]; !ok {
		t.Error("Expected 'servers' key for VS Code, not found")
	}
	if _, ok := data["mcpServers"]; ok {
		t.Error("Unexpected 'mcpServers' key for VS Code")
	}

	servers := data["servers"].(map[string]interface{})
	entry := servers["mcpproxy"].(map[string]interface{})
	if entry["type"] != "http" {
		t.Errorf("Expected type=http for VS Code, got %v", entry["type"])
	}
}

func TestConnect_Cursor_SSEType(t *testing.T) {
	svc, _ := testService(t)

	result, err := svc.Connect("cursor", "", false)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected success, got: %s", result.Message)
	}

	raw, _ := os.ReadFile(result.ConfigPath)
	var data map[string]interface{}
	json.Unmarshal(raw, &data)

	servers := data["mcpServers"].(map[string]interface{})
	entry := servers["mcpproxy"].(map[string]interface{})
	if entry["type"] != "sse" {
		t.Errorf("Expected type=sse for Cursor, got %v", entry["type"])
	}
}

func TestConnect_Windsurf_ServerUrl(t *testing.T) {
	svc, homeDir := testService(t)

	cfgPath := ConfigPath("windsurf", homeDir)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Connect("windsurf", "", false)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected success, got: %s", result.Message)
	}

	raw, _ := os.ReadFile(result.ConfigPath)
	var data map[string]interface{}
	json.Unmarshal(raw, &data)

	servers := data["mcpServers"].(map[string]interface{})
	entry := servers["mcpproxy"].(map[string]interface{})
	if entry["serverUrl"] != "http://127.0.0.1:8080/mcp" {
		t.Errorf("Expected serverUrl for Windsurf, got %v", entry["serverUrl"])
	}
	if entry["type"] != "sse" {
		t.Errorf("Expected type=sse for Windsurf, got %v", entry["type"])
	}
}

func TestConnect_Gemini_HttpUrl(t *testing.T) {
	svc, homeDir := testService(t)

	cfgPath := ConfigPath("gemini", homeDir)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Connect("gemini", "", false)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected success, got: %s", result.Message)
	}

	raw, _ := os.ReadFile(result.ConfigPath)
	var data map[string]interface{}
	json.Unmarshal(raw, &data)

	servers := data["mcpServers"].(map[string]interface{})
	entry := servers["mcpproxy"].(map[string]interface{})
	if entry["httpUrl"] != "http://127.0.0.1:8080/mcp" {
		t.Errorf("Expected httpUrl for Gemini, got %v", entry["httpUrl"])
	}
}

// ---------- TOML client tests (Codex) ----------

func TestConnect_Codex_NewFile(t *testing.T) {
	svc, homeDir := testService(t)

	cfgPath := ConfigPath("codex", homeDir)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Connect("codex", "", false)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected success, got: %s", result.Message)
	}
	if result.Action != "created" {
		t.Errorf("Expected action=created, got %s", result.Action)
	}

	// Verify TOML content
	raw, _ := os.ReadFile(result.ConfigPath)
	var data map[string]interface{}
	if _, err := toml.Decode(string(raw), &data); err != nil {
		t.Fatalf("Parse TOML failed: %v", err)
	}

	servers, ok := data["mcp_servers"].(map[string]interface{})
	if !ok {
		t.Fatal("Missing mcp_servers section")
	}

	entry, ok := servers["mcpproxy"].(map[string]interface{})
	if !ok {
		t.Fatal("Missing mcpproxy entry")
	}

	if entry["url"] != "http://127.0.0.1:8080/mcp" {
		t.Errorf("Expected url=http://127.0.0.1:8080/mcp, got %v", entry["url"])
	}
}

func TestConnect_Codex_ExistingFile(t *testing.T) {
	svc, homeDir := testService(t)

	cfgPath := ConfigPath("codex", homeDir)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	// Write existing TOML config
	existing := `[mcp_servers.other-server]
url = "http://other.server/mcp"
`
	if err := os.WriteFile(cfgPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Connect("codex", "", false)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected success, got: %s", result.Message)
	}

	// Verify both entries exist
	raw, _ := os.ReadFile(cfgPath)
	var data map[string]interface{}
	toml.Decode(string(raw), &data)

	servers := data["mcp_servers"].(map[string]interface{})
	if _, ok := servers["other-server"]; !ok {
		t.Error("other-server entry was lost")
	}
	if _, ok := servers["mcpproxy"]; !ok {
		t.Error("mcpproxy entry was not added")
	}
}

func TestConnect_Codex_AlreadyExists(t *testing.T) {
	svc, homeDir := testService(t)

	cfgPath := ConfigPath("codex", homeDir)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	// First connect
	_, err := svc.Connect("codex", "", false)
	if err != nil {
		t.Fatal(err)
	}

	// Second without force
	result, err := svc.Connect("codex", "", false)
	if err != nil {
		t.Fatalf("Second connect error: %v", err)
	}
	if result.Success {
		t.Error("Expected failure for duplicate without force")
	}
	if result.Action != "already_exists" {
		t.Errorf("Expected already_exists, got %s", result.Action)
	}
}

// ---------- Disconnect tests ----------

func TestDisconnect_JSON(t *testing.T) {
	svc, _ := testService(t)

	// Connect first
	_, err := svc.Connect("claude-code", "", false)
	if err != nil {
		t.Fatal(err)
	}

	// Disconnect
	result, err := svc.Disconnect("claude-code", "")
	if err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected success, got: %s", result.Message)
	}
	if result.Action != "removed" {
		t.Errorf("Expected action=removed, got %s", result.Action)
	}
	if result.BackupPath == "" {
		t.Error("Expected backup for disconnect")
	}

	// Verify entry is gone
	raw, _ := os.ReadFile(result.ConfigPath)
	var data map[string]interface{}
	json.Unmarshal(raw, &data)

	servers := data["mcpServers"].(map[string]interface{})
	if _, ok := servers["mcpproxy"]; ok {
		t.Error("mcpproxy entry should have been removed")
	}
}

func TestDisconnect_NotFound(t *testing.T) {
	svc, _ := testService(t)

	// Try to disconnect without connecting first — file doesn't exist
	result, err := svc.Disconnect("claude-code", "")
	if err != nil {
		t.Fatalf("Disconnect error: %v", err)
	}
	if result.Success {
		t.Error("Expected failure when disconnecting non-existent entry")
	}
	if result.Action != "not_found" {
		t.Errorf("Expected action=not_found, got %s", result.Action)
	}
}

func TestDisconnect_TOML(t *testing.T) {
	svc, homeDir := testService(t)

	cfgPath := ConfigPath("codex", homeDir)
	os.MkdirAll(filepath.Dir(cfgPath), 0o755)

	// Connect first
	_, err := svc.Connect("codex", "", false)
	if err != nil {
		t.Fatal(err)
	}

	// Disconnect
	result, err := svc.Disconnect("codex", "")
	if err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected success, got: %s", result.Message)
	}
	if result.Action != "removed" {
		t.Errorf("Expected action=removed, got %s", result.Action)
	}
}

// ---------- Unsupported client tests ----------

func TestConnect_UnsupportedClient(t *testing.T) {
	svc, _ := testService(t)

	_, err := svc.Connect("claude-desktop", "", false)
	if err == nil {
		t.Fatal("Expected error for unsupported client")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("Expected 'not supported' in error, got: %v", err)
	}
}

func TestConnect_UnknownClient(t *testing.T) {
	svc, _ := testService(t)

	_, err := svc.Connect("nonexistent", "", false)
	if err == nil {
		t.Fatal("Expected error for unknown client")
	}
	if !strings.Contains(err.Error(), "unknown client") {
		t.Errorf("Expected 'unknown client' in error, got: %v", err)
	}
}

// ---------- Custom server name tests ----------

func TestConnect_CustomServerName(t *testing.T) {
	svc, _ := testService(t)

	result, err := svc.Connect("claude-code", "my-proxy", false)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("Expected success, got: %s", result.Message)
	}
	if result.ServerName != "my-proxy" {
		t.Errorf("Expected serverName=my-proxy, got %s", result.ServerName)
	}

	raw, _ := os.ReadFile(result.ConfigPath)
	var data map[string]interface{}
	json.Unmarshal(raw, &data)

	servers := data["mcpServers"].(map[string]interface{})
	if _, ok := servers["my-proxy"]; !ok {
		t.Error("Expected entry under custom name 'my-proxy'")
	}
}

// ---------- GetAllStatus tests ----------

func TestGetAllStatus(t *testing.T) {
	svc, _ := testService(t)

	statuses := svc.GetAllStatus()
	if len(statuses) != len(allClients) {
		t.Errorf("Expected %d statuses, got %d", len(allClients), len(statuses))
	}

	// Verify claude-desktop is not supported
	for _, s := range statuses {
		if s.ID == "claude-desktop" {
			if s.Supported {
				t.Error("claude-desktop should not be supported")
			}
			if s.Reason == "" {
				t.Error("claude-desktop should have a reason")
			}
		}
	}
}

func TestGetAllStatus_AfterConnect(t *testing.T) {
	svc, _ := testService(t)

	// Connect to claude-code
	_, err := svc.Connect("claude-code", "", false)
	if err != nil {
		t.Fatal(err)
	}

	statuses := svc.GetAllStatus()
	for _, s := range statuses {
		if s.ID == "claude-code" {
			if !s.Exists {
				t.Error("Expected exists=true for claude-code after connect")
			}
			if !s.Connected {
				t.Error("Expected connected=true for claude-code after connect")
			}
			break
		}
	}
}

// ---------- Backup utility tests ----------

func TestBackupFile(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "test.json")

	content := []byte(`{"test": true}`)
	if err := os.WriteFile(original, content, 0o644); err != nil {
		t.Fatal(err)
	}

	backupPath, err := backupFile(original)
	if err != nil {
		t.Fatalf("backupFile failed: %v", err)
	}
	if backupPath == "" {
		t.Fatal("Expected non-empty backup path")
	}

	// Verify backup content
	backupContent, _ := os.ReadFile(backupPath)
	if string(backupContent) != string(content) {
		t.Error("Backup content mismatch")
	}
}

func TestBackupFile_NonExistent(t *testing.T) {
	backupPath, err := backupFile("/nonexistent/file.json")
	if err != nil {
		t.Fatalf("Expected nil error for non-existent file, got: %v", err)
	}
	if backupPath != "" {
		t.Errorf("Expected empty backup path for non-existent file, got: %s", backupPath)
	}
}

func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "test.json")

	content := []byte(`{"atomic": true}`)
	if err := atomicWriteFile(path, content, 0o644); err != nil {
		t.Fatalf("atomicWriteFile failed: %v", err)
	}

	// Verify content
	read, _ := os.ReadFile(path)
	if string(read) != string(content) {
		t.Error("Content mismatch")
	}

	// Verify directory was created
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Error("Directory was not created")
	}
}

// ---------- ConfigPath tests ----------

func TestConfigPath_AllClients(t *testing.T) {
	homeDir := t.TempDir()
	for _, c := range allClients {
		path := ConfigPath(c.ID, homeDir)
		if path == "" {
			t.Errorf("Empty path for client %s", c.ID)
		}
		// On Windows, some clients (claude-desktop, vscode) use APPDATA
		// instead of homeDir, so only check that the path is non-empty and absolute.
		if !filepath.IsAbs(path) {
			t.Errorf("Path for %s is not absolute: %s", c.ID, path)
		}
	}
}

func TestConfigPath_UnknownClient(t *testing.T) {
	path := ConfigPath("unknown", "/test/home")
	if path != "" {
		t.Errorf("Expected empty path for unknown client, got: %s", path)
	}
}

// ---------- mcpURL tests ----------

func TestMcpURL_NoAPIKey(t *testing.T) {
	svc := NewService("127.0.0.1:8080", "")
	url := svc.mcpURL()
	if url != "http://127.0.0.1:8080/mcp" {
		t.Errorf("Expected http://127.0.0.1:8080/mcp, got %s", url)
	}
}

func TestMcpURL_WithAPIKey(t *testing.T) {
	svc := NewService("127.0.0.1:8080", "my-secret")
	url := svc.mcpURL()
	if url != "http://127.0.0.1:8080/mcp?apikey=my-secret" {
		t.Errorf("Expected url with apikey, got %s", url)
	}
}

func TestMcpURL_APIKeyWithSpecialChars(t *testing.T) {
	svc := NewService("127.0.0.1:8080", "key with spaces&special=chars")
	url := svc.mcpURL()
	if !strings.Contains(url, "apikey=") {
		t.Errorf("Expected apikey param in URL, got %s", url)
	}
	// Should be URL-encoded
	if strings.Contains(url, " ") {
		t.Error("URL should not contain raw spaces")
	}
}

// ---------- Client definitions tests ----------

func TestFindClient(t *testing.T) {
	c := FindClient("cursor")
	if c == nil {
		t.Fatal("Expected to find cursor client")
	}
	if c.Name != "Cursor" {
		t.Errorf("Expected name=Cursor, got %s", c.Name)
	}

	c = FindClient("nonexistent")
	if c != nil {
		t.Error("Expected nil for nonexistent client")
	}
}

func TestGetAllClients(t *testing.T) {
	clients := GetAllClients()
	if len(clients) != 7 {
		t.Errorf("Expected 7 clients, got %d", len(clients))
	}

	// Verify all have non-empty IDs and names
	for _, c := range clients {
		if c.ID == "" {
			t.Error("Client with empty ID")
		}
		if c.Name == "" {
			t.Errorf("Client %s has empty name", c.ID)
		}
	}
}

// ---------- Edge case tests ----------

func TestConnect_FilePermissionsPreserved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file permissions not supported on Windows")
	}

	svc, homeDir := testService(t)

	cfgPath := filepath.Join(homeDir, ".claude.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := svc.Connect("claude-code", "", false)
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("Expected permissions 0600, got %o", info.Mode().Perm())
	}
}
