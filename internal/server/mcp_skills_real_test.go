package server

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/skillstest"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 PR-3 (T036 rest, T038): the three agent tools against the REAL
// runtime catalog. The rig is a real Server (the e2e environment) with a
// temp data dir and the skillstest fixture as a connected streamable-HTTP
// upstream, trust_mode manual and tool-level quarantine on, so a new or
// changed skill waits for a person.

const realSkillsSrv = "skfx"

type realSkills struct {
	env *TestEnvironment
	fx  *skillstest.Fixture
	rt  *runtime.Runtime
	p   *MCPProxyServer
}

func realSkillMD(name, desc, body string) []byte {
	return []byte("---\nname: " + name + "\ndescription: " + desc + "\n---\n" + body + "\n")
}

// newRealSkills starts a real Server whose only skills upstream is the fixture.
// Skills added to the fixture by seed are present at the first refresh.
func newRealSkills(t *testing.T, seed func(fx *skillstest.Fixture), mutate func(cfg *config.Config)) *realSkills {
	t.Helper()
	fx := skillstest.New(t)
	if seed != nil {
		seed(fx)
	}
	env := NewTestEnvironmentWithOptions(t, TestEnvironmentOptions{
		Mutate: func(cfg *config.Config, _ string) {
			quarantine := true
			cfg.QuarantineEnabled = &quarantine
			cfg.EnableSkills = true
			cfg.Servers = []*config.ServerConfig{
				{Name: realSkillsSrv, URL: fx.URL(), Protocol: "streamable-http", Enabled: true, TrustMode: "manual"},
				// A second, never-connected server, so profiles and tokens can name it.
				{Name: "other", URL: "http://127.0.0.1:1", Protocol: "streamable-http", Enabled: false},
			}
			if mutate != nil {
				mutate(cfg)
			}
		},
		Logger: func(*config.Config) (*zap.Logger, error) { return zap.NewNop(), nil },
	})
	t.Cleanup(env.Cleanup)
	rt := env.proxyServer.runtime
	require.NotNil(t, rt)
	require.NotNil(t, env.proxyServer.mcpProxy)
	g := &realSkills{env: env, fx: fx, rt: rt, p: env.proxyServer.mcpProxy}
	// A server added from config with quarantine on starts quarantined, and a
	// quarantined server is not asked for skills; release it the way an
	// operator would.
	_, err := rt.Commit("release", func(tx *runtime.CommitTx) error { return tx.SetQuarantined(realSkillsSrv, false) })
	require.NoError(t, err)
	// A refresh also waits for the scan of what it read, which can take long
	// under -race for a big skill: start it in the background and wait for the
	// records, not for the refresh.
	go func() { _ = g.refresh() }()
	require.Eventually(t, func() bool {
		recs, _ := rt.ListSkillRecords(realSkillsSrv)
		return len(recs) > 0
	}, 150*time.Second, 100*time.Millisecond, "the fixture's skills must be read")
	return g
}

func (g *realSkills) refresh() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return g.rt.RefreshServerSkills(ctx, realSkillsSrv)
}

// refreshUntil refreshes until cond holds (a refresh joined to a flight that
// started before the fixture changed may not see the change).
func (g *realSkills) refreshUntil(t *testing.T, cond func() bool) {
	t.Helper()
	require.Eventually(t, func() bool { _ = g.refresh(); return cond() }, 30*time.Second, 200*time.Millisecond)
}

func (g *realSkills) rec(t *testing.T, path string) *storage.SkillApprovalRecord {
	t.Helper()
	recs, err := g.rt.ListSkillRecords(realSkillsSrv)
	require.NoError(t, err)
	for _, r := range recs {
		if r.SkillPath == path {
			return r
		}
	}
	t.Fatalf("no record for skill %q", path)
	return nil
}

func (g *realSkills) approve(t *testing.T, paths ...string) {
	t.Helper()
	exp := map[string]string{}
	for _, p := range paths {
		exp[p] = g.rec(t, p).CurrentHash
	}
	_, err := g.rt.ApproveSkillsReviewedAt(adminCtx(), realSkillsSrv, paths, exp, "test", 0)
	require.NoError(t, err)
}

