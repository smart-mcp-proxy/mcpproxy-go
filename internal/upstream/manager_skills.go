package upstream

import (
	"context"
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// skillsClient finds the managed client of a server for a skills read. It
// refuses a missing, disabled or quarantined server, and a server that is not
// connected. It never calls tryReconnectOnUse: a background skills read must
// not wake a sleeping server (Spec 125 D3).
func (m *Manager) skillsClient(serverName string) (*managed.Client, error) {
	m.mu.RLock()
	var target *managed.Client
	for _, c := range m.clients {
		if c == nil {
			continue
		}
		if cfg := c.GetConfig(); cfg != nil && cfg.Name == serverName {
			target = c
			break
		}
	}
	m.mu.RUnlock()

	if target == nil {
		return nil, fmt.Errorf("no client found for server: %s", serverName)
	}
	cfg := target.GetConfig()
	if !cfg.Enabled {
		return nil, fmt.Errorf("client for server %s is disabled", serverName)
	}
	if cfg.Quarantined {
		return nil, fmt.Errorf("server %s is quarantined", serverName)
	}
	if !target.IsConnected() {
		return nil, fmt.Errorf("server '%s' is not connected (state: %s)", serverName, target.GetState().String())
	}
	return target, nil
}

// ListServerSkills lists the skills one server serves. A *skills.TruncatedError
// comes back next to the partial entries.
func (m *Manager) ListServerSkills(ctx context.Context, serverName string) ([]skills.Entry, []skills.EntryError, managed.InventoryCapture, error) {
	c, err := m.skillsClient(serverName)
	if err != nil {
		return nil, nil, managed.InventoryCapture{}, err
	}
	return c.ListSkillsCaptured(ctx)
}

// ReadServerSkillFile reads one listed file of a server's skill. The caller
// verifies the digest.
func (m *Manager) ReadServerSkillFile(ctx context.Context, serverName string, ref skills.FileRef) ([]byte, bool, string, error) {
	c, err := m.skillsClient(serverName)
	if err != nil {
		return nil, false, "", err
	}
	return c.ReadSkillFile(ctx, ref)
}

// ServerSupportsSkills reports whether the named server is usable and
// announced the skills extension. False for any server a read would refuse.
func (m *Manager) ServerSupportsSkills(serverName string) bool {
	c, err := m.skillsClient(serverName)
	return err == nil && c.SupportsSkills()
}
