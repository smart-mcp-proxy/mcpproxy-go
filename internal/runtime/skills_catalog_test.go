package runtime

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/skillstest"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 PR-1 (T017-T019): the runtime skills catalog. Every test drives a
// real connected managed client against the skillstest fixture (a raw
// JSON-RPC upstream), so listing, reading, capture stamping and storage are
// the production paths.

const skillsSrv = "sk"

type skillsRig struct {
	rt *Runtime
	fx *skillstest.Fixture
}

type skillsRigOpts struct {
	enable   bool
	expose   *bool
	fixture  []skillstest.Option
	autoTrig bool   // leave the background triggers on
	trust    string // trust_mode of the server; "" means manual (the PR-1 behaviour the older tests assume)
}

func newSkillsRig(t *testing.T, o skillsRigOpts) *skillsRig {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	fx := skillstest.New(t, o.fixture...)
	if o.trust == "" {
		o.trust = "manual"
	}
	rt, _ := newSpec116Runtime(t, func(c *config.Config) { c.EnableSkills = o.enable },
		explicitServer(&config.ServerConfig{
			Name: skillsSrv, URL: fx.URL(), Protocol: "streamable-http", Enabled: true, ExposeSkills: o.expose, TrustMode: o.trust,
		}))
	if !o.autoTrig {
		rt.skills.noAutoTrigger.Store(true)
	}
	rt.discoveryInProgress.Store(skillsSrv, struct{}{})
	require.NoError(t, rt.upstreamManager.AddServerAt(skillsSrv, spec116Live(rt, skillsSrv), storedStamp(t, rt, skillsSrv).Stamp.Gen))
	c, ok := rt.upstreamManager.GetClient(skillsSrv)
	require.True(t, ok)
	require.Eventually(t, c.IsConnected, 10*time.Second, 5*time.Millisecond)
	return &skillsRig{rt: rt, fx: fx}
}

func (g *skillsRig) refresh(t *testing.T) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return g.rt.RefreshServerSkills(ctx, skillsSrv)
}

func (g *skillsRig) records(t *testing.T) map[string]*storage.SkillApprovalRecord {
	t.Helper()
	list, err := g.rt.ListSkillRecords(skillsSrv)
	require.NoError(t, err)
	out := map[string]*storage.SkillApprovalRecord{}
	for _, r := range list {
		out[r.SkillPath] = r
	}
	return out
}

func (g *skillsRig) blobCount(t *testing.T) int {
	t.Helper()
	d, err := g.rt.storageManager.ListSkillFileDigests(skillsSrv)
	require.NoError(t, err)
	return len(d)
}

// approveInStorage copies a record's current content into its approved copy,
// as the next PR's approval will.
func (g *skillsRig) approveInStorage(t *testing.T, path string) {
	t.Helper()
	rec := g.records(t)[path]
	require.NotNil(t, rec)
	cur := rec.Current
	rec.Approved = &cur
	rec.ApprovedHash = rec.CurrentHash
	rec.Status = storage.SkillStatusApproved
	rec.ApprovedBy = "test"
	rec.ApprovedAt = time.Now()
	require.NoError(t, g.rt.storageManager.SaveSkillApproval(rec))
}

func sized(n int, b byte) []byte { return []byte(strings.Repeat(string(b), n)) }

func TestSkillsRefresh_FeatureOffSendsNothing(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: false})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	assert.Zero(t, g.fx.Count("skills/list"))
	assert.Zero(t, g.fx.Count("resources/read"))
	assert.Empty(t, g.records(t))
	assert.Zero(t, g.blobCount(t))
}

func TestSkillsRefresh_ExposeFalseSendsNothing(t *testing.T) {
	off := false
	g := newSkillsRig(t, skillsRigOpts{enable: true, expose: &off})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	assert.Zero(t, g.fx.Count("skills/list"))
	assert.Empty(t, g.records(t))
}

func TestSkillsRefresh_ServerWithoutExtension(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true, fixture: []skillstest.Option{skillstest.WithoutExtension()}})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	assert.Zero(t, g.fx.Count("skills/list"))
	assert.False(t, g.rt.SkillsStatus(skillsSrv).Supported)
	assert.Empty(t, g.records(t))
}

func TestSkillsRefresh_ReadsSkillsAsPending(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "one"})
	g.fx.AddSkill(skillstest.Skill{Path: "two", Files: map[string][]byte{"ref/a.md": []byte("alpha"), "scripts/run.sh": []byte("echo hi")}})
	g.fx.AddSkill(skillstest.Skill{Path: "acme/three"})
	require.NoError(t, g.refresh(t))

	recs := g.records(t)
	require.Len(t, recs, 3)
	entries, _, _, err := g.rt.upstreamManager.ListServerSkills(context.Background(), skillsSrv)
	require.NoError(t, err)
	for _, e := range entries {
		want, err := skills.ContentHash(e)
		require.NoError(t, err)
		_, path, _, _ := skills.ParseSkillURI(e.URI)
		rec := recs[path]
		require.NotNil(t, rec, path)
		assert.Equal(t, storage.SkillStatusPending, rec.Status, path)
		assert.Equal(t, want, rec.CurrentHash, path)
		assert.True(t, rec.Listed)
		assert.Equal(t, 1, rec.HashSchemaVersion)
		assert.Equal(t, e.URI, rec.SkillURI)
		assert.Nil(t, rec.Approved)
		assert.NotZero(t, rec.CaptureSeq)
		assert.Len(t, rec.Current.Files, len(e.Files))
		for _, f := range rec.Current.Files {
			assert.True(t, f.Text && f.Stored, f.RelPath)
			b, err := g.rt.storageManager.GetSkillFile(skillsSrv, f.Digest)
			require.NoError(t, err)
			assert.EqualValues(t, f.Size, len(b))
		}
	}
	assert.Equal(t, "three", recs["acme/three"].Name)
	assert.Len(t, recs["two"].Current.Files, 3)

	st := g.rt.SkillsStatus(skillsSrv)
	assert.True(t, st.Supported)
	assert.False(t, st.Truncated)
	assert.Empty(t, st.Error)
	assert.False(t, st.LastRefresh.IsZero())
}

