package core

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// ListPrompts returns the prompts advertised by the upstream server. It
// returns (nil, nil) — not an error — when the server doesn't advertise
// Capabilities.Prompts, or when this server's config has explicitly opted
// out via ExposePrompts: false.
func (c *Client) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	c.mu.RLock()
	upstreamClient := c.client
	serverInfo := c.serverInfo
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

	if c.config.ExposePrompts != nil && !*c.config.ExposePrompts {
		return nil, nil
	}

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
	c.mu.RUnlock()

	if !c.IsConnected() || upstreamClient == nil {
		return nil, fmt.Errorf("client not connected")
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