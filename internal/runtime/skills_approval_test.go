package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/skillstest"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// skillEvents collects the skill-related events a runtime publishes.
type skillEvents struct {
	ch chan Event
	rt *Runtime
}

func watchSkillEvents(t *testing.T, rt *Runtime) *skillEvents {
	t.Helper()
	ch := rt.SubscribeEvents()
	t.Cleanup(func() { rt.UnsubscribeEvents(ch) })
	return &skillEvents{ch: ch, rt: rt}
}

// drain returns what has been published so far, in order.
func (e *skillEvents) drain() (activity []Event, changed []Event) {
	time.Sleep(50 * time.Millisecond)
	for {
		select {
		case evt := <-e.ch:
			switch evt.Type {
			case EventTypeActivitySkillQuarantineChange:
				activity = append(activity, evt)
			case EventTypeSkillsChanged:
				changed = append(changed, evt)
			}
		default:
			return
		}
	}
}

func hashes(t *testing.T, g *skillsRig, paths ...string) map[string]string {
	t.Helper()
	recs := g.records(t)
	out := map[string]string{}
	for _, p := range paths {
		require.NotNil(t, recs[p], p)
		out[p] = recs[p].CurrentHash
	}
	return out
}

func adminCtx() context.Context {
	return auth.WithAuthContext(context.Background(), auth.AdminContext())
}

func TestApproveSkillsReviewedAt_ApprovesAndRecords(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"a.md": []byte("alpha")}})
	g.fx.AddSkill(skillstest.Skill{Path: "doc"})
	require.NoError(t, g.refresh(t))
	ev := watchSkillEvents(t, g.rt)
	h := hashes(t, g, "pdf", "doc")

	n, err := g.rt.ApproveSkillsReviewedAt(adminCtx(), skillsSrv, []string{"pdf", "pdf", "doc"}, h, "api", 0)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "duplicates are named once")
	for _, p := range []string{"pdf", "doc"} {
		rec := g.records(t)[p]
		assert.Equal(t, storage.SkillStatusApproved, rec.Status, p)
		assert.Equal(t, rec.CurrentHash, rec.ApprovedHash)
		require.NotNil(t, rec.Approved)
		assert.Equal(t, rec.Current.Files, rec.Approved.Files)
		assert.Equal(t, "api", rec.ApprovedBy)
		assert.False(t, rec.ApprovedAt.IsZero())
		assert.True(t, g.rt.SkillAvailable(skillsSrv, rec))
	}
	activity, changed := ev.drain()
	require.Len(t, activity, 2)
	assert.Equal(t, "approved", activity[0].Payload["action"])
	assert.Equal(t, "api_key", activity[0].Payload["actor_kind"], "the caller rides in ctx")
	assert.Equal(t, g.records(t)["pdf"].CurrentHash, activity[0].Payload["current_hash"])
	require.Len(t, changed, 1)
	counts := changed[0].Payload["counts"].(map[string]int)
	assert.Equal(t, 2, counts["available"])

	// Already approved for the current content: untouched, not counted, silent.
	n, err = g.rt.ApproveSkillsReviewedAt(adminCtx(), skillsSrv, []string{"pdf"}, h, "api", 0)
	require.NoError(t, err)
	assert.Zero(t, n)
	activity, changed = ev.drain()
	assert.Empty(t, activity)
	assert.Empty(t, changed)
}

