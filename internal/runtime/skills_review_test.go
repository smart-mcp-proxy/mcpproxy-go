package runtime

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 T031/T045: held skills (pending or changed, not blocked, listed,
// on a server whose skills are read) are owed a review. They show up in the
// review queue and the attention list; who may see them is decided at the
// REST layer (skillVisibleTo).

func saveHeldSkill(t *testing.T, rt *Runtime, server, path, status string, mut ...func(*storage.SkillApprovalRecord)) {
	t.Helper()
	rec := &storage.SkillApprovalRecord{
		ServerName: server, SkillURI: "skill://" + path + "/SKILL.md", SkillPath: path, Name: path,
		Status: status, Listed: true, CurrentHash: "sk1-" + path,
		FirstSeen: time.Now().UTC().Add(-time.Hour), UpdatedAt: time.Now().UTC(),
	}
	for _, m := range mut {
		m(rec)
	}
	require.NoError(t, rt.storageManager.SaveSkillApproval(rec))
}

func heldSkillsRuntime(t *testing.T, enable bool) *Runtime {
	t.Helper()
	off := false
	srv := func(name string, enabled, quarantined bool, expose *bool) *config.ServerConfig {
		return explicitServer(&config.ServerConfig{
			Name: name, URL: "http://127.0.0.1:9/mcp", Protocol: "http",
			Enabled: enabled, Quarantined: quarantined, ExposeSkills: expose,
		})
	}
	rt, _ := newSpec116Runtime(t, func(c *config.Config) { c.EnableSkills = enable },
		srv("both", true, false, nil),
		srv("skills-only", true, false, nil),
		srv("quarantined", true, true, nil),
		srv("excluded", true, false, &off),
		srv("parked", false, false, nil),
		srv("settled", true, false, nil),
	)

	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "both", ToolName: "t", Status: storage.ToolApprovalStatusPending, PendingSince: time.Now().UTC(),
	}))
	saveHeldSkill(t, rt, "both", "a", storage.SkillStatusChanged)

	saveHeldSkill(t, rt, "skills-only", "p1", storage.SkillStatusPending)
	saveHeldSkill(t, rt, "skills-only", "p2", storage.SkillStatusPending)
	saveHeldSkill(t, rt, "skills-only", "c1", storage.SkillStatusChanged)
	saveHeldSkill(t, rt, "skills-only", "ok", storage.SkillStatusApproved)
	saveHeldSkill(t, rt, "skills-only", "bad", storage.SkillStatusUnusable)
	saveHeldSkill(t, rt, "skills-only", "blocked", storage.SkillStatusPending, func(r *storage.SkillApprovalRecord) { r.Blocked = true })
	saveHeldSkill(t, rt, "skills-only", "gone", storage.SkillStatusChanged, func(r *storage.SkillApprovalRecord) { r.Listed = false })

	saveHeldSkill(t, rt, "quarantined", "q", storage.SkillStatusPending)
	saveHeldSkill(t, rt, "excluded", "x", storage.SkillStatusPending)
	saveHeldSkill(t, rt, "parked", "y", storage.SkillStatusPending)
	saveHeldSkill(t, rt, "settled", "z", storage.SkillStatusApproved)
	return rt
}

func reviewRowsByName(t *testing.T, rt *Runtime) (*ReviewQueue, map[string]ReviewQueueRow) {
	t.Helper()
	queue, err := rt.GetReviewQueue(context.Background())
	require.NoError(t, err)
	byName := map[string]ReviewQueueRow{}
	for _, row := range queue.Servers {
		byName[row.Server] = row
	}
	return queue, byName
}

