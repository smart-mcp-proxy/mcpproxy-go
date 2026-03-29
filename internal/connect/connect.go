package connect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// ConnectResult describes the outcome of a connect or disconnect operation.
type ConnectResult struct {
	Success    bool   `json:"success"`
	Client     string `json:"client"`
	ConfigPath string `json:"config_path"`
	BackupPath string `json:"backup_path,omitempty"`
	ServerName string `json:"server_name"`
	Action     string `json:"action"` // "created", "updated", "already_exists", "removed", "not_found"
	Message    string `json:"message"`
}

// ClientStatus describes the current state of a client's configuration
// with respect to an MCPProxy entry.
type ClientStatus struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ConfigPath string `json:"config_path"`
	Exists     bool   `json:"exists"`           // config file exists on disk
	Connected  bool   `json:"connected"`        // mcpproxy entry present in config
	Supported  bool   `json:"supported"`        // client supports HTTP/SSE
	Reason     string `json:"reason,omitempty"` // why not supported
	Icon       string `json:"icon"`
	ServerName string `json:"server_name,omitempty"` // name under which mcpproxy is registered
}

// Service provides connect/disconnect operations for MCP client configurations.
type Service struct {
	listenAddr string // e.g. "127.0.0.1:8080"
	apiKey     string // optional API key
	homeDir    string // override for testing; empty means use os.UserHomeDir
}

// NewService creates a Service that will inject the given listen address
// and optional API key into client configurations.
func NewService(listenAddr, apiKey string) *Service {
	return &Service{
		listenAddr: listenAddr,
		apiKey:     apiKey,
	}
}

// NewServiceWithHome creates a Service with a custom home directory (for testing).
func NewServiceWithHome(listenAddr, apiKey, homeDir string) *Service {
	return &Service{
		listenAddr: listenAddr,
		apiKey:     apiKey,
		homeDir:    homeDir,
	}
}

// mcpURL builds the MCPProxy MCP endpoint URL.
func (s *Service) mcpURL() string {
	addr := s.listenAddr
	// If listen address starts with ":" (no host), default to localhost
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	base := fmt.Sprintf("http://%s/mcp", addr)
	if s.apiKey != "" {
		base += "?apikey=" + url.QueryEscape(s.apiKey)
	}
	return base
}

// defaultServerName is the key used in client config files.
const defaultServerName = "mcpproxy"

// GetAllStatus returns the connection status for every known client.
func (s *Service) GetAllStatus() []ClientStatus {
	clients := GetAllClients()
	statuses := make([]ClientStatus, 0, len(clients))

	for _, c := range clients {
		cfgPath := ConfigPath(c.ID, s.homeDir)
		status := ClientStatus{
			ID:         c.ID,
			Name:       c.Name,
			ConfigPath: cfgPath,
			Supported:  c.Supported,
			Reason:     c.Reason,
			Icon:       c.Icon,
		}

		if _, err := os.Stat(cfgPath); err == nil {
			status.Exists = true
		}

		// Check if mcpproxy entry exists in the config
		if status.Exists && c.Supported {
			if name, found := s.findEntry(c, cfgPath); found {
				status.Connected = true
				status.ServerName = name
			}
		}

		statuses = append(statuses, status)
	}

	return statuses
}

// Connect registers MCPProxy in the specified client's configuration file.
// serverName defaults to "mcpproxy" if empty. If force is false and an entry
// already exists, an error is returned.
func (s *Service) Connect(clientID, serverName string, force bool) (*ConnectResult, error) {
	client := FindClient(clientID)
	if client == nil {
		return nil, fmt.Errorf("unknown client: %s", clientID)
	}
	if !client.Supported {
		return nil, fmt.Errorf("client %s is not supported: %s", client.Name, client.Reason)
	}

	if serverName == "" {
		serverName = defaultServerName
	}

	cfgPath := ConfigPath(clientID, s.homeDir)
	if cfgPath == "" {
		return nil, fmt.Errorf("cannot determine config path for %s", clientID)
	}

	mcpURL := s.mcpURL()

	if client.Format == "toml" {
		return s.connectTOML(client, cfgPath, serverName, mcpURL, force)
	}
	return s.connectJSON(client, cfgPath, serverName, mcpURL, force)
}