func TestApproveSkillsReviewedAt_RefusalsWriteNothing(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	g.fx.AddSkill(skillstest.Skill{Path: "doc"})
	g.fx.AddSkill(skillstest.Skill{Path: "wd", Files: map[string][]byte{"a.md": []byte("a")}, WrongDigest: []string{"a.md"}})
	g.fx.AddSkill(skillstest.Skill{Path: "dyn", Dynamic: true})
	require.NoError(t, g.refresh(t))
	h := hashes(t, g, "pdf", "doc")
	ev := watchSkillEvents(t, g.rt)
	untouched := func() {
		t.Helper()
		for _, p := range []string{"pdf", "doc"} {
			assert.Equal(t, storage.SkillStatusPending, g.records(t)[p].Status, p)
			assert.Nil(t, g.records(t)[p].Approved)
		}
		activity, changed := ev.drain()
		assert.Empty(t, activity)
		assert.Empty(t, changed)
	}

	// Stale: the hash differs.
	_, err := g.rt.ApproveSkillsReviewedAt(adminCtx(), skillsSrv, []string{"pdf", "doc"}, map[string]string{"pdf": h["pdf"], "doc": "sk1-stale"}, "api", 0)
	var stale *StaleSkillReviewError
	require.True(t, errors.As(err, &stale))
	assert.Equal(t, []string{"doc"}, stale.Changed)
	untouched()

	// Unreviewed: no hash for a named skill.
	_, err = g.rt.ApproveSkillsReviewedAt(adminCtx(), skillsSrv, []string{"pdf", "doc"}, map[string]string{"pdf": h["pdf"]}, "api", 0)
	require.True(t, errors.As(err, &stale))
	assert.Equal(t, []string{"doc"}, stale.Unreviewed)
	untouched()

	// Unusable: a failed check, and a dynamic listing.
	for _, bad := range []string{"wd", "dyn"} {
		hh := map[string]string{"pdf": h["pdf"], bad: g.records(t)[bad].CurrentHash}
		_, err = g.rt.ApproveSkillsReviewedAt(adminCtx(), skillsSrv, []string{"pdf", bad}, hh, "api", 0)
		var unusable *SkillsUnusableError
		require.True(t, errors.As(err, &unusable), bad)
		assert.Equal(t, []string{bad}, unusable.Skills)
		assert.Equal(t, storage.SkillStatusUnusable, g.records(t)[bad].Status)
		untouched()
	}

	// Unknown skill: refused, and the known one named with it is not written.
	_, err = g.rt.ApproveSkillsReviewedAt(adminCtx(), skillsSrv, []string{"pdf", "nope"}, map[string]string{"pdf": h["pdf"], "nope": "x"}, "api", 0)
	require.ErrorIs(t, err, ErrSkillNotFound)
	assert.Contains(t, err.Error(), "nope")
	untouched()
}

func TestApproveSkillsReviewedAt_ReplacedServer(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	h := hashes(t, g, "pdf")
	old, ok := g.rt.ConfigSnapshot().ServerGeneration(skillsSrv)
	require.True(t, ok)

	removeAndReAdd(t, g.rt, manualStdio(skillsSrv, false))

	_, err := g.rt.ApproveSkillsReviewedAt(adminCtx(), skillsSrv, []string{"pdf"}, h, "api", old.Incarnation)
	require.ErrorIs(t, err, commitview.ErrServerReplaced)
	_, err = g.rt.SetSkillsBlockedAt(adminCtx(), skillsSrv, []string{"pdf"}, true, "api", old.Incarnation)
	require.ErrorIs(t, err, commitview.ErrServerReplaced)
}

