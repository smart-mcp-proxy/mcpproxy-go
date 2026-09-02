package core

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"

	"go.uber.org/zap"
)

const (
	osLinux   = "linux"
	osDarwin  = "darwin"
	osWindows = "windows"

	dockerCleanupTimeout = 30 * time.Second

	// Subprocess shutdown timeouts
	// mcpClientCloseTimeout is the max time to wait for graceful MCP client close
	mcpClientCloseTimeout = 10 * time.Second
	// processGracefulTimeout is the max time to wait after SIGTERM before SIGKILL
	// Must be less than mcpClientCloseTimeout to complete within the close timeout
	processGracefulTimeout = 9 * time.Second
	// processTerminationPollInterval is how often to check if process exited
	processTerminationPollInterval = 100 * time.Millisecond

	// Transport types
	transportHTTP           = "http"
	transportHTTPStreamable = "streamable-http"
	transportSSE            = "sse"
)

// Context key types
type contextKey string

const (
	manualOAuthKey contextKey = "manual_oauth"
)

// ErrOAuthPending represents a deferred OAuth authentication requirement.
// This error indicates that OAuth is required but has been intentionally deferred
// (e.g., for user action via tray UI or CLI) rather than being a connection failure.

// IsOAuthPending checks if an error is an ErrOAuthPending

// Used by Phase 3 (Spec 020) to return auth URL and browser status synchronously.

// parseOAuthError extracts structured error information from OAuth provider responses

// Connect establishes connection to the upstream server
// logSafeURL renders the configured upstream URL for a LOG FIELD, with its
// query credentials masked (issue #1148).
//
// Connect() logs the URL on every attempt, to main.log and to the per-server
// server-<name>.log — and `upstream_servers tail_log` hands the latter to any
// MCP caller. A URL is one of the commonest places an MCP credential lives
// (`?token=…`, an Azure SAS `sig=`, an AWS `X-Amz-Signature=`), so the value is
// masked at the point it is written rather than only where it is read back:
// the file outlives the process and is readable by anything with disk access.
//
// The host and path survive, which is what makes the line diagnostic.
//
// #1158 (review round 2, finding B6): this delegates to oauth.LogSafeURL — the
// DEEP audit renderer — rather than the name-rule-only RedactURLQueryParams it
// used before. logSafeAuthURL below already argued that case for the authorize
// URL; the same argument applies verbatim to the configured URL this renders,
// and this value is served to REST callers inside OAuthErrorDetails.ServerURL.
func (c *Client) logSafeURL() string {
	if c.config == nil {
		return ""
	}
	return oauth.LogSafeURL(c.config.URL)
}

// logSafeAuthURL renders an OAuth AUTHORIZATION URL for a LOG FIELD or for
// stdout (issue #1158).
//
// The authorize URL is the highest-frequency credential leak in the tree and
// needs no debug flag to reach main.log: oauth.autoDetectResource returns
// serverConfig.URL verbatim on every fallback branch (no resource_metadata in
// WWW-Authenticate, metadata without a `resource` field, a failed fetch, any
// 4xx/5xx), that value becomes extraParams["resource"], and every authorize-URL
// builder splices each extra param into the query. The result was logged at
// INFO and printed with fmt.Printf on every login attempt, so a configured
// `https://host/mcp?token=SECRET` landed on disk and on the terminal.
//
// URLValueDeep rather than RedactURLQueryParams because the credential arrives
// percent-encoded one level down (`resource=https%3A%2F%2Fhost%2Fmcp%3Ftoken%3D...`),
// where neither the sensitive-parameter name rule nor secretPattern can see it.
//
// The authorize endpoint's scheme, host, path and its own non-secret parameters
// survive, which is what lets an operator still diagnose the flow.
func logSafeAuthURL(authURL string) string {
	return oauth.AuditRedaction.URLValueDeep(authURL)
}

// logSafeArgs renders a child process's argument vector for a LOG FIELD
// (issue #1158).
//
// Every spawn log line in this package previously used
// shellwrap.RedactDockerArgs alone, which is a STRUCTURAL rule: it masks the
// value half of every `-e KEY=VALUE` docker env injection but is blind to
// `--api-key sk-live-...` and to a vendor-formatted credential sitting in a
// positional argument. oauth.Redaction.SpawnArgv composes that rule with the
// flag-name + value-shape rule, and also reaches inside the single
// `-c "<whole command line>"` element the login-shell wrap produces - the form
// every non-docker stdio server on macOS actually spawns as.
//
// AuditRedaction rather than LiveRedaction because these lines land in
// ~/.mcpproxy/logs/main.log and server-<name>.log, which outlive the process
// and are exported through `upstream_servers tail_log`: the `••••` marker
// carries neither the secret's length nor its trailing bytes, unlike the
// interactive `sk-****89` rendering the previous helper emitted.
func logSafeArgs(args []string) []string {
	return oauth.AuditRedaction.SpawnArgv(args)
}