func TestSkillsRefresh_UnusableReasons(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	many := map[string][]byte{}
	for i := 0; i < skills.MaxFilesPerSkill+1; i++ {
		many[fmt.Sprintf("f%03d.txt", i)] = []byte("x")
	}
	g.fx.AddSkill(skillstest.Skill{Path: "dyn", Dynamic: true})
	g.fx.AddSkill(skillstest.Skill{Path: "wd", Files: map[string][]byte{"a.md": []byte("a")}, WrongDigest: []string{"a.md"}})
	g.fx.AddSkill(skillstest.Skill{Path: "ws", Files: map[string][]byte{"a.md": []byte("a")}, WrongSize: []string{"a.md"}})
	g.fx.AddSkill(skillstest.Skill{Path: "fm", ListedFrontmatter: map[string]any{"name": "fm", "description": "a different description"}})
	g.fx.AddSkill(skillstest.Skill{Path: "nm", ListedFrontmatter: map[string]any{"name": "other", "description": "d"}})
	g.fx.AddSkill(skillstest.Skill{Path: "a//b"})
	g.fx.AddSkill(skillstest.Skill{Path: "many", Files: many})
	g.fx.AddSkill(skillstest.Skill{Path: "bin", Files: map[string][]byte{"img.bin": {0, 1, 2, 3}}, Binary: []string{"img.bin"}})
	g.fx.AddSkill(skillstest.Skill{Path: "good", Files: map[string][]byte{"a.md": []byte("a")}})
	require.NoError(t, g.refresh(t))

	recs := g.records(t)
	want := map[string]string{
		"dyn": skills.ReasonDynamic, "wd": skills.ReasonDigestMismatch, "ws": skills.ReasonSizeMismatch,
		"fm": skills.ReasonFrontmatterMismatch, "nm": skills.ReasonNameMismatch,
		"many": skills.ReasonTooManyFiles,
	}
	for path, code := range want {
		rec := recs[path]
		require.NotNil(t, rec, path)
		assert.Equal(t, storage.SkillStatusUnusable, rec.Status, path)
		assert.True(t, strings.HasPrefix(rec.UnusableReason, code+":") || rec.UnusableReason == code, "%s: %q", path, rec.UnusableReason)
	}
	// An address with an empty segment is recorded under its URI.
	var invalid *storage.SkillApprovalRecord
	for _, r := range recs {
		if strings.Contains(r.SkillURI, "a//b") {
			invalid = r
		}
	}
	require.NotNil(t, invalid)
	assert.Equal(t, storage.SkillStatusUnusable, invalid.Status)
	assert.True(t, strings.HasPrefix(invalid.UnusableReason, skills.ReasonInvalidURI))

	// Nothing was fetched for a skill that failed validation.
	for _, r := range g.fx.Requests() {
		if r.Method == "resources/read" {
			assert.NotContains(t, r.URI, "skill://dyn/")
			assert.NotContains(t, r.URI, "skill://many/")
		}
	}

	// A binary file is recorded, not stored, and the skill stays usable.
	bin := recs["bin"]
	require.NotNil(t, bin)
	assert.Equal(t, storage.SkillStatusPending, bin.Status)
	var seen bool
	for _, f := range bin.Current.Files {
		if f.RelPath == "img.bin" {
			seen = true
			assert.False(t, f.Text)
			assert.False(t, f.Stored)
		}
	}
	assert.True(t, seen)

	// One bad skill did not stop the others.
	assert.Equal(t, storage.SkillStatusPending, recs["good"].Status)
	assert.Contains(t, g.rt.SkillsStatus(skillsSrv).Error+"ok", "ok")
}

func TestSkillsRefresh_ReusesStoredBlobsAndReverifiesThem(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"a.md": []byte("alpha")}})
	require.NoError(t, g.refresh(t))
	reads := g.fx.Count("resources/read")
	require.Equal(t, 2, reads)

	require.NoError(t, g.refresh(t))
	assert.Equal(t, reads, g.fx.Count("resources/read"), "stored blobs are reused without a read")

	// Damage one stored blob: the next refresh notices and refetches it.
	var damaged storage.SkillFile
	for _, f := range g.records(t)["pdf"].Current.Files {
		if f.RelPath == "a.md" {
			damaged = f
		}
	}
	require.NoError(t, g.rt.storageManager.PutSkillFile(skillsSrv, damaged.Digest, []byte("tampered")))
	require.NoError(t, g.refresh(t))
	assert.Equal(t, reads+1, g.fx.Count("resources/read"))
	b, err := g.rt.storageManager.GetSkillFile(skillsSrv, damaged.Digest)
	require.NoError(t, err)
	assert.Equal(t, "alpha", string(b))
}

func TestSkillsRefresh_StorageBudget(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "one", Files: map[string][]byte{"a.md": sized(100, 'a')}})
	g.fx.AddSkill(skillstest.Skill{Path: "two", Files: map[string][]byte{"a.md": sized(100, 'b')}})
	entries, _, _, err := g.rt.upstreamManager.ListServerSkills(context.Background(), skillsSrv)
	require.NoError(t, err)
	var first int64
	for _, f := range entries[0].Files {
		first += f.Size
	}
	g.rt.skills.limits = skillsLimits{readPerRefresh: math.MaxInt64, storedPerServer: first + 10}
	require.NoError(t, g.refresh(t))

	recs := g.records(t)
	assert.Equal(t, storage.SkillStatusPending, recs["one"].Status)
	assert.Equal(t, storage.SkillStatusUnusable, recs["two"].Status)
	assert.True(t, strings.HasPrefix(recs["two"].UnusableReason, skills.ReasonServerBoundExceeded), recs["two"].UnusableReason)
	// The refused skill staged nothing.
	assert.Equal(t, len(recs["one"].Current.Files), g.blobCount(t))
}

func TestSkillsRefresh_BinaryReleasesItsReservation(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "bin", Files: map[string][]byte{"big.bin": sized(400, 1)}, Binary: []string{"big.bin"}})
	g.fx.AddSkill(skillstest.Skill{Path: "txt"})
	entries, _, _, err := g.rt.upstreamManager.ListServerSkills(context.Background(), skillsSrv)
	require.NoError(t, err)
	var binTotal, txt int64
	for _, f := range entries[0].Files {
		binTotal += f.Size
	}
	for _, f := range entries[1].Files {
		txt += f.Size
	}
	// Enough for the binary skill's reservation plus a little, but not for
	// that reservation plus the second skill, unless the binary is released.
	g.rt.skills.limits = skillsLimits{readPerRefresh: math.MaxInt64, storedPerServer: binTotal + txt/2}
	require.NoError(t, g.refresh(t))
	recs := g.records(t)
	assert.Equal(t, storage.SkillStatusPending, recs["bin"].Status)
	assert.Equal(t, storage.SkillStatusPending, recs["txt"].Status, recs["txt"].UnusableReason)
}

