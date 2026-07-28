package connect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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
	accessDenied     = "denied"     // blocked by OS permission (macOS TCC App-Data)
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
	// requireMCPAuth mirrors config.RequireMCPAuth. When false (the default),
	// the /mcp endpoint accepts unauthenticated requests, so connect writes NO
	// credential into client configs — embedding the REST-admin API key there
	// would leak it for no benefit (Spec 078 security fix). When true, the
	// credential is written via an HTTP header where the client config supports
	// one, falling back to the ?apikey= query only where it cannot.
	requireMCPAuth bool
	// configProvider, when set, supplies the LIVE listen address, API key, and
	// require_mcp_auth on every call instead of the startup snapshot above. The
	// /mcp auth middleware already honors require_mcp_auth live (server.go), so
	// the long-lived HTTP connect service must too — otherwise a runtime toggle
	// leaves a stale snapshot that re-introduces the API-key leak this fix closes
	// (auth turned off, but connect still embeds the key) or writes keyless
	// entries that cannot authenticate (auth turned on). Nil for CLI one-shots,
	// where the freshly-loaded config is already current (Spec 078).
	configProvider func() (listenAddr, apiKey string, requireMCPAuth bool)
	// readFile is the content-read seam (Spec 075 T003). Defaults to os.ReadFile;
	// tests inject a permission-denied error or a call counter through it.
	readFile func(string) ([]byte, error)
}

// WithRequireMCPAuth sets whether the /mcp endpoint requires authentication,
// which decides whether connect embeds a credential in client configs at all.
// Threaded from config.RequireMCPAuth at the wiring sites, alongside listenAddr
// and apiKey. Returns the receiver for chaining.
func (s *Service) WithRequireMCPAuth(v bool) *Service {
	s.requireMCPAuth = v
	return s
}

// WithConfigProvider installs a live-config accessor so the service reflects
// runtime changes to listen/api_key/require_mcp_auth (hot-reloaded via the file
// watcher or the wizard's require_mcp_auth toggle) rather than a startup
// snapshot. Wired only for the long-lived HTTP server; CLI one-shots leave it
// nil. Returns the receiver for chaining.
func (s *Service) WithConfigProvider(fn func() (listenAddr, apiKey string, requireMCPAuth bool)) *Service {
	s.configProvider = fn
	return s
}