// logSafeCommand renders a whole child command LINE for a LOG FIELD. See
// logSafeArgs.
func logSafeCommand(cmd string) string {
	return oauth.AuditRedaction.SpawnCommandString(cmd)
}

// redactURLCredentialsInError strips URL-embedded credentials from an error's
// TEXT while keeping the error itself intact for errors.Is/As and for the
// substring classification the connect paths do (isAuthError, isConfigError,
// "connection refused"): only the sensitive query values and userinfo
// passwords are rewritten.
//
// Issue #1148, round 3: masking the `url` LOG FIELDS is not enough, because the
// transport error carries the same URL inside its message —
// `Post "http://host/mcp?token=…": dial tcp: connection refused` — and that
// message is logged at Error level by the manager on every failed attempt, is
// written to the per-server log, and is stored as the client's last error.
// Redacting once, where the transport error enters mcpproxy, is what keeps a
// future log site from re-leaking it.
func redactURLCredentialsInError(err error) error {
	if err == nil {
		return nil
	}
	// #1158 (review round 2, live check): upgraded from RedactSensitiveData to
	// ScrubUpstreamText, for the reason logSafeErrorField and its twin in
	// internal/transport were upgraded — the NAME rule cannot see a credential
	// under an unrecognised query-parameter name. A live run with
	// `?opaque=ghp_…` in the configured URL still put the token in main.log 15
	// times, because this ONE seam is what every downstream log site inherits:
	// managed.Client, upstream.Manager, the supervisor and the runtime all
	// zap.Error this value, and none of them can be fixed from here except
	// through the string they are handed.
	msg := oauth.ScrubUpstreamText(err.Error())
	if msg == err.Error() {
		return err
	}
	return &urlRedactedError{msg: msg, cause: err}
}

// urlRedactedError re-renders an error's message with credentials removed and
// keeps the original reachable through Unwrap.
type urlRedactedError struct {
	msg   string
	cause error
}

func (e *urlRedactedError) Error() string { return e.msg }
func (e *urlRedactedError) Unwrap() error { return e.cause }

// logSafeErrorField renders an error as a log field with any URL-embedded
// credential removed. Use it instead of zap.Error on the connection paths.
//
// #1158: upgraded from RedactSensitiveData to ScrubUpstreamText. The name rule
// alone cannot see a credential under an unrecognised parameter name
// (`?opaque=ghp_…`), and a transport error is exactly the free-form,
// originated-outside-mcpproxy text ScrubUpstreamText is documented for.
func logSafeErrorField(err error) zap.Field {
	if err == nil {
		return zap.Skip()
	}
	return zap.String("error", oauth.ScrubUpstreamText(err.Error()))
}