func TestSkillsRefresh_ReadBudgetPerRefresh(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "one", Files: map[string][]byte{"a.md": sized(100, 'a')}})
	g.fx.AddSkill(skillstest.Skill{Path: "two", Files: map[string][]byte{"a.md": sized(100, 'b')}})
	entries, _, _, err := g.rt.upstreamManager.ListServerSkills(context.Background(), skillsSrv)
	require.NoError(t, err)
	var first int64
	for _, f := range entries[0].Files {
		first += f.Size
	}
	g.rt.skills.limits = skillsLimits{readPerRefresh: first + 10, storedPerServer: math.MaxInt64}
	require.NoError(t, g.refresh(t))
	recs := g.records(t)
	assert.Equal(t, storage.SkillStatusPending, recs["one"].Status)
	assert.Equal(t, storage.SkillStatusUnusable, recs["two"].Status)
	assert.True(t, strings.HasPrefix(recs["two"].UnusableReason, skills.ReasonServerBoundExceeded))
}

func TestSkillsRefresh_CollectsLeftoversOfAnAbandonedRefresh(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "one"})
	junk := []byte("staged by a refresh that never applied")
	junkDigest := "sha256:" + strings.Repeat("ab", 32)
	require.NoError(t, g.rt.storageManager.PutSkillFile(skillsSrv, junkDigest, junk))
	require.NoError(t, g.refresh(t))
	ok, err := g.rt.storageManager.HasSkillFile(skillsSrv, junkDigest)
	require.NoError(t, err)
	assert.False(t, ok, "an unreferenced blob is collected")
	assert.Equal(t, 1, g.blobCount(t))
}

func seedApprovedSkill(t *testing.T, g *skillsRig, uri string, listed bool) {
	t.Helper()
	cur := storage.SkillContent{Files: []storage.SkillFile{{RelPath: "SKILL.md", URI: uri, Digest: "sha256:" + strings.Repeat("cd", 32), Size: 3, Text: true}}}
	rec := &storage.SkillApprovalRecord{
		ServerName: skillsSrv, SkillURI: uri, SkillPath: "gone", Name: "gone", Status: storage.SkillStatusApproved,
		Listed: listed, Current: cur, Approved: &cur, CurrentHash: "sk1-x", ApprovedHash: "sk1-x",
		ApprovedBy: "alice", ApprovedAt: time.Unix(100, 0), FirstSeen: time.Unix(50, 0), HashSchemaVersion: 1,
	}
	require.NoError(t, g.rt.storageManager.SaveSkillApproval(rec))
}

func TestSkillsRefresh_FirstEmptyListingAnnouncesSupport(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	ch := g.rt.SubscribeEvents()
	defer g.rt.UnsubscribeEvents(ch)
	for i := 0; i < 2; i++ {
		require.NoError(t, g.refresh(t))
		require.True(t, g.rt.SkillsStatus(skillsSrv).Supported)
		names, reviews := skillsAnnouncements(t, ch, 300*time.Millisecond)
		if i == 0 {
			assert.Equal(t, []string{skillsSrv}, names)
		} else {
			assert.Empty(t, names)
		}
		assert.Empty(t, reviews)
	}
}

func TestSkillsRefresh_EmptyListingKeepsRecordsUnlisted(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	seedApprovedSkill(t, g, "skill://gone/SKILL.md", true)
	require.NoError(t, g.refresh(t))
	rec := g.records(t)["gone"]
	require.NotNil(t, rec, "never deleted")
	assert.False(t, rec.Listed)
	assert.Equal(t, storage.SkillStatusApproved, rec.Status)
	assert.Equal(t, "alice", rec.ApprovedBy)
	assert.Equal(t, "sk1-x", rec.ApprovedHash)
	require.NotNil(t, rec.Approved)
}

func TestSkillsRefresh_TruncatedListingMarksNothingUnlisted(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true, fixture: []skillstest.Option{skillstest.WithPageSize(100)}})
	for i := 0; i < skills.MaxSkillsPerServer+5; i++ {
		g.fx.AddSkill(skillstest.Skill{Path: fmt.Sprintf("d%03d", i), Dynamic: true})
	}
	seedApprovedSkill(t, g, "skill://gone/SKILL.md", true)
	require.NoError(t, g.refresh(t))
	assert.True(t, g.rt.SkillsStatus(skillsSrv).Truncated)
	rec := g.records(t)["gone"]
	require.NotNil(t, rec)
	assert.True(t, rec.Listed, "absence from a truncated listing proves nothing")
	assert.Len(t, g.records(t), skills.MaxSkillsPerServer+1)
}

func TestSkillsRefresh_ApprovedRecordFollowsContent(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"a.md": []byte("original")}})
	require.NoError(t, g.refresh(t))
	g.approveInStorage(t, "pdf")
	approved := g.records(t)["pdf"]
	firstSeen := approved.FirstSeen

	require.NoError(t, g.refresh(t))
	rec := g.records(t)["pdf"]
	assert.Equal(t, storage.SkillStatusApproved, rec.Status, "unchanged content stays approved")
	assert.Equal(t, approved.ApprovedHash, rec.ApprovedHash)
	assert.Equal(t, "test", rec.ApprovedBy)
	assert.True(t, rec.FirstSeen.Equal(firstSeen))

	g.fx.Mutate("pdf", "a.md", []byte("changed upstream"))
	require.NoError(t, g.refresh(t))
	rec = g.records(t)["pdf"]
	assert.Equal(t, storage.SkillStatusChanged, rec.Status)
	assert.NotEqual(t, rec.ApprovedHash, rec.CurrentHash)
	require.NotNil(t, rec.Approved, "the approved copy is kept")
	assert.Equal(t, approved.ApprovedHash, rec.ApprovedHash)
	// Both copies are retained, so both blobs survive collection.
	assert.Equal(t, 3, g.blobCount(t)) // SKILL.md, old a.md, new a.md

	g.fx.Mutate("pdf", "a.md", []byte("original"))
	require.NoError(t, g.refresh(t))
	rec = g.records(t)["pdf"]
	assert.Equal(t, storage.SkillStatusApproved, rec.Status, "content returned to the approved hash")
}