func TestSkillsApproval_ApprovedCopyIsRetainedWhenContentChanges(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"a.md": []byte("original")}})
	require.NoError(t, g.refresh(t))
	_, err := g.rt.ApproveSkillsReviewedAt(adminCtx(), skillsSrv, []string{"pdf"}, hashes(t, g, "pdf"), "api", 0)
	require.NoError(t, err)
	approvedHash := g.records(t)["pdf"].ApprovedHash
	ev := watchSkillEvents(t, g.rt)

	g.fx.Mutate("pdf", "a.md", []byte("rewritten"))
	require.NoError(t, g.refresh(t))
	rec := g.records(t)["pdf"]
	assert.Equal(t, storage.SkillStatusChanged, rec.Status)
	assert.Equal(t, approvedHash, rec.ApprovedHash)
	assert.NotEqual(t, rec.CurrentHash, rec.ApprovedHash)
	require.NotNil(t, rec.Approved)
	assert.False(t, g.rt.SkillAvailable(skillsSrv, rec), "a changed skill is not served")

	old, _, err := g.rt.SkillFileText(skillsSrv, "pdf", "a.md", true)
	require.NoError(t, err)
	assert.Equal(t, "original", string(old))
	cur, _, err := g.rt.SkillFileText(skillsSrv, "pdf", "a.md", false)
	require.NoError(t, err)
	assert.Equal(t, "rewritten", string(cur))

	activity, changed := ev.drain()
	require.Len(t, activity, 1)
	assert.Equal(t, "held_changed", activity[0].Payload["action"])
	assert.Equal(t, approvedHash, activity[0].Payload["previous_hash"])
	assert.Equal(t, "system", activity[0].Payload["actor_kind"])
	assert.NotEmpty(t, changed)

	// A second refresh of the same held content says nothing more.
	require.NoError(t, g.refresh(t))
	activity, _ = ev.drain()
	assert.Empty(t, activity)

	// The content returns to the approved copy: approved again, one "restored".
	g.fx.Mutate("pdf", "a.md", []byte("original"))
	require.NoError(t, g.refresh(t))
	assert.Equal(t, storage.SkillStatusApproved, g.records(t)["pdf"].Status)
	activity, _ = ev.drain()
	require.Len(t, activity, 1)
	assert.Equal(t, "restored", activity[0].Payload["action"])
}

func TestSkillsApproval_BlockSurvivesRefreshChangeAndAutoApproval(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"a.md": []byte("one")}})
	require.NoError(t, g.refresh(t))
	_, err := g.rt.ApproveSkillsReviewedAt(adminCtx(), skillsSrv, []string{"pdf"}, hashes(t, g, "pdf"), "api", 0)
	require.NoError(t, err)
	require.True(t, g.rt.SkillAvailable(skillsSrv, g.records(t)["pdf"]))
	ev := watchSkillEvents(t, g.rt)

	n, err := g.rt.SetSkillsBlockedAt(adminCtx(), skillsSrv, []string{"pdf", "pdf"}, true, "ops", 0)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	rec := g.records(t)["pdf"]
	assert.True(t, rec.Blocked)
	assert.Equal(t, storage.SkillStatusApproved, rec.Status, "blocking is not a status")
	assert.False(t, g.rt.SkillAvailable(skillsSrv, rec))
	activity, changed := ev.drain()
	require.Len(t, activity, 1)
	assert.Equal(t, "blocked", activity[0].Payload["action"])
	assert.Equal(t, "api_key", activity[0].Payload["actor_kind"])
	require.Len(t, changed, 1)
	assert.Equal(t, 1, changed[0].Payload["counts"].(map[string]int)["blocked"])

	// Blocking again changes nothing and says nothing.
	n, err = g.rt.SetSkillsBlockedAt(adminCtx(), skillsSrv, []string{"pdf"}, true, "ops", 0)
	require.NoError(t, err)
	assert.Zero(t, n)
	activity, _ = ev.drain()
	assert.Empty(t, activity)

	// A refresh, a content change and an auto-mode approval all leave it set.
	require.NoError(t, g.refresh(t))
	assert.True(t, g.records(t)["pdf"].Blocked)
	g.fx.Mutate("pdf", "a.md", []byte("two"))
	require.NoError(t, g.refresh(t))
	assert.True(t, g.records(t)["pdf"].Blocked)
	setTrustMode(t, g, "auto")
	require.NoError(t, g.refresh(t))
	rec = g.records(t)["pdf"]
	assert.Equal(t, storage.SkillStatusApproved, rec.Status, "auto mode approves the new content")
	assert.Equal(t, "auto", rec.ApprovedBy)
	assert.True(t, rec.Blocked, "...and the block survives it")
	assert.False(t, g.rt.SkillAvailable(skillsSrv, rec))

	// Unblocking restores availability with no new review.
	n, err = g.rt.SetSkillsBlockedAt(adminCtx(), skillsSrv, []string{"pdf"}, false, "ops", 0)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	rec = g.records(t)["pdf"]
	assert.False(t, rec.Blocked)
	assert.True(t, g.rt.SkillAvailable(skillsSrv, rec))
	assert.Equal(t, "auto", rec.ApprovedBy, "unblocking is not an approval")

	// A skill nobody has a record for is refused and nothing else is written.
	_, err = g.rt.SetSkillsBlockedAt(adminCtx(), skillsSrv, []string{"pdf", "nope"}, true, "ops", 0)
	require.ErrorIs(t, err, ErrSkillNotFound)
	assert.False(t, g.records(t)["pdf"].Blocked)
}

