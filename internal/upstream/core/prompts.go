package core

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

// ListPrompts returns the prompts advertised by the upstream server. It
// returns (nil, nil) — not an error — when the server doesn't advertise
// Capabilities.Prompts, or when this server's config has explicitly opted
// out via ExposePrompts: false.
func (c *Client) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	c.mu.RLock()
	upstreamClient := c.client
	serverInfo := c.serverInfo
	transportType := c.transportType
	c.mu.RUnlock()

	if !c.IsConnected() || upstreamClient == nil {
		return nil, fmt.Errorf("client not connected")
	}

	if serverInfo == nil {
		return nil, fmt.Errorf("server info not available")
	}

	if serverInfo.Capabilities.Prompts == nil {
		return nil, nil
	}

	if v := c.exposePrompts.Load(); v != nil && !*v {
		return nil, nil
	}

	// SSE transport requires request serialization to prevent concurrent request issues
	if transportType == "sse" {
		c.logger.Debug("SSE transport detected - serializing ListPrompts request",
			zap.String("server", c.config.Name))
		c.sseRequestMu.Lock()
		defer c.sseRequestMu.Unlock()
	}

	// TODO: NextCursor is ignored, so only the first page is fetched. Fine for
	// now — ListTools has the same limitation today — but worth revisiting if
	// an upstream ever paginates prompts (PR #973 review).
	result, err := upstreamClient.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list prompts: %w", err)
	}

	return result.Prompts, nil
}

// GetPrompt resolves a single prompt by its unqualified name on the
// upstream server.
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	c.mu.RLock()
	upstreamClient := c.client
	serverInfo := c.serverInfo
	transportType := c.transportType
	c.mu.RUnlock()

	if !c.IsConnected() || upstreamClient == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// Mirror the ListPrompts gates (PR #973 review, finding F3): expose_prompts
	// was enforced only at list time, so a client that cached a prompt name (or
	// guesses it) could still fetch content from an opted-out server via
	// prompts/get. Enforce the same capability + expose_prompts checks here.
	// Unlike ListPrompts these return errors, not (nil, nil): a GetPrompt caller
	// must get either a result or an error, never a nil result.
	if serverInfo == nil {
		return nil, fmt.Errorf("server info not available")
	}
	if serverInfo.Capabilities.Prompts == nil {
		return nil, fmt.Errorf("server %s does not advertise prompts capability", c.config.Name)
	}
	if v := c.exposePrompts.Load(); v != nil && !*v {
		return nil, fmt.Errorf("prompts not exposed for server %s", c.config.Name)
	}

	// SSE transport requires request serialization to prevent concurrent request issues
	if transportType == "sse" {
		c.logger.Debug("SSE transport detected - serializing GetPrompt request",
			zap.String("server", c.config.Name),
			zap.String("prompt", name))
		c.sseRequestMu.Lock()
		defer c.sseRequestMu.Unlock()
	}

	request := mcp.GetPromptRequest{}
	request.Params.Name = name
	request.Params.Arguments = args

	result, err := upstreamClient.GetPrompt(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("GetPrompt failed for '%s': %w", name, err)
	}

	return result, nil
}