func (g *realSkills) commitServer(t *testing.T, mut func(sc *config.ServerConfig)) {
	t.Helper()
	_, err := g.rt.Commit("test_server", func(tx *runtime.CommitTx) error {
		return tx.UpdateServer(realSkillsSrv, func(sc *config.ServerConfig) error { mut(sc); return nil })
	})
	require.NoError(t, err)
}

func (g *realSkills) commitGlobal(t *testing.T, mut func(c *config.Config)) {
	t.Helper()
	_, err := g.rt.Commit("test_global", func(tx *runtime.CommitTx) error {
		return tx.MutateGlobal(func(c *config.Config) error { mut(c); return nil })
	})
	require.NoError(t, err)
}

func (g *realSkills) call(ctx context.Context, tool string, args map[string]any) (string, bool) {
	var (
		res *mcp.CallToolResult
		err error
	)
	switch tool {
	case toolLoadSkill:
		res, err = g.p.handleLoadSkill(ctx, skillReq(args))
	case toolReadSkillFile:
		res, err = g.p.handleReadSkillFile(ctx, skillReq(args))
	default:
		res, err = g.p.handleRetrieveSkills(ctx, skillReq(args))
	}
	if err != nil {
		return "ERR:" + err.Error(), true
	}
	tc, _ := res.Content[0].(mcp.TextContent)
	return tc.Text, res.IsError
}

func (g *realSkills) search(t *testing.T, ctx context.Context, query string) skillSearchResponse {
	t.Helper()
	text, isErr := g.call(ctx, toolRetrieveSkills, map[string]any{"query": query})
	require.False(t, isErr, text)
	var out skillSearchResponse
	require.NoError(t, json.Unmarshal([]byte(text), &out), text)
	return out
}

var (
	versionRe  = regexp.MustCompile(`(?m)^Version: (.+)$`)
	continueRe = regexp.MustCompile(`Continue with offset=(\d+) and version="([^"]+)"`)
)

func versionOf(t *testing.T, text string) string {
	t.Helper()
	m := versionRe.FindStringSubmatch(text)
	require.NotNil(t, m, text)
	return m[1]
}

// readAll follows offset and version from each header until the end and
// returns the joined text and the number of parts.
func (g *realSkills) readAll(t *testing.T, tool string, args map[string]any) (string, int) {
	t.Helper()
	var joined strings.Builder
	offset, parts, version := 0, 0, ""
	for {
		a := map[string]any{"offset": float64(offset)}
		for k, v := range args {
			a[k] = v
		}
		if version != "" {
			a["version"] = version
		}
		text, isErr := g.call(adminCtx(), tool, a)
		require.False(t, isErr, text)
		header, body, ok := strings.Cut(text, "\n---\n")
		require.True(t, ok)
		joined.WriteString(body)
		parts++
		m := continueRe.FindStringSubmatch(header)
		if m == nil {
			return joined.String(), parts
		}
		offset, _ = strconv.Atoi(m[1])
		version = m[2]
		require.Less(t, parts, 50)
	}
}

func (g *realSkills) skillAccessRows(t *testing.T, n int) []*storage.ActivityRecord {
	t.Helper()
	var rows []*storage.ActivityRecord
	require.Eventually(t, func() bool {
		f := storage.DefaultActivityFilter()
		f.Types = []string{string(storage.ActivityTypeSkillAccess)}
		rows, _, _ = g.rt.StorageManager().ListActivities(f)
		return len(rows) >= n
	}, 10*time.Second, 50*time.Millisecond)
	return rows
}

// ---------------------------------------------------------------------------