func TestReviewQueue_HeldSkillsCountAndGetTheirOwnRow(t *testing.T) {
	rt := heldSkillsRuntime(t, true)
	queue, rows := reviewRowsByName(t, rt)

	both := rows["both"]
	assert.Equal(t, "tool_review", both.Kind, "a server with held tools keeps its kind")
	assert.Equal(t, 1, both.Pending)
	assert.Equal(t, 0, both.SkillsPending)
	assert.Equal(t, 1, both.SkillsChanged)

	only, ok := rows["skills-only"]
	require.True(t, ok, "a server with only held skills gets a row")
	assert.Equal(t, ReviewKindSkillReview, only.Kind)
	assert.Equal(t, 2, only.SkillsPending, "blocked and unlisted skills are not owed a review")
	assert.Equal(t, 1, only.SkillsChanged)
	assert.Zero(t, only.Pending+only.Changed)
	assert.True(t, only.Enabled)
	assert.NotNil(t, only.Since)

	q := rows["quarantined"]
	assert.Equal(t, "server_review", q.Kind)
	assert.Zero(t, q.SkillsPending+q.SkillsChanged, "a quarantined server's skills are not read")

	for _, name := range []string{"excluded", "parked", "settled"} {
		_, present := rows[name]
		assert.Falsef(t, present, "%s has no skill owed a review", name)
	}
	assert.Equal(t, 3, queue.Count)
	assert.Len(t, queue.Servers, 3)
}

func TestReviewQueue_HeldSkillsIgnoredWithTheFeatureOff(t *testing.T) {
	rt := heldSkillsRuntime(t, false)
	queue, rows := reviewRowsByName(t, rt)
	_, present := rows["skills-only"]
	assert.False(t, present)
	assert.Zero(t, rows["both"].SkillsChanged)
	assert.Equal(t, 2, queue.Count)
}

// Review round 1, finding 3: a skill records read that fails must not fail
// the whole queue (a 500 for every caller would let a scoped caller observe
// it). The skill counts of that server read as zero; tool rows are intact.
func TestReviewQueue_ASkillsReadFailureLeavesTheToolRows(t *testing.T) {
	rt := heldSkillsRuntime(t, true)
	require.NoError(t, rt.storageManager.GetDB().Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(storage.SkillApprovalBucket)).Put([]byte("both:skill://broken/SKILL.md"), []byte("{not json"))
	}))
	queue, rows := reviewRowsByName(t, rt)
	both, ok := rows["both"]
	require.True(t, ok, "the tool row stays")
	assert.Equal(t, "tool_review", both.Kind)
	assert.Equal(t, 1, both.Pending)
	assert.Zero(t, both.SkillsPending+both.SkillsChanged, "a failed read counts no skills")
	assert.Equal(t, ReviewKindSkillReview, rows["skills-only"].Kind, "other servers are unaffected")
	assert.Equal(t, 3, queue.Count)
}

// Review round 2, finding 2: a count change keeps the item ids (approving one
// of three pending skills leaves skill_review:server:<name>:pending) but must
// still publish attention.changed, with a content fingerprint that moved, so
// administrators refetch the new count.
func TestAttentionSubscriber_ACountChangeWithStableIDsIsPublished(t *testing.T) {
	rt := heldSkillsRuntime(t, true)
	a := newAttentionSubscriber(rt, time.Millisecond)
	a.clientSource = nil
	a.servers = []contracts.Server{contractsServerForAttention(skillsServerConfig(rt.Config(), "skills-only"))}
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)

	next := func() map[string]any {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case evt := <-ch:
				if evt.Type == EventTypeAttentionChanged {
					return evt.Payload
				}
			case <-deadline:
				t.Fatal("no attention.changed")
				return nil
			}
		}
	}
	a.recompute()
	first := next()
	saveHeldSkill(t, rt, "skills-only", "p3", storage.SkillStatusPending)
	a.recompute()
	second := next()
	assert.Equal(t, first["items"], second["items"], "the ids did not move")
	assert.NotEmpty(t, second[AttentionFingerprintKey])
	assert.NotEqual(t, first[AttentionFingerprintKey], second[AttentionFingerprintKey])
}

