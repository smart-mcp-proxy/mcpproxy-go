package storage

import (
	"bytes"
	"container/heap"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"

	"go.etcd.io/bbolt"
	bboltErrors "go.etcd.io/bbolt/errors"
	"go.uber.org/zap"
)

// Manager provides a unified interface for storage operations
type Manager struct {
	db       *BoltDB
	mu       sync.RWMutex
	logger   *zap.SugaredLogger
	asyncMgr *AsyncManager
}

// NewManager creates a new storage manager
func NewManager(dataDir string, logger *zap.SugaredLogger) (*Manager, error) {
	db, err := NewBoltDB(dataDir, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create bolt database: %w", err)
	}

	asyncMgr := NewAsyncManager(db, logger)
	asyncMgr.Start()

	return &Manager{
		db:       db,
		logger:   logger,
		asyncMgr: asyncMgr,
	}, nil
}

// StopAsync stops the async operation manager, draining any queued
// operations to the database. It is idempotent: a second call (including
// the one inside Close) is a no-op, so callers may StopAsync then Close.
//
// Use this when a final DB write must happen strictly AFTER all queued
// async operations have flushed but BEFORE the DB handle closes — e.g.
// the telemetry shutdown marker (Spec 080 FR-010): StopAsync, write the
// marker, then Close, which then closes the DB with no intervening work.
func (m *Manager) StopAsync() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.asyncMgr != nil {
		m.asyncMgr.Stop()
	}
}

// Close closes the storage manager: it stops the async manager (draining
// queued operations to the DB — a no-op if StopAsync already ran), then
// closes the underlying BBolt database.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop async manager first to ensure all operations complete
	if m.asyncMgr != nil {
		m.asyncMgr.Stop()
	}

	if m.db != nil {
		return m.db.Close()
	}
	return nil
}

// GetDB returns the underlying BBolt database for direct access
func (m *Manager) GetDB() *bbolt.DB {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.db != nil {
		return m.db.db
	}
	return nil
}

// GetBoltDB returns the wrapped BoltDB instance for higher-level operations
func (m *Manager) GetBoltDB() *BoltDB {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.db
}

// Upstream operations

// SaveUpstreamServer saves an upstream server configuration
func (m *Manager) SaveUpstreamServer(serverConfig *config.ServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	record := &UpstreamRecord{
		ID:                     serverConfig.Name, // Use name as ID for simplicity
		Name:                   serverConfig.Name,
		URL:                    serverConfig.URL,
		Protocol:               serverConfig.Protocol,
		Command:                serverConfig.Command,
		Args:                   serverConfig.Args,
		WorkingDir:             serverConfig.WorkingDir,
		Env:                    serverConfig.Env,
		Headers:                serverConfig.Headers,
		OAuth:                  serverConfig.OAuth,
		Enabled:                serverConfig.Enabled,
		Quarantined:            serverConfig.Quarantined,
		Created:                serverConfig.Created,
		Updated:                time.Now(),
		Isolation:              serverConfig.Isolation,
		ReconnectOnUse:         serverConfig.ReconnectOnUse,
		AutoApproveToolChanges: serverConfig.AutoApproveToolChanges,
		TrustMode:              serverConfig.TrustMode,
		LauncherWaitTimeout:    serverConfig.LauncherWaitTimeout,
		EnabledTools:           serverConfig.EnabledTools,
		DisabledTools:          serverConfig.DisabledTools,

		SourceRegistryID:         serverConfig.SourceRegistryID,
		SourceRegistryProvenance: serverConfig.SourceRegistryProvenance,
		HealthCheckInterval:      serverConfig.HealthCheckInterval,
		ToolDiscoveryInterval:    serverConfig.ToolDiscoveryInterval,
		InitTimeout:              serverConfig.InitTimeout,
		ToonOutput:               serverConfig.ToonOutput,
	}

	return m.db.SaveUpstream(record)
}

// GetUpstreamServer retrieves an upstream server by name
func (m *Manager) GetUpstreamServer(name string) (*config.ServerConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	record, err := m.db.GetUpstream(name)
	if err != nil {
		return nil, err
	}

	return &config.ServerConfig{
		Name:                   record.Name,
		URL:                    record.URL,
		Protocol:               record.Protocol,
		Command:                record.Command,
		Args:                   record.Args,
		WorkingDir:             record.WorkingDir,
		Env:                    record.Env,
		Headers:                record.Headers,
		OAuth:                  record.OAuth,
		Enabled:                record.Enabled,
		Quarantined:            record.Quarantined,
		Created:                record.Created,
		Updated:                record.Updated,
		Isolation:              record.Isolation,
		ReconnectOnUse:         record.ReconnectOnUse,
		AutoApproveToolChanges: record.AutoApproveToolChanges,
		TrustMode:              record.TrustMode,
		LauncherWaitTimeout:    record.LauncherWaitTimeout,
		EnabledTools:           record.EnabledTools,
		DisabledTools:          record.DisabledTools,

		SourceRegistryID:         record.SourceRegistryID,
		SourceRegistryProvenance: record.SourceRegistryProvenance,
		HealthCheckInterval:      record.HealthCheckInterval,
		ToolDiscoveryInterval:    record.ToolDiscoveryInterval,
		InitTimeout:              record.InitTimeout,
		ToonOutput:               record.ToonOutput,
	}, nil
}

// ListUpstreamServers returns all upstream servers
func (m *Manager) ListUpstreamServers() ([]*config.ServerConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	records, err := m.db.ListUpstreams()
	if err != nil {
		return nil, err
	}

	var servers []*config.ServerConfig
	for _, record := range records {
		servers = append(servers, &config.ServerConfig{
			Name:                   record.Name,
			URL:                    record.URL,
			Protocol:               record.Protocol,
			Command:                record.Command,
			Args:                   record.Args,
			WorkingDir:             record.WorkingDir,
			Env:                    record.Env,
			Headers:                record.Headers,
			OAuth:                  record.OAuth,
			Enabled:                record.Enabled,
			Quarantined:            record.Quarantined,
			Created:                record.Created,
			Updated:                record.Updated,
			Isolation:              record.Isolation,
			ReconnectOnUse:         record.ReconnectOnUse,
			AutoApproveToolChanges: record.AutoApproveToolChanges,
			TrustMode:              record.TrustMode,
			LauncherWaitTimeout:    record.LauncherWaitTimeout,
			EnabledTools:           record.EnabledTools,
			DisabledTools:          record.DisabledTools,

			SourceRegistryID:         record.SourceRegistryID,
			SourceRegistryProvenance: record.SourceRegistryProvenance,
			HealthCheckInterval:      record.HealthCheckInterval,
			ToolDiscoveryInterval:    record.ToolDiscoveryInterval,
			InitTimeout:              record.InitTimeout,
			ToonOutput:               record.ToonOutput,
		})
	}

	return servers, nil
}

