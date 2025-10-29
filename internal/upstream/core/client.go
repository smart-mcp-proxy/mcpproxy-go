package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/secret"
	"mcpproxy-go/internal/secureenv"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/upstream/types"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

// Client implements basic MCP client functionality without state management
type Client struct {
	id           string
	config       *config.ServerConfig
	globalConfig *config.Config
	storage      *storage.BoltDB
	logger       *zap.Logger

	// Upstream server specific logger for debugging
	upstreamLogger *zap.Logger

	// MCP client and server info
	client     *client.Client
	serverInfo *mcp.InitializeResult

	// Environment manager for stdio transport
	envManager *secureenv.Manager

	// Secret resolver for keyring/env placeholder expansion
	secretResolver *secret.Resolver

	// Isolation manager for Docker isolation
	isolationManager *IsolationManager

	// Connection state protection
	mu         sync.RWMutex
	connected  bool
	connecting bool // Prevent concurrent connection attempts

	// OAuth progress tracking (separate mutex to prevent reentrant deadlock)
	oauthMu            sync.RWMutex
	oauthInProgress    bool
	oauthCompleted     bool
	lastOAuthTimestamp time.Time

	// SSE request serialization (prevent concurrent requests on SSE transport)
	// SSE transport has limitations with concurrent requests - responses can get lost
	// when multiple requests are in-flight simultaneously
	sseRequestMu sync.Mutex

	// Transport type and stderr access (for stdio)
	transportType string
	stderr        io.Reader

	// Cached tools list from successful immediate call
	cachedTools []mcp.Tool

	// Stderr monitoring
	stderrMonitoringCtx    context.Context
	stderrMonitoringCancel context.CancelFunc
	stderrMonitoringWG     sync.WaitGroup

	// Process monitoring (for stdio transport)
	processCmd           *exec.Cmd
	processGroupID       int // Process group ID for proper cleanup
	processMonitorCtx    context.Context
	processMonitorCancel context.CancelFunc
	processMonitorWG     sync.WaitGroup

	// Docker container tracking
	containerID     string
	containerName   string // Store container name for cleanup via docker container commands
	isDockerCommand bool
}

// NewClient creates a new core MCP client
func NewClient(id string, serverConfig *config.ServerConfig, logger *zap.Logger, logConfig *config.LogConfig, globalConfig *config.Config, storage *storage.BoltDB, secretResolver *secret.Resolver) (*Client, error) {
	return NewClientWithOptions(id, serverConfig, logger, logConfig, globalConfig, storage, false, secretResolver)
}