// setTrustMode changes the server's trust mode in the live configuration.
func setTrustMode(t *testing.T, g *skillsRig, mode string) {
	t.Helper()
	_, err := g.rt.Commit("test_trust_mode", func(tx *CommitTx) error {
		return tx.UpdateServer(skillsSrv, func(sc *config.ServerConfig) error {
			sc.TrustMode = mode
			return nil
		})
	})
	require.NoError(t, err)
	g.rt.skills.wait()
}

func TestSkillsRefresh_TrustModeDecidesNewAndChangedSkills(t *testing.T) {
	clean := func(n *atomic.Int32) skillScanFunc { return countingScan(n, "clean", true) }
	dirty := func(string, string, string) (string, []scanner.ScanFinding, bool) {
		return "warnings", []scanner.ScanFinding{{RuleID: "x", Severity: "low"}}, true
	}
	cases := []struct {
		trust       string
		scan        func(*atomic.Int32) skillScanFunc
		wantNew     string
		wantBy      string
		wantChanged string
	}{
		{"auto", clean, storage.SkillStatusApproved, "auto", storage.SkillStatusApproved},
		{"scan", clean, storage.SkillStatusApproved, "scan", storage.SkillStatusChanged},
		{"scan", func(*atomic.Int32) skillScanFunc { return dirty }, storage.SkillStatusPending, "", storage.SkillStatusPending},
		{"manual", clean, storage.SkillStatusPending, "", storage.SkillStatusPending},
	}
	for _, c := range cases {
		name := c.trust
		if c.wantNew == storage.SkillStatusPending && c.trust == "scan" {
			name = "scan-dirty"
		}
		t.Run(name, func(t *testing.T) {
			g := newSkillsRig(t, skillsRigOpts{enable: true, trust: c.trust})
			var n atomic.Int32
			g.rt.skills.scanFunc = c.scan(&n)
			ev := watchSkillEvents(t, g.rt)
			g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"a.md": []byte("one")}})
			require.NoError(t, g.refresh(t))
			rec := g.records(t)["pdf"]
			assert.Equal(t, c.wantNew, rec.Status)
			assert.Equal(t, c.wantBy, rec.ApprovedBy)
			if c.wantBy != "" {
				assert.Equal(t, rec.CurrentHash, rec.ApprovedHash)
				activity, _ := ev.drain()
				require.Len(t, activity, 1)
				assert.Equal(t, "auto_approved", activity[0].Payload["action"])
				assert.Equal(t, c.wantBy, activity[0].Payload["approved_by"])
				assert.Equal(t, "system", activity[0].Payload["actor_kind"])
			}

			g.fx.Mutate("pdf", "a.md", []byte("two"))
			require.NoError(t, g.refresh(t))
			rec = g.records(t)["pdf"]
			assert.Equal(t, c.wantChanged, rec.Status)
			if c.wantChanged == storage.SkillStatusApproved {
				assert.Equal(t, rec.CurrentHash, rec.ApprovedHash)
			}
		})
	}
}

func TestSkillsRefresh_TrustModeChangeRefreshesAndApprovesPending(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true, trust: "manual", autoTrig: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	assert.Equal(t, storage.SkillStatusPending, g.records(t)["pdf"].Status)

	setTrustMode(t, g, "auto")
	require.Eventually(t, func() bool { return g.records(t)["pdf"].Status == storage.SkillStatusApproved }, 10*time.Second, 20*time.Millisecond)
}