// ListQuarantinedUpstreamServers returns all quarantined upstream servers
func (m *Manager) ListQuarantinedUpstreamServers() ([]*config.ServerConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.logger.Debug("ListQuarantinedUpstreamServers called")

	records, err := m.db.ListUpstreams()
	if err != nil {
		m.logger.Errorw("Failed to list upstreams for quarantine filtering",
			"error", err)
		return nil, err
	}

	m.logger.Debugw("Retrieved all upstream records for quarantine filtering",
		"total_records", len(records))

	var quarantinedServers []*config.ServerConfig
	for _, record := range records {
		m.logger.Debugw("Checking server quarantine status",
			"server", record.Name,
			"quarantined", record.Quarantined,
			"enabled", record.Enabled)

		if record.Quarantined {
			quarantinedServers = append(quarantinedServers, &config.ServerConfig{
				Name:          record.Name,
				URL:           record.URL,
				Protocol:      record.Protocol,
				Command:       record.Command,
				Args:          record.Args,
				WorkingDir:    record.WorkingDir,
				Env:           record.Env,
				Headers:       record.Headers,
				OAuth:         record.OAuth,
				Enabled:       record.Enabled,
				Quarantined:   record.Quarantined,
				Created:       record.Created,
				Updated:       record.Updated,
				Isolation:     record.Isolation,
				EnabledTools:  record.EnabledTools,
				DisabledTools: record.DisabledTools,

				SourceRegistryID:         record.SourceRegistryID,
				SourceRegistryProvenance: record.SourceRegistryProvenance,
			})

			m.logger.Debugw("Added server to quarantined list",
				"server", record.Name,
				"total_quarantined_so_far", len(quarantinedServers))
		}
	}

	m.logger.Debugw("ListQuarantinedUpstreamServers completed",
		"total_quarantined", len(quarantinedServers))

	return quarantinedServers, nil
}

// ListQuarantinedTools returns tools from quarantined servers with full descriptions for security analysis
func (m *Manager) ListQuarantinedTools(serverName string) ([]map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Check if server is quarantined
	server, err := m.GetUpstreamServer(serverName)
	if err != nil {
		return nil, err
	}

	if !server.Quarantined {
		return nil, fmt.Errorf("server '%s' is not quarantined", serverName)
	}

	// Return placeholder for now - actual implementation would need to connect to server
	// and retrieve tools with full descriptions for security analysis
	// TODO: This should connect to the upstream server and return actual tool descriptions
	// for security analysis, but currently we only return placeholder information
	tools := []map[string]interface{}{
		{
			"message":        fmt.Sprintf("Server '%s' is quarantined. The actual tool descriptions should be retrieved from the upstream manager for security analysis.", serverName),
			"server":         serverName,
			"status":         "quarantined",
			"implementation": "PLACEHOLDER",
			"next_steps":     "The upstream manager should be used to connect to this server and retrieve actual tool descriptions with full schemas for LLM security analysis",
			"security_note":  "Real implementation needs to: 1) Connect to quarantined server, 2) Retrieve all tools with descriptions, 3) Include input schemas, 4) Add security analysis prompts, 5) Return quoted tool descriptions for LLM inspection",
		},
	}

	return tools, nil
}

// DeleteUpstreamServer deletes an upstream server
func (m *Manager) DeleteUpstreamServer(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.DeleteUpstream(name)
}

// EnableUpstreamServer enables/disables an upstream server using async operations
func (m *Manager) EnableUpstreamServer(name string, enabled bool) error {
	// Use async manager to avoid deadlocks
	return m.asyncMgr.EnableServerSync(name, enabled)
}

// QuarantineUpstreamServer sets the quarantine status of an upstream server using async operations
func (m *Manager) QuarantineUpstreamServer(name string, quarantined bool) error {
	m.logger.Debugw("QuarantineUpstreamServer called",
		"server", name,
		"quarantined", quarantined)

	// Use async manager to avoid deadlocks
	err := m.asyncMgr.QuarantineServerSync(name, quarantined)
	if err != nil {
		m.logger.Errorw("Failed to quarantine server via async manager",
			"server", name,
			"quarantined", quarantined,
			"error", err)
		return err
	}

	m.logger.Debugw("Successfully queued quarantine operation",
		"server", name,
		"quarantined", quarantined)

	return nil
}

// Tool statistics operations

// IncrementToolUsage increments the usage count for a tool
func (m *Manager) IncrementToolUsage(toolName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.logger.Debugf("Incrementing usage for tool: %s", toolName)
	return m.db.IncrementToolStats(toolName)
}

// GetToolUsage retrieves usage statistics for a tool
func (m *Manager) GetToolUsage(toolName string) (*ToolStatRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.GetToolStats(toolName)
}

// GetToolStatistics returns aggregated tool statistics
func (m *Manager) GetToolStatistics(topN int) (*config.ToolStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	records, err := m.db.ListToolStats()
	if err != nil {
		return nil, err
	}

	// Sort by usage count (descending)
	sort.Slice(records, func(i, j int) bool {
		return records[i].Count > records[j].Count
	})

	// Limit to topN
	if topN > 0 && len(records) > topN {
		records = records[:topN]
	}

	// Convert to config format
	var topTools []config.ToolStatEntry
	for _, record := range records {
		topTools = append(topTools, config.ToolStatEntry{
			ToolName: record.ToolName,
			Count:    record.Count,
		})
	}

	return &config.ToolStats{
		TotalTools: len(records),
		TopTools:   topTools,
	}, nil
}

// Tool hash operations

// SaveToolHash saves a tool hash for change detection
func (m *Manager) SaveToolHash(toolName, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.SaveToolHash(toolName, hash)
}

// GetToolHash retrieves a tool hash
func (m *Manager) GetToolHash(toolName string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.GetToolHash(toolName)
}

// HasToolChanged checks if a tool has changed based on its hash
func (m *Manager) HasToolChanged(toolName, currentHash string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	storedHash, err := m.db.GetToolHash(toolName)
	if err != nil {
		// If hash doesn't exist, consider it changed (new tool)
		return true, nil
	}

	return storedHash != currentHash, nil
}

// DeleteToolHash deletes a tool hash
func (m *Manager) DeleteToolHash(toolName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.DeleteToolHash(toolName)
}

// Tool approval operations (tool-level quarantine)

// SaveToolApproval saves a tool approval record
func (m *Manager) SaveToolApproval(record *ToolApprovalRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.SaveToolApproval(record)
}

// GetToolApproval retrieves a tool approval record by server and tool name
func (m *Manager) GetToolApproval(serverName, toolName string) (*ToolApprovalRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.GetToolApproval(serverName, toolName)
}

// ListToolApprovals returns all tool approval records for a server.
// If serverName is empty, returns all records across all servers.
func (m *Manager) ListToolApprovals(serverName string) ([]*ToolApprovalRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.ListToolApprovals(serverName)
}

// DeleteToolApproval deletes a tool approval record
func (m *Manager) DeleteToolApproval(serverName, toolName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.DeleteToolApproval(serverName, toolName)
}

// DeleteServerToolApprovals deletes all tool approval records for a server
func (m *Manager) DeleteServerToolApprovals(serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.DeleteServerToolApprovals(serverName)
}

// PruneOrphanToolApprovals removes tool-approval records for servers that are
// no longer in the configured set, returning the number removed. Configured
// servers (even disabled ones) are preserved so re-enabling never re-quarantines
// previously-approved tools (MCP-1002).
func (m *Manager) PruneOrphanToolApprovals(configuredServers []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	keep := make(map[string]bool, len(configuredServers))
	for _, name := range configuredServers {
		keep[name] = true
	}
	return m.db.PruneToolApprovalsNotIn(keep)
}

// Security Scanner methods (Spec 039)

// SaveScanner saves a scanner plugin record
func (m *Manager) SaveScanner(s *scanner.ScannerPlugin) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.SaveScanner(s)
}

// GetScanner retrieves a scanner plugin by ID
func (m *Manager) GetScanner(id string) (*scanner.ScannerPlugin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.GetScanner(id)
}

// ListScanners returns all scanner plugin records
func (m *Manager) ListScanners() ([]*scanner.ScannerPlugin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.ListScanners()
}

// DeleteScanner deletes a scanner plugin by ID
func (m *Manager) DeleteScanner(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.DeleteScanner(id)
}

// SaveScanJob saves a scan job record
func (m *Manager) SaveScanJob(job *scanner.ScanJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.SaveScanJob(job)
}

