package upstream

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/skillstest"
)

func addSkillsServer(t *testing.T, m *Manager, id string, cfg *config.ServerConfig, fx *skillstest.Fixture, connect bool) {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	cfg.Protocol = "streamable-http"
	cfg.URL = fx.URL()
	require.NoError(t, m.AddServerConfig(id, cfg))
	client, ok := m.GetClient(id)
	require.True(t, ok)
	if connect {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, client.Connect(ctx))
	}
}

func TestManager_ListServerSkills(t *testing.T) {
	m := newTestManager(t)
	fx := skillstest.New(t)
	fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"a.md": []byte("A")}})
	addSkillsServer(t, m, "id-a", &config.ServerConfig{Name: "srv-a", Enabled: true}, fx, true)

	assert.True(t, m.ServerSupportsSkills("srv-a"))
	assert.False(t, m.ServerSupportsSkills("nope"))

	entries, errs, capture, err := m.ListServerSkills(context.Background(), "srv-a")
	require.NoError(t, err)
	assert.Empty(t, errs)
	require.Len(t, entries, 1)
	assert.NotZero(t, capture.Seq)

	content, isText, _, err := m.ReadServerSkillFile(context.Background(), "srv-a", entries[0].Files[0])
	require.NoError(t, err)
	assert.True(t, isText)
	assert.NotEmpty(t, content)
}

func TestManager_ListServerSkills_Refusals(t *testing.T) {
	m := newTestManager(t)

	_, _, _, err := m.ListServerSkills(context.Background(), "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no client found")

	fxDis := skillstest.New(t)
	addSkillsServer(t, m, "id-d", &config.ServerConfig{Name: "srv-dis", Enabled: false}, fxDis, false)
	_, _, _, err = m.ListServerSkills(context.Background(), "srv-dis")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")

	fxQ := skillstest.New(t)
	addSkillsServer(t, m, "id-q", &config.ServerConfig{Name: "srv-q", Enabled: true, Quarantined: true}, fxQ, false)
	_, _, _, err = m.ListServerSkills(context.Background(), "srv-q")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quarantined")
	_, _, _, err = m.ReadServerSkillFile(context.Background(), "srv-q", skills.FileRef{URI: "skill://pdf/SKILL.md", Size: 1})
	require.Error(t, err)
	assert.False(t, m.ServerSupportsSkills("srv-q"))
}

func TestManager_ListServerSkills_NeverReconnects(t *testing.T) {
	m := newTestManager(t)
	fx := skillstest.New(t)
	fx.AddSkill(skillstest.Skill{Path: "pdf"})
	addSkillsServer(t, m, "id-a", &config.ServerConfig{Name: "srv-a", Enabled: true, ReconnectOnUse: true}, fx, true)
	client, _ := m.GetClient("id-a")
	require.NoError(t, client.Disconnect())
	before := len(fx.Requests())

	_, _, _, err := m.ListServerSkills(context.Background(), "srv-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not connected")
	_, _, _, err = m.ReadServerSkillFile(context.Background(), "srv-a", skills.FileRef{URI: "skill://pdf/SKILL.md", Size: 1})
	require.Error(t, err)
	assert.Equal(t, before, len(fx.Requests()), "a background read must not wake the server")
}
