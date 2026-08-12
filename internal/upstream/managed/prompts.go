package managed

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

// ListPrompts returns the prompts advertised by the upstream server.
func (mc *Client) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	if !mc.IsConnected() {
		return nil, fmt.Errorf("client not connected (state: %s)", mc.StateManager.GetState().String())
	}

	prompts, err := mc.coreClient.ListPrompts(ctx)
	if err != nil {
		mc.logger.Error("Failed to list prompts",
			zap.String("server", mc.GetConfig().Name),
			zap.Error(err))
		return nil, err
	}

	return prompts, nil
}

// GetPrompt resolves a single prompt by its unqualified name on the
// upstream server.
func (mc *Client) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	if !mc.IsConnected() {
		return nil, fmt.Errorf("client not connected (state: %s)", mc.StateManager.GetState().String())
	}

	result, err := mc.coreClient.GetPrompt(ctx, name, args)
	if err != nil {
		if mc.isConnectionError(err) {
			mc.StateManager.SetError(err)
		}
		mc.logger.Error("GetPrompt failed",
			zap.String("server", mc.GetConfig().Name),
			zap.String("prompt", name),
			zap.Error(err))
		return nil, err
	}

	return result, nil
}