func TestRealSkills_ChangeIsHeldAndNeverServed_ThenApprovedAndVersioned(t *testing.T) {
	const textA, textB = "TEXT-A first version", "TEXT-B second version"
	g := newRealSkills(t, func(fx *skillstest.Fixture) {
		fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"SKILL.md": realSkillMD("pdf", "reads pdf files", textA)}})
	}, nil)
	id := realSkillsSrv + ":pdf"

	// Pending: nothing is served.
	text, isErr := g.call(adminCtx(), toolLoadSkill, map[string]any{"id": id})
	require.True(t, isErr)
	assert.Equal(t, "skill not found: "+id, text)
	assert.Zero(t, g.search(t, adminCtx(), "pdf").Total)

	g.approve(t, "pdf")
	loadA, isErr := g.call(adminCtx(), toolLoadSkill, map[string]any{"id": id})
	require.False(t, isErr, loadA)
	assert.Contains(t, loadA, textA)
	versionA := versionOf(t, loadA)
	assert.Equal(t, 1, g.search(t, adminCtx(), "pdf").Total)

	// One byte changes upstream; after the refresh the skill is held.
	g.fx.Mutate("pdf", "SKILL.md", realSkillMD("pdf", "reads pdf files", textB))
	g.refreshUntil(t, func() bool { return g.rec(t, "pdf").CurrentHash != g.rec(t, "pdf").ApprovedHash })
	assert.Equal(t, storage.SkillStatusChanged, g.rec(t, "pdf").Status)

	var everything []string
	for _, tool := range []struct {
		name string
		args map[string]any
	}{
		{toolLoadSkill, map[string]any{"id": id}},
		{toolLoadSkill, map[string]any{"id": id, "version": versionA}},
		{toolReadSkillFile, map[string]any{"id": id, "path": "SKILL.md"}},
		{toolRetrieveSkills, map[string]any{"query": "pdf"}},
		{toolRetrieveSkills, map[string]any{"query": "TEXT-B"}},
	} {
		out, _ := g.call(adminCtx(), tool.name, tool.args)
		everything = append(everything, out)
		if tool.name != toolRetrieveSkills {
			assert.Equal(t, "skill not found: "+id, out, tool.name)
		}
	}
	assert.Zero(t, g.search(t, adminCtx(), "pdf").Total, "a changed skill is no longer listed")
	for _, out := range everything {
		assert.NotContains(t, out, textB, "nothing of the held text reaches an agent")
		assert.NotContains(t, out, textA, "and the old text is not served as the current one either")
	}

	// Approved again: text B, a different version, and the old version is refused.
	g.approve(t, "pdf")
	loadB, isErr := g.call(adminCtx(), toolLoadSkill, map[string]any{"id": id})
	require.False(t, isErr, loadB)
	assert.Contains(t, loadB, textB)
	assert.NotContains(t, loadB, textA)
	versionB := versionOf(t, loadB)
	assert.NotEqual(t, versionA, versionB)

	stale, isErr := g.call(adminCtx(), toolLoadSkill, map[string]any{"id": id, "offset": float64(10), "version": versionA})
	assert.True(t, isErr)
	assert.Equal(t, "skill changed since you started reading it: load it again from offset 0", stale)
	ok, isErr := g.call(adminCtx(), toolLoadSkill, map[string]any{"id": id, "offset": float64(0), "version": versionB})
	assert.False(t, isErr, ok)
}