// GetScanJob retrieves a scan job by ID
func (m *Manager) GetScanJob(id string) (*scanner.ScanJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.GetScanJob(id)
}

// ListScanJobs returns scan jobs, optionally filtered by server name
func (m *Manager) ListScanJobs(serverName string) ([]*scanner.ScanJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.ListScanJobs(serverName)
}

// ListScanJobMetas returns lightweight scan-job metadata, optionally filtered by
// server name (MCP-2205).
func (m *Manager) ListScanJobMetas(serverName string) ([]*scanner.ScanJobMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.ListScanJobMetas(serverName)
}

// GetLatestScanJob returns the most recent scan job for a server
func (m *Manager) GetLatestScanJob(serverName string) (*scanner.ScanJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.GetLatestScanJob(serverName)
}

// DeleteScanJob deletes a scan job by ID
func (m *Manager) DeleteScanJob(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.DeleteScanJob(id)
}

// DeleteServerScanJobs deletes all scan jobs for a server
func (m *Manager) DeleteServerScanJobs(serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.DeleteServerScanJobs(serverName)
}

// SaveScanReport saves a scan report record
func (m *Manager) SaveScanReport(report *scanner.ScanReport) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.SaveScanReport(report)
}

// GetScanReport retrieves a scan report by ID
func (m *Manager) GetScanReport(id string) (*scanner.ScanReport, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.GetScanReport(id)
}

// ListScanReports returns scan reports, optionally filtered by server name
func (m *Manager) ListScanReports(serverName string) ([]*scanner.ScanReport, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.ListScanReports(serverName)
}

// ListScanReportsByJob returns all scan reports for a specific scan job
func (m *Manager) ListScanReportsByJob(jobID string) ([]*scanner.ScanReport, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.ListScanReportsByJob(jobID)
}

// DeleteScanReport deletes a scan report by ID
func (m *Manager) DeleteScanReport(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.DeleteScanReport(id)
}

// DeleteServerScanReports deletes all scan reports for a server
func (m *Manager) DeleteServerScanReports(serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.DeleteServerScanReports(serverName)
}

// SaveIntegrityBaseline saves an integrity baseline record
func (m *Manager) SaveIntegrityBaseline(baseline *scanner.IntegrityBaseline) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.SaveIntegrityBaseline(baseline)
}

// GetIntegrityBaseline retrieves an integrity baseline by server name
func (m *Manager) GetIntegrityBaseline(serverName string) (*scanner.IntegrityBaseline, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.GetIntegrityBaseline(serverName)
}

// DeleteIntegrityBaseline deletes an integrity baseline by server name
func (m *Manager) DeleteIntegrityBaseline(serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.DeleteIntegrityBaseline(serverName)
}

// ListIntegrityBaselines returns all integrity baseline records
func (m *Manager) ListIntegrityBaselines() ([]*scanner.IntegrityBaseline, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.ListIntegrityBaselines()
}

// Docker recovery state operations

// SaveDockerRecoveryState saves the Docker recovery state to persistent storage
func (m *Manager) SaveDockerRecoveryState(state *DockerRecoveryState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(MetaBucket))
		if err != nil {
			return fmt.Errorf("failed to create meta bucket: %w", err)
		}

		data, err := state.MarshalBinary()
		if err != nil {
			return fmt.Errorf("failed to marshal recovery state: %w", err)
		}

		return bucket.Put([]byte(DockerRecoveryStateKey), data)
	})
}

// LoadDockerRecoveryState loads the Docker recovery state from persistent storage
func (m *Manager) LoadDockerRecoveryState() (*DockerRecoveryState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var state DockerRecoveryState

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(MetaBucket))
		if bucket == nil {
			return bboltErrors.ErrBucketNotFound
		}

		data := bucket.Get([]byte(DockerRecoveryStateKey))
		if data == nil {
			return bboltErrors.ErrBucketNotFound
		}

		return state.UnmarshalBinary(data)
	})

	if err != nil {
		if err == bboltErrors.ErrBucketNotFound {
			// No state exists yet, return nil without error
			return nil, nil
		}
		return nil, fmt.Errorf("failed to load recovery state: %w", err)
	}

	return &state, nil
}

// ClearDockerRecoveryState removes the Docker recovery state from persistent storage
func (m *Manager) ClearDockerRecoveryState() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(MetaBucket))
		if bucket == nil {
			// No bucket, nothing to clear
			return nil
		}

		return bucket.Delete([]byte(DockerRecoveryStateKey))
	})
}

// Maintenance operations

// Backup creates a backup of the database
func (m *Manager) Backup(destPath string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.Backup(destPath)
}

// GetSchemaVersion returns the current schema version
func (m *Manager) GetSchemaVersion() (uint64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.GetSchemaVersion()
}

// SetSchemaVersion stores the current migration schema version.
func (m *Manager) SetSchemaVersion(version uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.SetSchemaVersion(version)
}

// GetStats returns storage statistics
func (m *Manager) GetStats() (map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return map[string]interface{}{
		"upstreams": "managed",
		"tools":     "indexed",
	}, nil
}

// Alias methods for compatibility with MCP server expectations

// ListUpstreams is an alias for ListUpstreamServers
func (m *Manager) ListUpstreams() ([]*config.ServerConfig, error) {
	return m.ListUpstreamServers()
}

// AddUpstream adds an upstream server and returns its ID
func (m *Manager) AddUpstream(serverConfig *config.ServerConfig) (string, error) {
	err := m.SaveUpstreamServer(serverConfig)
	if err != nil {
		return "", err
	}
	return serverConfig.Name, nil // Use name as ID
}

// RemoveUpstream removes an upstream server by ID/name
func (m *Manager) RemoveUpstream(id string) error {
	return m.DeleteUpstreamServer(id)
}

// UpdateUpstream updates an upstream server configuration
func (m *Manager) UpdateUpstream(id string, serverConfig *config.ServerConfig) error {
	// Ensure the ID matches the name
	serverConfig.Name = id
	return m.SaveUpstreamServer(serverConfig)
}

// GetToolStats gets tool statistics formatted for MCP responses
func (m *Manager) GetToolStats(topN int) ([]map[string]interface{}, error) {
	stats, err := m.GetToolStatistics(topN)
	if err != nil {
		return nil, err
	}

	var result []map[string]interface{}
	for _, tool := range stats.TopTools {
		result = append(result, map[string]interface{}{
			"tool_name": tool.ToolName,
			"count":     tool.Count,
		})
	}

	return result, nil
}

// Server Identity Management

// RegisterServerIdentity registers or updates a server identity
func (m *Manager) RegisterServerIdentity(server *config.ServerConfig, configPath string) (*ServerIdentity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	serverID := GenerateServerID(server)

	// Try to get existing identity
	identity, err := m.getServerIdentityByID(serverID)
	if err != nil && err != bboltErrors.ErrBucketNotFound {
		return nil, fmt.Errorf("failed to get server identity: %w", err)
	}

	if identity == nil {
		// Create new identity
		identity = NewServerIdentity(server, configPath)
		m.logger.Debugw("Created new server identity",
			"server_name", server.Name,
			"server_id", serverID,
			"fingerprint", identity.Fingerprint,
			"config_path", configPath)
	} else {
		// Update existing identity
		identity.UpdateLastSeen(configPath)
		m.logger.Debugw("Updated existing server identity",
			"server_name", server.Name,
			"server_id", serverID,
			"config_path", configPath)
	}

	// Save identity
	err = m.saveServerIdentity(identity)
	if err != nil {
		return nil, fmt.Errorf("failed to save server identity: %w", err)
	}

	return identity, nil
}

// GetServerIdentity gets server identity by config
func (m *Manager) GetServerIdentity(server *config.ServerConfig) (*ServerIdentity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	serverID := GenerateServerID(server)
	return m.getServerIdentityByID(serverID)
}

