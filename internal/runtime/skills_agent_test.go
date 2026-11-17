package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/skillstest"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 PR-3 (T034-T036): the accessors agents use. They answer from the
// approved copy only and say "not found" for every reason a skill is withheld.

func skillMD(name, desc, body string) []byte {
	return []byte("---\nname: " + name + "\ndescription: " + desc + "\n---\n" + body + "\n")
}

func (g *skillsRig) approve(t *testing.T, paths ...string) {
	t.Helper()
	_, err := g.rt.ApproveSkillsReviewedAt(adminCtx(), skillsSrv, paths, hashes(t, g, paths...), "test", 0)
	require.NoError(t, err)
}

func (g *skillsRig) setServer(t *testing.T, mut func(*config.ServerConfig)) {
	t.Helper()
	_, err := g.rt.Commit("test_server", func(tx *CommitTx) error {
		return tx.UpdateServer(skillsSrv, func(sc *config.ServerConfig) error { mut(sc); return nil })
	})
	require.NoError(t, err)
	g.rt.skills.wait()
}

func (g *skillsRig) setGlobal(t *testing.T, mut func(*config.Config)) {
	t.Helper()
	_, err := g.rt.Commit("test_global", func(tx *CommitTx) error {
		return tx.MutateGlobal(func(c *config.Config) error { mut(c); return nil })
	})
	require.NoError(t, err)
	g.rt.skills.wait()
}

func TestAvailableSkill_ApprovedCopyOnly(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{
		"SKILL.md": skillMD("pdf", "reads pdf files", "body"), "ref/a.md": []byte("alpha")}})
	require.NoError(t, g.refresh(t))

	_, err := g.rt.AvailableSkill(skillsSrv, "pdf")
	require.ErrorIs(t, err, ErrSkillNotFound, "pending")

	g.approve(t, "pdf")
	got, err := g.rt.AvailableSkill(skillsSrv, "pdf")
	require.NoError(t, err)
	assert.Equal(t, skillsSrv+":pdf", got.ID)
	assert.Equal(t, "pdf", got.Name)
	assert.Equal(t, "reads pdf files", got.Description)
	assert.Equal(t, g.records(t)["pdf"].ApprovedHash, got.ApprovedHash)
	assert.Equal(t, "skill://pdf/SKILL.md", got.URI)
	require.Len(t, got.Files, 2)
	var sum int64
	for _, f := range got.Files {
		sum += f.Size
	}
	assert.Equal(t, sum, got.TotalSize)

	// Upstream changes and is refreshed: the skill is held, and nothing of the
	// new content reaches an agent.
	g.fx.Mutate("pdf", "SKILL.md", skillMD("pdf", "NEW DESCRIPTION", "new body"))
	require.NoError(t, g.refresh(t))
	_, err = g.rt.AvailableSkill(skillsSrv, "pdf")
	require.ErrorIs(t, err, ErrSkillNotFound, "changed skills are held")
}

func TestAvailableSkill_NameDescriptionAndFilesComeFromTheApprovedCopy(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{
		"SKILL.md": skillMD("pdf", "approved words", "body"), "a.md": []byte("alpha")}})
	require.NoError(t, g.refresh(t))
	g.approve(t, "pdf")

	// Make the current copy differ from the approved one while the record
	// still reads approved: the answer must follow the approved copy.
	rec := g.records(t)["pdf"]
	rec.Name = "renamed"
	rec.Description = "current words"
	rec.Current.Files = rec.Current.Files[:1]
	require.NoError(t, g.rt.storageManager.SaveSkillApproval(rec))

	got, err := g.rt.AvailableSkill(skillsSrv, "pdf")
	require.NoError(t, err)
	assert.Equal(t, "pdf", got.Name)
	assert.Equal(t, "approved words", got.Description)
	assert.Len(t, got.Files, 2)
}

