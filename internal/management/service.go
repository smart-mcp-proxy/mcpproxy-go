// Package management provides unified server lifecycle and diagnostic operations.
// It consolidates duplicate logic from CLI, REST, and MCP interfaces into a single service layer.
package management

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/contracts"
	"mcpproxy-go/internal/reqcontext"
	"mcpproxy-go/internal/secret"
)

// BulkOperationResult holds the results of a bulk operation across multiple servers.
type BulkOperationResult struct {
	Total      int               `json:"total"`       // Total servers processed
	Successful int               `json:"successful"`  // Number of successful operations
	Failed     int               `json:"failed"`      // Number of failed operations
	Errors     map[string]string `json:"errors"`      // Map of server name to error message
}

// Service defines the management interface for all server lifecycle and diagnostic operations.
// All CLI commands, REST endpoints, and MCP tools delegate to this service.
type Service interface {
	// Server Lifecycle Operations

	// ListServers returns all configured servers with their current status and aggregate statistics.
	// This method respects configuration gates but never blocks read operations.
	ListServers(ctx context.Context) ([]*contracts.Server, *contracts.ServerStats, error)

	// GetServerLogs retrieves recent log entries for a specific server.
	// The tail parameter controls how many recent entries to return.
	// Returns empty slice if server doesn't exist or has no logs.
	GetServerLogs(ctx context.Context, name string, tail int) ([]contracts.LogEntry, error)

	// EnableServer enables or disables a specific upstream server.
	// This operation respects disable_management and read_only configuration gates.
	// Emits "servers.changed" event on successful state change.
	EnableServer(ctx context.Context, name string, enabled bool) error

	// RestartServer stops and restarts the connection to a specific upstream server.
	// This operation respects disable_management and read_only configuration gates.
	// Emits "servers.changed" event on successful restart.
	RestartServer(ctx context.Context, name string) error

	// Bulk Operations

	// RestartAll restarts all configured servers sequentially.
	// Returns detailed results including success/failure counts and per-server errors.
	// Continues on partial failures, collecting all errors in the result.
	// This operation respects disable_management and read_only configuration gates.
	RestartAll(ctx context.Context) (*BulkOperationResult, error)

	// EnableAll enables all configured servers.
	// Returns detailed results including success/failure counts and per-server errors.
	// This operation respects disable_management and read_only configuration gates.
	EnableAll(ctx context.Context) (*BulkOperationResult, error)

	// DisableAll disables all configured servers.
	// Returns detailed results including success/failure counts and per-server errors.
	// This operation respects disable_management and read_only configuration gates.
	DisableAll(ctx context.Context) (*BulkOperationResult, error)

	// Diagnostics Operations

	// Doctor aggregates health diagnostics from all system components.
	// Returns comprehensive health information including:
	// - Upstream server connection errors
	// - OAuth authentication requirements
	// - Missing secrets referenced in configuration
	// - Docker daemon status (if isolation is enabled)
	// - General runtime warnings
	// Target completion time: <3 seconds for 20 servers.
	Doctor(ctx context.Context) (*contracts.Diagnostics, error)

	// AuthStatus returns detailed OAuth authentication status for a specific server.
	// Returns nil if server doesn't use OAuth or doesn't exist.
	AuthStatus(ctx context.Context, name string) (*contracts.AuthStatus, error)
}

// EventEmitter defines the interface for emitting runtime events.
// This is used by the service to notify subscribers of state changes.
type EventEmitter interface {
	EmitServersChanged(reason string, extra map[string]any)
}

// RuntimeOperations defines the interface for runtime operations needed by the service.
// This allows the service to delegate to runtime without a direct dependency.
type RuntimeOperations interface {
	EnableServer(serverName string, enabled bool) error
	RestartServer(serverName string) error
	GetAllServers() ([]map[string]interface{}, error)
}