// GetServerIdentityByID gets server identity by ID
func (m *Manager) GetServerIdentityByID(serverID string) (*ServerIdentity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.getServerIdentityByID(serverID)
}

// ListServerIdentities lists all server identities
func (m *Manager) ListServerIdentities() ([]*ServerIdentity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var identities []*ServerIdentity

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("server_identities"))
		if bucket == nil {
			return nil // No identities yet
		}

		return bucket.ForEach(func(k, v []byte) error {
			var identity ServerIdentity
			if err := json.Unmarshal(v, &identity); err != nil {
				m.logger.Warnw("Failed to unmarshal server identity", "key", string(k), "error", err)
				return nil // Skip malformed records
			}
			identities = append(identities, &identity)
			return nil
		})
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list server identities: %w", err)
	}

	return identities, nil
}

// RecordToolCall records a tool call for a server
func (m *Manager) RecordToolCall(record *ToolCallRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bucketName := fmt.Sprintf("server_%s_tool_calls", record.ServerID)
	key := fmt.Sprintf("%d_%s", record.Timestamp.UnixNano(), record.ID)

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(bucketName))
		if err != nil {
			return err
		}

		data, err := json.Marshal(record)
		if err != nil {
			return err
		}

		return bucket.Put([]byte(key), data)
	})
}

// GetServerToolCalls gets tool calls for a server
func (m *Manager) GetServerToolCalls(serverID string, limit int) ([]*ToolCallRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var records []*ToolCallRecord
	bucketName := fmt.Sprintf("server_%s_tool_calls", serverID)

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		if bucket == nil {
			return nil // No calls yet
		}

		// Get keys in reverse order (most recent first)
		cursor := bucket.Cursor()
		count := 0
		for k, v := cursor.Last(); k != nil && count < limit; k, v = cursor.Prev() {
			var record ToolCallRecord
			if err := json.Unmarshal(v, &record); err != nil {
				m.logger.Warnw("Failed to unmarshal tool call record", "key", string(k), "error", err)
				continue
			}
			records = append(records, &record)
			count++
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get server tool calls: %w", err)
	}

	return records, nil
}

// RecordServerDiagnostic records a diagnostic event for a server
func (m *Manager) RecordServerDiagnostic(record *DiagnosticRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bucketName := fmt.Sprintf("server_%s_diagnostics", record.ServerID)
	key := fmt.Sprintf("%d_%s_%s", record.Timestamp.UnixNano(), record.Type, record.Category)

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(bucketName))
		if err != nil {
			return err
		}

		data, err := json.Marshal(record)
		if err != nil {
			return err
		}

		return bucket.Put([]byte(key), data)
	})
}

// GetServerDiagnostics gets diagnostic records for a server
func (m *Manager) GetServerDiagnostics(serverID string, limit int) ([]*DiagnosticRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var records []*DiagnosticRecord
	bucketName := fmt.Sprintf("server_%s_diagnostics", serverID)

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		if bucket == nil {
			return nil // No diagnostics yet
		}

		// Get keys in reverse order (most recent first)
		cursor := bucket.Cursor()
		count := 0
		for k, v := cursor.Last(); k != nil && count < limit; k, v = cursor.Prev() {
			var record DiagnosticRecord
			if err := json.Unmarshal(v, &record); err != nil {
				m.logger.Warnw("Failed to unmarshal diagnostic record", "key", string(k), "error", err)
				continue
			}
			records = append(records, &record)
			count++
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get server diagnostics: %w", err)
	}

	return records, nil
}

// UpdateServerStatistics updates server statistics
func (m *Manager) UpdateServerStatistics(stats *ServerStatistics) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bucketName := "server_statistics"
	key := stats.ServerID

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(bucketName))
		if err != nil {
			return err
		}

		stats.UpdatedAt = time.Now()
		data, err := json.Marshal(stats)
		if err != nil {
			return err
		}

		return bucket.Put([]byte(key), data)
	})
}

// GetServerStatistics gets statistics for a server
func (m *Manager) GetServerStatistics(serverID string) (*ServerStatistics, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var stats ServerStatistics
	bucketName := "server_statistics"

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		if bucket == nil {
			return nil // No stats yet
		}

		data := bucket.Get([]byte(serverID))
		if data == nil {
			return nil // No stats for this server
		}

		return json.Unmarshal(data, &stats)
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get server statistics: %w", err)
	}

	return &stats, nil
}

// CleanupStaleServerData removes data for servers that haven't been seen for a threshold period
func (m *Manager) CleanupStaleServerData(threshold time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	identities, err := m.ListServerIdentities()
	if err != nil {
		return fmt.Errorf("failed to list server identities: %w", err)
	}

	var staleServers []string
	for _, identity := range identities {
		if identity.IsStale(threshold) {
			staleServers = append(staleServers, identity.ID)
			m.logger.Infow("Found stale server for cleanup",
				"server_name", identity.ServerName,
				"server_id", identity.ID,
				"last_seen", identity.LastSeen)
		}
	}

	if len(staleServers) == 0 {
		return nil
	}

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		for _, serverID := range staleServers {
			// Remove server identity
			if bucket := tx.Bucket([]byte("server_identities")); bucket != nil {
				bucket.Delete([]byte(serverID))
			}

			// Remove tool calls
			toolCallsBucket := fmt.Sprintf("server_%s_tool_calls", serverID)
			tx.DeleteBucket([]byte(toolCallsBucket))

			// Remove diagnostics
			diagnosticsBucket := fmt.Sprintf("server_%s_diagnostics", serverID)
			tx.DeleteBucket([]byte(diagnosticsBucket))

			// Remove statistics
			if bucket := tx.Bucket([]byte("server_statistics")); bucket != nil {
				bucket.Delete([]byte(serverID))
			}

			m.logger.Infow("Cleaned up stale server data", "server_id", serverID)
		}
		return nil
	})
}

// Private helper methods

func (m *Manager) getServerIdentityByID(serverID string) (*ServerIdentity, error) {
	var identity ServerIdentity

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("server_identities"))
		if bucket == nil {
			return bboltErrors.ErrBucketNotFound
		}

		data := bucket.Get([]byte(serverID))
		if data == nil {
			return bboltErrors.ErrBucketNotFound
		}

		return json.Unmarshal(data, &identity)
	})

	if err != nil {
		return nil, err
	}

	return &identity, nil
}

func (m *Manager) saveServerIdentity(identity *ServerIdentity) error {
	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte("server_identities"))
		if err != nil {
			return err
		}

		data, err := json.Marshal(identity)
		if err != nil {
			return err
		}

		return bucket.Put([]byte(identity.ID), data)
	})
}

// Session storage operations

// SessionRecord represents a stored MCP session
type SessionRecord struct {
	ID            string     `json:"id"`
	ClientName    string     `json:"client_name,omitempty"`
	ClientVersion string     `json:"client_version,omitempty"`
	Status        string     `json:"status"`
	StartTime     time.Time  `json:"start_time"`
	EndTime       *time.Time `json:"end_time,omitempty"`
	LastActivity  time.Time  `json:"last_activity"`
	ToolCallCount int        `json:"tool_call_count"`
	TotalTokens   int        `json:"total_tokens"`
	// MCP Client Capabilities
	HasRoots     bool     `json:"has_roots,omitempty"`    // Whether client supports roots
	HasSampling  bool     `json:"has_sampling,omitempty"` // Whether client supports sampling
	Experimental []string `json:"experimental,omitempty"` // Experimental capability names

	// Workspace / work session (Spec 082).
	//
	// WorkspaceRoot is the project the client is working in, fetched once from
	// the client's MCP roots. It is a LOCAL FILESYSTEM PATH: it stays on this
	// machine and is never sent to telemetry. WorkspaceName is its basename, and
	// is what the UI shows.
	//
	// WorkSessionID groups this connection with the other connections that make
	// up the same stretch of user work (see internal/runtime/worksession.go).
	// A client reconnecting every few minutes produces many session records that
	// all share one WorkSessionID.
	WorkspaceRoot string `json:"workspace_root,omitempty"`
	WorkspaceName string `json:"workspace_name,omitempty"`
	WorkSessionID string `json:"work_session_id,omitempty"`
}