func TestSkillsConfigChange_TriggersTrustQuarantineAndEligibility(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true, autoTrig: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	mk := func(mut func(*config.Config, *config.ServerConfig)) *config.Config {
		sc := &config.ServerConfig{Name: skillsSrv, Enabled: true}
		c := &config.Config{EnableSkills: true, Servers: []*config.ServerConfig{sc}}
		if mut != nil {
			mut(c, sc)
		}
		return c
	}
	lists := func() int { g.rt.skills.wait(); return g.fx.Count("skills/list") }
	base := lists()

	g.rt.onSkillsConfigChanged(mk(nil), mk(nil))
	assert.Equal(t, base, lists(), "nothing changed")

	// A server leaving quarantine, and one being enabled, is refreshed.
	g.rt.onSkillsConfigChanged(mk(func(_ *config.Config, s *config.ServerConfig) { s.Quarantined = true }), mk(nil))
	assert.Equal(t, base+1, lists(), "left quarantine")
	g.rt.onSkillsConfigChanged(mk(func(_ *config.Config, s *config.ServerConfig) { s.Enabled = false }), mk(nil))
	assert.Equal(t, base+2, lists(), "enabled")

	// Entering quarantine or being disabled is not.
	g.rt.onSkillsConfigChanged(mk(nil), mk(func(_ *config.Config, s *config.ServerConfig) { s.Quarantined = true }))
	g.rt.onSkillsConfigChanged(mk(nil), mk(func(_ *config.Config, s *config.ServerConfig) { s.Enabled = false }))
	assert.Equal(t, base+2, lists())

	// An effective trust-mode change.
	g.rt.onSkillsConfigChanged(mk(func(_ *config.Config, s *config.ServerConfig) { s.TrustMode = "manual" }),
		mk(func(_ *config.Config, s *config.ServerConfig) { s.TrustMode = "auto" }))
	assert.Equal(t, base+3, lists(), "trust mode changed")
	// ...but spelling the default out is not a change.
	g.rt.onSkillsConfigChanged(mk(nil), mk(func(_ *config.Config, s *config.ServerConfig) { s.TrustMode = "scan" }))
	assert.Equal(t, base+3, lists())

	// quarantine_enabled flipping refreshes every eligible server.
	off := false
	g.rt.onSkillsConfigChanged(mk(nil), mk(func(c *config.Config, _ *config.ServerConfig) { c.QuarantineEnabled = &off }))
	assert.Equal(t, base+4, lists(), "quarantine_enabled changed")
}

func TestActivityService_SkillQuarantineChangeRecord(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	svc := NewActivityService(store, zap.NewNop())

	svc.handleEvent(Event{
		Type:      EventTypeActivitySkillQuarantineChange,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "acme", "skill": "pdf", "skill_uri": "skill://pdf/SKILL.md", "action": "held_changed",
			"previous_hash": "sk1-a", "current_hash": "sk1-b", "reason": "", "approved_by": "",
			"actor_kind": "system", "request_id": "req-1",
		},
	})
	rec := onlyRecord(t, store)
	assert.Equal(t, storage.ActivityTypeSkillQuarantineChange, rec.Type)
	assert.Equal(t, "acme", rec.ServerName)
	assert.Equal(t, "held_changed", rec.Status)
	assert.Equal(t, "req-1", rec.RequestID)
	assert.Equal(t, "pdf", rec.Metadata["skill"])
	assert.Equal(t, "skill://pdf/SKILL.md", rec.Metadata["skill_uri"])
	assert.Equal(t, "sk1-a", rec.Metadata["previous_hash"])
	assert.Equal(t, "sk1-b", rec.Metadata["current_hash"])
	assert.Equal(t, "system", rec.Metadata["actor_kind"])
	assert.Contains(t, storage.ValidActivityTypes, "skill_quarantine_change")
	assert.True(t, storage.IsAdminOnlyActivityType(rec.Type))
}