// resolveConfig returns the effective listen address, API key, and
// require_mcp_auth: live from the provider when one is installed, otherwise the
// startup snapshot. All credential/URL construction routes through here so a
// runtime toggle is honored consistently (Spec 078).
func (s *Service) resolveConfig() (listenAddr, apiKey string, requireMCPAuth bool) {
	if s.configProvider != nil {
		return s.configProvider()
	}
	return s.listenAddr, s.apiKey, s.requireMCPAuth
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

// baseURL builds the credential-free MCPProxy MCP endpoint URL. This is the
// anchor used both to construct client entries and to recognize existing ones
// (with or without a trailing ?apikey= query), so matching works across the
// pre- and post-Spec-078 entry shapes.
func (s *Service) baseURL() string {
	addr, _, _ := s.resolveConfig()
	// If listen address starts with ":" (no host), default to localhost
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	return fmt.Sprintf("http://%s/mcp", addr)
}

// serverEntryParams carries everything buildServerEntry needs. credential is the
// value to embed in the entry (as an X-API-Key header, a --header bridge arg, or
// an ?apikey= query, per client). It is empty when no credential should be
// written (require_mcp_auth off, or no key) and may hold the mask token in a
// preview.
type serverEntryParams struct {
	baseURL    string
	credential string
}

// entryParams resolves the credential to embed. When require_mcp_auth is off, or
// no API key is set, credential stays empty so connect writes a clean, keyless
// entry. When masked is true the real key is replaced with the display mask for
// previews (the real key never leaves the core in a preview payload).
func (s *Service) entryParams(masked bool) serverEntryParams {
	_, apiKey, requireMCPAuth := s.resolveConfig()
	cred := ""
	if requireMCPAuth && apiKey != "" {
		cred = apiKey
		if masked {
			cred = apiKeyMask
		}
	}
	return serverEntryParams{baseURL: s.baseURL(), credential: cred}
}

// containsCredential reports whether connect will write a credential into the
// client config for the current configuration.
func (s *Service) containsCredential() bool {
	_, apiKey, requireMCPAuth := s.resolveConfig()
	return requireMCPAuth && apiKey != ""
}

// credentialQuery appends the credential as an ?apikey= query to base, for the
// clients whose config cannot express an HTTP header. The real key is
// URL-escaped; the display mask is left literal so the preview renders cleanly.
func credentialQuery(base, credential string) string {
	if credential == "" {
		return base
	}
	v := credential
	if credential != apiKeyMask {
		v = url.QueryEscape(credential)
	}
	return base + "?apikey=" + v
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
		cfgPath := s.configPath(c.ID)
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

	cfgPath := s.configPath(c.ID)
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
	switch {
	case outcome == accessAccessible && found:
		status.Connected = true
		status.ServerName = name
	case outcome == accessDenied:
		// A macOS App-Data block must surface as actionable remediation, not as
		// a plain "not connected" (Spec 075 FR-004).
		status.Remediation = remediationText(c.Name)
	}
	return status, nil
}

// entryAccess reads the client config exactly once via the seam, then reports
// the registered server name (if any), whether mcpproxy is connected, and the
// access outcome classified strictly from the error class (Spec 075 FR-011):
// a read error maps to absent/denied/malformed via classifyAccess, and a parse
// failure on otherwise-readable bytes maps to malformed.
func (s *Service) entryAccess(client ClientDef, cfgPath string) (name string, found bool, outcome string) {
	raw, err := s.read(cfgPath)
	if err != nil {
		return "", false, classifyAccess(err)
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

	cfgPath := s.configPath(clientID)
	if cfgPath == "" {
		return nil, fmt.Errorf("cannot determine config path for %s", clientID)
	}
	if client.ID == "opencode" {
		// configPath already prefers whichever candidate exists; reaching a
		// nonexistent path here means NO OpenCode global config was found.
		if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"no OpenCode config found (looked for opencode.jsonc and opencode.json in %s) — is OpenCode installed?",
				filepath.Dir(cfgPath))
		}
	}

	var res *ConnectResult
	var err error
	if client.Format == "toml" {
		res, err = s.connectTOML(client, cfgPath, serverName, force)
	} else {
		res, err = s.connectJSON(client, cfgPath, serverName, force)
	}
	// A permission denial anywhere in the read/backup/write chain (the errors
	// preserve their OS cause via %w) surfaces as a typed *AccessError with
	// remediation; other errors keep their existing semantics (Spec 075 FR-004).
	return res, s.asAccessError(client, cfgPath, err)
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

	cfgPath := s.configPath(clientID)
	if cfgPath == "" {
		return nil, fmt.Errorf("cannot determine config path for %s", clientID)
	}

	var res *ConnectResult
	var err error
	if client.Format == "toml" {
		res, err = s.disconnectTOML(client, cfgPath, serverName)
	} else {
		res, err = s.disconnectJSON(client, cfgPath, serverName)
	}
	// OpenCode candidate drift (#922): if the entry was written to opencode.json
	// and OpenCode later bootstrapped opencode.jsonc, the resolver now targets
	// the .jsonc — but the entry still lives in the .json. When the resolved
	// file has no entry, retry the other existing candidate before giving up.
	if err == nil && res != nil && !res.Success && res.Action == "not_found" && clientID == "opencode" {
		for _, alt := range opencodeConfigCandidates(s.homeDir) {
			if alt == cfgPath {
				continue
			}
			if _, statErr := os.Stat(alt); statErr != nil {
				continue
			}
			altRes, altErr := s.disconnectJSON(client, alt, serverName)
			if altErr != nil {
				// An unreadable/malformed/comment-guarded alternate is a real
				// failure — surfacing "not_found" would wrongly claim the entry
				// is absent when we could not actually check the file.
				return altRes, s.asAccessError(client, alt, altErr)
			}
			if altRes != nil && altRes.Success {
				return altRes, nil
			}
		}
	}
	return res, s.asAccessError(client, cfgPath, err)
}

// ---------- JSON helpers ----------

// connectJSON adds or updates the mcpproxy entry in a JSON config file.
// guardJsoncComments refuses to rewrite a .jsonc file that actually uses
// comments: mcpproxy re-serializes plain JSON, which would silently strip them
// (#922). Comment-free .jsonc (OpenCode's bootstrap stub) rewrites safely.
// Absent or unreadable files pass — the normal read/write path handles those.
func (s *Service) guardJsoncComments(cfgPath string) error {
	if !strings.HasSuffix(cfgPath, ".jsonc") {
		return nil
	}
	raw, err := s.read(cfgPath)
	if err != nil {
		return nil
	}
	if jsonHasComments(raw) {
		return fmt.Errorf(
			"%s contains comments, which mcpproxy would strip on rewrite — edit the \"mcp\" section manually or remove the comments and retry",
			cfgPath)
	}
	return nil
}