func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// CRITICAL FIX: Check for concurrent connection attempts to prevent duplicate containers
	if c.connecting {
		c.logger.Debug("Connection already in progress, rejecting concurrent attempt",
			zap.String("server", c.config.Name))
		return fmt.Errorf("connection already in progress")
	}

	// Allow reconnection if OAuth was recently completed (bypass "already connected" check)
	if c.connected && !c.wasOAuthRecentlyCompleted() {
		c.logger.Debug("Client already connected and OAuth not recent",
			zap.String("server", c.config.Name),
			zap.Bool("connected", c.connected))
		return fmt.Errorf("client already connected")
	}

	// Set connecting flag to prevent concurrent attempts
	c.connecting = true
	defer func() {
		c.connecting = false
	}()

	// Reset connection state for fresh connection attempt
	if c.connected {
		c.logger.Info("🔄 Reconnecting after OAuth completion",
			zap.String("server", c.config.Name))
		c.connected = false
		if c.client != nil {
			c.client.Close()
			c.client = nil
		}
	}

	// Start each attempt with an empty rate-limit slate (#1040). Without this a
	// hint recorded during an earlier attempt outlives it: a manual reconnect
	// that then fails for an unrelated reason (connection refused, DNS) would be
	// re-parked for the remainder of a window the upstream never repeated.
	// Whatever this attempt observes is recorded again, on its own merits.
	// A swap, not a Clear: a request still in flight on the superseded client
	// keeps the recorder it was built with instead of writing into this attempt.
	c.beginRetryAfterGeneration()

	c.logger.Info("Connecting to upstream MCP server",
		zap.String("server", c.config.Name),
		zap.String("url", c.logSafeURL()),
		zap.String("command", c.config.Command),
		zap.String("protocol", c.config.Protocol))

	// Determine transport type
	c.transportType = transport.DetermineTransportType(c.config)

	// Log to server-specific log file as well
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Starting connection attempt",
			zap.String("transport", c.transportType),
			zap.String("url", c.logSafeURL()),
			zap.String("command", c.config.Command),
			zap.String("protocol", c.config.Protocol))
	}

	// Debug: Show transport type determination
	c.logger.Debug("🔍 Transport Type Determination",
		zap.String("server", c.config.Name),
		zap.String("command", c.config.Command),
		zap.String("url", c.logSafeURL()),
		zap.String("protocol", c.config.Protocol),
		zap.String("determined_transport", c.transportType))

	// Create and connect client based on transport type
	var err error
	// Locally-launched HTTP/SSE upstreams: spawn the child process before
	// the transport-level connect, then wait for its URL to become
	// reachable. Stdio is excluded because the stdio transport spawns
	// through mcp-go itself; running the launcher here would double-spawn.
	switch c.transportType {
	case transportHTTP, transportHTTPStreamable, transportSSE:
		if c.config.Command != "" {
			c.logger.Debug("🚀 Launching local upstream before HTTP/SSE connect",
				zap.String("server", c.config.Name),
				zap.String("transport", c.transportType))
			if launchErr := c.connectWithLauncher(ctx); launchErr != nil {
				return fmt.Errorf("failed to launch local upstream: %w", launchErr)
			}
		}
	}
	switch c.transportType {
	case transportStdio:
		c.logger.Debug("📡 Using STDIO transport")
		err = c.connectStdio(ctx)
	case transportHTTP, transportHTTPStreamable:
		c.logger.Debug("🌐 Using HTTP transport")
		err = c.connectHTTP(ctx)
	case transportSSE:
		c.logger.Debug("📡 Using SSE transport")
		err = c.connectSSE(ctx)
	default:
		return fmt.Errorf("unsupported transport type: %s", c.transportType)
	}

	if err != nil {
		// #1148: the transport error text embeds the configured URL, credentials
		// and all. Redact it HERE, before it is logged, returned, or stored as
		// the client's last error — every consumer downstream inherits the safe
		// rendering, and the original is still reachable through Unwrap.
		err = redactURLCredentialsInError(err)

		// Log connection failure to server-specific log
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Connection failed",
				zap.String("transport", c.transportType),
				zap.Error(err))
		}

		// CRITICAL FIX: Cleanup Docker containers when any connection type fails
		// This prevents container accumulation when connections fail after Docker setup
		if c.isDockerCommand {
			c.logger.Warn("Connection failed for Docker command - cleaning up container",
				zap.String("server", c.config.Name),
				zap.String("transport", c.transportType),
				zap.String("container_name", c.containerName),
				zap.String("container_id", c.containerID),
				zap.Error(err))

			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), dockerCleanupTimeout)
			defer cleanupCancel()

			// Try to cleanup using container name first, then ID, then pattern matching
			if c.containerName != "" {
				c.logger.Debug("Attempting container cleanup by name after connection failure")
				if success := c.killDockerContainerByNameWithContext(cleanupCtx, c.containerName); success {
					c.logger.Info("Successfully cleaned up container by name after connection failure")
				}
			} else if c.containerID != "" {
				c.logger.Debug("Attempting container cleanup by ID after connection failure")
				c.killDockerContainerWithContext(cleanupCtx)
			} else {
				c.logger.Debug("Attempting container cleanup by pattern matching after connection failure")
				c.killDockerContainerByCommandWithContext(cleanupCtx)
			}
		}

		// CRITICAL FIX: Also cleanup process groups to prevent zombie processes on connection failure
		if c.processGroupID > 0 {
			c.logger.Warn("Connection failed - cleaning up process group to prevent zombie processes",
				zap.String("server", c.config.Name),
				zap.Int("pgid", c.processGroupID))

			if err := killProcessGroup(c.processGroupID, c.logger, c.config.Name); err != nil {
				c.logger.Error("Failed to clean up process group after connection failure",
					zap.String("server", c.config.Name),
					zap.Int("pgid", c.processGroupID),
					zap.Error(err))
			}
			c.processGroupID = 0
		}

		// Stop any locally-launched upstream child the HTTP/SSE path
		// started — connectWithLauncher itself only stops it on
		// wait-for-url failure, not on subsequent transport-level
		// connect failure.
		//
		// IMPORTANT: c.mu is held for the duration of Connect (see
		// the c.mu.Lock at the top of this function), so we can read
		// the launcher fields directly. We release the lock briefly
		// around handle.Stop because Stop blocks until the child is
		// reaped and we don't want to hold c.mu that long; the
		// `connecting` flag already prevents a concurrent Connect.
		if c.launcherHandle != nil {
			handle := c.launcherHandle
			cidFile := c.launcherCIDFile
			c.launcherHandle = nil
			c.launcherCIDFile = ""
			c.mu.Unlock()
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
			if stopErr := handle.Stop(stopCtx); stopErr != nil {
				c.logger.Warn("error stopping launcher during connect-failure cleanup",
					zap.String("server", c.config.Name),
					zap.Error(stopErr))
			}
			stopCancel()
			if cidFile != "" {
				_ = os.Remove(cidFile)
			}
			c.mu.Lock()
		}

		return fmt.Errorf("failed to connect: %w", err)
	}

	// CRITICAL FIX: Authentication strategies now handle initialize() testing
	// This eliminates the duplicate initialize() call that was causing OAuth strategy
	// to never be reached when no-auth succeeded at Start() but failed at initialize()
	// All authentication strategies (tryNoAuth, tryHeadersAuth, tryOAuthAuth) now test
	// both client.Start() AND c.initialize() to ensure OAuth errors are properly detected

	c.connected = true

	// The upstream answered, so any rate-limit window we were holding is spent.
	// Dropping it here keeps a stale hint from parking a future reconnect (#1040).
	c.ClearRetryAfter()

	// If we had an OAuth flow in progress and connection succeeded, mark OAuth as complete
	if c.isOAuthInProgress() {
		c.logger.Info("✅ OAuth flow completed successfully - connection established with token",
			zap.String("server", c.config.Name))
		c.markOAuthComplete()
	}

	c.logger.Info("Successfully connected to upstream MCP server",
		zap.String("server", c.config.Name),
		zap.String("transport", c.transportType))

	// Tools caching disabled - will make direct calls to upstream server each time
	c.logger.Debug("Tools caching disabled - will make direct calls to upstream server",
		zap.String("server", c.config.Name),
		zap.String("transport", c.transportType))

	// Log successful connection to server-specific log
	if c.upstreamLogger != nil {
		if c.serverInfo != nil && c.serverInfo.ServerInfo.Name != "" {
			c.upstreamLogger.Info("Successfully connected and initialized",
				zap.String("transport", c.transportType),
				zap.String("server_name", c.serverInfo.ServerInfo.Name),
				zap.String("server_version", c.serverInfo.ServerInfo.Version),
				zap.String("protocol_version", c.serverInfo.ProtocolVersion))
		} else {
			c.upstreamLogger.Info("Successfully connected",
				zap.String("transport", c.transportType),
				zap.String("note", "serverInfo not yet available"))
		}
	}

	return nil
}