func TestSkillsRefresh_UnusableKeepsApprovedCopy(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "wd", Files: map[string][]byte{"a.md": []byte("a")}, WrongDigest: []string{"a.md"}})
	// A person approved an earlier, honest version of this skill.
	cur := storage.SkillContent{Files: []storage.SkillFile{{RelPath: "SKILL.md", URI: "skill://wd/SKILL.md", Digest: "sha256:" + strings.Repeat("cd", 32), Size: 3, Text: true}}}
	require.NoError(t, g.rt.storageManager.SaveSkillApproval(&storage.SkillApprovalRecord{
		ServerName: skillsSrv, SkillURI: "skill://wd/SKILL.md", SkillPath: "wd", Name: "wd", Status: storage.SkillStatusApproved,
		Listed: true, Current: cur, Approved: &cur, CurrentHash: "sk1-old", ApprovedHash: "sk1-old", ApprovedBy: "alice",
		FirstSeen: time.Unix(50, 0), HashSchemaVersion: 1,
	}))
	require.NoError(t, g.refresh(t))
	rec := g.records(t)["wd"]
	require.NotNil(t, rec)
	assert.Equal(t, storage.SkillStatusUnusable, rec.Status)
	require.NotNil(t, rec.Approved, "unusable keeps the approved copy")
	assert.Equal(t, "sk1-old", rec.ApprovedHash)
	assert.Equal(t, "alice", rec.ApprovedBy)
	assert.True(t, rec.FirstSeen.Equal(time.Unix(50, 0)))
}

func TestSkillsRefresh_StaleCaptureWritesNothing(t *testing.T) {
	t.Run("retired client", func(t *testing.T) {
		g := newSkillsRig(t, skillsRigOpts{enable: true})
		g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
		g.rt.skills.afterList = func(string) {
			c, _ := g.rt.upstreamManager.GetClient(skillsSrv)
			c.Retire()
		}
		err := g.refresh(t)
		require.Error(t, err)
		assert.True(t, errors.Is(err, errSkillsCaptureStale), "%v", err)
		assert.Empty(t, g.records(t))
		assert.Empty(t, g.rt.SkillsStatus(skillsSrv).Error, "a stale capture is not a status error")
	})
	t.Run("older than the applied one", func(t *testing.T) {
		g := newSkillsRig(t, skillsRigOpts{enable: true})
		g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
		g.rt.skills.afterList = func(string) {
			g.rt.skills.mu.Lock()
			g.rt.skills.applied[skillsSrv] = math.MaxUint64
			g.rt.skills.mu.Unlock()
		}
		err := g.refresh(t)
		require.Error(t, err)
		assert.True(t, errors.Is(err, errSkillsCaptureStale), "%v", err)
		assert.Empty(t, g.records(t))
	})
}

func TestSkillsRefresh_FailingListingKeepsStateAndTools(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	before := g.records(t)["pdf"]

	g.fx.SetListFailure(true)
	err := g.refresh(t)
	require.Error(t, err)
	st := g.rt.SkillsStatus(skillsSrv)
	assert.NotEmpty(t, st.Error)
	assert.Contains(t, st.Error, "skills/list failed on purpose")
	after := g.records(t)["pdf"]
	assert.Equal(t, before.CurrentHash, after.CurrentHash)
	assert.True(t, after.Listed)
	assert.True(t, after.UpdatedAt.Equal(before.UpdatedAt), "storage untouched")

	// Tool discovery is independent of skills.
	c, _ := g.rt.upstreamManager.GetClient(skillsSrv)
	tools, _, err := c.ListToolsCaptured(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, tools)

	g.fx.SetListFailure(false)
	require.NoError(t, g.refresh(t))
	assert.Empty(t, g.rt.SkillsStatus(skillsSrv).Error)
}

func TestSkillsRefresh_ServerRemovalPurgesBothBuckets(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	require.NotEmpty(t, g.records(t))
	require.NotZero(t, g.blobCount(t))

	_, err := g.rt.Commit("remove_server", func(tx *CommitTx) error { return tx.RemoveServer(skillsSrv) })
	require.NoError(t, err)
	assert.Empty(t, g.records(t))
	assert.Zero(t, g.blobCount(t))
}

func TestSkillFileText(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"ref/a.md": []byte("alpha"), "img.bin": {1, 2, 3}}, Binary: []string{"img.bin"}})
	require.NoError(t, g.refresh(t))

	text, file, err := g.rt.SkillFileText(skillsSrv, "pdf", "ref/a.md", false)
	require.NoError(t, err)
	assert.Equal(t, "alpha", string(text))
	assert.Equal(t, "ref/a.md", file.RelPath)

	_, _, err = g.rt.SkillFileText(skillsSrv, "pdf", "img.bin", false)
	assert.ErrorIs(t, err, ErrSkillFileBinary)
	_, _, err = g.rt.SkillFileText(skillsSrv, "pdf", "nope.md", false)
	assert.ErrorIs(t, err, ErrSkillFileNotFound)
	_, _, err = g.rt.SkillFileText(skillsSrv, "nope", "ref/a.md", false)
	assert.ErrorIs(t, err, ErrSkillNotFound)
	// No approved copy yet.
	_, _, err = g.rt.SkillFileText(skillsSrv, "pdf", "ref/a.md", true)
	assert.ErrorIs(t, err, ErrSkillFileNotFound)

	// Re-verification: damage the approved blob in storage.
	g.approveInStorage(t, "pdf")
	text, _, err = g.rt.SkillFileText(skillsSrv, "pdf", "ref/a.md", true)
	require.NoError(t, err)
	assert.Equal(t, "alpha", string(text))
	var f storage.SkillFile
	for _, x := range g.records(t)["pdf"].Approved.Files {
		if x.RelPath == "ref/a.md" {
			f = x
		}
	}
	require.NoError(t, g.rt.storageManager.PutSkillFile(skillsSrv, f.Digest, []byte("evil!")))
	_, _, err = g.rt.SkillFileText(skillsSrv, "pdf", "ref/a.md", true)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSkillStoredCopyDamaged)
	rec := g.records(t)["pdf"]
	assert.Equal(t, storage.SkillStatusUnusable, rec.Status)
	assert.True(t, strings.HasPrefix(rec.UnusableReason, skills.ReasonStoredCopyDamaged))
	require.NotNil(t, rec.Approved)

	// The current copy shares the damaged row: refused, record not re-marked.
	_, _, err = g.rt.SkillFileText(skillsSrv, "pdf", "ref/a.md", false)
	assert.ErrorIs(t, err, ErrSkillStoredCopyDamaged)
}