func TestAvailableSkill_EveryUnavailabilityIsNotFound(t *testing.T) {
	no := false
	cases := []struct {
		name string
		do   func(t *testing.T, g *skillsRig)
	}{
		{"unknown server", func(t *testing.T, g *skillsRig) {}},
		{"unknown path", func(t *testing.T, g *skillsRig) {}},
		{"blocked", func(t *testing.T, g *skillsRig) {
			_, err := g.rt.SetSkillsBlockedAt(adminCtx(), skillsSrv, []string{"pdf"}, true, "test", 0)
			require.NoError(t, err)
		}},
		{"unusable", func(t *testing.T, g *skillsRig) {
			rec := g.records(t)["pdf"]
			rec.Status = storage.SkillStatusUnusable
			require.NoError(t, g.rt.storageManager.SaveSkillApproval(rec))
		}},
		{"not listed", func(t *testing.T, g *skillsRig) {
			rec := g.records(t)["pdf"]
			rec.Listed = false
			require.NoError(t, g.rt.storageManager.SaveSkillApproval(rec))
		}},
		{"no approved copy", func(t *testing.T, g *skillsRig) {
			rec := g.records(t)["pdf"]
			rec.Approved = nil
			require.NoError(t, g.rt.storageManager.SaveSkillApproval(rec))
		}},
		{"server disabled", func(t *testing.T, g *skillsRig) { g.setServer(t, func(sc *config.ServerConfig) { sc.Enabled = false }) }},
		{"server quarantined", func(t *testing.T, g *skillsRig) {
			g.setServer(t, func(sc *config.ServerConfig) { sc.Quarantined = true })
		}},
		{"server excluded", func(t *testing.T, g *skillsRig) {
			g.setServer(t, func(sc *config.ServerConfig) { sc.ExposeSkills = &no })
		}},
		{"feature off", func(t *testing.T, g *skillsRig) { g.setGlobal(t, func(c *config.Config) { c.EnableSkills = false }) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newSkillsRig(t, skillsRigOpts{enable: true})
			g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"a.md": []byte("alpha")}})
			require.NoError(t, g.refresh(t))
			g.approve(t, "pdf")
			_, err := g.rt.AvailableSkill(skillsSrv, "pdf")
			require.NoError(t, err, "available before")

			tc.do(t, g)
			server, path := skillsSrv, "pdf"
			switch tc.name {
			case "unknown server":
				server = "nope"
			case "unknown path":
				path = "nope"
			}
			sk, err := g.rt.AvailableSkill(server, path)
			require.ErrorIs(t, err, ErrSkillNotFound)
			assert.Nil(t, sk)
			_, _, sk2, err := g.rt.AvailableSkillFile(server, path, "a.md")
			require.ErrorIs(t, err, ErrSkillNotFound)
			assert.Nil(t, sk2)
		})
	}
}

func TestAvailableSkillFile(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{
		"ref/a.md": []byte("alpha"), "img.bin": {1, 2, 3}}, Binary: []string{"img.bin"}})
	g.fx.AddSkill(skillstest.Skill{Path: "other", Files: map[string][]byte{"x.md": []byte("secret")}})
	require.NoError(t, g.refresh(t))
	g.approve(t, "pdf", "other")

	data, file, sk, err := g.rt.AvailableSkillFile(skillsSrv, "pdf", "ref/a.md")
	require.NoError(t, err)
	assert.Equal(t, "alpha", string(data))
	assert.Equal(t, "ref/a.md", file.RelPath)
	assert.Equal(t, "pdf", sk.Path)

	_, _, _, err = g.rt.AvailableSkillFile(skillsSrv, "pdf", "img.bin")
	assert.ErrorIs(t, err, ErrSkillFileBinary)
	for _, p := range []string{"nope.md", "../other/x.md", "/etc/passwd", "ref/../ref/a.md", "./ref/a.md", "x.md", ""} {
		_, _, _, err = g.rt.AvailableSkillFile(skillsSrv, "pdf", p)
		assert.ErrorIs(t, err, ErrSkillNotFound, p)
	}

	// The approved file is served even after upstream changed it and a refresh
	// stored the new bytes as the current copy.
	g.fx.Mutate("pdf", "ref/a.md", []byte("rewritten"))
	require.NoError(t, g.refresh(t))
	_, _, _, err = g.rt.AvailableSkillFile(skillsSrv, "pdf", "ref/a.md")
	require.ErrorIs(t, err, ErrSkillNotFound, "changed skill is held, nothing served")
	rec := g.records(t)["pdf"]
	require.NotNil(t, rec.Approved)
	old, _, err := g.rt.SkillFileText(skillsSrv, "pdf", "ref/a.md", true)
	require.NoError(t, err)
	assert.Equal(t, "alpha", string(old))
}

