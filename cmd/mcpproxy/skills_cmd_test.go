package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
)

// Spec 125 PR-1 (T021/T022): `mcpproxy skills list|show|refresh`.

const skillsListBody = `{
  "enabled": true, "server": "alpha", "supported": true, "expose_skills": null,
  "last_refresh": "2026-11-16T19:20:00Z", "error": "", "truncated": false,
  "skills": [
    {"id": "alpha:hf-cli", "uri": "skill://hf-cli/SKILL.md", "path": "hf-cli", "name": "hf-cli",
     "description": "Hugging Face CLI", "status": "pending", "blocked": false, "listed": true,
     "available": false, "unusable_reason": "", "current_hash": "sk1-abc", "approved_hash": "",
     "files": [{"path": "SKILL.md", "size": 2048, "digest": "sha256:aa", "text": true},
               {"path": "logo.png", "size": 1024, "digest": "sha256:bb", "text": false}],
     "total_size": 3072, "scan": {"verdict": "", "complete": false, "findings": []}},
    {"id": "alpha:bad", "uri": "skill://bad/SKILL.md", "path": "bad", "name": "bad", "description": "",
     "status": "unusable", "blocked": false, "listed": true, "available": false,
     "unusable_reason": "digest_mismatch", "current_hash": "", "approved_hash": "",
     "files": [], "total_size": 0, "scan": {"verdict": "", "complete": false, "findings": []}}
  ]}`

const skillsEmptyBeta = `{"enabled": true, "server": "beta", "supported": true, "last_refresh": "2026-11-16T19:21:00Z",
  "error": "list failed: timeout", "truncated": true, "skills": []}`

func serversBody(names ...string) string {
	rows := make([]string, len(names))
	for i, n := range names {
		rows[i] = `{"name":"` + n + `","enabled":true}`
	}
	return `{"servers":[` + strings.Join(rows, ",") + `]}`
}

func TestSkillsList_AllServersTable(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers":              okResp(serversBody("alpha", "beta")),
		"GET /api/v1/servers/alpha/skills": okResp(skillsListBody),
		"GET /api/v1/servers/beta/skills":  okResp(skillsEmptyBeta),
	})
	out, _, err := runCLI(t, GetSkillsCommand, "table", "list")
	require.NoError(t, err)
	for _, want := range []string{"SERVER", "SKILL", "STATUS", "FILES", "SIZE", "alpha", "hf-cli", "pending", "3.0 KB", "unusable (digest_mismatch)"} {
		require.Contains(t, out, want)
	}
	require.Contains(t, out, "beta: list failed: timeout")
	require.Contains(t, out, "beta: listing truncated")
	require.Len(t, rec.all(), 3)
}

func TestSkillsList_OneServer(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills": okResp(skillsListBody),
	})
	out, _, err := runCLI(t, GetSkillsCommand, "table", "list", "--server", "alpha")
	require.NoError(t, err)
	require.Contains(t, out, "hf-cli")
	require.Equal(t, "/api/v1/servers/alpha/skills", rec.only(t).Path)
}

func TestSkillsList_FeatureOffPrintsOneLine(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers":              okResp(serversBody("alpha", "beta")),
		"GET /api/v1/servers/alpha/skills": okResp(`{"enabled": false, "skills": []}`),
	})
	out, _, err := runCLI(t, GetSkillsCommand, "table", "list")
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(strings.TrimSpace(out), "\n")+1, out)
	require.Contains(t, out, "enable_skills: true")
}

func TestSkillsList_JSONAndYAML(t *testing.T) {
	routes := map[string]cannedResponse{
		"GET /api/v1/servers":              okResp(serversBody("alpha")),
		"GET /api/v1/servers/alpha/skills": okResp(skillsListBody),
	}
	newRESTRecorder(t, routes)
	out, _, err := runCLI(t, GetSkillsCommand, "json", "list")
	require.NoError(t, err)
	var list []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &list), out)
	require.Len(t, list, 1)
	require.Equal(t, "alpha", list[0]["server"])
	require.Len(t, list[0]["skills"], 2)

	newRESTRecorder(t, routes)
	out, _, err = runCLI(t, GetSkillsCommand, "yaml", "list")
	require.NoError(t, err)
	var y []map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &y), out)
	require.Len(t, y, 1)
}

func TestSkillsShow_FollowsNextOffset(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills": okResp(skillsListBody),
	})
	rec.mu.Lock()
	rec.sequences = map[string][]cannedResponse{
		"GET /api/v1/servers/alpha/skills/content": {
			okResp(`{"text":"# Hello\n","offset":0,"next_offset":8,"size":14,"digest":"sha256:aa"}`),
			okResp(`{"text":"second\n","offset":8,"size":14,"digest":"sha256:aa"}`),
		},
	}
	rec.mu.Unlock()
	out, _, err := runCLI(t, GetSkillsCommand, "table", "show", "alpha:hf-cli")
	require.NoError(t, err)
	for _, want := range []string{"hf-cli", "Hugging Face CLI", "pending", "sk1-abc", "SKILL.md", "logo.png", "# Hello\nsecond\n"} {
		require.Contains(t, out, want)
	}
	var contentReqs []recordedRequest
	for _, r := range rec.all() {
		if strings.HasSuffix(r.Path, "/content") {
			contentReqs = append(contentReqs, r)
		}
	}
	require.Len(t, contentReqs, 2)
	require.Contains(t, contentReqs[0].RawQuery, "skill=hf-cli")
	require.NotContains(t, contentReqs[0].RawQuery, "offset")
	require.Contains(t, contentReqs[1].RawQuery, "offset=8")
}