func TestRealSkills_EveryUnavailabilityGivesTheSameRefusal(t *testing.T) {
	g := newRealSkills(t, func(fx *skillstest.Fixture) {
		for _, n := range []string{"pending", "changed", "blocked", "unusable", "unlisted", "ok"} {
			fx.AddSkill(skillstest.Skill{Path: n, Files: map[string][]byte{
				"SKILL.md": realSkillMD(n, "skill "+n, "body of "+n), "ref/a.md": []byte("alpha")}})
		}
	}, nil)
	g.approve(t, "changed", "blocked", "unusable", "unlisted", "ok")

	// Reach one state per skill.
	g.fx.Mutate("changed", "SKILL.md", realSkillMD("changed", "skill changed", "different body"))
	g.refreshUntil(t, func() bool { return g.rec(t, "changed").Status == storage.SkillStatusChanged })
	_, err := g.rt.SetSkillsBlockedAt(adminCtx(), realSkillsSrv, []string{"blocked"}, true, "test", 0)
	require.NoError(t, err)
	r := g.rec(t, "unusable")
	r.Status = storage.SkillStatusUnusable
	require.NoError(t, g.rt.StorageManager().SaveSkillApproval(r))
	r = g.rec(t, "unlisted")
	r.Listed = false
	require.NoError(t, g.rt.StorageManager().SaveSkillApproval(r))

	refused := func(ctx context.Context, id string) {
		t.Helper()
		want := "skill not found: " + id
		out, isErr := g.call(ctx, toolLoadSkill, map[string]any{"id": id})
		assert.True(t, isErr, id)
		assert.Equal(t, want, out, "load_skill %s", id)
		out, isErr = g.call(ctx, toolReadSkillFile, map[string]any{"id": id, "path": "SKILL.md"})
		assert.True(t, isErr, id)
		assert.Equal(t, want, out, "read_skill_file %s", id)
	}
	for _, n := range []string{"pending", "changed", "blocked", "unusable", "unlisted"} {
		refused(adminCtx(), realSkillsSrv+":"+n)
	}
	refused(adminCtx(), realSkillsSrv+":nothere")
	refused(adminCtx(), "nosuch:ok")
	assert.Equal(t, 1, g.search(t, adminCtx(), "skill").Total, "only the one available skill is listed")

	// The one that works, then the same id refused for each server-wide reason.
	okID := realSkillsSrv + ":ok"
	loads := func() bool {
		_, isErr := g.call(adminCtx(), toolLoadSkill, map[string]any{"id": okID})
		return !isErr
	}
	require.True(t, loads(), "control: the approved skill loads")

	refused(skillAgentCtx("other"), okID) // out of reach for the token
	for _, c := range []struct {
		name         string
		set, restore func(t *testing.T)
	}{
		{"server disabled",
			func(t *testing.T) { g.commitServer(t, func(sc *config.ServerConfig) { sc.Enabled = false }) },
			func(t *testing.T) { g.commitServer(t, func(sc *config.ServerConfig) { sc.Enabled = true }) }},
		{"server quarantined",
			func(t *testing.T) { g.commitServer(t, func(sc *config.ServerConfig) { sc.Quarantined = true }) },
			func(t *testing.T) { g.commitServer(t, func(sc *config.ServerConfig) { sc.Quarantined = false }) }},
		{"expose_skills false",
			func(t *testing.T) {
				f := false
				g.commitServer(t, func(sc *config.ServerConfig) { sc.ExposeSkills = &f })
			},
			func(t *testing.T) { g.commitServer(t, func(sc *config.ServerConfig) { sc.ExposeSkills = nil }) }},
		{"enable_skills off",
			func(t *testing.T) { g.commitGlobal(t, func(c *config.Config) { c.EnableSkills = false }) },
			func(t *testing.T) { g.commitGlobal(t, func(c *config.Config) { c.EnableSkills = true }) }},
	} {
		c.set(t)
		out, isErr := g.call(adminCtx(), toolLoadSkill, map[string]any{"id": okID})
		assert.True(t, isErr, c.name)
		assert.Equal(t, "skill not found: "+okID, out, c.name)
		assert.Zero(t, g.search(t, adminCtx(), "skill").Total, c.name)
		c.restore(t)
		require.Eventually(t, loads, 20*time.Second, 200*time.Millisecond, "restored after "+c.name+": the stored approval is kept")
	}
}

func TestRealSkills_DamagedStoredCopyIsReportedThenTheSkillIsUnusable(t *testing.T) {
	g := newRealSkills(t, func(fx *skillstest.Fixture) {
		fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"SKILL.md": realSkillMD("pdf", "reads pdf", "good body")}})
	}, nil)
	g.approve(t, "pdf")
	id := realSkillsSrv + ":pdf"
	var f storage.SkillFile
	for _, x := range g.rec(t, "pdf").Approved.Files {
		if x.RelPath == "SKILL.md" {
			f = x
		}
	}
	require.NoError(t, g.rt.StorageManager().PutSkillFile(realSkillsSrv, f.Digest, []byte("evil!")))

	out, isErr := g.call(adminCtx(), toolLoadSkill, map[string]any{"id": id})
	assert.True(t, isErr)
	assert.Equal(t, "skill unavailable: its stored copy failed verification", out)
	assert.NotContains(t, out, "evil")

	out, isErr = g.call(adminCtx(), toolLoadSkill, map[string]any{"id": id})
	assert.True(t, isErr)
	assert.Equal(t, "skill not found: "+id, out, "the skill is unusable now")
	assert.Equal(t, storage.SkillStatusUnusable, g.rec(t, "pdf").Status)
	assert.Zero(t, g.search(t, adminCtx(), "pdf").Total)
}