// service implements the Service interface with dependency injection.
type service struct {
	runtime        RuntimeOperations
	config         *config.Config
	eventEmitter   EventEmitter
	secretResolver *secret.Resolver
	logger         *zap.SugaredLogger
}

// NewService creates a new management service with the given dependencies.
// The runtime parameter should implement RuntimeOperations (typically *runtime.Runtime).
func NewService(
	runtime RuntimeOperations,
	cfg *config.Config,
	eventEmitter EventEmitter,
	secretResolver *secret.Resolver,
	logger *zap.SugaredLogger,
) Service {
	return &service{
		runtime:        runtime,
		config:         cfg,
		eventEmitter:   eventEmitter,
		secretResolver: secretResolver,
		logger:         logger,
	}
}

// checkWriteGates verifies if write operations are allowed based on configuration.
// Returns an error if disable_management or read_only mode is enabled.
func (s *service) checkWriteGates() error {
	if s.config.DisableManagement {
		return fmt.Errorf("management operations are disabled (disable_management=true)")
	}
	if s.config.ReadOnlyMode {
		return fmt.Errorf("management operations are disabled (read_only_mode=true)")
	}
	return nil
}

// ListServers returns all configured servers with aggregate statistics.
// This is a read operation and never blocked by configuration gates.
func (s *service) ListServers(ctx context.Context) ([]*contracts.Server, *contracts.ServerStats, error) {
	// Get servers from runtime
	serversRaw, err := s.runtime.GetAllServers()
	if err != nil {
		s.logger.Errorw("Failed to list servers", "error", err)
		return nil, nil, fmt.Errorf("failed to list servers: %w", err)
	}

	// Convert to contracts.Server format
	servers := make([]*contracts.Server, 0, len(serversRaw))
	stats := &contracts.ServerStats{}

	for _, srvRaw := range serversRaw {
		// Convert map to Server struct
		srv := &contracts.Server{}

		// Extract basic fields
		if name, ok := srvRaw["name"].(string); ok {
			srv.Name = name
		}
		if id, ok := srvRaw["id"].(string); ok {
			srv.ID = id
		}
		if protocol, ok := srvRaw["protocol"].(string); ok {
			srv.Protocol = protocol
		}
		if enabled, ok := srvRaw["enabled"].(bool); ok {
			srv.Enabled = enabled
		}
		if connected, ok := srvRaw["connected"].(bool); ok {
			srv.Connected = connected
		}
		if quarantined, ok := srvRaw["quarantined"].(bool); ok {
			srv.Quarantined = quarantined
		}
		if status, ok := srvRaw["status"].(string); ok {
			srv.Status = status
		}

		// Extract numeric fields
		if toolCount, ok := srvRaw["tool_count"].(int); ok {
			srv.ToolCount = toolCount
			stats.TotalTools += toolCount
		}
		if retryCount, ok := srvRaw["retry_count"].(int); ok {
			srv.ReconnectCount = retryCount
		}

		// Extract timestamp fields
		if created, ok := srvRaw["created"].(time.Time); ok {
			srv.Created = created
		}
		if updated, ok := srvRaw["updated"].(time.Time); ok {
			srv.Updated = updated
		}

		servers = append(servers, srv)

		// Update stats
		stats.TotalServers++
		if srv.Connected {
			stats.ConnectedServers++
		}
		if srv.Quarantined {
			stats.QuarantinedServers++
		}
	}

	return servers, stats, nil
}

// GetServerLogs retrieves recent log entries for a specific server.
// This is a read operation and never blocked by configuration gates.
func (s *service) GetServerLogs(ctx context.Context, name string, tail int) ([]contracts.LogEntry, error) {
	// TODO: Implement later (not in critical path)
	return nil, fmt.Errorf("not implemented")
}

