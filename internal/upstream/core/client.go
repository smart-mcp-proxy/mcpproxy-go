package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/hash"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/headerfwd"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secureenv"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	proxytransport "github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/launcher"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Client implements basic MCP client functionality without state management
type Client struct {
	id     string
	config *config.ServerConfig
	// exposePrompts mirrors config.ExposePrompts but can be updated without a
	// reconnect (PR #973 review, P2): config itself is set once in NewClient
	// and never reassigned, so ListPrompts/GetPrompt would otherwise keep
	// enforcing whatever ExposePrompts value was in effect when the
	// connection was created even after a config hot-reload. Updated via
	// SetExposePrompts, mirroring managed.Client's cfg pointer swap.
	exposePrompts atomic.Pointer[bool]
	globalConfig  *config.Config
	storage       *storage.BoltDB
	logger        *zap.Logger

	// Upstream server specific logger for debugging
	upstreamLogger *zap.Logger
	// upstreamLogCloser releases upstreamLogger's file sink. Disconnect
	// closes it (issue #1266); the sink reopens on the next write, so a
	// reconnecting server logs on as before. nil for console (CLI) loggers.
	upstreamLogCloser io.Closer

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

	// authStrategy holds the name of the HTTP/SSE auth strategy the current
	// connection was established with (see AuthStrategy). Atomic rather than
	// under c.mu because Connect holds c.mu for the whole attempt — an OAuth
	// flow can take minutes — and the server-list projection must be able to
	// read it without waiting on that.
	authStrategy atomic.Value

	// OAuth progress tracking (separate mutex to prevent reentrant deadlock)
	oauthMu            sync.RWMutex
	oauthInProgress    bool
	oauthCompleted     bool
	lastOAuthTimestamp time.Time

	// SSE request serialization (prevent concurrent requests on SSE transport)
	// SSE transport has limitations with concurrent requests - responses can get lost
	// when multiple requests are in-flight simultaneously
	sseRequestMu sync.Mutex

	// retryAfter collects the `Retry-After` hints this upstream's HTTP/SSE
	// responses carry. mcp-go flattens a 429 into an error string long before
	// the connection state machine sees it, so the hint is captured by a
	// RoundTripper installed under the MCP client and read back here via
	// RetryAfterDeadline (#1040).
	//
	// It is a generation pointer, not a fixed recorder: each connect attempt
	// swaps in a fresh one (beginRetryAfterGeneration). Transports built for an
	// earlier attempt keep the recorder they captured at construction, so a
	// request still in flight on a superseded client cannot write into the
	// current attempt's slate — and the current attempt starts empty by
	// construction rather than by racing a Clear.
	retryAfter atomic.Pointer[proxytransport.RetryAfterRecorder]

	// forwardPolicy supplies the LIVE client-header forwarding policy (Spec 112
	// R6). The managed client installs a closure over its atomically swapped
	// config, so allowlist edits and the global switch apply without a
	// reconnect. Only the provider function is stored here: no header names or
	// values live on the client (FR-011). Unset means nothing is forwarded.
	forwardPolicy atomic.Pointer[func() headerfwd.Policy]

	// Transport type and stderr access (for stdio)
	transportType string
	stderr        io.Reader

	// Cached tools list from successful immediate call
	cachedTools []mcp.Tool

	// monitoringMu serializes the stderr/process monitoring lifecycle methods
	// (Start*/Stop*Monitoring). Connect (StartStderrMonitoring) and Disconnect
	// (StopStderrMonitoring) can run concurrently on the same client during a
	// reconcile-vs-shutdown overlap, racing the ctx/cancel/WaitGroup fields
	// below (notably WG.Add vs WG.Wait). This mutex makes start and stop
	// mutually exclusive. It is never held across c.mu.
	monitoringMu sync.Mutex

	// Stderr monitoring. stderrMonitoringDone is a per-cycle channel closed by
	// the monitor goroutine when it exits; Stop waits on it instead of a reused
	// sync.WaitGroup, so an abandoned (timed-out) wait never races a later
	// Start's counter. All three fields are written only under monitoringMu.
	stderrMonitoringCtx    context.Context
	stderrMonitoringCancel context.CancelFunc
	stderrMonitoringDone   chan struct{}

	// Ring buffer of recent stderr lines from the subprocess.
	// Populated by monitorStderr; surfaced in initialize failure messages so
	// users don't have to hunt through server logs to see why the child
	// process never responded.
	recentStderrMu sync.Mutex
	recentStderr   []string

	// Process monitoring (for stdio transport)
	processCmd           *exec.Cmd
	processGroupID       int // Process group ID for proper cleanup
	processMonitorCtx    context.Context
	processMonitorCancel context.CancelFunc
	processMonitorDone   chan struct{}

	// Docker container tracking
	containerID     string
	containerOwner  string // com.mcpproxy.server label read back when containerID was verified (Spec 105 D9)
	containerName   string // Store container name for cleanup via docker container commands
	isDockerCommand bool

	// Local launcher tracking — only populated when this Client is using
	// HTTP/SSE/streamable-HTTP transport AND ServerConfig.Command is set.
	// In that mode mcpproxy spawns the upstream process before connecting,
	// and owns its lifecycle via the handle below. Stdio servers leave
	// these fields nil — they spawn through mcp-go's stdio transport.
	launcherHandle  launcher.Handle
	launcherCIDFile string

	// Notification callback for tools/list_changed
	onToolsChanged func(serverName string)

	// Notification callback for prompts/list_changed (F13)
	onPromptsChanged func(serverName string)
}