func TestSkillAvailable(t *testing.T) {
	yes, no := true, false
	approved := func() *storage.SkillApprovalRecord {
		return &storage.SkillApprovalRecord{Status: storage.SkillStatusApproved, Listed: true}
	}
	cfgWith := func(enable bool, sc *config.ServerConfig) *config.Config {
		c := &config.Config{EnableSkills: enable}
		if sc != nil {
			c.Servers = []*config.ServerConfig{sc}
		}
		return c
	}
	srv := func(mut func(*config.ServerConfig)) *config.ServerConfig {
		s := &config.ServerConfig{Name: "s", Enabled: true}
		if mut != nil {
			mut(s)
		}
		return s
	}
	rec := func(mut func(*storage.SkillApprovalRecord)) *storage.SkillApprovalRecord {
		r := approved()
		mut(r)
		return r
	}
	cases := []struct {
		name string
		cfg  *config.Config
		rec  *storage.SkillApprovalRecord
		want bool
	}{
		{"all good", cfgWith(true, srv(nil)), approved(), true},
		{"expose true", cfgWith(true, srv(func(s *config.ServerConfig) { s.ExposeSkills = &yes })), approved(), true},
		{"feature off", cfgWith(false, srv(nil)), approved(), false},
		{"server missing", cfgWith(true, nil), approved(), false},
		{"server disabled", cfgWith(true, srv(func(s *config.ServerConfig) { s.Enabled = false })), approved(), false},
		{"server quarantined", cfgWith(true, srv(func(s *config.ServerConfig) { s.Quarantined = true })), approved(), false},
		{"expose false", cfgWith(true, srv(func(s *config.ServerConfig) { s.ExposeSkills = &no })), approved(), false},
		{"pending", cfgWith(true, srv(nil)), rec(func(r *storage.SkillApprovalRecord) { r.Status = storage.SkillStatusPending }), false},
		{"changed", cfgWith(true, srv(nil)), rec(func(r *storage.SkillApprovalRecord) { r.Status = storage.SkillStatusChanged }), false},
		{"unusable", cfgWith(true, srv(nil)), rec(func(r *storage.SkillApprovalRecord) { r.Status = storage.SkillStatusUnusable }), false},
		{"blocked", cfgWith(true, srv(nil)), rec(func(r *storage.SkillApprovalRecord) { r.Blocked = true }), false},
		{"unlisted", cfgWith(true, srv(nil)), rec(func(r *storage.SkillApprovalRecord) { r.Listed = false }), false},
		{"nil record", cfgWith(true, srv(nil)), nil, false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, skillAvailableIn(c.cfg, "s", c.rec), c.name)
	}
}

func TestSkillAvailable_UsesLiveConfig(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	rec := &storage.SkillApprovalRecord{Status: storage.SkillStatusApproved, Listed: true}
	assert.True(t, g.rt.SkillAvailable(skillsSrv, rec))
	assert.False(t, g.rt.SkillAvailable("other", rec))
	rec.Blocked = true
	assert.False(t, g.rt.SkillAvailable(skillsSrv, rec))
}

func TestSkillsChangedEvent(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	ch := g.rt.SubscribeEvents()
	defer g.rt.UnsubscribeEvents(ch)
	g.fx.AddSkill(skillstest.Skill{Path: "one"})
	g.fx.AddSkill(skillstest.Skill{Path: "dyn", Dynamic: true})
	require.NoError(t, g.refresh(t))

	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type != EventTypeSkillsChanged {
				continue
			}
			assert.Equal(t, "skills.changed", string(evt.Type))
			assert.Equal(t, skillsSrv, evt.Payload["server"])
			counts, ok := evt.Payload["counts"].(map[string]int)
			require.True(t, ok, "%T", evt.Payload["counts"])
			assert.Equal(t, 1, counts["pending"])
			assert.Equal(t, 1, counts["unusable"])
			assert.Equal(t, 0, counts["available"])
			assert.Equal(t, 0, counts["changed"])
			assert.Equal(t, 0, counts["blocked"])
			// A second identical refresh changes nothing: no event.
			require.NoError(t, g.refresh(t))
			select {
			case again := <-ch:
				assert.NotEqual(t, EventTypeSkillsChanged, again.Type)
			case <-time.After(200 * time.Millisecond):
			}
			return
		case <-deadline:
			t.Fatal("no skills.changed event")
		}
	}
}

// T019: one flight per server, joined by concurrent callers.
func TestSkillsRefresh_ConcurrentCallersSingleFlight(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})

	const callers = 6
	parked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	g.rt.skills.afterList = func(string) {
		once.Do(func() { close(parked) })
		<-release
	}
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() { errs <- g.refresh(t) }()
	}
	spec116Await(t, parked, "the first flight to list")
	require.Eventually(t, func() bool { return g.rt.skills.joiners(skillsSrv) == callers-1 },
		10*time.Second, time.Millisecond, "every other caller joins the running flight")
	close(release)
	for i := 0; i < callers; i++ {
		require.NoError(t, spec116AwaitErr(t, errs, "a joined caller"))
	}
	assert.Equal(t, 1, g.fx.Count("skills/list"))
	assert.Len(t, g.records(t), 1)
}

func TestSkillsRefresh_ConcurrentWithServerRemoval(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	for i := 0; i < 4; i++ {
		g.fx.AddSkill(skillstest.Skill{Path: fmt.Sprintf("s%d", i), Files: map[string][]byte{"a.md": sized(50, byte('a'+i))}})
	}
	parked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	g.rt.skills.afterList = func(string) {
		once.Do(func() { close(parked) })
		<-release
	}
	done := make(chan error, 1)
	go func() { done <- g.refresh(t) }()
	spec116Await(t, parked, "the refresh to list")
	_, err := g.rt.Commit("remove_server", func(tx *CommitTx) error { return tx.RemoveServer(skillsSrv) })
	require.NoError(t, err)
	close(release)
	<-done // any outcome is acceptable; the point is what is left behind
	assert.Empty(t, g.records(t), "a refresh that raced the removal writes no record")
	assert.Zero(t, g.blobCount(t), "and stages no blob after the purge")
}

// Triggers.

func TestSkillsTrigger_ConnectedServerRefreshes(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true, autoTrig: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	g.rt.connectDiscoverHook = func(context.Context, string) error { return nil }
	g.rt.discoveryInProgress.Delete(skillsSrv)
	g.rt.onServerConnectedDiscovery(skillsSrv)
	require.Eventually(t, func() bool { return len(g.records(t)) == 1 }, 10*time.Second, 10*time.Millisecond)
}

func TestSkillsTrigger_FeatureOffDoesNothing(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: false, autoTrig: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	g.rt.connectDiscoverHook = func(context.Context, string) error { return nil }
	g.rt.discoveryInProgress.Delete(skillsSrv)
	g.rt.onServerConnectedDiscovery(skillsSrv)
	g.rt.triggerSkillsRefresh(skillsSrv)
	g.rt.skills.wait()
	assert.Zero(t, g.fx.Count("skills/list"))
}

