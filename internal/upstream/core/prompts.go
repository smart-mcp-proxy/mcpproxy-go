package core

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

// Defensive pagination caps for ListPrompts (Finding F14). mcp-go's
// client.Client.ListPrompts follows NextCursor with only a ctx.Done() guard, so
// an upstream that returns a fresh cursor on every page could loop forever under
// a deadline-less aggregation context. We drive pagination ourselves via
// ListPromptsByPage and bound it with both a page and an item cap.
const (
	// maxListPromptsPages bounds how many prompts/list pages one upstream may be
	// paged through before ListPrompts gives up. Far above any legitimate server:
	// with any page size >= 4 the item cap below is reached first, so this only
	// bites the pathological "one item + endless cursor" server, stopping it
	// after 50 round-trips.
	maxListPromptsPages = 50

	// maxListPromptsItems bounds how many prompts ListPrompts accumulates for one
	// upstream before it stops following cursors. It mirrors the per-server cap in
	// manager_prompts.go (upstream.maxPromptsPerServer = 200, Finding F12), which
	// trims each server's contribution to that many, so fetching past it is pure
	// waste. Kept as a separate constant because core and upstream are different
	// packages; the two values MUST stay in sync.
	maxListPromptsItems = 200
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

	// Follow prompts/list pagination with a defensive page + item cap.
	//
	// mcp-go's client.Client.ListPrompts already follows NextCursor, but its loop
	// is bounded only by ctx cancellation, so a hostile upstream that hands out a
	// fresh cursor on every page would spin forever under a deadline-less
	// aggregation context, accumulating prompts in memory. Drive pagination via
	// ListPromptsByPage instead and stop at maxListPromptsPages pages, at
	// maxListPromptsItems accumulated prompts (aligned with the per-server trim in
	// Manager.ListPrompts), or on ctx expiry — whichever comes first (Finding F14).
	listReq := mcp.ListPromptsRequest{}
	var prompts []mcp.Prompt
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("failed to list prompts: %w", err)
		}

		result, err := upstreamClient.ListPromptsByPage(ctx, listReq)
		if err != nil {
			return nil, fmt.Errorf("failed to list prompts: %w", err)
		}
		prompts = append(prompts, result.Prompts...)

		if result.NextCursor == "" {
			break // normal termination: no more pages
		}
		if len(prompts) >= maxListPromptsItems {
			c.logger.Warn("upstream prompt pagination reached item cap; stopping",
				zap.String("server", c.config.Name),
				zap.Int("items", len(prompts)),
				zap.Int("cap", maxListPromptsItems))
			break
		}
		if page >= maxListPromptsPages {
			c.logger.Warn("upstream prompt pagination reached page cap; stopping",
				zap.String("server", c.config.Name),
				zap.Int("pages", page),
				zap.Int("cap", maxListPromptsPages))
			break
		}
		listReq.Params.Cursor = result.NextCursor
	}

	return prompts, nil
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