func TestRealSkills_LargeMultiByteFileIsReadInPartsExactly(t *testing.T) {
	var big strings.Builder
	for i := 0; big.Len() < 62000; i++ {
		fmt.Fprintf(&big, "é€😀 line %d of the reference\n", i)
	}
	bigText := big.String()
	g := newRealSkills(t, func(fx *skillstest.Fixture) {
		fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{
			"SKILL.md":   realSkillMD("pdf", "reads pdf", "short main text"),
			"ref/big.md": []byte(bigText)}})
	}, func(cfg *config.Config) {
		// auto trust approves without the offline scan, which takes about a
		// minute for this much text under -race and is not what this test is about.
		cfg.Servers[0].TrustMode = "auto"
	})
	g.approve(t, "pdf")
	id := realSkillsSrv + ":pdf"

	joined, parts := g.readAll(t, toolReadSkillFile, map[string]any{"id": id, "path": "ref/big.md"})
	assert.Equal(t, bigText, joined, "the parts join to the approved bytes exactly")
	assert.Equal(t, 2, parts, "a file just over 60000 bytes comes in two parts")

	// Small parts: still exact, still on character boundaries.
	small, smallParts := g.readAll(t, toolReadSkillFile, map[string]any{"id": id, "path": "ref/big.md", "limit": float64(25000)})
	assert.Equal(t, bigText, small)
	assert.Greater(t, smallParts, parts)
}

func TestRealSkills_ReadSkillFile(t *testing.T) {
	g := newRealSkills(t, func(fx *skillstest.Fixture) {
		fx.AddSkill(skillstest.Skill{Path: "pdf", Binary: []string{"img.bin"}, Files: map[string][]byte{
			"SKILL.md": realSkillMD("pdf", "reads pdf", "main"), "scripts/x.py": []byte("print('hello')\n"),
			"img.bin": {0x00, 0x01, 0xff, 0xfe}}})
		fx.AddSkill(skillstest.Skill{Path: "other", Files: map[string][]byte{
			"SKILL.md": realSkillMD("other", "another skill", "OTHER-BODY"), "secret.txt": []byte("OTHER-SECRET")}})
	}, nil)
	g.approve(t, "pdf", "other")
	id := realSkillsSrv + ":pdf"
	read := func(path string) (string, bool) {
		return g.call(adminCtx(), toolReadSkillFile, map[string]any{"id": id, "path": path})
	}

	text, isErr := read("scripts/x.py")
	require.False(t, isErr, text)
	assert.True(t, strings.HasPrefix(text, `[mcpproxy] File "scripts/x.py" of skill "pdf" from upstream server "skfx".`), text)
	assert.True(t, strings.HasSuffix(text, "---\nprint('hello')\n"), text)
	assert.Contains(t, text, "img.bin", "the manifest lists the binary file")

	text, isErr = read("SKILL.md")
	assert.False(t, isErr)
	assert.Contains(t, text, "main")

	text, isErr = read("img.bin")
	assert.True(t, isErr)
	assert.Equal(t, "binary files are not served: img.bin", text)

	for _, p := range []string{"../other/SKILL.md", "other/SKILL.md", "secret.txt", "../secret.txt", "/etc/passwd"} {
		text, isErr = read(p)
		assert.True(t, isErr, p)
		assert.Equal(t, "skill not found: "+id, text, p)
		assert.NotContains(t, text, "OTHER")
	}
}