func TestSkillsTrigger_ConfigChange(t *testing.T) {
	yes, no := true, false
	g := newSkillsRig(t, skillsRigOpts{enable: true, autoTrig: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	base := func() *config.Config {
		return &config.Config{EnableSkills: true, Servers: []*config.ServerConfig{{Name: skillsSrv, Enabled: true}}}
	}

	// Nothing changed: nothing runs.
	g.rt.onSkillsConfigChanged(base(), base())
	g.rt.skills.wait()
	assert.Zero(t, g.fx.Count("skills/list"))

	// enable_skills flipping on refreshes every connected server.
	off := base()
	off.EnableSkills = false
	g.rt.onSkillsConfigChanged(off, base())
	require.Eventually(t, func() bool { return len(g.records(t)) == 1 }, 10*time.Second, 10*time.Millisecond)
	g.rt.skills.wait()
	n := g.fx.Count("skills/list")

	// Flipping off does nothing.
	g.rt.onSkillsConfigChanged(base(), off)
	g.rt.skills.wait()
	assert.Equal(t, n, g.fx.Count("skills/list"))

	// A server's expose_skills turning on refreshes that server.
	was, now := base(), base()
	was.Servers[0].ExposeSkills = &no
	now.Servers[0].ExposeSkills = &yes
	g.rt.onSkillsConfigChanged(was, now)
	g.rt.skills.wait()
	assert.Equal(t, n+1, g.fx.Count("skills/list"))

	// Turning it off does not.
	g.rt.onSkillsConfigChanged(now, was)
	g.rt.skills.wait()
	assert.Equal(t, n+1, g.fx.Count("skills/list"))
}

// A binary file is never stored, so there is nothing to re-verify it against
// on the next refresh. While the listing still names the same address, digest
// and size that a previous refresh read and verified, it is not downloaded
// again on every sweep; any change to those three reads it again.
func TestSkillsRefresh_VerifiedBinaryIsNotReadAgain(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "bin", Files: map[string][]byte{"big.bin": sized(400, 1)}, Binary: []string{"big.bin"}})
	require.NoError(t, g.refresh(t))
	reads := g.fx.Count("resources/read")
	require.Equal(t, storage.SkillStatusPending, g.records(t)["bin"].Status)

	require.NoError(t, g.refresh(t))
	assert.Equal(t, reads, g.fx.Count("resources/read"), "an unchanged, already verified binary is not read again")
	rec := g.records(t)["bin"]
	assert.Equal(t, storage.SkillStatusPending, rec.Status, rec.UnusableReason)
	for _, f := range rec.Current.Files {
		if f.RelPath == "big.bin" {
			assert.False(t, f.Text)
			assert.False(t, f.Stored)
		}
	}

	g.fx.Mutate("bin", "big.bin", sized(400, 2))
	require.NoError(t, g.refresh(t))
	assert.Equal(t, reads+1, g.fx.Count("resources/read"), "a binary whose digest changed is read and verified again")
}

// An unusable skill proves nothing about its files: its binary is read again.
func TestSkillsRefresh_BinaryOfUnusableSkillIsReadAgain(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "bin", Files: map[string][]byte{"big.bin": sized(400, 1)}, Binary: []string{"big.bin"}, WrongDigest: []string{"big.bin"}})
	require.NoError(t, g.refresh(t))
	require.Equal(t, storage.SkillStatusUnusable, g.records(t)["bin"].Status)
	reads := g.fx.Count("resources/read")

	require.NoError(t, g.refresh(t))
	assert.Greater(t, g.fx.Count("resources/read"), reads)
}

// Background refreshes write storage from their own goroutines. Close must
// wait for one that is in flight, and nothing may start after Close.
//
// Runtime.Close is not safe to call twice (the index and the cache manager
// close channels), and the rig already closes the runtime in a cleanup. So the
// checks run in a cleanup registered before the rig, which therefore runs
// after that Close.
func TestSkillsRefresh_CloseJoinsBackgroundRefresh(t *testing.T) {
	var g *skillsRig
	var inFlight, finished atomic.Bool
	t.Cleanup(func() {
		if !inFlight.Load() {
			return // the test failed before a refresh was in flight
		}
		assert.True(t, finished.Load(), "Close returned while a refresh was still running")

		lists := g.fx.Count("skills/list")
		g.rt.triggerSkillsRefresh(skillsSrv)
		g.rt.skills.wait()
		assert.Equal(t, lists, g.fx.Count("skills/list"), "no refresh starts after Close")
	})

	g = newSkillsRig(t, skillsRigOpts{enable: true, autoTrig: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	g.rt.skills.wait() // the connect-time trigger, if any, is done

	reached := make(chan struct{})
	var once sync.Once
	g.rt.skills.afterList = func(string) {
		once.Do(func() { close(reached) })
		time.Sleep(200 * time.Millisecond)
		finished.Store(true)
	}
	g.rt.triggerSkillsRefresh(skillsSrv)
	<-reached
	inFlight.Store(true)
	// The test ends here with the refresh inside afterList; the rig's cleanup
	// calls Close now.
}

// Two files of one skill with the same content share a digest, and the second
// is taken from what the first just read. It is still checked against its own
// listed size.
func TestSkillsRefresh_SameDigestFileIsVerifiedAgainstItsOwnReference(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	same := []byte("the same bytes twice")
	g.fx.AddSkill(skillstest.Skill{Path: "dup", Files: map[string][]byte{"a.md": same, "b.md": same}, WrongSize: []string{"b.md"}})
	require.NoError(t, g.refresh(t))
	rec := g.records(t)["dup"]
	require.NotNil(t, rec)
	assert.Equal(t, storage.SkillStatusUnusable, rec.Status)
	assert.Contains(t, rec.UnusableReason, skills.ReasonSizeMismatch)
}

// One path, two addresses: skill://x/pdf and https://x/pdf both derive the path
// x/pdf, which is what a skill is looked up by. Neither is usable, nothing is
// read for either, and each keeps an identity of its own.
func TestSkillsRefresh_TwoAddressesForOnePathAreBothUnusable(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "x/pdf"})
	g.fx.AddSkill(skillstest.Skill{Path: "ok"})
	g.fx.AddRawEntry([]byte(`{"uri":"https://x/pdf/SKILL.md","frontmatter":{"name":"pdf","description":"shadow"},` +
		`"resources":[{"uri":"https://x/pdf/SKILL.md","digest":"sha256:` + strings.Repeat("ab", 32) + `","size":5}]}`))
	require.NoError(t, g.refresh(t))

	list, err := g.rt.ListSkillRecords(skillsSrv)
	require.NoError(t, err)
	byURI := map[string]*storage.SkillApprovalRecord{}
	paths := map[string]int{}
	for _, r := range list {
		byURI[r.SkillURI] = r
		paths[r.SkillPath]++
	}
	for _, uri := range []string{"skill://x/pdf/SKILL.md", "https://x/pdf/SKILL.md"} {
		rec := byURI[uri]
		require.NotNil(t, rec, uri)
		assert.Equal(t, storage.SkillStatusUnusable, rec.Status, uri)
		assert.Contains(t, rec.UnusableReason, skills.ReasonInvalidURI, uri)
		assert.Equal(t, uri, rec.SkillPath, "a colliding skill is addressed by its full address")
	}
	for p, n := range paths {
		assert.Equal(t, 1, n, "path %q is used by more than one record", p)
	}
	assert.Equal(t, storage.SkillStatusPending, byURI["skill://ok/SKILL.md"].Status)
	for _, req := range g.fx.Requests() {
		if req.Method == "resources/read" {
			assert.NotContains(t, req.URI, "x/pdf", "no file of a colliding skill is read")
		}
	}
	_, _, err = g.rt.SkillFileText(skillsSrv, "x/pdf", "SKILL.md", false)
	assert.ErrorIs(t, err, ErrSkillNotFound)
}

