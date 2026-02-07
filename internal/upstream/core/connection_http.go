package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
	"go.uber.org/zap"
)

// connectHTTP establishes HTTP transport connection with auth fallback
func (c *Client) connectHTTP(ctx context.Context) error {
	// Try authentication strategies in order: headers -> no-auth -> OAuth
	authStrategies := []func(context.Context) error{
		c.tryHeadersAuth,
		c.tryNoAuth,
		c.tryOAuthAuth,
	}

	var lastErr error
	for i, authFunc := range authStrategies {
		strategyName := []string{"headers", "no-auth", "OAuth"}[i]
		c.logger.Debug("🔐 Trying authentication strategy",
			zap.Int("strategy_index", i),
			zap.String("strategy", strategyName))

		if err := authFunc(ctx); err != nil {
			lastErr = err
			c.logger.Debug("🚫 Auth strategy failed",
				zap.Int("strategy_index", i),
				zap.String("strategy", strategyName),
				zap.Error(err))

			// For configuration errors (like no headers), always try next strategy
			if c.isConfigError(err) {
				continue
			}

			// For OAuth errors, continue to OAuth strategy
			if c.isOAuthError(err) {
				continue
			}

			// If it's not an auth error, don't try fallback
			if !c.isAuthError(err) {
				return err
			}
			continue
		}
		c.logger.Info("✅ Authentication successful",
			zap.Int("strategy_index", i),
			zap.String("strategy", strategyName))

		// Register notification handler for tools/list_changed
		c.registerNotificationHandler()

		return nil
	}

	return fmt.Errorf("all authentication strategies failed, last error: %w", lastErr)
}

// connectSSE establishes SSE transport connection with auth fallback
func (c *Client) connectSSE(ctx context.Context) error {
	// Try authentication strategies in order: headers -> no-auth -> OAuth
	authStrategies := []func(context.Context) error{
		c.trySSEHeadersAuth,
		c.trySSENoAuth,
		c.trySSEOAuthAuth,
	}

	var lastErr error
	for i, authFunc := range authStrategies {
		strategyName := []string{"headers", "no-auth", "OAuth"}[i]
		c.logger.Debug("🔐 Trying SSE authentication strategy",
			zap.Int("strategy_index", i),
			zap.String("strategy", strategyName))

		if err := authFunc(ctx); err != nil {
			lastErr = err
			c.logger.Debug("🚫 SSE auth strategy failed",
				zap.Int("strategy_index", i),
				zap.String("strategy", strategyName),
				zap.Error(err))

			// For configuration errors (like no headers), always try next strategy
			if c.isConfigError(err) {
				continue
			}

			// For OAuth errors, continue to OAuth strategy
			if c.isOAuthError(err) {
				continue
			}

			// If it's not an auth error, don't try fallback
			if !c.isAuthError(err) {
				return err
			}
			continue
		}
		c.logger.Info("✅ SSE Authentication successful",
			zap.Int("strategy_index", i),
			zap.String("strategy", strategyName))

		// Register notification handler for tools/list_changed
		c.registerNotificationHandler()

		return nil
	}

	return fmt.Errorf("all SSE authentication strategies failed, last error: %w", lastErr)
}

// tryHeadersAuth attempts authentication using configured headers
func (c *Client) tryHeadersAuth(ctx context.Context) error {
	if len(c.config.Headers) == 0 {
		return fmt.Errorf("no headers configured")
	}

	httpConfig := transport.CreateHTTPTransportConfig(c.config, nil)
	httpClient, err := transport.CreateHTTPClient(httpConfig)
	if err != nil {
		return fmt.Errorf("failed to create HTTP client with headers: %w", err)
	}

	c.client = httpClient

	// Start the client
	if err := c.client.Start(ctx); err != nil {
		return err
	}

	// CRITICAL FIX: Test initialize() to detect OAuth errors during auth strategy phase
	// This ensures OAuth strategy will be tried if headers-auth fails during MCP initialization
	if err := c.initialize(ctx); err != nil {
		return fmt.Errorf("MCP initialize failed during headers-auth strategy: %w", err)
	}

	return nil
}

// tryNoAuth attempts connection without authentication
func (c *Client) tryNoAuth(ctx context.Context) error {
	// Create config without headers
	configNoAuth := *c.config
	configNoAuth.Headers = nil

	httpConfig := transport.CreateHTTPTransportConfig(&configNoAuth, nil)
	httpClient, err := transport.CreateHTTPClient(httpConfig)
	if err != nil {
		return fmt.Errorf("failed to create HTTP client without auth: %w", err)
	}

	c.client = httpClient

	// Start the client
	if err := c.client.Start(ctx); err != nil {
		return err
	}

	// CRITICAL FIX: Test initialize() to detect OAuth errors during auth strategy phase
	// This ensures OAuth strategy will be tried if no-auth fails during MCP initialization
	if err := c.initialize(ctx); err != nil {
		return fmt.Errorf("MCP initialize failed during no-auth strategy: %w", err)
	}

	return nil
}