// sessionRetentionLimit is the hard cap on stored session records. The cap is
// absolute: it holds even when every retained session is "active" (see
// enforceSessionRetention).
const sessionRetentionLimit = 100

// retentionDeleteBatch bounds how many victim keys retention holds at once.
// Deleting cannot happen while a cursor is traversing the bucket, so victims
// are collected in batches.
//
// This bounds the victim slice only. It does NOT bound peak memory for the
// trim — bbolt's own transaction state dominates that. See trimSessionsToLimit.
const retentionDeleteBatch = 512

// CreateSession creates a new session record
func (m *Manager) CreateSession(session *SessionRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(SessionsBucket))
		if err != nil {
			return fmt.Errorf("failed to create sessions bucket: %w", err)
		}

		// Check if session already exists - if so, update it
		var existingKey []byte
		var existingSession SessionRecord
		c := bucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			keyStr := string(k)
			// Check if key ends with _{session_id}
			if strings.HasSuffix(keyStr, "_"+session.ID) {
				existingKey = k
				if err := json.Unmarshal(v, &existingSession); err != nil {
					m.logger.Warnw("Failed to unmarshal existing session", "error", err)
					continue
				}
				// Merge new data with existing session (preserve certain fields)
				if session.ClientName != "" {
					existingSession.ClientName = session.ClientName
				}
				if session.ClientVersion != "" {
					existingSession.ClientVersion = session.ClientVersion
				}
				// Update capabilities
				existingSession.HasRoots = session.HasRoots
				existingSession.HasSampling = session.HasSampling
				existingSession.Experimental = session.Experimental
				session = &existingSession
				m.logger.Debugw("Updating existing session with new data", "session_id", session.ID, "client_name", session.ClientName)
				break
			}
		}

		data, err := json.Marshal(session)
		if err != nil {
			return fmt.Errorf("failed to marshal session: %w", err)
		}

		// Use existing key if found, otherwise create new key
		var key []byte
		if existingKey != nil {
			key = existingKey
		} else {
			// Key format: {timestamp_ns}_{session_id} for reverse chronological ordering
			keyStr := fmt.Sprintf("%d_%s", session.StartTime.UnixNano(), session.ID)
			key = []byte(keyStr)
			m.logger.Debugw("Creating new session", "session_id", session.ID, "client_name", session.ClientName)
		}

		if err := bucket.Put(key, data); err != nil {
			return fmt.Errorf("failed to store session: %w", err)
		}

		// Enforce retention limit only when creating new sessions
		if existingKey == nil {
			return m.enforceSessionRetention(bucket, sessionRetentionLimit)
		}
		return nil
	})
}

// CloseSession marks a session as closed with end time
func (m *Manager) CloseSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(SessionsBucket))
		if bucket == nil {
			return fmt.Errorf("sessions bucket not found")
		}

		// Find the session by iterating (session_id is in the key suffix)
		var sessionKey []byte
		var session SessionRecord

		c := bucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			// Key format: {timestamp_ns}_{session_id}
			keyStr := string(k)
			// Check if key ends with _{session_id}
			if strings.HasSuffix(keyStr, "_"+sessionID) {
				sessionKey = k
				if err := json.Unmarshal(v, &session); err != nil {
					return fmt.Errorf("failed to unmarshal session: %w", err)
				}
				break
			}
		}

		if sessionKey == nil {
			return fmt.Errorf("session not found: %s", sessionID)
		}

		// Update session status and end time
		now := time.Now()
		session.Status = "closed"
		session.EndTime = &now

		data, err := json.Marshal(session)
		if err != nil {
			return fmt.Errorf("failed to marshal session: %w", err)
		}

		m.logger.Debugw("Session closed", "session_id", sessionID)
		return bucket.Put(sessionKey, data)
	})
}

// GetRecentSessions returns the sessions that were active most recently.
//
// "Recent" means LastActivity, not StartTime. Bucket keys are
// "{startUnixNano}_{id}", so a cursor walk is start-time order — and a client
// that connected this morning and is still calling tools sits at the very
// bottom of it, one reconnect-happy neighbour away from being truncated out of
// the page. The tray would then say the busiest client on the machine is gone.
//
// So the walk collects every retained record first, sorts by LastActivity
// descending, and only then filters and truncates. Ordering before truncation
// is the whole guarantee (contracts/api-deltas.md §3); doing it in the caller
// can only reorder rows that already survived the cut. Session retention caps
// the bucket at 100 records (enforceSessionRetention), so the full scan and
// sort are bounded.
//
// status filters on SessionRecord.Status ("active" / "closed"); an empty string
// means no filtering. It is applied after the sort and before the limit, so an
// old-but-active session is never dropped by a page full of newer ones. The
// returned total counts the whole bucket when unfiltered, and every matching
// record when filtered.
func (m *Manager) GetRecentSessions(limit int, status string) ([]*SessionRecord, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var all []*SessionRecord

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(SessionsBucket))
		if bucket == nil {
			return nil // No sessions yet
		}

		all = make([]*SessionRecord, 0, bucket.Stats().KeyN)
		c := bucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var session SessionRecord
			if err := json.Unmarshal(v, &session); err != nil {
				m.logger.Warnw("Failed to unmarshal session", "error", err)
				continue
			}
			all = append(all, &session)
		}

		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	// Newest activity first. StartTime breaks ties so the order is stable for
	// records that share a LastActivity (or have none recorded at all).
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].LastActivity.Equal(all[j].LastActivity) {
			return all[i].LastActivity.After(all[j].LastActivity)
		}
		return all[i].StartTime.After(all[j].StartTime)
	})

	if status != "" {
		total := 0
		sessions := make([]*SessionRecord, 0, limit)
		for _, session := range all {
			if session.Status != status {
				continue
			}
			total++
			if len(sessions) < limit {
				sessions = append(sessions, session)
			}
		}
		return sessions, total, nil
	}

	if limit < len(all) {
		return all[:limit], len(all), nil
	}
	return all, len(all), nil
}

// GetSessionByID retrieves a session by its ID
func (m *Manager) GetSessionByID(sessionID string) (*SessionRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var session *SessionRecord

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(SessionsBucket))
		if bucket == nil {
			return fmt.Errorf("session not found: %s", sessionID)
		}

		// Find the session by iterating
		c := bucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			keyStr := string(k)
			// Check if key ends with _{session_id}
			if strings.HasSuffix(keyStr, "_"+sessionID) {
				var s SessionRecord
				if err := json.Unmarshal(v, &s); err != nil {
					return fmt.Errorf("failed to unmarshal session: %w", err)
				}
				session = &s
				return nil
			}
		}

		return fmt.Errorf("session not found: %s", sessionID)
	})

	return session, err
}