func TestAvailableSkillFile_DamagedStoredCopyRefusesAndMarksUnusable(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"ref/a.md": []byte("alpha")}})
	require.NoError(t, g.refresh(t))
	g.approve(t, "pdf")
	var f storage.SkillFile
	for _, x := range g.records(t)["pdf"].Approved.Files {
		if x.RelPath == "ref/a.md" {
			f = x
		}
	}
	require.NoError(t, g.rt.storageManager.PutSkillFile(skillsSrv, f.Digest, []byte("evil!")))

	_, _, _, err := g.rt.AvailableSkillFile(skillsSrv, "pdf", "ref/a.md")
	require.ErrorIs(t, err, ErrSkillStoredCopyDamaged)
	assert.Equal(t, storage.SkillStatusUnusable, g.records(t)["pdf"].Status)
	_, err = g.rt.AvailableSkill(skillsSrv, "pdf")
	assert.ErrorIs(t, err, ErrSkillNotFound)
}

func TestSearchAvailableSkills(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	for _, n := range []string{"pdf-tools", "doc-writer", "hf-cli"} {
		g.fx.AddSkill(skillstest.Skill{Path: n, Files: map[string][]byte{"SKILL.md": skillMD(n, "handles "+n+" tasks", "b")}})
	}
	require.NoError(t, g.refresh(t))

	hits, total, err := g.rt.SearchAvailableSkills("pdf", 10, nil)
	require.NoError(t, err)
	assert.Empty(t, hits, "nothing approved yet")
	assert.Zero(t, total)

	g.approve(t, "pdf-tools", "hf-cli")
	hits, total, err = g.rt.SearchAvailableSkills("pdf", 10, nil)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, skillsSrv+":pdf-tools", hits[0].ID)
	assert.Equal(t, 1, total)

	hits, _, err = g.rt.SearchAvailableSkills("hf cli", 10, nil)
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	assert.Equal(t, skillsSrv+":hf-cli", hits[0].ID)

	// Not approved: never found, even by its exact name.
	hits, total, err = g.rt.SearchAvailableSkills("doc-writer", 10, nil)
	require.NoError(t, err)
	assert.Empty(t, hits)
	assert.Zero(t, total)

	// total counts matches, hits are limited.
	hits, total, err = g.rt.SearchAvailableSkills("handles", 1, nil)
	require.NoError(t, err)
	assert.Len(t, hits, 1)
	assert.Equal(t, 2, total)
}

func TestSearchAvailableSkills_ReachFiltersHitsAndTotal(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	g.approve(t, "pdf")

	hits, total, err := g.rt.SearchAvailableSkills("pdf", 10, func(string) bool { return false })
	require.NoError(t, err)
	assert.Empty(t, hits)
	assert.Zero(t, total, "total never counts skills outside the caller's reach")

	hits, total, err = g.rt.SearchAvailableSkills("pdf", 10, func(s string) bool { return s == skillsSrv })
	require.NoError(t, err)
	assert.Len(t, hits, 1)
	assert.Equal(t, 1, total)
}

func TestSearchAvailableSkills_FollowsBlockAndConfigWithoutAnIndexCall(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	g.approve(t, "pdf")
	count := func() int { _, n, err := g.rt.SearchAvailableSkills("pdf", 10, nil); require.NoError(t, err); return n }
	require.Equal(t, 1, count())

	_, err := g.rt.SetSkillsBlockedAt(adminCtx(), skillsSrv, []string{"pdf"}, true, "test", 0)
	require.NoError(t, err)
	assert.Zero(t, count(), "blocked skill disappears")
	_, err = g.rt.SetSkillsBlockedAt(adminCtx(), skillsSrv, []string{"pdf"}, false, "test", 0)
	require.NoError(t, err)
	assert.Equal(t, 1, count(), "unblocked skill is back")

	g.setServer(t, func(sc *config.ServerConfig) { sc.Quarantined = true })
	assert.Zero(t, count())
	g.setServer(t, func(sc *config.ServerConfig) { sc.Quarantined = false })
	assert.Equal(t, 1, count())

	g.setGlobal(t, func(c *config.Config) { c.EnableSkills = false })
	hits, total, err := g.rt.SearchAvailableSkills("pdf", 10, nil)
	require.NoError(t, err)
	assert.Empty(t, hits)
	assert.Zero(t, total)
}

// builtOf is the generation the only cached index was built from.
func (g *skillsRig) builtOf(t *testing.T) uint64 {
	t.Helper()
	g.rt.skills.idxMu.Lock()
	defer g.rt.skills.idxMu.Unlock()
	require.Len(t, g.rt.skills.idxCache, 1)
	for _, e := range g.rt.skills.idxCache {
		return e.built
	}
	return 0
}