// NewClientWithOptions creates a new core MCP client with additional options
func NewClientWithOptions(id string, serverConfig *config.ServerConfig, logger *zap.Logger, logConfig *config.LogConfig, globalConfig *config.Config, storage *storage.BoltDB, cliDebugMode bool, secretResolver *secret.Resolver) (*Client, error) {
	// Resolve secrets in server config before using it
	resolvedServerConfig := *serverConfig // Create a copy
	if secretResolver != nil {
		// Create a context for secret resolution
		ctx := context.Background()

		// Resolve secrets in environment variables
		if len(resolvedServerConfig.Env) > 0 {
			resolvedEnv := make(map[string]string)
			for k, v := range resolvedServerConfig.Env {
				resolvedValue, err := secretResolver.ExpandSecretRefs(ctx, v)
				if err != nil {
					logger.Error("CRITICAL: Failed to resolve secret in environment variable - server will use UNRESOLVED placeholder",
						zap.String("server", serverConfig.Name),
						zap.String("env_var", k),
						zap.String("reference", v),
						zap.Error(err),
						zap.String("help", "Use Web UI (http://localhost:8080/ui/) or API to add the secret to keyring"))
					resolvedValue = v // Use original value on error - THIS IS THE PROBLEM!
				} else if resolvedValue != v {
					logger.Debug("Secret resolved successfully",
						zap.String("server", serverConfig.Name),
						zap.String("env_var", k),
						zap.String("reference", v))
				}
				resolvedEnv[k] = resolvedValue
			}
			resolvedServerConfig.Env = resolvedEnv
		}

		// Resolve secrets in arguments
		if len(resolvedServerConfig.Args) > 0 {
			resolvedArgs := make([]string, len(resolvedServerConfig.Args))
			for i, arg := range resolvedServerConfig.Args {
				resolvedValue, err := secretResolver.ExpandSecretRefs(ctx, arg)
				if err != nil {
					logger.Error("CRITICAL: Failed to resolve secret in argument - server will use UNRESOLVED placeholder",
						zap.String("server", serverConfig.Name),
						zap.Int("arg_index", i),
						zap.String("reference", arg),
						zap.Error(err),
						zap.String("help", "Use Web UI (http://localhost:8080/ui/) or API to add the secret to keyring"))
					resolvedValue = arg // Use original value on error - THIS IS THE PROBLEM!
				} else if resolvedValue != arg {
					logger.Debug("Secret resolved successfully",
						zap.String("server", serverConfig.Name),
						zap.Int("arg_index", i),
						zap.String("reference", arg))
				}
				resolvedArgs[i] = resolvedValue
			}
			resolvedServerConfig.Args = resolvedArgs
		}
	}

	c := &Client{
		id:             id,
		config:         &resolvedServerConfig, // Use resolved config
		globalConfig:   globalConfig,
		storage:        storage,
		secretResolver: secretResolver, // Store resolver for future use
		logger: logger.With(
			zap.String("upstream_id", id),
			zap.String("upstream_name", serverConfig.Name),
		),
	}

	// Create secure environment manager
	var envConfig *secureenv.EnvConfig
	if globalConfig != nil && globalConfig.Environment != nil {
		envConfig = globalConfig.Environment
	} else {
		envConfig = secureenv.DefaultEnvConfig()
	}

	// Enable PATH enhancement for Docker and other tools when using stdio transport
	// This helps with Launchd scenarios where PATH is minimal
	if serverConfig.Command != "" {
		// Create a copy of the config to avoid modifying the original
		envConfigCopy := *envConfig
		envConfigCopy.EnhancePath = true
		envConfig = &envConfigCopy
	}

	// Add server-specific environment variables
	// IMPORTANT: Use resolvedServerConfig.Env which has secrets expanded
	if len(resolvedServerConfig.Env) > 0 {
		serverEnvConfig := *envConfig
		if serverEnvConfig.CustomVars == nil {
			serverEnvConfig.CustomVars = make(map[string]string)
		} else {
			customVars := make(map[string]string)
			for k, v := range serverEnvConfig.CustomVars {
				customVars[k] = v
			}
			serverEnvConfig.CustomVars = customVars
		}

		for k, v := range resolvedServerConfig.Env {
			serverEnvConfig.CustomVars[k] = v
		}
		envConfig = &serverEnvConfig
	}

	c.envManager = secureenv.NewManager(envConfig)

	// Initialize isolation manager for Docker isolation
	if globalConfig != nil && globalConfig.DockerIsolation != nil {
		c.isolationManager = NewIsolationManager(globalConfig.DockerIsolation)
	}

	// Create upstream server logger if provided
	if logConfig != nil {
		var upstreamLogger *zap.Logger
		var err error

		// Use CLI logger for debugging or regular logger for daemon mode
		if cliDebugMode {
			upstreamLogger, err = logs.CreateCLIUpstreamServerLogger(logConfig, serverConfig.Name)
		} else {
			upstreamLogger, err = logs.CreateUpstreamServerLogger(logConfig, serverConfig.Name)
		}

		if err != nil {
			logger.Warn("Failed to create upstream server logger",
				zap.String("server", serverConfig.Name),
				zap.Bool("cli_debug_mode", cliDebugMode),
				zap.Error(err))
		} else {
			c.upstreamLogger = upstreamLogger
			if logConfig.Level == "trace" && cliDebugMode {
				c.upstreamLogger.Debug("TRACE LEVEL ENABLED - All JSON-RPC frames will be logged to console",
					zap.String("server", serverConfig.Name))
			}
		}
	}

	return c, nil
}