// Review round 2, finding 3 (FR-019a timing): a queue composed for a caller
// that may not see held skills reads no skill approvals at all, so its cost
// does not grow with held skills. A broken skill record proves it: the
// tool-only queue neither notices it (no warning) nor shows any skill data.
func TestReviewQueue_WithoutSkillsReadsNoSkillApprovals(t *testing.T) {
	rt := heldSkillsRuntime(t, true)
	core, logs := observer.New(zap.WarnLevel)
	rt.logger = zap.New(core)
	require.NoError(t, rt.storageManager.GetDB().Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(storage.SkillApprovalBucket)).Put([]byte("both:skill://broken/SKILL.md"), []byte("{not json"))
	}))

	queue, err := rt.GetReviewQueueWithSkills(context.Background(), false)
	require.NoError(t, err)
	for _, row := range queue.Servers {
		assert.NotEqual(t, ReviewKindSkillReview, row.Kind)
		assert.Zero(t, row.SkillsPending+row.SkillsChanged)
	}
	assert.Equal(t, 2, queue.Count, "both (tools) and quarantined")
	assert.Zero(t, logs.Len(), "the tool-only queue must not read skill records: %v", logs.All())

	// The administrator's queue does read them (and logs the broken one).
	full, err := rt.GetReviewQueueWithSkills(context.Background(), true)
	require.NoError(t, err)
	assert.Equal(t, 3, full.Count)
	assert.NotZero(t, logs.Len())
}

func TestAttentionSkillReviewOneItemPerNonZeroState(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	items := Compute(AttentionInput{Now: now, Servers: []AttentionServer{
		{Name: "io.github.o/hf", Enabled: true, SkillsPending: 3, SkillsChanged: 1, Health: readyHealth(), StateSince: now},
		{Name: "quiet", Enabled: true, Health: readyHealth(), StateSince: now},
		{Name: "held-q", Enabled: true, Quarantined: true, SkillsPending: 2, Health: readyHealth(), StateSince: now},
		{Name: "off", Enabled: false, SkillsPending: 2, Health: readyHealth(), StateSince: now},
	}})
	var got []string
	for _, it := range items {
		if it.Kind == AttentionKindSkillReview {
			got = append(got, it.ID)
			assert.Equal(t, "server", it.Subject.Type)
			assert.Equal(t, AttentionFixReview, it.Fix.Verb)
			assert.Equal(t, "Review", it.Fix.Label)
			assert.Equal(t, "/servers/io.github.o%2Fhf?tab=skills", it.Fix.Target)
		}
	}
	assert.Equal(t, []string{"skill_review:server:io.github.o/hf:changed", "skill_review:server:io.github.o/hf:pending"}, got)
	for _, it := range items {
		switch it.ID {
		case "skill_review:server:io.github.o/hf:changed":
			assert.Equal(t, AttentionRankSkillReviewChanged, it.Rank)
			assert.Equal(t, "io.github.o/hf: 1 skill(s) changed, needs review", it.Summary)
		case "skill_review:server:io.github.o/hf:pending":
			assert.Equal(t, AttentionRankSkillReviewPending, it.Rank)
			assert.Equal(t, "io.github.o/hf: 3 new skill(s), needs review", it.Summary)
		}
	}
}

func TestAttentionSkillReviewRanksBetweenToolReviewAndClientNeverSeen(t *testing.T) {
	assert.Less(t, AttentionRankToolReviewPending, AttentionRankSkillReviewChanged)
	assert.Less(t, AttentionRankSkillReviewChanged, AttentionRankSkillReviewPending)
	assert.Less(t, AttentionRankSkillReviewPending, AttentionRankClientNeverSeen)
	kinds := AttentionKinds()
	var toolAt, skillAt, neverAt int
	for i, k := range kinds {
		switch k {
		case AttentionKindToolReview:
			toolAt = i
		case AttentionKindSkillReview:
			skillAt = i
		case AttentionKindClientNeverSeen:
			neverAt = i
		}
	}
	assert.Equal(t, toolAt+1, skillAt)
	assert.Equal(t, skillAt+1, neverAt)
}

func TestAttentionSubscriber_RecomputesOnSkillsChanged(t *testing.T) {
	assert.True(t, attentionTriggers(EventTypeSkillsChanged))
}

