package storage

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"mcpproxy-go/internal/config"

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

// Close closes the storage manager
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
		ID:          serverConfig.Name, // Use name as ID for simplicity
		Name:        serverConfig.Name,
		URL:         serverConfig.URL,
		Protocol:    serverConfig.Protocol,
		Command:     serverConfig.Command,
		Args:        serverConfig.Args,
		WorkingDir:  serverConfig.WorkingDir,
		Env:         serverConfig.Env,
		Headers:     serverConfig.Headers,
		OAuth:       serverConfig.OAuth,
		Enabled:     serverConfig.Enabled,
		Quarantined: serverConfig.Quarantined,
		Created:     serverConfig.Created,
		Updated:     time.Now(),
		Isolation:   serverConfig.Isolation,
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
		Name:        record.Name,
		URL:         record.URL,
		Protocol:    record.Protocol,
		Command:     record.Command,
		Args:        record.Args,
		WorkingDir:  record.WorkingDir,
		Env:         record.Env,
		Headers:     record.Headers,
		OAuth:       record.OAuth,
		Enabled:     record.Enabled,
		Quarantined: record.Quarantined,
		Created:     record.Created,
		Updated:     record.Updated,
		Isolation:   record.Isolation,
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
			Name:        record.Name,
			URL:         record.URL,
			Protocol:    record.Protocol,
			Command:     record.Command,
			Args:        record.Args,
			WorkingDir:  record.WorkingDir,
			Env:         record.Env,
			Headers:     record.Headers,
			OAuth:       record.OAuth,
			Enabled:     record.Enabled,
			Quarantined: record.Quarantined,
			Created:     record.Created,
			Updated:     record.Updated,
			Isolation:   record.Isolation,
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
				Name:        record.Name,
				URL:         record.URL,
				Protocol:    record.Protocol,
				Command:     record.Command,
				Args:        record.Args,
				WorkingDir:  record.WorkingDir,
				Env:         record.Env,
				Headers:     record.Headers,
				OAuth:       record.OAuth,
				Enabled:     record.Enabled,
				Quarantined: record.Quarantined,
				Created:     record.Created,
				Updated:     record.Updated,
				Isolation:   record.Isolation,
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
}

// CreateSession creates a new session record
func (m *Manager) CreateSession(session *SessionRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(SessionsBucket))
		if err != nil {
			return fmt.Errorf("failed to create sessions bucket: %w", err)
		}

		// Check if session already exists (avoid duplicates)
		c := bucket.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			keyStr := string(k)
			// Check if key ends with the session ID (after the underscore)
			if len(keyStr) > len(session.ID) && keyStr[len(keyStr)-len(session.ID):] == session.ID {
				// Session already exists, skip creation
				m.logger.Debugw("Session already exists, skipping creation", "session_id", session.ID)
				return nil
			}
		}

		data, err := json.Marshal(session)
		if err != nil {
			return fmt.Errorf("failed to marshal session: %w", err)
		}

		// Key format: {timestamp_ns}_{session_id} for reverse chronological ordering
		key := fmt.Sprintf("%d_%s", session.StartTime.UnixNano(), session.ID)
		if err := bucket.Put([]byte(key), data); err != nil {
			return fmt.Errorf("failed to store session: %w", err)
		}

		m.logger.Debugw("Session created", "session_id", session.ID, "client_name", session.ClientName)

		// Enforce retention limit (keep 100 most recent)
		return m.enforceSessionRetention(bucket, 100)
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
			// Check if key ends with the session ID (after the underscore)
			if len(keyStr) > len(sessionID) && keyStr[len(keyStr)-len(sessionID):] == sessionID {
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

// GetRecentSessions returns the most recent sessions
func (m *Manager) GetRecentSessions(limit int) ([]*SessionRecord, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var sessions []*SessionRecord
	var total int

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(SessionsBucket))
		if bucket == nil {
			return nil // No sessions yet
		}

		// Count total
		total = bucket.Stats().KeyN

		// Iterate in reverse (newest first due to timestamp key prefix)
		c := bucket.Cursor()
		count := 0
		for k, v := c.Last(); k != nil && count < limit; k, v = c.Prev() {
			var session SessionRecord
			if err := json.Unmarshal(v, &session); err != nil {
				m.logger.Warnw("Failed to unmarshal session", "error", err)
				continue
			}
			sessions = append(sessions, &session)
			count++
		}

		return nil
	})

	return sessions, total, err
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
			// Check if key ends with the session ID (after the underscore)
			if len(keyStr) > len(sessionID) && keyStr[len(keyStr)-len(sessionID):] == sessionID {
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
			// Check if key ends with the session ID (after the underscore)
			if len(keyStr) > len(sessionID) && keyStr[len(keyStr)-len(sessionID):] == sessionID {
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

// enforceSessionRetention deletes oldest sessions if count exceeds limit
func (m *Manager) enforceSessionRetention(bucket *bbolt.Bucket, maxSessions int) error {
	stats := bucket.Stats()
	if stats.KeyN <= maxSessions {
		return nil
	}

	// Delete oldest sessions (first keys since they have oldest timestamps)
	toDelete := stats.KeyN - maxSessions
	deleted := 0

	c := bucket.Cursor()
	for k, _ := c.First(); k != nil && deleted < toDelete; k, _ = c.Next() {
		if err := bucket.Delete(k); err != nil {
			return fmt.Errorf("failed to delete old session: %w", err)
		}
		deleted++
	}

	m.logger.Debugw("Enforced session retention", "deleted", deleted, "remaining", maxSessions)
	return nil
}
