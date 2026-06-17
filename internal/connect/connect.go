package connect

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// AccessState classifies a per-client config access (Spec 075). It is left as
// accessUnknown by the content-read-free overall status and resolved by the
// on-demand GetStatus / connect / disconnect paths.
const (
	accessUnknown    = "unknown"    // overall status: not content-checked
	accessAccessible = "accessible" // config read and parsed successfully
	accessAbsent     = "absent"     // config file does not exist (not installed)
	accessMalformed  = "malformed"  // config read but contents unparseable
	// accessDenied ("denied", macOS TCC App-Data) is added in US2.
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
	Supported  bool   `json:"supported"`        // client can be connected (directly or via a bridge)
	Reason     string `json:"reason,omitempty"` // why not supported
	Note       string `json:"note,omitempty"`   // caveat for supported clients (e.g. bridge requirement)
	Bridge     bool   `json:"bridge,omitempty"` // connects via a stdio bridge; connectable even without an existing config
	Icon       string `json:"icon"`
	ServerName string `json:"server_name,omitempty"` // name under which mcpproxy is registered

	// AccessState classifies the per-client content access (Spec 075, additive).
	// Empty/"unknown" in the content-read-free overall status; resolved to
	// "accessible"/"absent"/"malformed" (and "denied" in US2) by on-demand reads.
	AccessState string `json:"access_state"`
	// Remediation carries actionable fix text, populated only when access is denied.
	Remediation string `json:"remediation,omitempty"`
}

// Service provides connect/disconnect operations for MCP client configurations.
type Service struct {
	listenAddr string // e.g. "127.0.0.1:8080"
	apiKey     string // optional API key
	homeDir    string // override for testing; empty means use os.UserHomeDir
	// readFile is the content-read seam (Spec 075 T003). Defaults to os.ReadFile;
	// tests inject a permission-denied error or a call counter through it.
	readFile func(string) ([]byte, error)
}

// NewService creates a Service that will inject the given listen address
// and optional API key into client configurations.
func NewService(listenAddr, apiKey string) *Service {
	return &Service{
		listenAddr: listenAddr,
		apiKey:     apiKey,
		readFile:   os.ReadFile,
	}
}

// NewServiceWithHome creates a Service with a custom home directory (for testing).
func NewServiceWithHome(listenAddr, apiKey, homeDir string) *Service {
	return &Service{
		listenAddr: listenAddr,
		apiKey:     apiKey,
		homeDir:    homeDir,
		readFile:   os.ReadFile,
	}
}

// NewServiceWithReader creates a Service with a custom content reader (for
// testing the access-classification seam without a real OS denial).
func NewServiceWithReader(listenAddr, apiKey, homeDir string, readFile func(string) ([]byte, error)) *Service {
	return &Service{
		listenAddr: listenAddr,
		apiKey:     apiKey,
		homeDir:    homeDir,
		readFile:   readFile,
	}
}

// setReadFile overrides the content-read seam (test helper).
func (s *Service) setReadFile(fn func(string) ([]byte, error)) { s.readFile = fn }