func TestAttentionSubscriber_FeedsHeldSkillCounts(t *testing.T) {
	rt := heldSkillsRuntime(t, true)
	a := newAttentionSubscriber(rt, time.Millisecond)
	a.servers = nil
	for _, name := range []string{"both", "skills-only", "quarantined", "excluded", "parked", "settled"} {
		sc := skillsServerConfig(rt.Config(), name)
		a.servers = append(a.servers, contractsServerForAttention(sc))
	}
	a.recompute()
	var ids []string
	for _, it := range a.Items() {
		if it.Kind == AttentionKindSkillReview {
			ids = append(ids, it.ID)
		}
	}
	assert.ElementsMatch(t, []string{
		"skill_review:server:both:changed",
		"skill_review:server:skills-only:changed",
		"skill_review:server:skills-only:pending",
	}, ids)
}

func readyHealth() contracts.HealthStatus {
	return contracts.HealthStatus{Status: health.StatusReady, Usable: true}
}

func contractsServerForAttention(sc *config.ServerConfig) contracts.Server {
	h := readyHealth()
	return contracts.Server{Name: sc.Name, Enabled: sc.Enabled, Quarantined: sc.Quarantined, Health: &h}
}

func TestSkillsChangedAlsoAnnouncesAnAdminOnlyReviewChange(t *testing.T) {
	rt := heldSkillsRuntime(t, true)
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)
	rt.publishSkillsChanged("skills-only", map[string]int{"pending": 2})
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type != EventTypeReviewChanged {
				continue
			}
			assert.Equal(t, "skills-only", evt.Payload["server"])
			assert.Equal(t, true, evt.Payload[ReviewChangedSkillsKey])
			return
		case <-deadline:
			t.Fatal("no review.changed after skills.changed")
		}
	}
}

// Review round 1, finding 4: skills.changed makes the list stale but must not
// push back a recompute that is already pending. Otherwise a steady stream of
// skill refreshes (or one at the wrong moment) delays every caller's frames,
// a scoped caller's included, and that delay is itself a sign of skills.
func TestAttentionSubscriber_SkillsChangedDoesNotPostponeAPendingRecompute(t *testing.T) {
	rt := heldSkillsRuntime(t, true)
	rt.skills.noAutoTrigger.Store(true)
	a := newAttentionSubscriber(rt, 500*time.Millisecond)
	a.clientSource = nil
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.start(ctx)
	// Let the start-up recompute (no servers known yet) run first.
	time.Sleep(time.Second)
	require.Empty(t, a.Items())

	servers := []contracts.Server{contractsServerForAttention(skillsServerConfig(rt.Config(), "skills-only"))}
	rt.publishEvent(newEvent(EventTypeServersChanged, map[string]any{"servers": servers}))
	hasSkillItem := func() bool {
		for _, it := range a.Items() {
			if it.Kind == AttentionKindSkillReview {
				return true
			}
		}
		return false
	}
	stop := time.After(3 * time.Second)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for !hasSkillItem() {
		select {
		case <-tick.C:
			rt.publishEvent(newEvent(EventTypeSkillsChanged, map[string]any{"server": "skills-only"}))
		case <-stop:
			t.Fatal("skills.changed every 100 ms kept the pending recompute from running")
		}
	}
}

// skillsAnnouncements collects, for a short window after a commit, the
// servers named by skills.changed and every skills-marked review.changed.
func skillsAnnouncements(t *testing.T, ch <-chan Event, window time.Duration) (skills []string, reviews []map[string]any) {
	t.Helper()
	deadline := time.After(window)
	for {
		select {
		case evt := <-ch:
			switch evt.Type {
			case EventTypeSkillsChanged:
				name, _ := evt.Payload["server"].(string)
				skills = append(skills, name)
			case EventTypeReviewChanged:
				if marked, _ := evt.Payload[ReviewChangedSkillsKey].(bool); marked {
					reviews = append(reviews, evt.Payload)
				}
			}
		case <-deadline:
			return skills, reviews
		}
	}
}