// NewClient creates a new core MCP client
func NewClient(id string, serverConfig *config.ServerConfig, logger *zap.Logger, logConfig *config.LogConfig, globalConfig *config.Config, storage *storage.BoltDB, secretResolver *secret.Resolver) (*Client, error) {
	return NewClientWithOptions(id, serverConfig, logger, logConfig, globalConfig, storage, false, secretResolver)
}

// NewClientWithOptions creates a new core MCP client with additional options
func NewClientWithOptions(id string, serverConfig *config.ServerConfig, logger *zap.Logger, logConfig *config.LogConfig, globalConfig *config.Config, storage *storage.BoltDB, cliDebugMode bool, secretResolver *secret.Resolver) (*Client, error) {
	// Deep-copy the config so expansion never mutates the caller's value (FR-004).
	// ExpandStructSecretsCollectErrors resolves ${env:...} and ${keyring:...} refs in
	// every string field of ServerConfig and its nested structs (IsolationConfig, OAuthConfig)
	// without an explicit per-field allowlist (FR-001 / US2).
	resolvedServerConfig := config.CopyServerConfig(serverConfig)
	if secretResolver != nil {
		ctx := context.Background()
		errs := secretResolver.ExpandStructSecretsCollectErrors(ctx, resolvedServerConfig)
		for _, e := range errs {
			logger.Error("CRITICAL: Failed to resolve secret reference - field will use UNRESOLVED placeholder",
				zap.String("server", serverConfig.Name),
				zap.String("field", e.FieldPath),
				zap.String("reference", e.Reference),
				zap.Error(e.Err),
				zap.String("help", "Use Web UI (http://localhost:8080/ui/) or API to add the secret to keyring"))
		}
	}

	c := &Client{
		id:             id,
		config:         resolvedServerConfig,
		globalConfig:   globalConfig,
		storage:        storage,
		secretResolver: secretResolver, // Store resolver for future use
		logger: logger.With(
			zap.String("upstream_id", id),
			zap.String("upstream_name", serverConfig.Name),
		),
	}
	c.exposePrompts.Store(resolvedServerConfig.ExposePrompts)
	c.retryAfter.Store(proxytransport.NewRetryAfterRecorder())

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
		// MCP-2769: opt-in proxy env forwarding to spawned stdio upstreams.
		if globalConfig != nil {
			envConfigCopy.ForwardProxyEnv = globalConfig.ForwardProxyEnv
		}
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
		var upstreamLogCloser io.Closer
		var err error

		// Use CLI logger for debugging or regular logger for daemon mode
		if cliDebugMode {
			upstreamLogger, err = logs.CreateCLIUpstreamServerLogger(logConfig, serverConfig.Name)
		} else {
			upstreamLogger, upstreamLogCloser, err = logs.NewUpstreamServerLogger(logConfig, serverConfig.Name)
		}

		if err != nil {
			logger.Warn("Failed to create upstream server logger",
				zap.String("server", serverConfig.Name),
				zap.Bool("cli_debug_mode", cliDebugMode),
				zap.Error(err))
		} else {
			c.upstreamLogger = upstreamLogger
			c.upstreamLogCloser = upstreamLogCloser
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

// Ping issues the MCP-standard lightweight liveness check (`ping`) to the
// upstream server. It is used by the managed client's health loop as a cheap
// replacement for re-listing every tool just to confirm the connection is
// alive (spec 074, FR-001). Returns an error if the client is not connected or
// the request fails so the caller can classify and drive reconnection.
func (c *Client) Ping(ctx context.Context) error {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if !c.IsConnected() || client == nil {
		return fmt.Errorf("client not connected")
	}

	// Spec 058: mcp-go deprecated Ping because the RPC was removed in protocol
	// 2026-07-28, where it returns success WITHOUT sending anything — a health
	// probe that always passes is worse than none, since it reports a dead
	// upstream as healthy.
	//
	// This remains a real probe today only because the upstream handshake is
	// pinned to the legacy era (FR-027, connection_lifecycle.go). Lifting that
	// pin MUST come with an era-aware replacement, or spec 074's health loop
	// goes blind; that is tracked as a required task of the pin lift, not as
	// cleanup.
	//nolint:staticcheck // SA1019: valid on the legacy era this client is pinned to; see above.
	return client.Ping(ctx)
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
		// Debug, not Error: both callers above (managed.Client.ListTools and
		// upstream.Manager.discoverTools) log this same failure, so logging it
		// here made one transient sweep miss cost three ERROR lines. The
		// outermost caller is the only layer that knows whether the failure
		// matters — a periodic sweep just retries on the next cycle.
		c.logger.Debug("Failed to list tools via direct call to upstream server",
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

		// Spec 056 (FR-A1): capture the tool's declared output schema so it is
		// available at call time for output-schema validation. captureOutputSchemaJSON
		// (Spec 056 / #527) prefers raw schema bytes and normalizes them for a stable
		// contract hash; a tool with no declared schema yields "", making validation a
		// no-op (FR-A7).
		outputSchemaJSON := captureOutputSchemaJSON(tool)

		// Spec 105 FR-009: stamp the exact upstream-reported name as RawName.
		// Name carries the same raw string for the index/search seams (#871),
		// but only RawName is an unambiguous identity — a raw name may itself
		// contain colons ("ns:erase") or even begin with this server's own
		// prefix, and every producer downstream (approval records, index
		// docIDs) keys on it via config.RawToolName.
		toolMeta := &config.ToolMetadata{
			ServerName:       c.config.Name,
			Name:             tool.Name,
			RawName:          tool.Name,
			Description:      tool.Description,
			ParamsJSON:       paramsJSON,
			OutputSchemaJSON: outputSchemaJSON,
		}

		// ToolAnnotation is a value type in mcp-go. Preserve even its zero value:
		// a fresh tools/list response has captured annotation metadata, while a
		// nil record is reserved for legacy records written before capture existed.
		// This makes an upstream annotations:{} record tier "unannotated", not
		// "unknown", in the informed review payload.
		hasAnnotations := tool.Annotations.Title != "" ||
			tool.Annotations.ReadOnlyHint != nil ||
			tool.Annotations.DestructiveHint != nil ||
			tool.Annotations.IdempotentHint != nil ||
			tool.Annotations.OpenWorldHint != nil

		// Log tool annotations at debug level for troubleshooting
		if hasAnnotations {
			c.logger.Debug("Tool with annotations from server",
				zap.String("server", c.config.Name),
				zap.String("tool", tool.Name),
				zap.String("title", tool.Annotations.Title))
		}

		toolMeta.Annotations = toolAnnotationsFromWire(tool.Annotations)

		// Compute hash for tool change detection.
		// Hash is based on serverName + toolName + description + inputSchema + outputSchema.
		toolMeta.Hash = hash.ComputeToolHashWithOutputSchema(c.config.Name, tool.Name, tool.Description, tool.InputSchema, outputSchemaJSON)

		tools = append(tools, toolMeta)
	}

	c.logger.Info("Successfully retrieved tools via direct call to upstream server",
		zap.String("server", c.config.Name),
		zap.Int("tool_count", len(tools)))

	return tools, nil
}

func toolAnnotationsFromWire(annotation mcp.ToolAnnotation) *config.ToolAnnotations {
	return &config.ToolAnnotations{
		Title:           annotation.Title,
		ReadOnlyHint:    annotation.ReadOnlyHint,
		DestructiveHint: annotation.DestructiveHint,
		IdempotentHint:  annotation.IdempotentHint,
		OpenWorldHint:   annotation.OpenWorldHint,
	}
}

// CallTool executes a tool on the upstream server
//
// Spec 112: this is the ONLY place that derives the per-server outbound set of
// forwarded client headers (from the edge snapshot in ctx, key A) and puts it
// on the context handed to mcp-go (key B). Every other request (initialize,
// list, reconnect) never sets key B and so cannot forward.
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
	if args == nil {
		args = map[string]interface{}{}
	}
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

	// Spec 112: derive the outbound set from the edge snapshot and this
	// server's live policy. The resolved transport type is authoritative here
	// ("auto" is already resolved), so SSE and stdio never forward (FR-014).
	var (
		outbound     headerfwd.Snapshot
		forwardAllow []string
	)
	if snap, ok := headerfwd.SnapshotFrom(ctx); ok && !snap.IsEmpty() {
		if pp := c.forwardPolicy.Load(); pp != nil {
			policy := (*pp)()
			policy.Transport = transportType
			forwardAllow = policy.Allow
			outbound = headerfwd.Outbound(snap, policy)
		}
	}
	callCtx = headerfwd.WithOutbound(callCtx, outbound)
	headerfwd.RecordOutbound(ctx, outbound)
	if !outbound.IsEmpty() {
		// Names and count only (FR-015a).
		c.logger.Debug("Forwarding client headers on tools/call",
			zap.String("server", c.config.Name),
			zap.Strings("headers", outbound.Names()),
			zap.Int("count", outbound.Len()))
	}

	// Extra debug before sending request through transport
	c.logger.Debug("Starting upstream CallTool",
		zap.String("server", c.config.Name),
		zap.String("tool", toolName))

	result, err := client.CallTool(callCtx, request)
	if err != nil {
		// Spec 112 FR-016.1: scrub echoed forwarded values before this error
		// reaches any sink (managed classification, health, activity, logs).
		err = headerfwd.ScrubError(err, outbound, forwardAllow)

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
				zap.String("formatted_json", headerfwd.Scrub(string(respBytes), outbound, forwardAllow)))
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

// httpTransportConfig builds the transport config for this client's HTTP/SSE
// connections, threading the per-server Retry-After recorder (#1040) into every
// mcp-go client we construct. Every HTTP/SSE connect path must go through it —
// a branch that calls proxytransport.CreateHTTPTransportConfig directly would
// silently lose the rate-limit hint for that auth strategy.
func (c *Client) httpTransportConfig(serverConfig *config.ServerConfig, oauthConfig *client.OAuthConfig) *proxytransport.HTTPTransportConfig {
	cfg := proxytransport.CreateHTTPTransportConfig(serverConfig, oauthConfig)
	// The transport captures THIS generation's recorder. A later attempt swaps
	// the pointer, and this client keeps writing to the recorder it was built
	// with — which is exactly what keeps generations from bleeding into each
	// other (#1040).
	cfg.RetryAfter = c.retryAfter.Load()
	// Spec 112 FR-018: the trace transport masks the live allowlisted names.
	cfg.ForwardNames = func() []string {
		if pp := c.forwardPolicy.Load(); pp != nil {
			return (*pp)().Allow
		}
		return nil
	}
	return cfg
}

// SetForwardPolicyProvider installs the live forwarding policy provider
// (Spec 112). It is read on every CallTool, so it takes effect immediately and
// never forces a reconnect. Passing nil removes forwarding.
func (c *Client) SetForwardPolicyProvider(p func() headerfwd.Policy) {
	if p == nil {
		c.forwardPolicy.Store(nil)
		return
	}
	c.forwardPolicy.Store(&p)
}

// beginRetryAfterGeneration retires the current recorder and installs a fresh
// one for a new connect attempt. Swapping rather than clearing means a request
// still in flight on a superseded transport writes into the retired recorder,
// where it can no longer be mistaken for something this attempt observed.
func (c *Client) beginRetryAfterGeneration() {
	c.retryAfter.Store(proxytransport.NewRetryAfterRecorder())
}

// RetryAfterDeadline reports the instant before which this upstream asked us not
// to come back (from a `Retry-After` on a 429/503), or the zero time when it gave
// no such hint. The managed client stamps it onto the state machine so both
// reconnect gates honour it (#1040).
func (c *Client) RetryAfterDeadline() time.Time {
	return c.retryAfter.Load().Deadline()
}

// ClearRetryAfter drops any recorded rate-limit hint. Called once a connection
// succeeds so a hint observed by an auth strategy that was superseded by a
// working one cannot hold back a later, unrelated reconnect.
func (c *Client) ClearRetryAfter() {
	c.retryAfter.Load().Clear()
}

// GetServerInfo returns server information from initialization
func (c *Client) GetServerInfo() *mcp.InitializeResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverInfo
}

// GetContainerID returns the Docker container ID if this is a Docker-based server
func (c *Client) GetContainerID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.containerID
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

// GetOAuthHandler returns the OAuth handler if the transport supports OAuth.
// Returns nil if no OAuth handler is configured or transport doesn't support OAuth.
func (c *Client) GetOAuthHandler() *transport.OAuthHandler {
	c.mu.RLock()
	mcpClient := c.client
	c.mu.RUnlock()

	return extractOAuthHandler(mcpClient)
}

// getOAuthHandlerLocked returns the OAuth handler without acquiring c.mu.
// MUST only be called when c.mu is already held by the caller (e.g., from Connect()).
func (c *Client) getOAuthHandlerLocked() *transport.OAuthHandler {
	return extractOAuthHandler(c.client)
}

// extractOAuthHandler extracts the OAuth handler from an MCP client's transport.
func extractOAuthHandler(mcpClient *client.Client) *transport.OAuthHandler {
	if mcpClient == nil {
		return nil
	}

	// Both StreamableHTTP and SSE transports implement GetOAuthHandler()
	type oauthTransport interface {
		GetOAuthHandler() *transport.OAuthHandler
	}

	t := mcpClient.GetTransport()
	if ot, ok := t.(oauthTransport); ok {
		return ot.GetOAuthHandler()
	}
	return nil
}

// RefreshOAuthTokenDirect forces an OAuth token refresh without reconnecting.
// This is used by the RefreshManager for proactive token refresh.
// Unlike ForceReconnect (which returns early when already connected),
// this refreshes even a still-valid token.
//
// Spec 113 FR-001: the refresh runs as a flight of the process-wide
// oauth.RefreshCoordinator, shared with the reactive path of the token store
// handed to mcp-go, so one expiry causes at most one token request. The flight
// uses the same refresh function as the reactive path (oauthRefreshFunc):
// mcp-go's handler.RefreshToken when the live handler has a client id, else
// the stored DCR credentials.
func (c *Client) RefreshOAuthTokenDirect(ctx context.Context) error {
	handler := c.GetOAuthHandler()
	if handler == nil {
		return fmt.Errorf("no OAuth handler available for %s", c.config.Name)
	}

	if c.storage == nil {
		return fmt.Errorf("no storage available for token refresh")
	}

	serverKey := oauth.GenerateServerKey(c.config.Name, c.config.URL)
	record, err := c.storage.GetOAuthToken(serverKey)
	if err != nil {
		return fmt.Errorf("failed to get stored token for %s: %w", c.config.Name, err)
	}
	if record.RefreshToken == "" {
		return fmt.Errorf("no refresh token available for %s", c.config.Name)
	}

	c.logger.Info("Executing direct OAuth token refresh",
		zap.String("server", c.config.Name),
		zap.Time("current_expiry", record.ExpiresAt),
		zap.Bool("handler_has_credentials", handler.GetClientID() != ""),
		zap.Bool("storage_has_credentials", record.ClientID != ""))

	req := c.oauthRefreshRequest(serverKey, handler)
	req.ObservedRefreshToken = record.RefreshToken
	req.Trigger = oauth.RefreshTriggerProactive
	_, skipped, err := oauth.DefaultRefreshCoordinator().Do(ctx, req)
	if err != nil {
		c.logger.Error("Direct OAuth token refresh failed",
			zap.String("server", c.config.Name),
			zap.Error(err))
		return fmt.Errorf("OAuth refresh failed for %s: %w", c.config.Name, err)
	}

	c.logger.Info("Direct OAuth token refresh completed successfully",
		zap.String("server", c.config.Name),
		zap.Bool("network_skipped", skipped))
	return nil
}

// oauthTokenResponse represents the token response from an OAuth server
type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
	Error        string `json:"error,omitempty"`
}

