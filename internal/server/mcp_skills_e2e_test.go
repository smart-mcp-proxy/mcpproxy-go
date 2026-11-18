package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/pkoukk/tiktoken-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/skillstest"
)

// T041, Spec 125 PR-3, end to end: a real MCP client session against a real
// proxy with enable_skills on and the skillstest fixture as the upstream.
//
// The name starts with TestE2E_ on purpose. Its neighbours that start a real
// proxy and talk MCP to it over HTTP carry that prefix, which the unit-test
// workflows skip (-skip "E2E|...") and the e2e workflow runs
// (-run TestE2E ./internal/server). This test does the same, so CI decides
// the same way for it as for them.

func e2eSkillDescription(i int) string {
	return fmt.Sprintf("Guides an agent through workflow number %d for the Hugging Face Hub - find a model or dataset, check its license and size, "+
		"download the files, run a short evaluation job and report the result in a table with the cost and the time it took.", i)
}

func callSkillTool(t *testing.T, c interface {
	CallTool(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
}, name string, args map[string]any) (string, bool, error) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := c.CallTool(ctx, req)
	if err != nil {
		return "", true, err
	}
	var sb strings.Builder
	for _, content := range res.Content {
		if tc, ok := content.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), res.IsError, nil
}

// reloadEnableSkills flips enable_skills through the config apply funnel, which
// writes the file and reloads it, so the config.reloaded listener runs as it
// does for a hand edit.
func reloadEnableSkills(t *testing.T, g *realSkills, on bool) {
	t.Helper()
	cfg := cloneConfig(t, g.rt.Config())
	cfg.EnableSkills = on
	// The e2e environment builds a minimal config; a reload validates it.
	d := config.DefaultConfig()
	if cfg.ToolsLimit == 0 {
		cfg.ToolsLimit = d.ToolsLimit
	}
	if cfg.CallToolTimeout == 0 {
		cfg.CallToolTimeout = d.CallToolTimeout
	}
	_, err := g.env.proxyServer.ApplyConfig(cfg, filepath.Join(t.TempDir(), "mcp_config.json"))
	require.NoError(t, err)
}

func TestE2E_SkillsOverMCP(t *testing.T) {
	const marker = "E2E-APPROVED-TEXT"
	g := newRealSkills(t, func(fx *skillstest.Fixture) {
		fx.AddSkill(skillstest.Skill{Path: "pdf-guide", Files: map[string][]byte{
			"SKILL.md":     realSkillMD("pdf-guide", "Reads and fills pdf forms", "# PDF guide\n"+marker+"\n"),
			"scripts/a.py": []byte("print('" + marker + "-script')\n"),
		}})
		for i := 0; i < 24; i++ {
			name := fmt.Sprintf("hub-workflow-%02d", i)
			fx.AddSkill(skillstest.Skill{Path: name, Files: map[string][]byte{
				"SKILL.md": realSkillMD(name, e2eSkillDescription(i), "body of "+name)}})
		}
	}, nil)
	paths := []string{"pdf-guide"}
	for i := 0; i < 24; i++ {
		paths = append(paths, fmt.Sprintf("hub-workflow-%02d", i))
	}
	require.Eventually(t, func() bool {
		_ = g.refresh()
		recs, _ := g.rt.ListSkillRecords(realSkillsSrv)
		return len(recs) == 25
	}, 60*time.Second, 200*time.Millisecond)
	g.approve(t, paths...)

	c := g.env.CreateProxyClient()
	defer c.Close()
	g.env.ConnectClient(c)

	toolNames := func() map[string]bool {
		res, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
		require.NoError(t, err)
		out := map[string]bool{}
		for _, tool := range res.Tools {
			out[tool.Name] = true
		}
		return out
	}

	names := toolNames()
	for _, n := range skillTools {
		assert.True(t, names[n], "tools/list carries %s", n)
	}
	assert.True(t, names["retrieve_tools"], "control")

	// retrieve_skills -> load_skill -> read_skill_file, all from the approved copy.
	out, isErr, err := callSkillTool(t, c, toolRetrieveSkills, map[string]any{"query": "pdf forms"})
	require.NoError(t, err)
	require.False(t, isErr, out)
	var found skillSearchResponse
	require.NoError(t, json.Unmarshal([]byte(out), &found), out)
	require.NotEmpty(t, found.Skills)
	assert.Equal(t, realSkillsSrv+":pdf-guide", found.Skills[0].ID)
	assert.NotContains(t, out, marker, "a search never returns a body")

	out, isErr, err = callSkillTool(t, c, toolLoadSkill, map[string]any{"id": found.Skills[0].ID})
	require.NoError(t, err)
	require.False(t, isErr, out)
	assert.Contains(t, out, marker)
	assert.Contains(t, out, "scripts/a.py", "the manifest names the supporting file")
	assert.True(t, strings.HasPrefix(out, `[mcpproxy] Skill "pdf-guide" from upstream server "skfx".`))

	out, isErr, err = callSkillTool(t, c, toolReadSkillFile, map[string]any{"id": found.Skills[0].ID, "path": "scripts/a.py"})
	require.NoError(t, err)
	require.False(t, isErr, out)
	assert.Contains(t, out, marker+"-script")

	// SC-003 on the real path: 25 skills match; the largest answer a caller can
	// ask for (20) stays under 2,000 tokens.
	out, isErr, err = callSkillTool(t, c, toolRetrieveSkills, map[string]any{"query": "workflow Hugging Face Hub", "limit": float64(20)})
	require.NoError(t, err)
	require.False(t, isErr, out)
	var many skillSearchResponse
	require.NoError(t, json.Unmarshal([]byte(out), &many), out)
	assert.Equal(t, 24, many.Total)
	assert.Len(t, many.Skills, 20)
	enc, encErr := tiktoken.GetEncoding("cl100k_base")
	require.NoError(t, encErr)
	tokens := len(enc.Encode(out, nil, nil))
	t.Logf("a 20-skill search answer is %d bytes, %d tokens (cl100k_base)", len(out), tokens)
	assert.Less(t, tokens, 2000, "SC-003")

	// Turning enable_skills off removes the tools and refuses a stale call.
	reloadEnableSkills(t, g, false)
	require.Eventually(t, func() bool {
		n := toolNames()
		return !n[toolRetrieveSkills] && !n[toolLoadSkill] && !n[toolReadSkillFile]
	}, 15*time.Second, 100*time.Millisecond, "the three tools leave tools/list after the switch is turned off")
	assert.True(t, toolNames()["retrieve_tools"], "the rest of the surface is untouched")

	out, isErr, err = callSkillTool(t, c, toolLoadSkill, map[string]any{"id": found.Skills[0].ID})
	assert.True(t, err != nil || isErr, "a stale call is refused, got %q", out)
	assert.NotContains(t, out, marker)

	// And back on: the stored approval is kept and the tools return.
	reloadEnableSkills(t, g, true)
	require.Eventually(t, func() bool { return toolNames()[toolLoadSkill] }, 15*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool {
		o, e, err := callSkillTool(t, c, toolLoadSkill, map[string]any{"id": found.Skills[0].ID})
		return err == nil && !e && strings.Contains(o, marker)
	}, 20*time.Second, 200*time.Millisecond)
}
