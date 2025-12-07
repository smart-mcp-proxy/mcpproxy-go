// Package management provides unified server lifecycle and diagnostic operations.
// It consolidates duplicate logic from CLI, REST, and MCP interfaces into a single service layer.
package management

import (
	"context"
	"fmt"
	"sync"
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

	// Server Tool Operations

	// GetServerTools retrieves all available tools for a specific upstream MCP server.
	// Delegates to runtime's GetServerTools() which reads from StateView cache.
	// This is a read-only operation that completes in <10ms (in-memory cache read).
	// Returns empty array if server has no tools.
	// Returns error if server name is empty, server not found, or server not connected.
	GetServerTools(ctx context.Context, name string) ([]map[string]interface{}, error)

	// TriggerOAuthLogin initiates an OAuth 2.x authentication flow for a specific server.
	// Delegates to upstream manager's StartManualOAuth() which launches browser-based flow.
	// This operation respects disable_management and read_only configuration gates.
	// Emits "servers.changed" event on successful OAuth completion.
	// Method returns immediately after starting OAuth flow (actual completion is asynchronous).
	// Returns error if server name is empty, server not found, config gates block operation,
	// or server doesn't support OAuth.
	TriggerOAuthLogin(ctx context.Context, name string) error

	// TriggerOAuthLogout clears OAuth token and disconnects a specific server.
	// This operation respects disable_management and read_only configuration gates.
	// Emits "servers.changed" event on successful logout.
	// Returns error if server name is empty, server not found, config gates block operation,
	// or server doesn't support OAuth.
	TriggerOAuthLogout(ctx context.Context, name string) error

	// LogoutAllOAuth clears OAuth tokens for all OAuth-enabled servers.
	// Returns BulkOperationResult with success/failure counts.
	// This operation respects disable_management and read_only configuration gates.
	LogoutAllOAuth(ctx context.Context) (*BulkOperationResult, error)
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
	BulkEnableServers(serverNames []string, enabled bool) (map[string]error, error)
	GetServerTools(serverName string) ([]map[string]interface{}, error)
	TriggerOAuthLogin(serverName string) error
	TriggerOAuthLogout(serverName string) error
	RefreshOAuthToken(serverName string) error
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
		if lastError, ok := srvRaw["last_error"].(string); ok {
			srv.LastError = lastError
		}
		if authenticated, ok := srvRaw["authenticated"].(bool); ok {
			srv.Authenticated = authenticated
		}

		// Extract OAuth config if present
		if oauthRaw, ok := srvRaw["oauth"].(map[string]interface{}); ok && oauthRaw != nil {
			oauthCfg := &contracts.OAuthConfig{}
			if clientID, ok := oauthRaw["client_id"].(string); ok {
				oauthCfg.ClientID = clientID
			}
			// Try both []string (from runtime) and []interface{} (from generic conversion)
			if scopes, ok := oauthRaw["scopes"].([]string); ok {
				oauthCfg.Scopes = scopes
			} else if scopes, ok := oauthRaw["scopes"].([]interface{}); ok {
				oauthCfg.Scopes = make([]string, 0, len(scopes))
				for _, scope := range scopes {
					if scopeStr, ok := scope.(string); ok {
						oauthCfg.Scopes = append(oauthCfg.Scopes, scopeStr)
					}
				}
			}
			if authURL, ok := oauthRaw["auth_url"].(string); ok {
				oauthCfg.AuthURL = authURL
			}
			if tokenURL, ok := oauthRaw["token_url"].(string); ok {
				oauthCfg.TokenURL = tokenURL
			}
			// Try both map[string]string (from runtime) and map[string]interface{} (from generic conversion)
			if extraParams, ok := oauthRaw["extra_params"].(map[string]string); ok {
				oauthCfg.ExtraParams = extraParams
			} else if extraParams, ok := oauthRaw["extra_params"].(map[string]interface{}); ok {
				oauthCfg.ExtraParams = make(map[string]string)
				for k, v := range extraParams {
					if vStr, ok := v.(string); ok {
						oauthCfg.ExtraParams[k] = vStr
					}
				}
			}
			if redirectPort, ok := oauthRaw["redirect_port"].(int); ok {
				oauthCfg.RedirectPort = redirectPort
			}
			if pkceEnabled, ok := oauthRaw["pkce_enabled"].(bool); ok {
				oauthCfg.PKCEEnabled = pkceEnabled
			}
			if tokenExpiresAt, ok := oauthRaw["token_expires_at"].(string); ok && tokenExpiresAt != "" {
				if parsedTime, err := time.Parse(time.RFC3339, tokenExpiresAt); err == nil {
					oauthCfg.TokenExpiresAt = &parsedTime
				}
			}
			if tokenValid, ok := oauthRaw["token_valid"].(bool); ok {
				oauthCfg.TokenValid = tokenValid
			}
			srv.OAuth = oauthCfg
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
	maxWorkers := 4

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
		Errors: make(map[string]string),
	}

	// Collect valid server names
	targetServers := make([]string, 0, len(servers))
	for _, server := range servers {
		name, ok := server["name"].(string)
		if !ok {
			s.logger.Warnw("Server missing name field, skipping",
				"correlation_id", correlationID,
				"server", server)
			continue
		}
		targetServers = append(targetServers, name)
	}

	result.Total = len(targetServers)
	if len(targetServers) == 0 {
		return result, nil
	}

	// Parallelize restarts with a small worker pool
	sem := make(chan struct{}, maxWorkers)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, name := range targetServers {
		// Respect context cancellation before spawning
		select {
		case <-ctx.Done():
			mu.Lock()
			result.Errors[name] = ctx.Err().Error()
			result.Failed++
			mu.Unlock()
			continue
		default:
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(serverName string) {
			defer wg.Done()
			defer func() { <-sem }()

			if err := s.runtime.RestartServer(serverName); err != nil {
				s.logger.Errorw("Failed to restart server in bulk operation",
					"correlation_id", correlationID,
					"server", serverName,
					"error", err)
				mu.Lock()
				result.Failed++
				result.Errors[serverName] = err.Error()
				mu.Unlock()
				return
			}

			s.logger.Infow("Successfully restarted server in bulk operation",
				"correlation_id", correlationID,
				"server", serverName)
			mu.Lock()
			result.Successful++
			mu.Unlock()
		}(name)
	}

	wg.Wait()

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

	// Filter to only servers that actually need an update
	targetServers := make([]string, 0, len(servers))
	for _, server := range servers {
		name, ok := server["name"].(string)
		enabled, hasEnabled := server["enabled"].(bool)
		if !ok {
			s.logger.Warnw("Server missing name field, skipping",
				"correlation_id", correlationID,
				"server", server)
			continue
		}
		if hasEnabled && enabled {
			continue // Already enabled; skip
		}
		targetServers = append(targetServers, name)
	}

	// Short-circuit if there's nothing to do
	if len(targetServers) == 0 {
		return &BulkOperationResult{
			Total:      0,
			Successful: 0,
			Failed:     0,
			Errors:     map[string]string{},
		}, nil
	}

	// Apply changes in one batch to reduce config writes
	perServerErrs, opErr := s.runtime.BulkEnableServers(targetServers, true)
	if opErr != nil {
		s.logger.Errorw("Failed to enable servers in bulk operation",
			"correlation_id", correlationID,
			"source", source,
			"error", opErr)
		return nil, opErr
	}

	result := &BulkOperationResult{
		Total:  len(targetServers),
		Errors: make(map[string]string),
	}

	for _, name := range targetServers {
		if errMsg, exists := perServerErrs[name]; exists && errMsg != nil {
			result.Failed++
			result.Errors[name] = errMsg.Error()
		} else {
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

	// Filter to only servers that actually need to be disabled
	targetServers := make([]string, 0, len(servers))
	for _, server := range servers {
		name, ok := server["name"].(string)
		enabled, hasEnabled := server["enabled"].(bool)
		if !ok {
			s.logger.Warnw("Server missing name field, skipping",
				"correlation_id", correlationID,
				"server", server)
			continue
		}
		if hasEnabled && !enabled {
			continue // Already disabled
		}
		targetServers = append(targetServers, name)
	}

	if len(targetServers) == 0 {
		return &BulkOperationResult{
			Total:      0,
			Successful: 0,
			Failed:     0,
			Errors:     map[string]string{},
		}, nil
	}

	perServerErrs, opErr := s.runtime.BulkEnableServers(targetServers, false)
	if opErr != nil {
		s.logger.Errorw("Failed to disable servers in bulk operation",
			"correlation_id", correlationID,
			"source", source,
			"error", opErr)
		return nil, opErr
	}

	result := &BulkOperationResult{
		Total:  len(targetServers),
		Errors: make(map[string]string),
	}

	for _, name := range targetServers {
		if errMsg, exists := perServerErrs[name]; exists && errMsg != nil {
			result.Failed++
			result.Errors[name] = errMsg.Error()
		} else {
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

// GetServerTools retrieves all tools for a specific upstream server (T013).
// This method delegates to runtime's GetServerTools() which reads from StateView cache.
func (s *service) GetServerTools(ctx context.Context, name string) ([]map[string]interface{}, error) {
	// Validate input
	if name == "" {
		return nil, fmt.Errorf("server name required")
	}

	// Delegate to runtime (existing implementation)
	tools, err := s.runtime.GetServerTools(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get tools: %w", err)
	}

	return tools, nil
}

// TriggerOAuthLogin initiates OAuth authentication flow for a server (T014).
// This method checks config gates, delegates to runtime, and emits events on completion.
func (s *service) TriggerOAuthLogin(ctx context.Context, name string) error {
	// Validate input
	if name == "" {
		return fmt.Errorf("server name required")
	}

	// Check configuration gates (T015)
	if err := s.checkWriteGates(); err != nil {
		return err
	}

	// Delegate to runtime (existing implementation)
	if err := s.runtime.TriggerOAuthLogin(name); err != nil {
		return fmt.Errorf("failed to start OAuth: %w", err)
	}

	// Event will be emitted by upstream manager on OAuth completion
	// (existing behavior - no changes needed)

	return nil
}

// AuthStatus returns detailed OAuth authentication status for a specific server.
func (s *service) AuthStatus(ctx context.Context, name string) (*contracts.AuthStatus, error) {
	// TODO: Implement later (not in critical path)
	return nil, fmt.Errorf("not implemented")
}

// TriggerOAuthLogout clears OAuth token and disconnects a specific server.
// This method checks config gates, delegates to runtime, and emits events on completion.
func (s *service) TriggerOAuthLogout(ctx context.Context, name string) error {
	correlationID := reqcontext.GetCorrelationID(ctx)
	source := reqcontext.GetRequestSource(ctx)

	// Validate input
	if name == "" {
		return fmt.Errorf("server name required")
	}

	s.logger.Infow("OAuth logout initiated",
		"correlation_id", correlationID,
		"source", source,
		"server", name)

	// Check configuration gates
	if err := s.checkWriteGates(); err != nil {
		s.logger.Warnw("TriggerOAuthLogout blocked by configuration gate",
			"correlation_id", correlationID,
			"source", source,
			"server", name,
			"error", err)
		return err
	}

	// Delegate to runtime
	if err := s.runtime.TriggerOAuthLogout(name); err != nil {
		s.logger.Errorw("Failed to trigger OAuth logout",
			"correlation_id", correlationID,
			"source", source,
			"server", name,
			"error", err)
		return fmt.Errorf("failed to logout: %w", err)
	}

	s.logger.Infow("OAuth logout completed successfully",
		"correlation_id", correlationID,
		"source", source,
		"server", name)

	// Emit event for UI updates
	if s.eventEmitter != nil {
		s.eventEmitter.EmitServersChanged("oauth_logout", map[string]any{"server": name})
	}

	return nil
}

// LogoutAllOAuth clears OAuth tokens for all OAuth-enabled servers.
// Returns BulkOperationResult with success/failure counts.
func (s *service) LogoutAllOAuth(ctx context.Context) (*BulkOperationResult, error) {
	startTime := time.Now()
	correlationID := reqcontext.GetCorrelationID(ctx)
	source := reqcontext.GetRequestSource(ctx)

	s.logger.Infow("Bulk OAuth logout initiated",
		"correlation_id", correlationID,
		"source", source)

	// Check configuration gates
	if err := s.checkWriteGates(); err != nil {
		s.logger.Warnw("LogoutAllOAuth blocked by configuration gate",
			"correlation_id", correlationID,
			"source", source,
			"error", err)
		return nil, err
	}

	// Get all servers
	servers, err := s.runtime.GetAllServers()
	if err != nil {
		s.logger.Errorw("Failed to get servers for LogoutAllOAuth",
			"correlation_id", correlationID,
			"source", source,
			"error", err)
		return nil, fmt.Errorf("failed to get servers: %w", err)
	}

	result := &BulkOperationResult{
		Errors: make(map[string]string),
	}

	// Filter to only OAuth-enabled servers and attempt logout
	for _, server := range servers {
		name, ok := server["name"].(string)
		if !ok {
			continue
		}

		// Check if server has OAuth config
		if _, hasOAuth := server["oauth"]; !hasOAuth {
			continue
		}

		result.Total++

		if err := s.runtime.TriggerOAuthLogout(name); err != nil {
			s.logger.Warnw("Failed to logout OAuth server in bulk operation",
				"correlation_id", correlationID,
				"server", name,
				"error", err)
			result.Failed++
			result.Errors[name] = err.Error()
		} else {
			result.Successful++
		}
	}

	duration := time.Since(startTime)
	s.logger.Infow("LogoutAllOAuth completed",
		"correlation_id", correlationID,
		"source", source,
		"duration_ms", duration.Milliseconds(),
		"total", result.Total,
		"successful", result.Successful,
		"failed", result.Failed)

	// Emit single event for all changes
	if s.eventEmitter != nil && result.Successful > 0 {
		s.eventEmitter.EmitServersChanged("oauth_logout_all", map[string]any{
			"count": result.Successful,
		})
	}

	return result, nil
}