func TestSkillsShow_FileAndApprovedFlags(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills":         okResp(skillsListBody),
		"GET /api/v1/servers/alpha/skills/content": okResp(`{"text":"x","offset":0,"size":1,"digest":"sha256:aa"}`),
	})
	_, _, err := runCLI(t, GetSkillsCommand, "table", "show", "alpha:hf-cli", "--file", "refs/a.md", "--approved")
	require.NoError(t, err)
	var q string
	for _, r := range rec.all() {
		if strings.HasSuffix(r.Path, "/content") {
			q = r.RawQuery
		}
	}
	require.Contains(t, q, "file=refs%2Fa.md")
	require.Contains(t, q, "copy=approved")
}

func TestSkillsShow_UnknownSkillAndBadTarget(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills": okResp(skillsListBody),
	})
	_, _, err := runCLI(t, GetSkillsCommand, "table", "show", "alpha:nope")
	require.Error(t, err)
	require.Contains(t, err.Error(), "skill not found")

	_, _, err = runCLI(t, GetSkillsCommand, "table", "show", "no-colon")
	require.Error(t, err)
	require.Contains(t, err.Error(), "<server>:<skill>")
}

func TestSkillsShow_JSON(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"GET /api/v1/servers/alpha/skills":         okResp(skillsListBody),
		"GET /api/v1/servers/alpha/skills/content": okResp(`{"text":"abc","offset":0,"size":3,"digest":"sha256:aa"}`),
	})
	out, _, err := runCLI(t, GetSkillsCommand, "json", "show", "alpha:hf-cli")
	require.NoError(t, err)
	var v map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &v), out)
	require.Equal(t, "alpha:hf-cli", v["id"])
	require.Equal(t, "abc", v["text"])
	require.Equal(t, "SKILL.md", v["file"])
}

func TestSkillsRefresh(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"POST /api/v1/servers/alpha/skills/refresh": okResp(skillsListBody),
	})
	out, _, err := runCLI(t, GetSkillsCommand, "table", "refresh", "alpha")
	require.NoError(t, err)
	require.Contains(t, out, "alpha")
	require.Contains(t, out, "2 skills")
	require.Equal(t, http.MethodPost, rec.only(t).Method)
}

func TestSkillsRefresh_RefusalKeepsCode(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{
		"POST /api/v1/servers/alpha/skills/refresh": refuse(http.StatusConflict,
			`{"success":false,"error":"skills_not_supported: server alpha does not announce the skills extension","error_code":"skills_not_supported"}`),
	})
	_, _, err := runCLI(t, GetSkillsCommand, "table", "refresh", "alpha")
	require.Error(t, err)
	require.Contains(t, err.Error(), "skills_not_supported")
}

func TestSkillsCommands_NoDaemonExit1(t *testing.T) {
	withNoDaemon(t)
	for _, args := range [][]string{{"list"}, {"show", "a:b"}, {"refresh", "a"}} {
		_, _, err := runCLI(t, GetSkillsCommand, "table", args...)
		require.Error(t, err, args)
		require.Equal(t, ExitCodeGeneralError, classifyError(err))
		require.Contains(t, err.Error(), "skills requires running daemon. Start with: mcpproxy serve")
	}
}

func TestSkillsCommand_RegisteredInRoot(t *testing.T) {
	root := docsSmokeRoot(t)
	found, _, err := root.Find([]string{"skills", "show"})
	require.NoError(t, err)
	require.Equal(t, "show", found.Name())
}

// TestSkillsHelpJSON runs the real --help-json hook in a helper process (the
// production hook exits after writing JSON): the positional argument must not
// block machine-readable discovery.
func TestSkillsHelpJSON(t *testing.T) {
	if target := os.Getenv("MCPPROXY_SKILLS_HELP_TARGET"); target != "" {
		root := &cobra.Command{Use: "mcpproxy"}
		root.AddCommand(GetSkillsCommand())
		clioutput.SetupHelpJSON(root)
		root.SetArgs(append(strings.Fields(target), "--help-json"))
		require.NoError(t, root.Execute())
		return
	}
	for _, target := range []string{"skills", "skills list", "skills show", "skills refresh"} {
		t.Run(target, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestSkillsHelpJSON$")
			cmd.Env = append(os.Environ(), "MCPPROXY_SKILLS_HELP_TARGET="+target)
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			var payload map[string]any
			require.NoError(t, json.Unmarshal(out, &payload), string(out))
			require.Equal(t, target[strings.LastIndex(target, " ")+1:], payload["name"])
		})
	}
}