// Disconnect removes the MCPProxy entry from the specified client's configuration.
func (s *Service) Disconnect(clientID, serverName string) (*ConnectResult, error) {
	client := FindClient(clientID)
	if client == nil {
		return nil, fmt.Errorf("unknown client: %s", clientID)
	}
	if !client.Supported {
		return nil, fmt.Errorf("client %s is not supported: %s", client.Name, client.Reason)
	}

	if serverName == "" {
		serverName = defaultServerName
	}

	cfgPath := ConfigPath(clientID, s.homeDir)
	if cfgPath == "" {
		return nil, fmt.Errorf("cannot determine config path for %s", clientID)
	}

	if client.Format == "toml" {
		return s.disconnectTOML(client, cfgPath, serverName)
	}
	return s.disconnectJSON(client, cfgPath, serverName)
}

// ---------- JSON helpers ----------

// connectJSON adds or updates the mcpproxy entry in a JSON config file.
func (s *Service) connectJSON(client *ClientDef, cfgPath, serverName, mcpURL string, force bool) (*ConnectResult, error) {
	// Read existing config or start fresh
	data, perm, err := readOrCreateJSON(cfgPath)
	if err != nil {
		return nil, err
	}

	// Get or create the servers section
	serversKey := client.ServerKey
	serversMap, ok := data[serversKey].(map[string]interface{})
	if !ok {
		serversMap = make(map[string]interface{})
	}

	action := "created"
	if _, exists := serversMap[serverName]; exists {
		if !force {
			return &ConnectResult{
				Success:    false,
				Client:     client.ID,
				ConfigPath: cfgPath,
				ServerName: serverName,
				Action:     "already_exists",
				Message:    fmt.Sprintf("%s already has an entry named %q; use force=true to overwrite", client.Name, serverName),
			}, nil
		}
		action = "updated"
	}

	// Create backup before modifying
	backupPath, err := backupFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("backup failed: %w", err)
	}

	// Build the entry
	entry := buildServerEntry(client.ID, mcpURL)
	serversMap[serverName] = entry
	data[serversKey] = serversMap

	// Write atomically
	encoded, err := marshalJSONIndent(data)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	if err := atomicWriteFile(cfgPath, encoded, perm); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	// Verify by re-reading
	if err := verifyJSONEntry(cfgPath, serversKey, serverName); err != nil {
		return nil, fmt.Errorf("verification failed: %w", err)
	}

	return &ConnectResult{
		Success:    true,
		Client:     client.ID,
		ConfigPath: cfgPath,
		BackupPath: backupPath,
		ServerName: serverName,
		Action:     action,
		Message:    fmt.Sprintf("MCPProxy registered in %s as %q", client.Name, serverName),
	}, nil
}