// trySSEHeadersAuth attempts SSE authentication using configured headers
func (c *Client) trySSEHeadersAuth(ctx context.Context) error {
	if len(c.config.Headers) == 0 {
		return fmt.Errorf("no headers configured")
	}

	httpConfig := transport.CreateHTTPTransportConfig(c.config, nil)
	sseClient, err := transport.CreateSSEClient(httpConfig)
	if err != nil {
		return fmt.Errorf("failed to create SSE client with headers: %w", err)
	}

	c.client = sseClient

	// Register connection lost handler for SSE transport to detect GOAWAY/disconnects
	c.client.OnConnectionLost(func(err error) {
		c.logger.Warn("⚠️ SSE connection lost detected",
			zap.String("server", c.config.Name),
			zap.Error(err),
			zap.String("transport", "sse"),
			zap.String("note", "Connection dropped by server or network - will attempt reconnection"))
	})

	// Start the client with persistent context so SSE stream keeps running
	// even if the connect context is short-lived (same as stdio transport).
	// SSE stream runs in a background goroutine and needs context to stay alive.
	persistentCtx := context.Background()
	if err := c.client.Start(persistentCtx); err != nil {
		return err
	}

	// CRITICAL FIX: Test initialize() to detect OAuth errors during auth strategy phase
	// This ensures OAuth strategy will be tried if SSE headers-auth fails during MCP initialization
	// Use caller's context for initialize() to respect timeouts
	if err := c.initialize(ctx); err != nil {
		return fmt.Errorf("MCP initialize failed during SSE headers-auth strategy: %w", err)
	}

	return nil
}

// trySSENoAuth attempts SSE connection without authentication
func (c *Client) trySSENoAuth(ctx context.Context) error {
	// Create config without headers
	configNoAuth := *c.config
	configNoAuth.Headers = nil

	httpConfig := transport.CreateHTTPTransportConfig(&configNoAuth, nil)
	sseClient, err := transport.CreateSSEClient(httpConfig)
	if err != nil {
		return fmt.Errorf("failed to create SSE client without auth: %w", err)
	}

	c.client = sseClient

	// Register connection lost handler for SSE transport to detect GOAWAY/disconnects
	c.client.OnConnectionLost(func(err error) {
		c.logger.Warn("⚠️ SSE connection lost detected",
			zap.String("server", c.config.Name),
			zap.Error(err),
			zap.String("transport", "sse"),
			zap.String("note", "Connection dropped by server or network - will attempt reconnection"))
	})

	// Start the client with persistent context so SSE stream keeps running
	// even if the connect context is short-lived (same as stdio transport).
	// SSE stream runs in a background goroutine and needs context to stay alive.
	persistentCtx := context.Background()
	if err := c.client.Start(persistentCtx); err != nil {
		return err
	}

	// CRITICAL FIX: Test initialize() to detect OAuth errors during auth strategy phase
	// This ensures OAuth strategy will be tried if SSE no-auth fails during MCP initialization
	// Use caller's context for initialize() to respect timeouts
	if err := c.initialize(ctx); err != nil {
		return fmt.Errorf("MCP initialize failed during SSE no-auth strategy: %w", err)
	}

	return nil
}

// isAuthError checks if error indicates authentication failure (non-OAuth)
func (c *Client) isAuthError(err error) bool {
	if err == nil {
		return false
	}

	// Don't catch OAuth errors here - they should be handled by isOAuthError() first
	if c.isOAuthError(err) {
		return false
	}

	errStr := err.Error()
	return containsAny(errStr, []string{
		"403", "Forbidden",
		"authentication", "auth",
	})
}

// isConfigError checks if error indicates a configuration issue that should trigger fallback
func (c *Client) isConfigError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return containsAny(errStr, []string{
		"no headers configured",
		"no command specified",
	})
}

// isDeprecatedEndpointError checks if error indicates a deprecated/removed endpoint (HTTP 410 Gone)
// This helps detect when an MCP server has migrated to a new endpoint URL
func (c *Client) isDeprecatedEndpointError(err error) bool {
	if err == nil {
		return false
	}

	// Check for transport.ErrEndpointDeprecated type first
	if transport.IsEndpointDeprecatedError(err) {
		return true
	}

	errStr := strings.ToLower(err.Error())
	deprecationIndicators := []string{
		"410",                            // HTTP 410 Gone
		"gone",                           // Status text
		"deprecated",                     // Common migration message
		"removed",                        // Endpoint removed
		"no longer supported",            // Common deprecation message
		"use the http transport",         // Sentry-specific migration hint
		"sse transport has been removed", // Sentry-specific error
	}

	for _, indicator := range deprecationIndicators {
		if strings.Contains(errStr, indicator) {
			return true
		}
	}

	return false
}

// isServerSideError checks if error indicates a server-side error (HTTP 5xx).
// Some servers crash with 500 instead of returning 401 when they receive
// an invalid/revoked token, so 5xx during OAuth strategy may indicate
// a stale token rather than a genuine server error.
func (c *Client) isServerSideError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return containsAny(errStr, []string{
		"status 500",
		"status 502",
		"status 503",
	})
}