// connectStdio establishes stdio transport connection

// handleOAuthAuthorization handles the manual OAuth flow following the mcp-go example pattern.
// extraParams contains auto-detected or manually configured OAuth extra parameters (e.g., RFC 8707 resource).

// handleOAuthAuthorizationWithResult handles the manual OAuth flow and returns the auth URL and browser status.
// This is used by Phase 3 (Spec 020) to return structured information about the OAuth flow start.

// isOAuthInProgress checks if OAuth is in progress

// markOAuthInProgress marks OAuth as in progress

// markOAuthComplete marks OAuth as complete and cleans up callback server

// wasOAuthRecentlyCompleted checks if OAuth was completed recently to prevent retry loops

// ClearOAuthState clears OAuth state (public API for manual OAuth flows)

// ForceOAuthFlow forces an OAuth authentication flow, bypassing rate limiting (for manual auth)

// StartOAuthFlowQuick starts the OAuth flow and returns browser status immediately.
// Unlike ForceOAuthFlowWithResult which blocks until OAuth completes, this function:
// 1. Gets authorization URL synchronously (quick operation)
// 2. Checks HEADLESS environment variable
// 3. Attempts browser open and captures result
// 4. Returns OAuthStartResult immediately
// 5. Continues OAuth callback handling in a goroutine
//
// This is used by the login API endpoint to return accurate browser_opened status
// without blocking the HTTP response for the full OAuth flow.

// getAuthorizationURLQuick gets the authorization URL without starting the full OAuth flow.
// Returns the URL, OAuth handler, code verifier, and state for later use.

// waitForOAuthCallbackAsync waits for OAuth callback and handles token exchange in background.

// ForceOAuthFlowWithResult forces an OAuth authentication flow and returns the auth URL and browser status.
// This is used by Phase 3 (Spec 020) to provide the auth URL to clients even when browser opens successfully.
// forceHTTPOAuthFlowWithResult forces OAuth flow for HTTP transport and returns auth URL/browser status.

// forceSSEOAuthFlowWithResult forces OAuth flow for SSE transport and returns auth URL/browser status.

// isManualOAuthFlow checks if this is a manual OAuth flow

// clearOAuthState clears OAuth state (for cleaning up stale state)