// disconnectJSON removes the mcpproxy entry from a JSON config file.
func (s *Service) disconnectJSON(client *ClientDef, cfgPath, serverName string) (*ConnectResult, error) {
	raw, err := os.ReadFile(cfgPath)
	if os.IsNotExist(err) {
		return &ConnectResult{
			Success:    false,
			Client:     client.ID,
			ConfigPath: cfgPath,
			ServerName: serverName,
			Action:     "not_found",
			Message:    fmt.Sprintf("Config file %s does not exist", cfgPath),
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	serversKey := client.ServerKey
	serversMap, ok := data[serversKey].(map[string]interface{})
	if !ok {
		return &ConnectResult{
			Success:    false,
			Client:     client.ID,
			ConfigPath: cfgPath,
			ServerName: serverName,
			Action:     "not_found",
			Message:    fmt.Sprintf("No %s section found in %s", serversKey, client.Name),
		}, nil
	}

	if _, exists := serversMap[serverName]; !exists {
		return &ConnectResult{
			Success:    false,
			Client:     client.ID,
			ConfigPath: cfgPath,
			ServerName: serverName,
			Action:     "not_found",
			Message:    fmt.Sprintf("No entry named %q in %s", serverName, client.Name),
		}, nil
	}

	// Create backup
	backupPath, err := backupFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("backup failed: %w", err)
	}

	delete(serversMap, serverName)
	data[serversKey] = serversMap

	info, _ := os.Stat(cfgPath)
	perm := os.FileMode(0o644)
	if info != nil {
		perm = info.Mode()
	}

	encoded, err := marshalJSONIndent(data)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	if err := atomicWriteFile(cfgPath, encoded, perm); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	return &ConnectResult{
		Success:    true,
		Client:     client.ID,
		ConfigPath: cfgPath,
		BackupPath: backupPath,
		ServerName: serverName,
		Action:     "removed",
		Message:    fmt.Sprintf("MCPProxy entry %q removed from %s", serverName, client.Name),
	}, nil
}

// ---------- TOML helpers (Codex) ----------

// connectTOML adds or updates the mcpproxy entry in a TOML config file (Codex).
func (s *Service) connectTOML(client *ClientDef, cfgPath, serverName, mcpURL string, force bool) (*ConnectResult, error) {
	data, perm, err := readOrCreateTOML(cfgPath)
	if err != nil {
		return nil, err
	}

	// Get or create mcp_servers section
	serversRaw, ok := data["mcp_servers"]
	var serversMap map[string]interface{}
	if ok {
		serversMap, _ = serversRaw.(map[string]interface{})
	}
	if serversMap == nil {
		serversMap = make(map[string]interface{})
	}

	action := "created"
	if _, exists := serversMap[serverName]; exists {
		if !force {
			return &ConnectResult{
				Success:    false,
				Client:     client.ID,
				ConfigPath: cfgPath,
				ServerName: serverName,
				Action:     "already_exists",
				Message:    fmt.Sprintf("%s already has an entry named %q; use force=true to overwrite", client.Name, serverName),
			}, nil
		}
		action = "updated"
	}

	// Backup
	backupPath, err := backupFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("backup failed: %w", err)
	}

	// Build Codex entry
	entry := map[string]interface{}{
		"url": mcpURL,
	}
	serversMap[serverName] = entry
	data["mcp_servers"] = serversMap

	// Encode TOML
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(data); err != nil {
		return nil, fmt.Errorf("encode TOML: %w", err)
	}

	if err := atomicWriteFile(cfgPath, buf.Bytes(), perm); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	return &ConnectResult{
		Success:    true,
		Client:     client.ID,
		ConfigPath: cfgPath,
		BackupPath: backupPath,
		ServerName: serverName,
		Action:     action,
		Message:    fmt.Sprintf("MCPProxy registered in %s as %q", client.Name, serverName),
	}, nil
}

// disconnectTOML removes the mcpproxy entry from a TOML config file.
func (s *Service) disconnectTOML(client *ClientDef, cfgPath, serverName string) (*ConnectResult, error) {
	raw, err := os.ReadFile(cfgPath)
	if os.IsNotExist(err) {
		return &ConnectResult{
			Success:    false,
			Client:     client.ID,
			ConfigPath: cfgPath,
			ServerName: serverName,
			Action:     "not_found",
			Message:    fmt.Sprintf("Config file %s does not exist", cfgPath),
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var data map[string]interface{}
	if _, err := toml.Decode(string(raw), &data); err != nil {
		return nil, fmt.Errorf("parse TOML: %w", err)
	}

	serversRaw, ok := data["mcp_servers"]
	var serversMap map[string]interface{}
	if ok {
		serversMap, _ = serversRaw.(map[string]interface{})
	}

	if serversMap == nil {
		return &ConnectResult{
			Success:    false,
			Client:     client.ID,
			ConfigPath: cfgPath,
			ServerName: serverName,
			Action:     "not_found",
			Message:    fmt.Sprintf("No mcp_servers section found in %s", client.Name),
		}, nil
	}

	if _, exists := serversMap[serverName]; !exists {
		return &ConnectResult{
			Success:    false,
			Client:     client.ID,
			ConfigPath: cfgPath,
			ServerName: serverName,
			Action:     "not_found",
			Message:    fmt.Sprintf("No entry named %q in %s", serverName, client.Name),
		}, nil
	}

	backupPath, err := backupFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("backup failed: %w", err)
	}

	delete(serversMap, serverName)
	data["mcp_servers"] = serversMap

	info, _ := os.Stat(cfgPath)
	perm := os.FileMode(0o644)
	if info != nil {
		perm = info.Mode()
	}

	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(data); err != nil {
		return nil, fmt.Errorf("encode TOML: %w", err)
	}

	if err := atomicWriteFile(cfgPath, buf.Bytes(), perm); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	return &ConnectResult{
		Success:    true,
		Client:     client.ID,
		ConfigPath: cfgPath,
		BackupPath: backupPath,
		ServerName: serverName,
		Action:     "removed",
		Message:    fmt.Sprintf("MCPProxy entry %q removed from %s", serverName, client.Name),
	}, nil
}

// ---------- Internal helpers ----------

// readOrCreateJSON reads a JSON config file, or returns an empty map with default permissions
// if the file does not exist.
func readOrCreateJSON(path string) (map[string]interface{}, os.FileMode, error) {
	perm := os.FileMode(0o644)

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return make(map[string]interface{}), perm, nil
	}
	if err != nil {
		return nil, perm, fmt.Errorf("read %s: %w", path, err)
	}

	info, _ := os.Stat(path)
	if info != nil {
		perm = info.Mode()
	}

	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, perm, fmt.Errorf("parse JSON in %s: %w", path, err)
	}

	return data, perm, nil
}