// refreshTokenWithStoredCredentials performs a token refresh using credentials
// from storage. A non-2xx response (or a 2xx carrying an RFC 6749 §5.2 error
// body) is returned as a typed *oauth.RefreshHTTPError (Spec 113 FR-007).
func (c *Client) refreshTokenWithStoredCredentials(ctx context.Context, tokenEndpoint string, record *storage.OAuthTokenRecord) (*storage.OAuthTokenRecord, error) {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", record.RefreshToken)
	data.Set("client_id", record.ClientID)
	if record.ClientSecret != "" {
		data.Set("client_secret", record.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var tokenResp oauthTokenResponse
	jsonErr := json.Unmarshal(body, &tokenResp)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 || (jsonErr == nil && tokenResp.Error != "") {
		// Issue #1158 (review round 2, investigation 2). The token endpoint's
		// error BODY was embedded verbatim, and this error is logged with the
		// server name and returned to the REST caller. Two problems, both real:
		// an OAuth error_description is provider-authored free text that does
		// echo request parameters back (and this request's form carries
		// client_secret and refresh_token), and the body is unbounded, so a
		// 502 HTML page from a proxy in front of the endpoint went into
		// main.log whole. Scrub with the free-text rule, then cap.
		// The exact credentials this request sent are removed verbatim first:
		// an opaque value echoed without a key=value shape is invisible to
		// the pattern scrubber.
		sent := []string{record.RefreshToken, record.ClientSecret, c.extraParamValue("client_secret")}
		detail := redactSent(string(body), sent...)
		httpErr := &oauth.RefreshHTTPError{Status: resp.StatusCode, Detail: cappedScrub(detail, 512)}
		code := ""
		if jsonErr == nil {
			code = tokenResp.Error
		} else if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			// A non-JSON body is capped above; take the RFC 6749 §5.2 code
			// from the whole body so the cap cannot hide it from
			// classification. Not for 5xx/429: a gateway page that merely
			// mentions a code must stay transient (the status decides).
			code = rfc6749CodeIn(detail)
		}
		httpErr.OAuthCode = cappedScrub(redactSent(code, sent...), 64)
		return nil, httpErr
	}

	if jsonErr != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", jsonErr)
	}

	var expiresAt time.Time
	if tokenResp.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	} else {
		// Default to 1 hour if server doesn't specify (consistent with mcp-go behavior)
		expiresAt = time.Now().Add(1 * time.Hour)
	}

	return &storage.OAuthTokenRecord{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresAt:    expiresAt,
	}, nil
}