// IsConnected returns whether the client is currently connected
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// ListTools retrieves available tools from the upstream server
func (c *Client) ListTools(ctx context.Context) ([]*config.ToolMetadata, error) {
	c.mu.RLock()
	client := c.client
	serverInfo := c.serverInfo
	transportType := c.transportType
	c.mu.RUnlock()

	if !c.IsConnected() || client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// Check if we have server info and if server supports tools
	if serverInfo == nil {
		c.logger.Debug("Server info not available")
		return nil, fmt.Errorf("server info not available")
	}

	if serverInfo.Capabilities.Tools == nil {
		c.logger.Debug("Server does not support tools")
		return nil, nil
	}

	// SSE transport requires request serialization to prevent concurrent request issues
	// Background: SSE sends requests via HTTP POST but receives responses via persistent stream
	// Concurrent requests can cause response delivery failures in mcp-go v0.42.0
	if transportType == "sse" {
		c.logger.Debug("SSE transport detected - serializing ListTools request",
			zap.String("server", c.config.Name))
		c.sseRequestMu.Lock()
		defer c.sseRequestMu.Unlock()
		c.logger.Debug("SSE request lock acquired",
			zap.String("server", c.config.Name))
	}

	// Always make direct call to upstream server (no caching)
	c.logger.Info("Making direct tools list call to upstream server",
		zap.String("server", c.config.Name))

	listReq := mcp.ListToolsRequest{}
	toolsResult, err := client.ListTools(ctx, listReq)
	if err != nil {
		c.logger.Error("Failed to list tools via direct call to upstream server",
			zap.String("server", c.config.Name),
			zap.Error(err))
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// Convert to our format
	tools := []*config.ToolMetadata{}
	for i := range toolsResult.Tools {
		tool := &toolsResult.Tools[i]
		var paramsJSON string
		if schemaBytes, err := json.Marshal(tool.InputSchema); err == nil {
			paramsJSON = string(schemaBytes)
		}

		toolMeta := &config.ToolMetadata{
			ServerName:  c.config.Name,
			Name:        tool.Name,
			Description: tool.Description,
			ParamsJSON:  paramsJSON,
		}
		tools = append(tools, toolMeta)
	}

	c.logger.Info("Successfully retrieved tools via direct call to upstream server",
		zap.String("server", c.config.Name),
		zap.Int("tool_count", len(tools)))

	return tools, nil
}

// CallTool executes a tool on the upstream server
func (c *Client) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	c.mu.RLock()
	client := c.client
	transportType := c.transportType
	c.mu.RUnlock()

	if !c.IsConnected() || client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// SSE transport requires request serialization to prevent concurrent request issues
	if transportType == "sse" {
		c.logger.Debug("SSE transport detected - serializing CallTool request",
			zap.String("server", c.config.Name),
			zap.String("tool", toolName))
		c.sseRequestMu.Lock()
		defer c.sseRequestMu.Unlock()
		c.logger.Debug("SSE request lock acquired for CallTool",
			zap.String("server", c.config.Name),
			zap.String("tool", toolName))
	}

	request := mcp.CallToolRequest{}
	request.Params.Name = toolName
	request.Params.Arguments = args

	// Log to server-specific log
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Starting CallTool operation",
			zap.String("tool_name", toolName))
	}

	// Log request for trace debugging
	if c.upstreamLogger != nil {
		if reqBytes, err := json.MarshalIndent(request, "", "  "); err == nil {
			c.upstreamLogger.Debug("JSON-RPC CallTool Request",
				zap.String("method", "tools/call"),
				zap.String("tool", toolName),
				zap.String("formatted_json", string(reqBytes)))
		}
	}

	// Add timeout wrapper to prevent hanging indefinitely
	// Use configured timeout or default to 2 minutes
	var timeout time.Duration
	if c.globalConfig != nil && c.globalConfig.CallToolTimeout.Duration() > 0 {
		timeout = c.globalConfig.CallToolTimeout.Duration()
	} else {
		timeout = 2 * time.Minute // Default fallback
	}

	// If the provided context doesn't have a timeout, add one
	callCtx := ctx
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > timeout {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Extra debug before sending request through transport
	c.logger.Debug("Starting upstream CallTool",
		zap.String("server", c.config.Name),
		zap.String("tool", toolName))

	result, err := client.CallTool(callCtx, request)
	if err != nil {
		// Log CallTool failure to server-specific log
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("CallTool operation failed",
				zap.String("tool_name", toolName),
				zap.Error(err))
		}

		// Provide more specific error context
		if callCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("CallTool '%s' timed out after %v", toolName, timeout)
		}

		// Extra diagnostics for broken pipe/closed pipe
		errStr := err.Error()
		if strings.Contains(errStr, "broken pipe") || strings.Contains(errStr, "closed pipe") {
			c.logger.Warn("CallTool write failed due to pipe closure",
				zap.String("server", c.config.Name),
				zap.String("tool", toolName),
				zap.String("transport", c.transportType))
		}

		return nil, fmt.Errorf("CallTool failed for '%s': %w", toolName, err)
	}

	// Log successful CallTool to server-specific log
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("CallTool operation completed successfully",
			zap.String("tool_name", toolName))
	}

	// Log response for trace debugging
	if c.upstreamLogger != nil {
		if respBytes, err := json.MarshalIndent(result, "", "  "); err == nil {
			c.upstreamLogger.Debug("JSON-RPC CallTool Response",
				zap.String("method", "tools/call"),
				zap.String("tool", toolName),
				zap.String("formatted_json", string(respBytes)))
		}
	}

	return result, nil
}

// GetConnectionInfo returns basic connection information
func (c *Client) GetConnectionInfo() types.ConnectionInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	state := types.StateDisconnected
	if c.connected {
		state = types.StateReady
	}

	return types.ConnectionInfo{
		State:      state,
		ServerName: c.getServerName(),
	}
}

// GetServerInfo returns server information from initialization
func (c *Client) GetServerInfo() *mcp.InitializeResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverInfo
}

// GetTransportType returns the transport type being used
func (c *Client) GetTransportType() string {
	return c.transportType
}

// GetStderr returns stderr reader for stdio transport
func (c *Client) GetStderr() io.Reader {
	return c.stderr
}

// GetEnvManager returns the environment manager for testing purposes
func (c *Client) GetEnvManager() interface{} {
	return c.envManager
}

// Helper methods

func (c *Client) getServerName() string {
	if c.serverInfo != nil {
		return c.serverInfo.ServerInfo.Name
	}
	return c.config.Name
}

func containsAny(str string, substrs []string) bool {
	for _, substr := range substrs {
		if substr != "" && len(str) >= len(substr) {
			for i := 0; i <= len(str)-len(substr); i++ {
				if str[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}

// Helper function to check if string contains substring
func containsString(str, substr string) bool {
	if substr == "" {
		return true
	}
	if len(str) < len(substr) {
		return false
	}

	for i := 0; i <= len(str)-len(substr); i++ {
		if str[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
