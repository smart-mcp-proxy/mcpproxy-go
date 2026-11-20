package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