// read performs a config content read through the seam, falling back to
// os.ReadFile for a zero-value Service.
func (s *Service) read(path string) ([]byte, error) {
	if s.readFile != nil {
		return s.readFile(path)
	}
	return os.ReadFile(path)
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

// GetConnectedCount returns the number of supported clients in which mcpproxy
// is currently registered. Used as the "has any client connected?" wizard
// predicate (Spec 046).
func (s *Service) GetConnectedCount() int {
	count := 0
	for _, c := range GetAllClients() {
		if !c.Supported {
			continue
		}
		// On-demand per-client read: GetConnectedCount/IDs are the one internal
		// caller that legitimately needs the connected truth for the wizard
		// predicate, and it reads lazily per client (Spec 075 T011).
		if st, err := s.GetStatus(c.ID); err == nil && st.Connected {
			count++
		}
	}
	return count
}

// GetConnectedIDs returns the identifiers of supported clients in which
// mcpproxy is currently registered. Identifiers come from the fixed
// per-client adapter table; user-entered values never appear here.
func (s *Service) GetConnectedIDs() []string {
	clients := GetAllClients()
	ids := make([]string, 0, len(clients))
	for _, c := range clients {
		if !c.Supported {
			continue
		}
		if st, err := s.GetStatus(c.ID); err == nil && st.Connected {
			ids = append(ids, st.ID)
		}
	}
	return ids
}

// GetAllStatus returns the connection status for every known client.
//
// It determines "installed" via os.Stat metadata only and performs ZERO config
// content reads (Spec 075 FR-001): no client config file is opened, so simply
// viewing status raises no macOS App-Data privacy prompt. AccessState is left as
// "unknown" and Connected stays false for installed clients until an explicit
// per-client read via GetStatus.
func (s *Service) GetAllStatus() []ClientStatus {
	clients := GetAllClients()
	statuses := make([]ClientStatus, 0, len(clients))

	for _, c := range clients {
		cfgPath := ConfigPath(c.ID, s.homeDir)
		status := ClientStatus{
			ID:          c.ID,
			Name:        c.Name,
			ConfigPath:  cfgPath,
			Supported:   c.Supported,
			Reason:      c.Reason,
			Note:        c.Note,
			Bridge:      c.Bridge,
			Icon:        c.Icon,
			AccessState: accessUnknown,
		}

		// Metadata-only existence check (no content read).
		if _, err := os.Stat(cfgPath); err == nil {
			status.Exists = true
		}

		statuses = append(statuses, status)
	}

	return statuses
}

// GetStatus returns the status for a single client, reading its config contents
// on demand (Spec 075 FR-002). This is the scoped, explicit-action path where a
// macOS App-Data prompt may legitimately appear. It resolves Connected and
// AccessState (accessible/absent/malformed; "denied" is added in US2).
func (s *Service) GetStatus(clientID string) (ClientStatus, error) {
	c := FindClient(clientID)
	if c == nil {
		return ClientStatus{}, fmt.Errorf("unknown client: %s", clientID)
	}

	cfgPath := ConfigPath(c.ID, s.homeDir)
	status := ClientStatus{
		ID:          c.ID,
		Name:        c.Name,
		ConfigPath:  cfgPath,
		Supported:   c.Supported,
		Reason:      c.Reason,
		Note:        c.Note,
		Bridge:      c.Bridge,
		Icon:        c.Icon,
		AccessState: accessUnknown,
	}

	if _, err := os.Stat(cfgPath); err == nil {
		status.Exists = true
	}
	if !status.Exists {
		status.AccessState = accessAbsent
		return status, nil
	}
	if !c.Supported {
		return status, nil
	}

	name, found, outcome := s.entryAccess(*c, cfgPath)
	status.AccessState = outcome
	if outcome == accessAccessible && found {
		status.Connected = true
		status.ServerName = name
	}
	return status, nil
}

// entryAccess reads the client config exactly once via the seam, then reports
// the registered server name (if any), whether mcpproxy is connected, and the
// access outcome. US1 classifies accessible/absent/malformed; US2 refines a
// permission denial (currently surfaced as accessUnknown) into accessDenied.
func (s *Service) entryAccess(client ClientDef, cfgPath string) (name string, found bool, outcome string) {
	raw, err := s.read(cfgPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, accessAbsent
		}
		// US1: unclassified read error. US2 maps fs.ErrPermission -> denied.
		return "", false, accessUnknown
	}
	name, found, parsedOK := s.findEntryFromBytes(client, raw)
	if !parsedOK {
		return "", false, accessMalformed
	}
	return name, found, accessAccessible
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
	if client.ID == "opencode" {
		if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("OpenCode config file %s does not exist", cfgPath)
		}
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
	data, perm, err := s.readOrCreateJSON(cfgPath)
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

	if client.ID == "opencode" {
		if adoptedName, found := findEquivalentJSONServerName(serversMap, mcpURL, serverName); found && adoptedName != serverName {
			if !force {
				return &ConnectResult{
					Success:    true,
					Client:     client.ID,
					ConfigPath: cfgPath,
					ServerName: adoptedName,
					Action:     "already_exists",
					Message:    fmt.Sprintf("%s already connected as %q", client.Name, adoptedName),
				}, nil
			}
			delete(serversMap, adoptedName)
			action = "updated"
		}
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
	if err := s.verifyJSONEntry(cfgPath, serversKey, serverName); err != nil {
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
	raw, err := s.read(cfgPath)
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
	if err := unmarshalLenientJSON(raw, &data); err != nil {
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
	data, perm, err := s.readOrCreateTOML(cfgPath)
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
	raw, err := s.read(cfgPath)
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
func (s *Service) readOrCreateJSON(path string) (map[string]interface{}, os.FileMode, error) {
	perm := os.FileMode(0o644)

	raw, err := s.read(path)
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
	if err := unmarshalLenientJSON(raw, &data); err != nil {
		return nil, perm, fmt.Errorf("parse JSON in %s: %w", path, err)
	}

	return data, perm, nil
}

// readOrCreateTOML reads a TOML config file, or returns an empty map with default permissions.
func (s *Service) readOrCreateTOML(path string) (map[string]interface{}, os.FileMode, error) {
	perm := os.FileMode(0o644)

	raw, err := s.read(path)
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
func (s *Service) verifyJSONEntry(path, serversKey, serverName string) error {
	raw, err := s.read(path)
	if err != nil {
		return fmt.Errorf("re-read %s: %w", path, err)
	}
	var data map[string]interface{}
	if err := unmarshalLenientJSON(raw, &data); err != nil {
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

func findEquivalentJSONServerName(serversMap map[string]interface{}, mcpURL, requestedServerName string) (string, bool) {
	baseURL := strings.SplitN(mcpURL, "?", 2)[0]
	for name, rawEntry := range serversMap {
		entry, ok := rawEntry.(map[string]interface{})
		if !ok {
			continue
		}
		for _, field := range []string{"url", "serverUrl", "httpUrl"} {
			entryURL, ok := entry[field].(string)
			if !ok {
				continue
			}
			if entryURL == mcpURL || entryURL == baseURL || strings.HasPrefix(entryURL, baseURL+"?") {
				return name, true
			}
		}
		if name == requestedServerName {
			return name, true
		}
	}
	return "", false
}

// findEntryFromBytes checks whether already-read config bytes contain an
// mcpproxy-like entry. It returns the server name, whether it was found, and
// whether the bytes parsed successfully (parsedOK=false => malformed). All
// content reads route through s.read (Spec 075 T010); this function never
// touches the filesystem.
func (s *Service) findEntryFromBytes(client ClientDef, raw []byte) (name string, found, parsedOK bool) {
	if client.Format == "toml" {
		return s.findEntryTOMLBytes(raw)
	}
	return s.findEntryJSONBytes(client, raw)
}

// findEntryJSONBytes parses JSON config bytes and looks for an entry that points
// to our MCP URL.
func (s *Service) findEntryJSONBytes(client ClientDef, raw []byte) (name string, found, parsedOK bool) {
	var data map[string]interface{}
	if err := unmarshalLenientJSON(raw, &data); err != nil {
		return "", false, false
	}

	serversMap, ok := data[client.ServerKey].(map[string]interface{})
	if !ok {
		return "", false, true
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
					return name, true, true
				}
			}
		}

		// Stdio-bridge clients (e.g. Claude Desktop) have no URL field; the
		// mcpproxy endpoint lives in the command args. Detect by inspecting
		// args so a bridge written under a custom server name is still found.
		if entryPointsToBridge(entry, mcpURL, baseURL) {
			return name, true, true
		}

		// Also match by server name
		if name == defaultServerName {
			return name, true, true
		}
	}

	return "", false, true
}

// entryPointsToBridge reports whether a JSON config entry is an mcp-remote
// stdio bridge targeting our MCP endpoint, regardless of the entry key.
func entryPointsToBridge(entry map[string]interface{}, mcpURL, baseURL string) bool {
	rawArgs, ok := entry["args"].([]interface{})
	if !ok {
		return false
	}
	hasBridgePkg := false
	pointsToUs := false
	for _, a := range rawArgs {
		s, ok := a.(string)
		if !ok {
			continue
		}
		if s == "mcp-remote" {
			hasBridgePkg = true
		}
		if s == mcpURL || s == baseURL || strings.HasPrefix(s, baseURL+"?") {
			pointsToUs = true
		}
	}
	return hasBridgePkg && pointsToUs
}

var trailingCommaPattern = regexp.MustCompile(`,\s*([}\]])`)

func unmarshalLenientJSON(raw []byte, out interface{}) error {
	if err := json.Unmarshal(raw, out); err == nil {
		return nil
	}
	cleaned := trailingCommaPattern.ReplaceAll(raw, []byte(`$1`))
	return json.Unmarshal(cleaned, out)
}

// findEntryTOMLBytes parses TOML config bytes and looks for an entry that points
// to our MCP URL.
func (s *Service) findEntryTOMLBytes(raw []byte) (name string, found, parsedOK bool) {
	var data map[string]interface{}
	if _, err := toml.Decode(string(raw), &data); err != nil {
		return "", false, false
	}

	serversRaw, ok := data["mcp_servers"]
	if !ok {
		return "", false, true
	}

	serversMap, ok := serversRaw.(map[string]interface{})
	if !ok {
		return "", false, true
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
				return name, true, true
			}
		}
		if name == defaultServerName {
			return name, true, true
		}
	}

	return "", false, true
}
