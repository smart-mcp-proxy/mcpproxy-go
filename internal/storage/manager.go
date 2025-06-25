package storage

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"mcpproxy-go/internal/config"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

// Manager provides a unified interface for storage operations
type Manager struct {
	db     *BoltDB
	mu     sync.RWMutex
	logger *zap.SugaredLogger
}

// NewManager creates a new storage manager
func NewManager(dataDir string, logger *zap.SugaredLogger) (*Manager, error) {
	db, err := NewBoltDB(dataDir, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create bolt database: %w", err)
	}

	return &Manager{
		db:     db,
		logger: logger,
	}, nil
}

// Close closes the storage manager
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

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

// isDatabaseOpen checks if the database is open and available
func (m *Manager) isDatabaseOpen() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.db == nil || m.db.db == nil {
		return false
	}

	// Test if database is actually usable by attempting a quick read
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		return nil
	})

	return err == nil
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
		Env:         serverConfig.Env,
		Headers:     serverConfig.Headers,
		Enabled:     serverConfig.Enabled,
		Quarantined: serverConfig.Quarantined,
		Created:     serverConfig.Created,
		Updated:     time.Now(),
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
		Env:         record.Env,
		Headers:     record.Headers,
		Enabled:     record.Enabled,
		Quarantined: record.Quarantined,
		Created:     record.Created,
		Updated:     record.Updated,
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
			Env:         record.Env,
			Headers:     record.Headers,
			Enabled:     record.Enabled,
			Quarantined: record.Quarantined,
			Created:     record.Created,
			Updated:     record.Updated,
		})
	}

	return servers, nil
}

// ListQuarantinedUpstreamServers returns all quarantined upstream servers
func (m *Manager) ListQuarantinedUpstreamServers() ([]*config.ServerConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	records, err := m.db.ListUpstreams()
	if err != nil {
		return nil, err
	}

	var quarantinedServers []*config.ServerConfig
	for _, record := range records {
		if record.Quarantined {
			quarantinedServers = append(quarantinedServers, &config.ServerConfig{
				Name:        record.Name,
				URL:         record.URL,
				Protocol:    record.Protocol,
				Command:     record.Command,
				Args:        record.Args,
				Env:         record.Env,
				Headers:     record.Headers,
				Enabled:     record.Enabled,
				Quarantined: record.Quarantined,
				Created:     record.Created,
				Updated:     record.Updated,
			})
		}
	}

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

// EnableUpstreamServer enables/disables an upstream server
func (m *Manager) EnableUpstreamServer(name string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	record, err := m.db.GetUpstream(name)
	if err != nil {
		return err
	}

	record.Enabled = enabled
	return m.db.SaveUpstream(record)
}

// QuarantineUpstreamServer sets the quarantine status of an upstream server
func (m *Manager) QuarantineUpstreamServer(name string, quarantined bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	record, err := m.db.GetUpstream(name)
	if err != nil {
		return err
	}

	record.Quarantined = quarantined
	record.Updated = time.Now()
	return m.db.SaveUpstream(record)
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