func TestSearchAvailableSkills_RebuildsOnlyWhenTheGenerationMoved(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	g.approve(t, "pdf")
	_, _, err := g.rt.SearchAvailableSkills("pdf", 10, nil)
	require.NoError(t, err)
	built := g.builtOf(t)
	_, _, err = g.rt.SearchAvailableSkills("pdf", 10, nil)
	require.NoError(t, err)
	assert.Equal(t, built, g.builtOf(t))
	g.rt.skills.bumpIndexGeneration()
	_, _, err = g.rt.SearchAvailableSkills("pdf", 10, nil)
	require.NoError(t, err)
	assert.Greater(t, g.builtOf(t), built)
}

// addApprovedServer adds a second server to the configuration with n approved
// skills written straight into the store (it is never connected).
func (g *skillsRig) addApprovedServer(t *testing.T, name string, n int, desc string) {
	t.Helper()
	_, err := g.rt.Commit("test_add", func(tx *CommitTx) error {
		return tx.CreateServer(&config.ServerConfig{Name: name, URL: "http://127.0.0.1:1/mcp", Protocol: "streamable-http", Enabled: true, TrustMode: "manual"})
	})
	require.NoError(t, err)
	_, err = g.rt.Commit("test_trust", func(tx *CommitTx) error { return tx.SetQuarantined(name, false) })
	require.NoError(t, err)
	g.rt.skills.wait()
	sc := skillsServerConfig(g.rt.Config(), name)
	require.True(t, skillsServerExposed(g.rt.Config(), sc), "the fixture server must be one that may serve skills")
	for i := 0; i < n; i++ {
		p := fmt.Sprintf("other-%03d", i)
		fm, merr := json.Marshal(map[string]string{"name": p, "description": desc})
		require.NoError(t, merr)
		content := storage.SkillContent{Frontmatter: fm, Files: []storage.SkillFile{{RelPath: "SKILL.md", URI: "skill://" + p + "/SKILL.md", Digest: "sha256:" + strings.Repeat("a", 64), Size: 1, Text: true, Stored: true}}}
		require.NoError(t, g.rt.storageManager.SaveSkillApproval(&storage.SkillApprovalRecord{
			ServerName: name, SkillURI: "skill://" + p + "/SKILL.md", SkillPath: p, Name: p, Description: desc,
			Status: storage.SkillStatusApproved, Listed: true, Current: content, Approved: &content,
			CurrentHash: "sk1-x", ApprovedHash: "sk1-x", FirstSeen: time.Now(), UpdatedAt: time.Now(),
		}))
	}
	g.rt.skills.bumpIndexGeneration()
}

// A full-text score depends on how common a word is in the whole index. With
// one index for everybody, skills a caller cannot reach would move the order
// of the caller's own results: an answer that changes with skills that must
// not exist for it. Each reach has its own index.
func TestSearchAvailableSkills_SkillsOutsideReachDoNotMoveTheOrder(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "one", Files: map[string][]byte{"SKILL.md": skillMD("one", "alpha alpha gamma", "b")}})
	g.fx.AddSkill(skillstest.Skill{Path: "two", Files: map[string][]byte{"SKILL.md": skillMD("two", "beta delta", "b")}})
	g.fx.AddSkill(skillstest.Skill{Path: "three", Files: map[string][]byte{"SKILL.md": skillMD("three", "alpha beta", "b")}})
	require.NoError(t, g.refresh(t))
	g.approve(t, "one", "two", "three")
	own := func(s string) bool { return s == skillsSrv }
	ids := func() ([]string, int) {
		hits, total, err := g.rt.SearchAvailableSkills("alpha beta", 10, own)
		require.NoError(t, err)
		var out []string
		for _, h := range hits {
			out = append(out, h.ID)
		}
		return out, total
	}
	before, totalBefore := ids()
	require.Len(t, before, 3)

	g.addApprovedServer(t, "other", 40, "alpha alpha alpha")
	after, totalAfter := ids()
	assert.Equal(t, before, after, "the order of a caller's results is the same with and without skills it cannot reach")
	assert.Equal(t, totalBefore, totalAfter)

	// The caller's index holds its own servers' skills and nothing else.
	g.rt.skills.idxMu.Lock()
	e := g.rt.skills.idxCache[skillsSrv]
	g.rt.skills.idxMu.Unlock()
	require.NotNil(t, e)
	assert.Equal(t, 3, e.ix.Len())

	all, total, err := g.rt.SearchAvailableSkills("alpha", 5, nil)
	require.NoError(t, err)
	assert.Len(t, all, 5)
	assert.Equal(t, 42, total, "an unrestricted caller counts every server")
}