// CloseAllActiveSessions marks all active sessions as closed
// This should be called on startup to clean up stale sessions from previous runs
func (m *Manager) CloseAllActiveSessions() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(SessionsBucket))
		if bucket == nil {
			return nil // No sessions bucket yet
		}

		now := time.Now()
		var keysToUpdate [][]byte
		var sessionsToUpdate []SessionRecord

		// First pass: find all active sessions
		c := bucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var session SessionRecord
			if err := json.Unmarshal(v, &session); err != nil {
				continue
			}
			if session.Status == "active" {
				keysToUpdate = append(keysToUpdate, k)
				session.Status = "closed"
				session.EndTime = &now
				sessionsToUpdate = append(sessionsToUpdate, session)
			}
		}

		// Second pass: update all active sessions
		for i, key := range keysToUpdate {
			data, err := json.Marshal(sessionsToUpdate[i])
			if err != nil {
				continue
			}
			if err := bucket.Put(key, data); err != nil {
				continue
			}
		}

		if len(keysToUpdate) > 0 {
			m.logger.Infow("Closed stale sessions on startup", "count", len(keysToUpdate))
		}

		return nil
	})
}

// SetSessionWorkspace backfills the workspace on an already-persisted session.
//
// Needed because the workspace is discovered asynchronously (the client is asked
// for its roots only after the handshake completes — asking during it deadlocks),
// so a busy session can be persisted before the answer arrives.
func (m *Manager) SetSessionWorkspace(sessionID, workspaceRoot string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(SessionsBucket))
		if bucket == nil {
			return fmt.Errorf("sessions bucket not found")
		}

		var sessionKey []byte
		var session SessionRecord
		c := bucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			if strings.HasSuffix(string(k), "_"+sessionID) {
				sessionKey = k
				if err := json.Unmarshal(v, &session); err != nil {
					return fmt.Errorf("failed to unmarshal session: %w", err)
				}
				break
			}
		}
		if sessionKey == nil {
			return fmt.Errorf("session not found: %s", sessionID)
		}

		session.WorkspaceRoot = workspaceRoot
		session.WorkspaceName = workspaceDisplayName(workspaceRoot)

		data, err := json.Marshal(session)
		if err != nil {
			return fmt.Errorf("failed to marshal session: %w", err)
		}
		return bucket.Put(sessionKey, data)
	})
}

// workspaceDisplayName is the basename of a workspace root. Only the basename is
// ever displayed or exported — the full path is local and private.
func workspaceDisplayName(root string) string {
	root = strings.TrimSpace(root)
	root = strings.TrimPrefix(root, "file://")
	root = strings.TrimRight(root, "/")
	if root == "" {
		return ""
	}
	if i := strings.LastIndex(root, "/"); i >= 0 {
		return root[i+1:]
	}
	return root
}

// UpdateSessionStats increments tool call count and adds tokens
func (m *Manager) UpdateSessionStats(sessionID string, tokens int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(SessionsBucket))
		if bucket == nil {
			return fmt.Errorf("sessions bucket not found")
		}

		// Find the session
		var sessionKey []byte
		var session SessionRecord

		c := bucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			keyStr := string(k)
			// Check if key ends with _{session_id}
			if strings.HasSuffix(keyStr, "_"+sessionID) {
				sessionKey = k
				if err := json.Unmarshal(v, &session); err != nil {
					return fmt.Errorf("failed to unmarshal session: %w", err)
				}
				break
			}
		}

		if sessionKey == nil {
			return fmt.Errorf("session not found: %s", sessionID)
		}

		// Update stats and last activity
		session.ToolCallCount++
		session.TotalTokens += tokens
		session.LastActivity = time.Now()

		data, err := json.Marshal(session)
		if err != nil {
			return fmt.Errorf("failed to marshal session: %w", err)
		}

		return bucket.Put(sessionKey, data)
	})
}

// UpdateSessionActivity updates LastActivity without incrementing tool call counts.
// Call this on any MCP message to keep the session alive.
func (m *Manager) UpdateSessionActivity(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(SessionsBucket))
		if bucket == nil {
			return nil
		}

		c := bucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			keyStr := string(k)
			if strings.HasSuffix(keyStr, "_"+sessionID) {
				var session SessionRecord
				if err := json.Unmarshal(v, &session); err != nil {
					return err
				}
				session.LastActivity = time.Now()
				data, err := json.Marshal(session)
				if err != nil {
					return err
				}
				return bucket.Put(k, data)
			}
		}
		return nil // Session not found is not an error - may not be persisted yet
	})
}

// CloseInactiveSessions closes sessions that haven't had activity for the specified duration
func (m *Manager) CloseInactiveSessions(inactivityTimeout time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var closedCount int

	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(SessionsBucket))
		if bucket == nil {
			return nil // No sessions bucket yet
		}

		now := time.Now()
		cutoff := now.Add(-inactivityTimeout)
		var keysToUpdate [][]byte
		var sessionsToUpdate []SessionRecord

		// Find all active sessions with no recent activity
		c := bucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var session SessionRecord
			if err := json.Unmarshal(v, &session); err != nil {
				continue
			}

			// Check if session is active and hasn't had activity within timeout
			if session.Status == "active" {
				lastActivity := session.LastActivity
				// If LastActivity is zero, use StartTime (for backwards compatibility)
				if lastActivity.IsZero() {
					lastActivity = session.StartTime
				}

				if lastActivity.Before(cutoff) {
					keysToUpdate = append(keysToUpdate, k)
					session.Status = "closed"
					session.EndTime = &now
					sessionsToUpdate = append(sessionsToUpdate, session)
				}
			}
		}

		// Update all inactive sessions
		for i, key := range keysToUpdate {
			data, err := json.Marshal(sessionsToUpdate[i])
			if err != nil {
				continue
			}
			if err := bucket.Put(key, data); err != nil {
				continue
			}
			closedCount++
		}

		return nil
	})

	if closedCount > 0 {
		m.logger.Infow("Closed inactive sessions", "count", closedCount, "timeout", inactivityTimeout.String())
	}

	return closedCount, err
}

// GetToolCallsBySession retrieves tool calls filtered by session ID
func (m *Manager) GetToolCallsBySession(sessionID string, limit, offset int) ([]*ToolCallRecord, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var toolCalls []*ToolCallRecord
	var total int

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		// We need to iterate all server tool call buckets
		return tx.ForEach(func(name []byte, b *bbolt.Bucket) error {
			bucketName := string(name)
			// Check if this is a tool calls bucket
			if len(bucketName) < 18 || bucketName[:7] != "server_" || bucketName[len(bucketName)-11:] != "_tool_calls" {
				return nil
			}

			c := b.Cursor()
			for k, v := c.Last(); k != nil; k, v = c.Prev() {
				var record ToolCallRecord
				if err := json.Unmarshal(v, &record); err != nil {
					continue
				}

				// Filter by session ID
				if record.MCPSessionID == sessionID {
					total++
					if total > offset && len(toolCalls) < limit {
						toolCalls = append(toolCalls, &record)
					}
				}
			}
			return nil
		})
	})

	// Sort by timestamp descending
	sort.Slice(toolCalls, func(i, j int) bool {
		return toolCalls[i].Timestamp.After(toolCalls[j].Timestamp)
	})

	return toolCalls, total, err
}

// Eviction tiers, worst first. The tier is the primary ranking key; activity
// only breaks ties inside a tier.
const (
	// sessionTierUnreadable is a record that cannot be unmarshalled. Nothing can
	// display, close, or update it, so it is worth strictly less than any record
	// that parses. This has to be its own tier rather than a derived property:
	// an unmarshal failure leaves the zero value, which is indistinguishable
	// from a VALID record whose status is not "active" and whose timestamps are
	// unset. Sharing a tier with those meant key order decided between them, and
	// the usable record could lose.
	sessionTierUnreadable = iota
	// sessionTierClosed is finished work — nothing will ever write to it again.
	sessionTierClosed
	// sessionTierActive is a session the proxy still believes is live.
	sessionTierActive
)