// EnableServer enables or disables a specific upstream server.
func (s *service) EnableServer(ctx context.Context, name string, enabled bool) error {
	// Check configuration gates
	if err := s.checkWriteGates(); err != nil {
		s.logger.Warnw("EnableServer blocked by configuration gate",
			"server", name,
			"enabled", enabled,
			"error", err)
		return err
	}

	// Delegate to runtime
	if err := s.runtime.EnableServer(name, enabled); err != nil {
		s.logger.Errorw("Failed to enable/disable server",
			"server", name,
			"enabled", enabled,
			"error", err)
		return fmt.Errorf("failed to enable server '%s': %w", name, err)
	}

	s.logger.Infow("Successfully changed server enabled state",
		"server", name,
		"enabled", enabled)

	// Note: Runtime already emits the event, so we don't duplicate it here
	return nil
}

// RestartServer stops and restarts a specific upstream server connection.
func (s *service) RestartServer(ctx context.Context, name string) error {
	// Check configuration gates
	if err := s.checkWriteGates(); err != nil {
		s.logger.Warnw("RestartServer blocked by configuration gate",
			"server", name,
			"error", err)
		return err
	}

	// Delegate to runtime
	if err := s.runtime.RestartServer(name); err != nil {
		s.logger.Errorw("Failed to restart server",
			"server", name,
			"error", err)
		return fmt.Errorf("failed to restart server '%s': %w", name, err)
	}

	s.logger.Infow("Successfully restarted server", "server", name)

	// Note: Runtime already emits the event, so we don't duplicate it here
	return nil
}

// T070: RestartAll restarts all configured servers sequentially.
// Continues on partial failures and returns detailed results.
func (s *service) RestartAll(ctx context.Context) (*BulkOperationResult, error) {
	startTime := time.Now()
	correlationID := reqcontext.GetCorrelationID(ctx)
	source := reqcontext.GetRequestSource(ctx)

	s.logger.Infow("Bulk operation initiated",
		"operation", "restart_all",
		"correlation_id", correlationID,
		"source", source)

	// Check configuration gates
	if err := s.checkWriteGates(); err != nil {
		s.logger.Warnw("RestartAll blocked by configuration gate",
			"correlation_id", correlationID,
			"source", source,
			"error", err)
		return nil, err
	}

	// Get all servers
	servers, err := s.runtime.GetAllServers()
	if err != nil {
		s.logger.Errorw("Failed to get servers for RestartAll",
			"correlation_id", correlationID,
			"source", source,
			"error", err)
		return nil, fmt.Errorf("failed to get servers: %w", err)
	}

	result := &BulkOperationResult{
		Total:  len(servers),
		Errors: make(map[string]string),
	}

	// Iterate through servers and restart each
	for _, server := range servers {
		name, ok := server["name"].(string)
		if !ok {
			s.logger.Warnw("Server missing name field, skipping",
				"correlation_id", correlationID,
				"server", server)
			continue
		}

		if err := s.runtime.RestartServer(name); err != nil {
			s.logger.Errorw("Failed to restart server in bulk operation",
				"correlation_id", correlationID,
				"server", name,
				"error", err)
			result.Failed++
			result.Errors[name] = err.Error()
		} else {
			s.logger.Infow("Successfully restarted server in bulk operation",
				"correlation_id", correlationID,
				"server", name)
			result.Successful++
		}
	}

	duration := time.Since(startTime)
	s.logger.Infow("RestartAll completed",
		"correlation_id", correlationID,
		"source", source,
		"duration_ms", duration.Milliseconds(),
		"total", result.Total,
		"successful", result.Successful,
		"failed", result.Failed)

	return result, nil
}