// readOrCreateTOML reads a TOML config file, or returns an empty map with default permissions.
func readOrCreateTOML(path string) (map[string]interface{}, os.FileMode, error) {
	perm := os.FileMode(0o644)

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return make(map[string]interface{}), perm, nil
	}
	if err != nil {
		return nil, perm, fmt.Errorf("read %s: %w", path, err)
	}

	info, _ := os.Stat(path)
	if info != nil {
		perm = info.Mode()
	}

	var data map[string]interface{}
	if _, err := toml.Decode(string(raw), &data); err != nil {
		return nil, perm, fmt.Errorf("parse TOML in %s: %w", path, err)
	}

	return data, perm, nil
}

// marshalJSONIndent encodes data as pretty-printed JSON with a trailing newline.
func marshalJSONIndent(data interface{}) ([]byte, error) {
	buf, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, err
	}
	buf = append(buf, '\n')
	return buf, nil
}

// verifyJSONEntry re-reads the config file and checks that the expected entry exists.
func verifyJSONEntry(path, serversKey, serverName string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("re-read %s: %w", path, err)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("re-parse %s: %w", path, err)
	}
	serversMap, ok := data[serversKey].(map[string]interface{})
	if !ok {
		return fmt.Errorf("missing %s key after write", serversKey)
	}
	if _, exists := serversMap[serverName]; !exists {
		return fmt.Errorf("entry %q missing after write", serverName)
	}
	return nil
}

// findEntry checks whether a config file contains an mcpproxy-like entry.
// It returns the server name and true if found.
func (s *Service) findEntry(client ClientDef, cfgPath string) (string, bool) {
	if client.Format == "toml" {
		return s.findEntryTOML(cfgPath)
	}
	return s.findEntryJSON(client, cfgPath)
}

// findEntryJSON looks for an entry in a JSON config that points to our MCP URL.
func (s *Service) findEntryJSON(client ClientDef, cfgPath string) (string, bool) {
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return "", false
	}

	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", false
	}

	serversMap, ok := data[client.ServerKey].(map[string]interface{})
	if !ok {
		return "", false
	}

	mcpURL := s.mcpURL()
	baseURL := fmt.Sprintf("http://%s/mcp", s.listenAddr)

	for name, v := range serversMap {
		entry, ok := v.(map[string]interface{})
		if !ok {
			continue
		}

		// Check various URL fields used by different clients
		for _, field := range []string{"url", "serverUrl", "httpUrl"} {
			if u, ok := entry[field].(string); ok {
				if u == mcpURL || u == baseURL || strings.HasPrefix(u, baseURL+"?") {
					return name, true
				}
			}
		}

		// Also match by server name
		if name == defaultServerName {
			return name, true
		}
	}

	return "", false
}

// findEntryTOML looks for an entry in a TOML config that points to our MCP URL.
func (s *Service) findEntryTOML(cfgPath string) (string, bool) {
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return "", false
	}

	var data map[string]interface{}
	if _, err := toml.Decode(string(raw), &data); err != nil {
		return "", false
	}

	serversRaw, ok := data["mcp_servers"]
	if !ok {
		return "", false
	}

	serversMap, ok := serversRaw.(map[string]interface{})
	if !ok {
		return "", false
	}

	mcpURL := s.mcpURL()
	baseURL := fmt.Sprintf("http://%s/mcp", s.listenAddr)

	for name, v := range serversMap {
		entry, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if u, ok := entry["url"].(string); ok {
			if u == mcpURL || u == baseURL || strings.HasPrefix(u, baseURL+"?") {
				return name, true
			}
		}
		if name == defaultServerName {
			return name, true
		}
	}

	return "", false
}