func TestRealSkills_ScopeFollowsReachNotToolPolicy(t *testing.T) {
	g := newRealSkills(t, func(fx *skillstest.Fixture) {
		fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"SKILL.md": realSkillMD("pdf", "reads pdf files", "body")}})
	}, func(cfg *config.Config) {
		cfg.Profiles = []config.ProfileConfig{
			{Name: "elsewhere", Servers: []string{"other"}},
			{Name: "ro-denied", Servers: []string{realSkillsSrv}, MaxTier: "read",
				Tools: &config.ProfileToolRules{Deny: []string{realSkillsSrv + ":*"}}},
		}
	})
	g.approve(t, "pdf")
	id := realSkillsSrv + ":pdf"

	mkAgent := func(pin string, servers ...string) context.Context {
		return auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type: auth.AuthTypeAgent, AgentName: "bot", ProfilePin: pin,
			AllowedServers: servers, Permissions: []string{auth.PermRead},
		})
	}
	reaches := func(ctx context.Context) bool {
		out, isErr := g.call(ctx, toolLoadSkill, map[string]any{"id": id})
		if isErr {
			assert.Equal(t, "skill not found: "+id, out)
		}
		return !isErr
	}
	total := func(ctx context.Context) int { return g.search(t, ctx, "pdf").Total }

	assert.True(t, reaches(adminCtx()))
	assert.Equal(t, 1, total(adminCtx()))

	for name, ctx := range map[string]context.Context{
		"token limited to another server":  mkAgent("", "other"),
		"URL profile excluding the server": urlProfileCtx(g.p, "elsewhere"),
		"token pinned to that profile":     mkAgent("elsewhere", "*"),
		"pin intersected with the grant":   mkAgent("ro-denied", "other"),
	} {
		assert.False(t, reaches(ctx), name)
		assert.Zero(t, total(ctx), "%s: total never counts skills outside reach", name)
	}

	for name, ctx := range map[string]context.Context{
		"token limited to the server":                     mkAgent("", realSkillsSrv),
		"URL profile: read-only cap and a deny rule":      urlProfileCtx(g.p, "ro-denied"),
		"token pinned to the read-only denying profile":   mkAgent("ro-denied", "*"),
		"token pinned, grant names the server explicitly": mkAgent("ro-denied", realSkillsSrv),
	} {
		assert.True(t, reaches(ctx), "%s: skills are not tool calls (FR-023a)", name)
		assert.Equal(t, 1, total(ctx), name)
	}
}

func TestRealSkills_EveryAccessWritesOneActivityRowWithoutText(t *testing.T) {
	const marker = "ZQXMARKER-NEVER-STORED"
	g := newRealSkills(t, func(fx *skillstest.Fixture) {
		fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{
			"SKILL.md": realSkillMD("pdf", "reads pdf files", "body "+marker), "ref/a.md": []byte("alpha " + marker)}})
	}, nil)
	g.approve(t, "pdf")
	id := realSkillsSrv + ":pdf"

	_ = g.search(t, adminCtx(), "pdf")
	text, isErr := g.call(adminCtx(), toolLoadSkill, map[string]any{"id": id})
	require.False(t, isErr)
	require.Contains(t, text, marker, "control: the marker is in what the agent gets")
	text, isErr = g.call(adminCtx(), toolReadSkillFile, map[string]any{"id": id, "path": "ref/a.md"})
	require.False(t, isErr)
	require.Contains(t, text, marker)

	rows := g.skillAccessRows(t, 3)
	require.Len(t, rows, 3)
	byAction := map[string]*storage.ActivityRecord{}
	for _, r := range rows {
		byAction[fmt.Sprint(r.Metadata["action"])] = r
		raw, err := json.Marshal(r)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), marker, "the stored row holds no skill text")
	}
	require.Len(t, byAction, 3, "one row each for search, load and read")
	assert.Equal(t, "pdf", byAction["search"].Arguments["query"])
	assert.Equal(t, "", byAction["search"].ServerName)
	assert.Equal(t, realSkillsSrv, byAction["load"].ServerName)
	assert.Equal(t, realSkillsSrv, byAction["read"].ServerName)
	assert.Equal(t, "ref/a.md", byAction["read"].Arguments["path"])
	assert.Equal(t, g.rec(t, "pdf").ApprovedHash, byAction["load"].Metadata["hash"])
	for _, r := range rows {
		assert.Equal(t, "success", r.Status)
	}
}
