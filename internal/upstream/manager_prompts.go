package upstream

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Finding F12 (P2): the prompt path had no size or count caps, unlike tools
// (ToolResponseLimit / the caching Truncator with read_cache pagination). An
// upstream advertising 100k prompts or returning a multi-hundred-MB prompt
// result was forwarded untruncated. These are safety ceilings, not helpfulness
// knobs, so they are constants rather than config fields.
const (
	// maxPromptResultChars caps the byte size of any single prompt message's
	// text content forwarded from an upstream. Prompts have no pagination path,
	// so an oversized message is truncated in place with a clear marker. 1 MiB
	// is far above any legitimate system prompt yet bounds a hostile blob.
	maxPromptResultChars = 1 << 20 // 1,048,576

	// maxPromptsPerServer caps how many prompts one upstream server may
	// contribute to the aggregated list, so a single hostile server cannot
	// exhaust the total budget on its own.
	maxPromptsPerServer = 200

	// maxAggregatedPrompts is the total hard ceiling on aggregated upstream
	// prompts across all servers. Mirrors the ToolsLimit validation ceiling.
	maxAggregatedPrompts = 1000

	// promptTruncationMarker is appended to a prompt message whose text was cut
	// to maxPromptResultChars, so a client can tell mcpproxy clipped it.
	promptTruncationMarker = "\n\n... [prompt truncated by mcpproxy: exceeded size cap]"
)

// truncatePromptText returns text cut to at most limit bytes without splitting a
// multi-byte UTF-8 rune, with promptTruncationMarker appended, and whether a cut
// happened. It mirrors server.safeTruncateBytes' rune-boundary handling so
// forwarded PromptMessage text never carries invalid UTF-8 (which downstream
// JSON encoders reject). A limit <= 0 disables the cap.
func truncatePromptText(text string, limit int) (string, bool) {
	if limit <= 0 || len(text) <= limit {
		return text, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + promptTruncationMarker, true
}

// capPromptResultSize truncates every oversized TextContent message in result in
// place (Finding F12 cap 1), logging one Warn per truncated message so a clipped
// payload is never silent. Non-text content (image/audio/embedded-resource) is
// left untouched. result may be nil.
func capPromptResultSize(result *mcp.GetPromptResult, limit int, logger *zap.Logger, server, prompt string) {
	if result == nil {
		return
	}
	for i := range result.Messages {
		tc, ok := result.Messages[i].Content.(mcp.TextContent)
		if !ok {
			continue
		}
		capped, didCut := truncatePromptText(tc.Text, limit)
		if !didCut {
			continue
		}
		orig := len(tc.Text)
		tc.Text = capped
		result.Messages[i].Content = tc
		logger.Warn("upstream prompt result exceeded size cap; message text truncated",
			zap.String("server", server),
			zap.String("prompt", prompt),
			zap.Int("message_index", i),
			zap.Int("original_chars", orig),
			zap.Int("cap_chars", limit))
	}
}

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

		// Finding F12 (P2): per-server prompt-count cap. A hostile server must
		// not be able to advertise 100k prompts and dominate the aggregated
		// surface. No silent drop — the excess is logged.
		if len(prompts) > maxPromptsPerServer {
			m.logger.Warn("upstream server advertised more prompts than the per-server cap; extra dropped",
				zap.String("server", snapshot.name),
				zap.Int("advertised", len(prompts)),
				zap.Int("cap", maxPromptsPerServer),
				zap.Int("dropped", len(prompts)-maxPromptsPerServer))
			prompts = prompts[:maxPromptsPerServer]
		}

		for i := range prompts {
			prompts[i].Name = snapshot.name + ":" + prompts[i].Name
		}
		allPrompts = append(allPrompts, prompts...)
	}

	// Finding F12 (P2): total aggregated prompt-count cap as a backstop across
	// all servers. Logged, never silent.
	if len(allPrompts) > maxAggregatedPrompts {
		m.logger.Warn("aggregated upstream prompt count exceeded cap; extra prompts dropped",
			zap.Int("total", len(allPrompts)),
			zap.Int("cap", maxAggregatedPrompts),
			zap.Int("dropped", len(allPrompts)-maxAggregatedPrompts))
		allPrompts = allPrompts[:maxAggregatedPrompts]
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
	var targetConfig *config.ServerConfig
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
			targetConfig = cfg
			break
		}
	}
	m.mu.RUnlock()

	if targetClient == nil {
		return nil, fmt.Errorf("no client found for server: %s", serverName)
	}

	// Defense-in-depth (PR #973 review): ListPrompts already excludes
	// disabled/quarantined servers from the aggregated list, but that only
	// protects a client that discovers prompts through this proxy. Enforce
	// the same guards Manager.CallTool applies, so a client that already
	// knows a qualified prompt name from before a quarantine flip can't
	// still forward the call during the race window before the next
	// servers.changed-driven refresh.
	if !targetConfig.Enabled {
		return nil, fmt.Errorf("client for server %s is disabled", serverName)
	}
	if targetConfig.Quarantined {
		return nil, fmt.Errorf("server %s is quarantined", serverName)
	}

	result, err := targetClient.GetPrompt(ctx, promptName, args)
	if err != nil {
		return nil, err
	}

	// Finding F12 (P2): cap the size of forwarded prompt message text. Prompts
	// have no read_cache pagination, so oversized text is truncated in place.
	capPromptResultSize(result, maxPromptResultChars, m.logger, serverName, promptName)
	return result, nil
}