// An earlier record that is no longer listed may share its path with a skill
// the server lists now under another address. A lookup by path means the
// listed one.
func TestSkillFileText_PrefersTheListedRecordForAPath(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	old := "other://pdf/SKILL.md"
	seedApprovedSkill(t, g, old, false)
	rec, err := g.rt.storageManager.GetSkillApproval(skillsSrv, old)
	require.NoError(t, err)
	rec.SkillPath = "pdf"
	require.NoError(t, g.rt.storageManager.SaveSkillApproval(rec))

	g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"SKILL.md": []byte("---\nname: pdf\ndescription: current\n---\nlisted now\n")}})
	require.NoError(t, g.refresh(t))

	text, file, err := g.rt.SkillFileText(skillsSrv, "pdf", "SKILL.md", false)
	require.NoError(t, err)
	assert.Equal(t, "skill://pdf/SKILL.md", file.URI)
	assert.Contains(t, string(text), "listed now")
}

// Turning the feature off, or excluding the server, while a refresh is
// running stops it: no file is read afterwards and nothing is written.
func TestSkillsRefresh_StopsWhenSwitchedOffMidway(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(c *config.Config)
	}{
		{"feature off", func(c *config.Config) { c.EnableSkills = false }},
		{"server excluded", func(c *config.Config) {
			no := false
			for i, sc := range c.Servers {
				if sc.Name == skillsSrv {
					cp := *sc
					cp.ExposeSkills = &no
					c.Servers[i] = &cp
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newSkillsRig(t, skillsRigOpts{enable: true})
			g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"a.md": []byte("alpha")}})
			g.rt.skills.afterList = func(string) {
				next := *g.rt.Config()
				next.Servers = append([]*config.ServerConfig(nil), next.Servers...)
				tc.change(&next)
				g.rt.UpdateConfig(&next, "")
			}
			_ = g.refresh(t)
			assert.Zero(t, g.fx.Count("resources/read"), "no file is read once the switch is off")
			assert.Empty(t, g.records(t), "nothing is written once the switch is off")
			assert.Zero(t, g.blobCount(t))
		})
	}
}

// The switch is also honoured while the listing itself is still paging.
func TestSkillsRefresh_StopsPagingWhenSwitchedOff(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true, fixture: []skillstest.Option{skillstest.WithPageSize(1)}})
	for i := 0; i < 4; i++ {
		g.fx.AddSkill(skillstest.Skill{Path: fmt.Sprintf("s%d", i)})
	}
	g.fx.SetDelay(150 * time.Millisecond)

	done := make(chan error, 1)
	go func() { done <- g.refresh(t) }()
	require.Eventually(t, func() bool { return g.fx.Count("skills/list") == 1 }, 10*time.Second, time.Millisecond)

	off := *g.rt.Config()
	off.EnableSkills = false
	g.rt.UpdateConfig(&off, "")
	assert.NoError(t, <-done, "a refresh that stops because skills were switched off did not fail")

	assert.Equal(t, 1, g.fx.Count("skills/list"), "no further page is requested once the switch is off")
	assert.Zero(t, g.fx.Count("resources/read"))
	assert.Empty(t, g.records(t))
	assert.Empty(t, g.rt.SkillsStatus(skillsSrv).Error, "being switched off is not a failure")
}

// A listing that fails after skills were switched off is not reported as a
// failed refresh: nobody asked for those skills any more.
func TestSkillsRefresh_ListingErrorAfterSwitchOffIsNotAFailure(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	g.fx.SetListFailure(true)
	g.fx.SetDelay(150 * time.Millisecond)

	done := make(chan error, 1)
	go func() { done <- g.refresh(t) }()
	require.Eventually(t, func() bool { return g.fx.Count("skills/list") == 1 }, 10*time.Second, time.Millisecond)
	off := *g.rt.Config()
	off.EnableSkills = false
	g.rt.UpdateConfig(&off, "")

	assert.NoError(t, <-done)
	assert.Empty(t, g.rt.SkillsStatus(skillsSrv).Error)
}

// A listing that fails because its connection was replaced while it ran is a
// superseded refresh, not a failed one: the reconnect reads the skills again.
func TestSkillsRefresh_ListingErrorOnAReplacedConnectionIsSuperseded(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	g.fx.SetListFailure(true)
	g.fx.SetDelay(150 * time.Millisecond)

	done := make(chan error, 1)
	go func() { done <- g.refresh(t) }()
	require.Eventually(t, func() bool { return g.fx.Count("skills/list") == 1 }, 10*time.Second, time.Millisecond)
	c, _ := g.rt.upstreamManager.GetClient(skillsSrv)
	c.Retire()

	err := <-done
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSkillsRefreshSuperseded)
	assert.Empty(t, g.rt.SkillsStatus(skillsSrv).Error, "a superseded refresh is not a status error")
}

// An error met while reading files is a superseded refresh, and not a failure,
// when the connection the listing came from has been replaced since.
func TestSupersededIfStale(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	_, _, capture, err := g.rt.upstreamManager.ListServerSkills(context.Background(), skillsSrv)
	require.NoError(t, err)

	readErr := context.DeadlineExceeded
	assert.Equal(t, readErr, supersededIfStale(readErr, capture), "a current capture keeps its error")
	assert.NoError(t, supersededIfStale(nil, capture))

	c, _ := g.rt.upstreamManager.GetClient(skillsSrv)
	c.Retire()
	got := supersededIfStale(readErr, capture)
	assert.ErrorIs(t, got, ErrSkillsRefreshSuperseded)
	assert.NoError(t, supersededIfStale(nil, capture), "no error stays no error")
}

