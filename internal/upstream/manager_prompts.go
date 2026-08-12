package upstream

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// ListPrompts aggregates prompts from every connected, enabled,
// non-quarantined upstream client. Each returned prompt's Name is rewritten
// to "serverName:promptName" so a subsequent GetPrompt call can be routed
// back to the right server. A per-client failure is logged and that client
// is skipped; the rest of the aggregation proceeds.
func (m *Manager) ListPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	type clientSnapshot struct {
		id          string
		name        string
		enabled     bool
		quarantined bool
		client      *managed.Client
	}

	m.mu.RLock()
	snapshots := make([]clientSnapshot, 0, len(m.clients))
	for id, c := range m.clients {
		name := ""
		quarantined := false
		var cfg *config.ServerConfig
		if c != nil {
			cfg = c.GetConfig()
		}
		if cfg != nil {
			name = cfg.Name
			quarantined = cfg.Quarantined
		}
		snapshots = append(snapshots, clientSnapshot{
			id:          id,
			name:        name,
			enabled:     cfg != nil && cfg.Enabled,
			quarantined: quarantined,
			client:      c,
		})
	}
	m.mu.RUnlock()

	var allPrompts []mcp.Prompt
	for _, snapshot := range snapshots {
		c := snapshot.client
		if c == nil || !snapshot.enabled || snapshot.quarantined || !c.IsConnected() {
			continue
		}

		prompts, err := c.ListPrompts(ctx)
		if err != nil {
			m.logger.Warn("Failed to list prompts from client",
				zap.String("id", snapshot.id),
				zap.String("server", snapshot.name),
				zap.Error(err))
			continue
		}

		for i := range prompts {
			prompts[i].Name = snapshot.name + ":" + prompts[i].Name
		}
		allPrompts = append(allPrompts, prompts...)
	}

	return allPrompts, nil
}

// GetPrompt resolves a colon-qualified "serverName:promptName" and forwards
// the call to the owning upstream client.
func (m *Manager) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	parts := strings.SplitN(name, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid prompt name format: %s (expected server:prompt)", name)
	}
	serverName := parts[0]
	promptName := parts[1]

	m.mu.RLock()
	var targetClient *managed.Client
	for _, c := range m.clients {
		if c == nil {
			continue
		}
		cfg := c.GetConfig()
		if cfg == nil {
			continue
		}
		if cfg.Name == serverName {
			targetClient = c
			break
		}
	}
	m.mu.RUnlock()

	if targetClient == nil {
		return nil, fmt.Errorf("no client found for server: %s", serverName)
	}

	return targetClient.GetPrompt(ctx, promptName, args)
}