// sessionEvictionCandidate is one stored session, reduced to the properties
// retention ranks by.
type sessionEvictionCandidate struct {
	key      []byte
	tier     int
	activity time.Time // LastActivity, falling back to StartTime when unset
}

// worseThan reports whether a should be evicted before b.
func (a sessionEvictionCandidate) worseThan(b sessionEvictionCandidate) bool {
	if a.tier != b.tier {
		return a.tier < b.tier
	}
	if !a.activity.Equal(b.activity) {
		return a.activity.Before(b.activity) // stalest first
	}
	return bytes.Compare(a.key, b.key) < 0 // deterministic tiebreak
}

// survivorHeap is a min-heap under worseThan, so its root is the weakest record
// currently being kept — i.e. the next one to be displaced.
type survivorHeap []sessionEvictionCandidate

func (h survivorHeap) Len() int           { return len(h) }
func (h survivorHeap) Less(i, j int) bool { return h[i].worseThan(h[j]) }
func (h survivorHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *survivorHeap) Push(x any) {
	c, ok := x.(sessionEvictionCandidate)
	if !ok {
		return
	}
	*h = append(*h, c)
}

func (h *survivorHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// classifySessionForRetention reduces a stored record to its ranking properties.
func classifySessionForRetention(key, value []byte, logger *zap.SugaredLogger) sessionEvictionCandidate {
	cand := sessionEvictionCandidate{
		key:  append([]byte(nil), key...),
		tier: sessionTierUnreadable,
	}

	var session SessionRecord
	if err := json.Unmarshal(value, &session); err != nil {
		logger.Warnw("Unreadable session record during retention", "key", string(key), "error", err)
		return cand
	}

	if session.Status == "active" {
		cand.tier = sessionTierActive
	} else {
		cand.tier = sessionTierClosed
	}

	cand.activity = session.LastActivity
	if cand.activity.IsZero() {
		// Records written before LastActivity existed. StartTime is the only
		// evidence available, and it cuts both ways: an ancient legacy record
		// must rank as stale, not as freshly active.
		cand.activity = session.StartTime
	}
	return cand
}

// enforceSessionRetention trims the sessions bucket down to maxSessions records.
//
// Victims are chosen by usefulness, NOT by key order. Session keys are
// {StartTime.UnixNano()}_{ID}, so deleting the lowest keys deletes the
// longest-lived session first — which is precisely the session most likely to
// still be connected and working. A client that stayed connected all day used to
// disappear from storage as soon as 100 newer sessions had been created; after
// that it was absent from the sessions API and its later close/stat updates had
// no record to find.
//
// Eviction order is therefore two-tiered:
//
//  1. closed sessions, stalest first — finished work; nothing will ever write to
//     these records again;
//  2. only if tier 1 does not free enough room, active sessions ordered by last
//     activity, stalest first — a client that died without closing goes before a
//     client that is genuinely working.
//
// Tier 2 is what keeps the cap absolute. Refusing to evict active sessions at
// all would let an all-active bucket (abandoned clients, a reconnect loop that
// never closes cleanly) grow without bound, which is a worse bug than the one
// this fixes.
func (m *Manager) enforceSessionRetention(bucket *bbolt.Bucket, maxSessions int) error {
	return trimSessionsToLimit(bucket, maxSessions, m.logger)
}

// trimSessionsToLimit is the retention pass itself, callable from any write
// path that can put the bucket over the cap — CreateSession is not the only
// one (see enforceSessionRetentionOnOpen).
//
// It runs in two passes:
//
//	Pass 1 ranks, keeping only a heap of the best maxSessions records seen so
//	far. Victims are counted but not collected.
//	Pass 2 deletes everything that is not a survivor, in batches of
//	retentionDeleteBatch, and reads keys only. The expensive JSON decode
//	therefore still happens exactly once per record across both passes.
//
// What that bounds, and what it does NOT:
//
// Bounded — the ranking state (a heap of maxSessions, not the whole bucket)
// and the explicit victim-key slice (one batch at a time, not every victim).
//
// NOT bounded — bbolt's transaction state. Every Bucket.Delete seeks to the
// key and calls Cursor.node(), which materialises the containing leaf page as
// an in-memory node cached in the bucket and retained until the transaction
// spills at commit. A trim touching P pages therefore holds O(P) — in practice
// O(n) — however the victim keys are batched. Measured on a 100k-record bucket:
// ~90 MB held inside the transaction. The victim slice is ~27 KB by
// construction (retentionDeleteBatch keys) — computed, not measured. Batching
// bounds a real term, but not the dominant one.
//
// This is accepted rather than fixed. Bounding it means committing between
// batches, which would trade away the property that migration and retention
// apply atomically under bbolt's exclusive file lock. The trade is not worth
// making, because no supported, IN-PROCESS write path produces the oversized
// bucket it would defend. Only two writers add keys to this bucket:
// CreateSession, where retention runs immediately after every insert, and the
// legacy migration, where it runs immediately after at open. Every other writer
// Puts on a key it already located by scanning, so it cannot grow the bucket.
// The realistic "oversized" case is the off-by-one that let it settle at 101 —
// not 10^5.
//
// Out-of-band writes are deliberately EXCLUDED from that claim. Direct bbolt
// manipulation, a database import or merge, or any other process writing this
// file can produce an arbitrarily large bucket — which is precisely why
// enforceSessionRetentionOnOpen repairs an oversized bucket whatever the
// reason, and why this function must stay correct (if not thrifty) at any size.
// If such a database turns up in practice, or a supported in-process path is
// ever added that inserts many records at once, this is the comment that has to
// change with it.
func trimSessionsToLimit(bucket *bbolt.Bucket, maxSessions int, logger *zap.SugaredLogger) error {
	if maxSessions <= 0 {
		return nil
	}

	// Pass 1 — rank. bucket.Stats() is deliberately not used to count: inside a
	// write transaction it does not account for records Put earlier in the same
	// transaction, which let the bucket settle one record above the cap forever.
	survivors := make(survivorHeap, 0, maxSessions)
	total := 0
	activeEvicted := 0

	c := bucket.Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		total++
		cand := classifySessionForRetention(k, v, logger)
		switch {
		case len(survivors) < maxSessions:
			heap.Push(&survivors, cand)
		case cand.worseThan(survivors[0]):
			// Weaker than every record currently being kept, so it is a victim.
			// Its key is not retained; pass 2 rediscovers it as "not a survivor".
			if cand.tier == sessionTierActive {
				activeEvicted++
			}
		default:
			if displaced, ok := heap.Pop(&survivors).(sessionEvictionCandidate); ok &&
				displaced.tier == sessionTierActive {
				activeEvicted++
			}
			heap.Push(&survivors, cand)
		}
	}

	if total <= maxSessions {
		return nil
	}

	// The survivor set, by key: exactly maxSessions entries.
	keep := make(map[string]struct{}, len(survivors))
	for _, s := range survivors {
		keep[string(s.key)] = struct{}{}
	}

	// Pass 2 — delete. Deleting cannot happen while a cursor is traversing the
	// bucket: bbolt documents mutation during traversal as unsafe ("Changing
	// data while traversing with a cursor may cause it to be invalidated and
	// return unexpected keys and/or values"). So each batch is collected, the
	// traversal abandoned, the batch deleted, and the scan resumed from the
	// first key the batch did not reach.
	deleted := 0
	var resume []byte
	for {
		batch := make([][]byte, 0, retentionDeleteBatch)

		cur := bucket.Cursor()
		var k []byte
		if resume == nil {
			k, _ = cur.First()
		} else {
			k, _ = cur.Seek(resume)
		}
		for ; k != nil && len(batch) < retentionDeleteBatch; k, _ = cur.Next() {
			if _, survives := keep[string(k)]; survives {
				continue
			}
			batch = append(batch, append([]byte(nil), k...))
		}

		// k is the first key this batch did not examine. It is never one of the
		// keys about to be deleted, so it is still there to seek back to.
		if k != nil {
			resume = append([]byte(nil), k...)
		}

		for _, key := range batch {
			if err := bucket.Delete(key); err != nil {
				return fmt.Errorf("failed to delete old session: %w", err)
			}
			deleted++
		}

		if k == nil {
			break
		}
	}

	if activeEvicted > 0 {
		// Only reachable when there was no closed or unreadable record left to
		// sacrifice.
		logger.Warnw("Session retention had to evict active sessions",
			"active_evicted", activeEvicted, "limit", maxSessions)
	}
	logger.Debugw("Enforced session retention", "deleted", deleted, "remaining", maxSessions)
	return nil
}