func (s *Service) connectJSON(client *ClientDef, cfgPath, serverName string, force bool) (*ConnectResult, error) {
	if err := s.guardJsoncComments(cfgPath); err != nil {
		return nil, err
	}
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
		if adoptedName, found := findEquivalentJSONServerName(serversMap, s.baseURL(), serverName); found && adoptedName != serverName {
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

	// Build the entry from the credential-aware params (no credential unless
	// require_mcp_auth is on).
	entry := buildServerEntry(client.ID, s.entryParams(false))
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
	if err := s.guardJsoncComments(cfgPath); err != nil {
		return nil, err
	}
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
func (s *Service) connectTOML(client *ClientDef, cfgPath, serverName string, force bool) (*ConnectResult, error) {
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

	// Build Codex entry via the shared constructor so what connect writes is
	// exactly what preview renders (Spec 078 FR-002).
	entry := buildServerEntry(client.ID, s.entryParams(false))
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

func findEquivalentJSONServerName(serversMap map[string]interface{}, baseURL, requestedServerName string) (string, bool) {
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
			// Match both a clean base URL and a legacy ?apikey= variant so an
			// upgrade adopts/updates the existing entry rather than duplicating.
			if entryURL == baseURL || strings.HasPrefix(entryURL, baseURL+"?") {
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

	// Anchor on the credential-free base URL so both new clean entries and
	// legacy entries carrying a ?apikey= query are recognized (Spec 078).
	baseURL := s.baseURL()

	for name, v := range serversMap {
		entry, ok := v.(map[string]interface{})
		if !ok {
			continue
		}

		// Check various URL fields used by different clients
		for _, field := range []string{"url", "serverUrl", "httpUrl"} {
			if u, ok := entry[field].(string); ok {
				if u == baseURL || strings.HasPrefix(u, baseURL+"?") {
					return name, true, true
				}
			}
		}

		// Stdio-bridge clients (e.g. Claude Desktop) have no URL field; the
		// mcpproxy endpoint lives in the command args. Detect by inspecting
		// args so a bridge written under a custom server name is still found.
		if entryPointsToBridge(entry, baseURL) {
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
// stdio bridge targeting our MCP endpoint, regardless of the entry key. Matches
// both the clean base URL (new entries) and a legacy ?apikey= variant.
func entryPointsToBridge(entry map[string]interface{}, baseURL string) bool {
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
		if s == baseURL || strings.HasPrefix(s, baseURL+"?") {
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
	// JSONC tolerance (#922): OpenCode bootstraps opencode.jsonc, which may
	// carry // and /* */ comments plus trailing commas. Strip comments first —
	// comment removal can expose new trailing commas — then clean commas.
	cleaned, cerr := stripJSONComments(raw)
	if cerr != nil {
		return cerr
	}
	cleaned = trailingCommaPattern.ReplaceAll(cleaned, []byte(`$1`))
	return json.Unmarshal(cleaned, out)
}

// stripJSONComments removes // line and /* */ block comments from JSONC input,
// string-aware so slashes inside JSON strings survive. Comment bytes are
// replaced with spaces (newlines kept) to preserve offsets for error messages.
// An unterminated /* block is an error — silently blanking to EOF would make
// truncated/malformed files parse as valid.
func stripJSONComments(raw []byte) ([]byte, error) {
	out := make([]byte, len(raw))
	copy(out, raw)
	inString := false
	escaped := false
	for i := 0; i < len(out); i++ {
		c := out[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
		case c == '/' && i+1 < len(out) && out[i+1] == '/':
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
		case c == '/' && i+1 < len(out) && out[i+1] == '*':
			out[i], out[i+1] = ' ', ' '
			i += 2
			closed := false
			for i < len(out) {
				if out[i] == '*' && i+1 < len(out) && out[i+1] == '/' {
					out[i], out[i+1] = ' ', ' '
					i++
					closed = true
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated /* block comment")
			}
		}
	}
	return out, nil
}

// jsonHasComments reports whether stripping comments would change the content —
// i.e. the file actually uses JSONC comments (not just slashes inside strings).
// An unterminated block comment counts as commented (the write guard refuses).
func jsonHasComments(raw []byte) bool {
	stripped, err := stripJSONComments(raw)
	return err != nil || !bytes.Equal(stripped, raw)
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

	baseURL := s.baseURL()

	for name, v := range serversMap {
		entry, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if u, ok := entry["url"].(string); ok {
			if u == baseURL || strings.HasPrefix(u, baseURL+"?") {
				return name, true, true
			}
		}
		if name == defaultServerName {
			return name, true, true
		}
	}

	return "", false, true
}