// redactSent removes the exact credential values a token request sent.
func redactSent(s string, sent ...string) string {
	for _, v := range sent {
		if v != "" {
			s = strings.ReplaceAll(s, v, "[REDACTED]")
		}
	}
	return s
}

// rfc6749CodeIn finds a known RFC 6749 §5.2 error code in a non-JSON body.
func rfc6749CodeIn(body string) string {
	for _, code := range []string{"invalid_grant", "invalid_client", "unauthorized_client",
		"unsupported_grant_type", "invalid_scope", "temporarily_unavailable", "server_error"} {
		if strings.Contains(body, code) {
			return code
		}
	}
	return ""
}

// GetConfig returns the server configuration
func (c *Client) GetConfig() *config.ServerConfig {
	return c.config
}

// SetExposePrompts updates the ExposePrompts override without requiring a
// reconnect (PR #973 review, P2). Call this whenever the owning
// managed.Client's config is refreshed so a hot-reloaded expose_prompts
// value takes effect immediately instead of only after the connection is
// torn down and recreated.
func (c *Client) SetExposePrompts(exposePrompts *bool) {
	c.exposePrompts.Store(exposePrompts)
}

// SetOnToolsChangedCallback sets the callback invoked when a notifications/tools/list_changed
// notification is received from the upstream MCP server. This enables reactive tool re-indexing.
func (c *Client) SetOnToolsChangedCallback(callback func(serverName string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onToolsChanged = callback
}

// SetOnPromptsChangedCallback sets the callback invoked when a
// notifications/prompts/list_changed notification is received from the upstream
// MCP server (F13). Enables reactive re-aggregation of upstream prompts.
func (c *Client) SetOnPromptsChangedCallback(callback func(serverName string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onPromptsChanged = callback
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

// oauthLogger returns the logger the OAuth layer should write through.
//
// It tees the main logger and this upstream's own per-server log. Both halves
// matter:
//
//   - c.logger reaches main.log, where operators and the docs look first.
//   - c.upstreamLogger writes server-<name>.log, which is what
//     `mcpproxy upstream logs <name>` serves. docs/configuration.md points the
//     operator at that command for a redirect_uri failure, so the records have
//     to actually be there — otherwise they find nothing and conclude the
//     diagnostic does not exist.
//
// Before this, internal/oauth logged through zap.L(), which is the no-op logger
// in this binary (zap.ReplaceGlobals is never called), so neither destination
// got anything at all.
func (c *Client) oauthLogger() *zap.Logger {
	switch {
	case c.upstreamLogger == nil:
		return c.logger
	case c.logger == nil:
		return c.upstreamLogger
	default:
		return zap.New(zapcore.NewTee(c.logger.Core(), c.upstreamLogger.Core()))
	}
}

// cappedScrub renders an upstream-authored response body for an error message:
// the free-form rule first, then the cap, and the cut moved back to a rune
// boundary because the mask rendering is multi-byte.
//
// Scrub-then-cap, not cap-then-scrub: cutting first hands the detectors a
// FRAGMENT of any credential straddling the boundary, and every vendor-shaped
// matcher is anchored on a complete token, so the fragment matches nothing and
// its leading bytes are published.
func cappedScrub(s string, limit int) string {
	scrubbed := oauth.ScrubUpstreamText(s)
	if len(scrubbed) <= limit {
		return scrubbed
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(scrubbed[cut]) {
		cut--
	}
	return scrubbed[:cut] + "… (truncated)"
}