func TestSkillsExposureChangeAnnouncesAReviewChange(t *testing.T) {
	off := false
	for _, tc := range []struct {
		name   string
		enable bool
		mutate func(*CommitTx) error
		want   []string
	}{
		// "settled" affects the Skills tab only.
		{"enable_skills on", false, func(tx *CommitTx) error {
			return tx.MutateGlobal(func(c *config.Config) error { c.EnableSkills = true; return nil })
		}, []string{"both", "skills-only"}},
		{"enable_skills off", true, func(tx *CommitTx) error {
			return tx.MutateGlobal(func(c *config.Config) error { c.EnableSkills = false; return nil })
		}, []string{"both", "skills-only"}},
		{"server disabled", true, func(tx *CommitTx) error {
			return tx.UpdateServer("skills-only", func(sc *config.ServerConfig) error { sc.Enabled = false; return nil })
		}, []string{"skills-only"}},
		{"server enabled", true, func(tx *CommitTx) error {
			return tx.UpdateServer("parked", func(sc *config.ServerConfig) error { sc.Enabled = true; return nil })
		}, []string{"parked"}},
		{"expose_skills off", true, func(tx *CommitTx) error {
			return tx.UpdateServer("skills-only", func(sc *config.ServerConfig) error { sc.ExposeSkills = &off; return nil })
		}, []string{"skills-only"}},
		{"expose_skills back on", true, func(tx *CommitTx) error {
			return tx.UpdateServer("excluded", func(sc *config.ServerConfig) error { sc.ExposeSkills = nil; return nil })
		}, []string{"excluded"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := heldSkillsRuntime(t, tc.enable)
			rt.skills.noAutoTrigger.Store(true)
			ch := rt.SubscribeEvents()
			defer rt.UnsubscribeEvents(ch)
			_, err := rt.Commit("test", tc.mutate)
			require.NoError(t, err)

			skills, reviews := skillsAnnouncements(t, ch, time.Second)
			wantSkills := append([]string{}, tc.want...)
			if tc.name == "enable_skills on" || tc.name == "enable_skills off" {
				wantSkills = append(wantSkills, "settled")
			}
			assert.ElementsMatch(t, wantSkills, skills, "skills.changed")
			require.Len(t, reviews, 1, "one skills-marked review.changed per commit")
			assert.NotContains(t, reviews[0], "server", "the coalesced frame names no single server")
			assert.ElementsMatch(t, tc.want, reviews[0]["servers"])
		})
	}
}

func TestSkillsExposureChangeWithNothingHeldRefreshesTabsOnly(t *testing.T) {
	var servers []*config.ServerConfig
	for i := 0; i < 8; i++ {
		servers = append(servers, explicitServer(&config.ServerConfig{
			Name: fmt.Sprintf("srv-%d", i), URL: "http://127.0.0.1:9/mcp", Protocol: "http", Enabled: true,
		}))
	}
	rt, _ := newSpec116Runtime(t, nil, servers...)
	rt.skills.noAutoTrigger.Store(true)
	saveHeldSkill(t, rt, "srv-0", "ok", storage.SkillStatusApproved)
	saveHeldSkill(t, rt, "srv-1", "blocked", storage.SkillStatusPending, func(r *storage.SkillApprovalRecord) { r.Blocked = true })
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)
	for _, enable := range []bool{true, false} {
		_, err := rt.Commit("test", func(tx *CommitTx) error {
			return tx.MutateGlobal(func(c *config.Config) error { c.EnableSkills = enable; return nil })
		})
		require.NoError(t, err)
		skills, reviews := skillsAnnouncements(t, ch, 500*time.Millisecond)
		var wantSkills []string
		for _, server := range servers {
			wantSkills = append(wantSkills, server.Name)
		}
		assert.ElementsMatch(t, wantSkills, skills)
		assert.Emptyf(t, reviews, "enable_skills=%v: skills-marked review.changed", enable)
	}
}