// A search reads one record per returned hit: a hit whose record is gone or
// no longer available is dropped and not counted.
func TestSearchAvailableSkills_AHitWithoutItsRecordIsDroppedAndNotCounted(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf-two"})
	require.NoError(t, g.refresh(t))
	g.approve(t, "pdf", "pdf-two")
	hits, total, err := g.rt.SearchAvailableSkills("pdf", 10, nil)
	require.NoError(t, err)
	require.Len(t, hits, 2)
	require.Equal(t, 2, total)

	// Removed behind the index's back (no generation bump).
	require.NoError(t, g.rt.storageManager.DeleteSkillApproval(skillsSrv, g.records(t)["pdf-two"].SkillURI))
	hits, total, err = g.rt.SearchAvailableSkills("pdf", 10, nil)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, skillsSrv+":pdf", hits[0].ID)
	assert.Equal(t, 1, total)
}

func TestSearchAvailableSkills_AVeryLongQueryIsCut(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	g.approve(t, "pdf")
	hits, _, err := g.rt.SearchAvailableSkills("pdf "+strings.Repeat("zzz ", 100000), 10, nil)
	require.NoError(t, err)
	assert.Len(t, hits, 1)
}

// A server that renames its skills on every listing leaves a record behind
// each time. The oldest are dropped; a blocked one is kept.
func TestSkillsRefresh_RecordsOfUnlistedSkillsAreBounded(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	old := time.Now().Add(-time.Hour)
	for i := 0; i < skills.MaxUnlistedSkillsPerServer+5; i++ {
		p := fmt.Sprintf("gone-%03d", i)
		require.NoError(t, g.rt.storageManager.SaveSkillApproval(&storage.SkillApprovalRecord{
			ServerName: skillsSrv, SkillURI: "skill://" + p + "/SKILL.md", SkillPath: p, Name: p,
			Status: storage.SkillStatusPending, Listed: false, Blocked: i == 0,
			FirstSeen: old, UpdatedAt: old.Add(time.Duration(i) * time.Second),
		}))
	}
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))

	recs := g.records(t)
	unlisted := 0
	for _, r := range recs {
		if !r.Listed && !r.Blocked {
			unlisted++
		}
	}
	assert.Equal(t, skills.MaxUnlistedSkillsPerServer, unlisted)
	require.Contains(t, recs, "gone-000", "a blocked record is kept: the mark is a person's decision")
	assert.True(t, recs["gone-000"].Blocked)
	assert.NotContains(t, recs, "gone-001", "the oldest unlisted records go first")
	assert.Contains(t, recs, fmt.Sprintf("gone-%03d", skills.MaxUnlistedSkillsPerServer+4))
	assert.Contains(t, recs, "pdf")
}

func TestSearchAvailableSkills_AfterCloseIsEmpty(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	g.approve(t, "pdf")
	_, _, err := g.rt.SearchAvailableSkills("pdf", 10, nil)
	require.NoError(t, err)
	g.rt.skills.closeIndex()
	hits, total, err := g.rt.SearchAvailableSkills("pdf", 10, nil)
	require.NoError(t, err)
	assert.Empty(t, hits)
	assert.Zero(t, total)
}

func TestListAvailableSkills(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	for _, n := range []string{"b-skill", "a-skill", "held"} {
		g.fx.AddSkill(skillstest.Skill{Path: n})
	}
	require.NoError(t, g.refresh(t))
	got, err := g.rt.ListAvailableSkills(skillsSrv)
	require.NoError(t, err)
	assert.Empty(t, got)
	g.approve(t, "b-skill", "a-skill")
	got, err = g.rt.ListAvailableSkills(skillsSrv)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "a-skill", got[0].Path)
	assert.Equal(t, "b-skill", got[1].Path)
	g.setServer(t, func(sc *config.ServerConfig) { sc.Enabled = false })
	got, err = g.rt.ListAvailableSkills(skillsSrv)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// A listing that is cut short proves nothing about what is gone, so its