// T071: EnableAll enables all configured servers.
// Continues on partial failures and returns detailed results.
func (s *service) EnableAll(ctx context.Context) (*BulkOperationResult, error) {
	startTime := time.Now()
	correlationID := reqcontext.GetCorrelationID(ctx)
	source := reqcontext.GetRequestSource(ctx)

	s.logger.Infow("Bulk operation initiated",
		"operation", "enable_all",
		"correlation_id", correlationID,
		"source", source)

	// Check configuration gates
	if err := s.checkWriteGates(); err != nil {
		s.logger.Warnw("EnableAll blocked by configuration gate",
			"correlation_id", correlationID,
			"source", source,
			"error", err)
		return nil, err
	}

	// Get all servers
	servers, err := s.runtime.GetAllServers()
	if err != nil {
		s.logger.Errorw("Failed to get servers for EnableAll",
			"correlation_id", correlationID,
			"source", source,
			"error", err)
		return nil, fmt.Errorf("failed to get servers: %w", err)
	}

	result := &BulkOperationResult{
		Total:  len(servers),
		Errors: make(map[string]string),
	}

	// Iterate through servers and enable each
	for _, server := range servers {
		name, ok := server["name"].(string)
		if !ok {
			s.logger.Warnw("Server missing name field, skipping",
				"correlation_id", correlationID,
				"server", server)
			continue
		}

		if err := s.runtime.EnableServer(name, true); err != nil {
			s.logger.Errorw("Failed to enable server in bulk operation",
				"correlation_id", correlationID,
				"server", name,
				"error", err)
			result.Failed++
			result.Errors[name] = err.Error()
		} else {
			s.logger.Infow("Successfully enabled server in bulk operation",
				"correlation_id", correlationID,
				"server", name)
			result.Successful++
		}
	}

	duration := time.Since(startTime)
	s.logger.Infow("EnableAll completed",
		"correlation_id", correlationID,
		"source", source,
		"duration_ms", duration.Milliseconds(),
		"total", result.Total,
		"successful", result.Successful,
		"failed", result.Failed)

	return result, nil
}

// T072: DisableAll disables all configured servers.
// Continues on partial failures and returns detailed results.
func (s *service) DisableAll(ctx context.Context) (*BulkOperationResult, error) {
	startTime := time.Now()
	correlationID := reqcontext.GetCorrelationID(ctx)
	source := reqcontext.GetRequestSource(ctx)

	s.logger.Infow("Bulk operation initiated",
		"operation", "disable_all",
		"correlation_id", correlationID,
		"source", source)

	// Check configuration gates
	if err := s.checkWriteGates(); err != nil {
		s.logger.Warnw("DisableAll blocked by configuration gate",
			"correlation_id", correlationID,
			"source", source,
			"error", err)
		return nil, err
	}

	// Get all servers
	servers, err := s.runtime.GetAllServers()
	if err != nil {
		s.logger.Errorw("Failed to get servers for DisableAll",
			"correlation_id", correlationID,
			"source", source,
			"error", err)
		return nil, fmt.Errorf("failed to get servers: %w", err)
	}

	result := &BulkOperationResult{
		Total:  len(servers),
		Errors: make(map[string]string),
	}

	// Iterate through servers and disable each
	for _, server := range servers {
		name, ok := server["name"].(string)
		if !ok {
			s.logger.Warnw("Server missing name field, skipping",
				"correlation_id", correlationID,
				"server", server)
			continue
		}

		if err := s.runtime.EnableServer(name, false); err != nil {
			s.logger.Errorw("Failed to disable server in bulk operation",
				"correlation_id", correlationID,
				"server", name,
				"error", err)
			result.Failed++
			result.Errors[name] = err.Error()
		} else {
			s.logger.Infow("Successfully disabled server in bulk operation",
				"correlation_id", correlationID,
				"server", name)
			result.Successful++
		}
	}

	duration := time.Since(startTime)
	s.logger.Infow("DisableAll completed",
		"correlation_id", correlationID,
		"source", source,
		"duration_ms", duration.Milliseconds(),
		"total", result.Total,
		"successful", result.Successful,
		"failed", result.Failed)

	return result, nil
}

// Doctor is now implemented in diagnostics.go (T040-T044)

// AuthStatus returns detailed OAuth authentication status for a specific server.
func (s *service) AuthStatus(ctx context.Context, name string) (*contracts.AuthStatus, error) {
	// TODO: Implement later (not in critical path)
	return nil, fmt.Errorf("not implemented")
}
