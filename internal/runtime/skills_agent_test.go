package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
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

func TestSearchAvailableSkills_RebuildsOnlyWhenTheGenerationMoved(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	g.approve(t, "pdf")
	_, _, err := g.rt.SearchAvailableSkills("pdf", 10, nil)
	require.NoError(t, err)
	built := g.rt.skills.idxBuilt
	_, _, err = g.rt.SearchAvailableSkills("pdf", 10, nil)
	require.NoError(t, err)
	assert.Equal(t, built, g.rt.skills.idxBuilt)
	g.rt.skills.bumpIndexGeneration()
	_, _, err = g.rt.SearchAvailableSkills("pdf", 10, nil)
	require.NoError(t, err)
	assert.Greater(t, g.rt.skills.idxBuilt, built)
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