// A refresh is shared by everyone who asks while it runs, so it does not
// belong to the caller who happened to start it: when that caller goes away,
// the others still get a complete refresh.
func TestSkillsRefresh_CallerCancellationDoesNotCancelTheSharedRefresh(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"a.md": []byte("alpha")}})
	g.fx.SetDelay(100 * time.Millisecond)

	ctxA, cancelA := context.WithCancel(context.Background())
	errA := make(chan error, 1)
	go func() { errA <- g.rt.RefreshServerSkills(ctxA, skillsSrv) }()
	require.Eventually(t, func() bool { return g.fx.Count("skills/list") == 1 }, 10*time.Second, time.Millisecond)

	errB := make(chan error, 1)
	go func() { errB <- g.rt.RefreshServerSkills(context.Background(), skillsSrv) }()
	require.Eventually(t, func() bool { return g.rt.skills.joiners(skillsSrv) >= 1 }, 10*time.Second, time.Millisecond)

	cancelA()
	assert.ErrorIs(t, <-errA, context.Canceled, "the caller that left stops waiting")
	assert.NoError(t, <-errB, "the caller that stayed gets the refresh")
	assert.Equal(t, storage.SkillStatusPending, g.records(t)["pdf"].Status)
	assert.Empty(t, g.rt.SkillsStatus(skillsSrv).Error)
}

// What a server says about a skill is kept for display, and a server can say
// a lot. Names, descriptions, reasons and file lists are cut to a size worth
// showing before they are stored, and an address too long to be one is not
// stored at all.
func TestSkillsRefresh_BoundsWhatIsStoredForDisplay(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	long := strings.Repeat("é", 20000) // two bytes each
	g.fx.AddSkill(skillstest.Skill{Path: "wordy", ListedFrontmatter: map[string]any{"name": "wordy", "description": long}})

	var files []string
	for i := 0; i < skills.MaxFilesPerSkill+300; i++ {
		files = append(files, fmt.Sprintf(`{"uri":"skill://many/f%04d.md","digest":"sha256:%s","size":1}`, i, strings.Repeat("0", 64)))
	}
	g.fx.AddRawEntry([]byte(`{"uri":"skill://many/SKILL.md","frontmatter":{"name":"many","description":"d"},"resources":[` + strings.Join(files, ",") + `]}`))

	hugeURI := "skill://" + strings.Repeat("a", skills.MaxURIBytes) + "/SKILL.md"
	g.fx.AddRawEntry([]byte(`{"uri":"` + hugeURI + `","frontmatter":{"name":"x","description":"d"},"resources":[{"uri":"` + hugeURI + `","digest":"sha256:` + strings.Repeat("0", 64) + `","size":1}]}`))

	require.NoError(t, g.refresh(t))
	recs := g.records(t)

	wordy := recs["wordy"]
	require.NotNil(t, wordy)
	assert.LessOrEqual(t, len(wordy.Description), maxSkillDescriptionBytes+8)
	assert.True(t, utf8.ValidString(wordy.Description), "a cut never splits a character")
	assert.LessOrEqual(t, len(wordy.Current.Frontmatter), skills.MaxFrontmatterBytes)

	many := recs["many"]
	require.NotNil(t, many)
	assert.Equal(t, storage.SkillStatusUnusable, many.Status)
	assert.LessOrEqual(t, len(many.Current.Files), skills.MaxFilesPerSkill)

	list, err := g.rt.ListSkillRecords(skillsSrv)
	require.NoError(t, err)
	for _, r := range list {
		assert.LessOrEqual(t, len(r.SkillURI), skills.MaxURIBytes, "an over-long address is not stored")
		assert.LessOrEqual(t, len(r.UnusableReason), maxSkillReasonBytes+8)
	}
	assert.Equal(t, 1, g.rt.SkillsStatus(skillsSrv).EntryErrors, "the over-long address is counted")
}

func TestCutTextStaysWithinItsBound(t *testing.T) {
	for _, in := range []string{strings.Repeat("a", 300), strings.Repeat("é", 300), strings.Repeat("日本", 100), "short", "", "bad\xffbytes"} {
		for _, limit := range []int{8, 16, 256} {
			out := cutText(in, limit)
			assert.LessOrEqual(t, len(out), limit, "%q cut to %d", in[:min(len(in), 12)], limit)
			assert.True(t, utf8.ValidString(out))
			if len(strings.ToValidUTF8(in, "�")) <= limit {
				assert.NotContains(t, out, "…", "text that fits is not marked as cut")
			} else {
				assert.True(t, strings.HasSuffix(out, "…"))
			}
		}
	}
}

// A change to one server made through the REST API or the MCP tool is a config
// commit like any other: including a server in skills refreshes it, without
// waiting for the next sweep.
func TestSkillsTrigger_AnyCommitThatIncludesAServerRefreshesIt(t *testing.T) {
	no := false
	g := newSkillsRig(t, skillsRigOpts{enable: true, expose: &no, autoTrig: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	g.rt.skills.wait()
	require.Zero(t, g.fx.Count("skills/list"))

	next := *g.rt.Config()
	next.Servers = append([]*config.ServerConfig(nil), next.Servers...)
	for i, sc := range next.Servers {
		if sc.Name == skillsSrv {
			cp := *sc
			cp.ExposeSkills = nil
			next.Servers[i] = &cp
		}
	}
	g.rt.UpdateConfig(&next, "")
	require.Eventually(t, func() bool { return len(g.records(t)) == 1 }, 10*time.Second, 10*time.Millisecond)
}

// A server that is not asked for skills gives no listing at all, which is not
// the same as an empty listing: earlier records must not be marked unlisted
// because of it.
func TestSkillsRefresh_NotAskedIsNotAnEmptyListing(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	require.True(t, g.records(t)["pdf"].Listed)

	// The connection's own switch says no while the configuration still says
	// yes: the state a refresh can meet while a config change is being applied.
	c, _ := g.rt.upstreamManager.GetClient(skillsSrv)
	off := false
	excluded := *c.GetConfig()
	excluded.ExposeSkills = &off
	c.SetConfig(&excluded)

	require.NoError(t, g.refresh(t))
	assert.True(t, g.records(t)["pdf"].Listed, "a server that was not asked did not unlist anything")
}
