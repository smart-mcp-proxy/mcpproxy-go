package server

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/skillstest"
)

// FR-019a: a scoped credential on the REST API gets the same answers as on the
// agent surface. The REST door reads skills through Server.AvailableSkillFile
// and Server.ListAvailableSkills, so those apply the output policy the agent
// tools apply.
func TestRealSkills_RESTDoorAppliesTheOutputPolicyLikeTheTools(t *testing.T) {
	secret := "AKIA1234567890ABCDEF"
	body := "use key " + secret + " now"
	g := newRealSkills(t, func(fx *skillstest.Fixture) {
		fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{
			"SKILL.md": realSkillMD("pdf", "pdf helper with key "+secret, body),
		}})
	}, nil)
	g.approve(t, "pdf")
	if g.p.sanitisationDetector == nil {
		g.p.sanitisationDetector = security.NewDetector(config.DefaultSensitiveDataDetectionConfig())
	}
	srv := g.env.proxyServer

	// No mutating policy: the approved bytes.
	data, _, sk, err := srv.AvailableSkillFile(realSkillsSrv, "pdf", "SKILL.md")
	require.NoError(t, err)
	assert.Contains(t, string(data), secret)
	assert.Contains(t, sk.Description, secret)

	g.commitGlobal(t, func(c *config.Config) {
		c.OutputSanitisation = &config.OutputSanitisationConfig{ResponseAction: "redact", MaxRedactions: 100}
	})
	data, _, sk, err = srv.AvailableSkillFile(realSkillsSrv, "pdf", "SKILL.md")
	require.NoError(t, err)
	assert.NotContains(t, string(data), secret)
	assert.Contains(t, string(data), "use key ")
	assert.NotContains(t, sk.Description, secret)

	list, err := srv.ListAvailableSkills(realSkillsSrv)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.NotContains(t, list[0].Description, secret)
	assert.Contains(t, list[0].Description, "pdf helper")

	// The tool gives the same text.
	out, isErr := g.call(adminCtx(), toolLoadSkill, map[string]any{"id": realSkillsSrv + ":pdf"})
	require.False(t, isErr, out)
	_, part, ok := strings.Cut(out, "\n---\n")
	require.True(t, ok)
	assert.Equal(t, string(data), part)

	g.commitGlobal(t, func(c *config.Config) {
		c.OutputSanitisation = &config.OutputSanitisationConfig{ResponseAction: "block"}
	})
	_, _, _, err = srv.AvailableSkillFile(realSkillsSrv, "pdf", "SKILL.md")
	require.ErrorIs(t, err, runtime.ErrSkillOutputBlocked)
	list, err = srv.ListAvailableSkills(realSkillsSrv)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, skillWordWithheld, list[0].Description)
}