// absent records keep their listed mark. They are bounded all the same: a
// server that names 200 new skills and a continuation on every refresh must
// not grow the store.
func TestSkillsRefresh_RecordsALaterListingDoesNotNameAreBounded(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	old := time.Now().Add(-time.Hour)
	for i := 0; i < skills.MaxUnlistedSkillsPerServer+7; i++ {
		p := fmt.Sprintf("stale-%03d", i)
		require.NoError(t, g.rt.storageManager.SaveSkillApproval(&storage.SkillApprovalRecord{
			ServerName: skillsSrv, SkillURI: "skill://" + p + "/SKILL.md", SkillPath: p, Name: p,
			Status: storage.SkillStatusPending, Listed: true, // still marked listed, as after a truncated refresh
			FirstSeen: old, UpdatedAt: old.Add(time.Duration(i) * time.Second),
		}))
	}
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))

	recs := g.records(t)
	assert.Len(t, recs, skills.MaxUnlistedSkillsPerServer+1, "the bound, plus the skill the listing named")
	assert.Contains(t, recs, "pdf")
	assert.NotContains(t, recs, "stale-000")
	assert.Contains(t, recs, fmt.Sprintf("stale-%03d", skills.MaxUnlistedSkillsPerServer+6))
}

// The index's count of matches it did not return cannot be trusted once a
// returned hit turned out to be gone: the search is repeated on a fresh index,
// so a held skill is never counted.
func TestSearchAvailableSkills_AStaleHitNeverLeavesAHeldSkillInTheTotal(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf-a"})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf-b"})
	require.NoError(t, g.refresh(t))
	g.approve(t, "pdf-a", "pdf-b")
	_, total, err := g.rt.SearchAvailableSkills("pdf", 1, nil)
	require.NoError(t, err)
	require.Equal(t, 2, total)

	// Both blocked behind the index's back (no generation bump), as when the
	// block lands between the index search and the record check.
	for _, p := range []string{"pdf-a", "pdf-b"} {
		rec := g.records(t)[p]
		rec.Blocked = true
		require.NoError(t, g.rt.storageManager.SaveSkillApproval(rec))
	}
	hits, total, err := g.rt.SearchAvailableSkills("pdf", 1, nil)
	require.NoError(t, err)
	assert.Empty(t, hits)
	assert.Zero(t, total, "a blocked skill is not counted, also when it was not among the returned hits")
}

// Every match is checked against its record, not only the returned ones: a
// skill blocked behind the index's back is not counted even when the returned
// hit is fine.
func TestSearchAvailableSkills_AHeldSkillBeyondTheReturnedPageIsNotCounted(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"SKILL.md": skillMD("pdf", "pdf pdf pdf", "b")}})
	g.fx.AddSkill(skillstest.Skill{Path: "other", Files: map[string][]byte{"SKILL.md": skillMD("other", "also about pdf", "b")}})
	require.NoError(t, g.refresh(t))
	g.approve(t, "pdf", "other")
	hits, total, err := g.rt.SearchAvailableSkills("pdf", 1, nil)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, skillsSrv+":pdf", hits[0].ID, "the fixture ranks pdf first")
	require.Equal(t, 2, total)

	// The second-ranked skill is blocked; nothing tells the index.
	rec := g.records(t)["other"]
	rec.Blocked = true
	require.NoError(t, g.rt.storageManager.SaveSkillApproval(rec))

	hits, total, err = g.rt.SearchAvailableSkills("pdf", 1, nil)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, skillsSrv+":pdf", hits[0].ID)
	assert.Equal(t, 1, total, "the blocked skill was not among the returned hits and is still not counted")
}

func TestSkillsByPath_AgreesWithSkillByPath(t *testing.T) {
	rec := func(uri, path string, listed bool) *storage.SkillApprovalRecord {
		return &storage.SkillApprovalRecord{SkillURI: uri, SkillPath: path, Listed: listed}
	}
	recs := []*storage.SkillApprovalRecord{
		rec("u1", "one", true),
		rec("u2", "two", false), rec("u3", "two", true),
		rec("u4", "three", true), rec("u5", "three", true),
		rec("u6", "four", false), rec("u7", "four", false),
		rec("u8", "five", false),
	}
	got := skillsByPath(recs)
	for _, p := range []string{"one", "two", "three", "four", "five", "none"} {
		want := skillByPath(recs, p)
		if want == nil {
			assert.NotContains(t, got, p)
			continue
		}
		assert.Same(t, want, got[p], p)
	}
}