// enforceSessionRetentionOnOpen brings the sessions bucket within the cap at
// database-open time.
//
// The cap is only an invariant if EVERY write path enforces it, and
// CreateSession is not the only one. The legacy-bucket migration moves an
// arbitrary number of records into this bucket, and a process that then only
// updates or closes existing sessions — never creating a new ID — would never
// call retention again and would sit above the cap indefinitely. Running here
// also repairs a bucket left oversized by any older version, whatever the
// reason.
//
// Cost is one scan of an already-capped bucket in the common case.
func enforceSessionRetentionOnOpen(tx *bbolt.Tx, logger *zap.SugaredLogger) error {
	bucket := tx.Bucket([]byte(SessionsBucket))
	if bucket == nil {
		return nil
	}
	return trimSessionsToLimit(bucket, sessionRetentionLimit, logger)
}

// GetOAuthToken retrieves an OAuth token for a server from storage
func (m *Manager) GetOAuthToken(serverName string) (*OAuthTokenRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.db == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	return m.db.GetOAuthToken(serverName)
}

// ListOAuthTokens returns all OAuth token records from storage.
// Used by RefreshManager to initialize proactive refresh schedules on startup.
func (m *Manager) ListOAuthTokens() ([]*OAuthTokenRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.db == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	return m.db.ListOAuthTokens()
}

// ClearOAuthState clears all OAuth state for a server (tokens, client registration, etc.)
// This should be called when OAuth configuration changes to force re-authentication
func (m *Manager) ClearOAuthState(serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db == nil {
		return fmt.Errorf("storage not initialized")
	}

	// Delete the exact server name key (legacy) and any hashed serverKey entries.
	// Tokens are stored using oauth.GenerateServerKey(name, url) which prefixes the server
	// name and appends a hash of the URL; clear both to ensure logout actually removes tokens.
	cleared := 0
	if err := m.db.DeleteOAuthToken(serverName); err == nil {
		cleared++
	}

	if err := m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(OAuthTokenBucket))
		if bucket == nil {
			return fmt.Errorf("oauth token bucket not found")
		}

		// Collect the whole prefix range before deleting any of it. bbolt cursors
		// are invalidated by mutations made during iteration ("Changing data while
		// traversing with a cursor may cause it to be invalidated and return
		// unexpected keys and/or values" — bbolt cursor.go): once any write in this
		// transaction has materialised the leaf into a node, deleting through the
		// cursor shifts the remaining entries under its index and Next() skips one.
		// A key skipped here leaves an access token, a refresh token and the DCR
		// client secret on disk after the user logged out, still reachable by
		// PersistentTokenStore and RefreshManager.
		prefix := []byte(serverName + "_")
		var keys [][]byte
		cursor := bucket.Cursor()
		for k, _ := cursor.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = cursor.Next() {
			keys = append(keys, append([]byte(nil), k...))
		}

		for _, k := range keys {
			if err := bucket.Delete(k); err != nil {
				return err
			}
			cleared++
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to clear OAuth state: %w", err)
	}

	m.logger.Infow("Cleared OAuth state for server", "server", serverName)
	if cleared == 0 {
		m.logger.Debugw("No OAuth tokens found to clear (expected if already removed)", "server", serverName)
	}
	return nil
}

// CleanupOrphanedOAuthTokens removes OAuth tokens for servers that no longer exist in the configuration.
// This should be called during startup to clean up tokens left behind when servers were removed
// while mcpproxy was not running, or from older versions that didn't clean up properly.
func (m *Manager) CleanupOrphanedOAuthTokens(validServerNames []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db == nil {
		return 0, fmt.Errorf("storage not initialized")
	}

	// Create a set of valid server names for fast lookup
	validServers := make(map[string]bool, len(validServerNames))
	for _, name := range validServerNames {
		validServers[name] = true
	}

	// Get all OAuth tokens
	tokens, err := m.db.ListOAuthTokens()
	if err != nil {
		return 0, fmt.Errorf("failed to list OAuth tokens: %w", err)
	}

	// Find orphaned tokens (tokens whose server no longer exists)
	var orphanedKeys []string
	for _, token := range tokens {
		serverName := token.GetServerName()
		if serverName == "" {
			// Token has no server name, consider it orphaned
			orphanedKeys = append(orphanedKeys, token.ServerName)
			continue
		}

		// Check if the server name matches a valid server
		if validServers[serverName] {
			continue
		}

		// For legacy tokens without DisplayName, GetServerName() returns the storage key
		// (e.g., "cloudflare-observability_8753c4ae5a2f60a6") instead of the server name.
		// Try extracting the server name prefix from the storage key before classifying as orphaned.
		if token.DisplayName == "" {
			if prefix := extractServerNameFromKey(token.ServerName); prefix != "" && validServers[prefix] {
				m.logger.Infow("Legacy OAuth token matched by key prefix (missing DisplayName)",
					"storage_key", token.ServerName,
					"extracted_name", prefix)
				continue
			}
		}

		orphanedKeys = append(orphanedKeys, token.ServerName)
		m.logger.Infow("Found orphaned OAuth token",
			"storage_key", token.ServerName,
			"display_name", serverName)
	}

	if len(orphanedKeys) == 0 {
		m.logger.Debug("No orphaned OAuth tokens found")
		return 0, nil
	}

	// Delete orphaned tokens
	deleted := 0
	for _, key := range orphanedKeys {
		if err := m.db.DeleteOAuthToken(key); err != nil {
			m.logger.Warnw("Failed to delete orphaned OAuth token",
				"key", key,
				"error", err)
			continue
		}
		deleted++
	}

	m.logger.Infow("Cleaned up orphaned OAuth tokens",
		"total_tokens", len(tokens),
		"orphaned_found", len(orphanedKeys),
		"deleted", deleted)

	return deleted, nil
}

// extractServerNameFromKey extracts the server name prefix from a storage key.
// Storage keys have the format "serverName_<16 hex chars>" (e.g., "my-server_8753c4ae5a2f60a6").
// Returns empty string if the key doesn't match this format.
func extractServerNameFromKey(key string) string {
	lastUnderscore := strings.LastIndex(key, "_")
	if lastUnderscore <= 0 || lastUnderscore >= len(key)-1 {
		return ""
	}

	suffix := key[lastUnderscore+1:]
	// Storage keys use exactly 16 hex characters as the hash suffix
	if len(suffix) != 16 {
		return ""
	}
	if _, err := hex.DecodeString(suffix); err != nil {
		return ""
	}

	return key[:lastUnderscore]
}

// Onboarding wizard operations (Spec 046)

// GetOnboardingState returns the current wizard engagement state.
func (m *Manager) GetOnboardingState() (*OnboardingState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.db.GetOnboardingState()
}

// SaveOnboardingState persists the wizard engagement state.
func (m *Manager) SaveOnboardingState(state *OnboardingState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.SaveOnboardingState(state)
}
