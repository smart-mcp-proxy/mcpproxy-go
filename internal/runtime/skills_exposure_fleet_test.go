package runtime

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/skillstest"
)

// Spec 125: flipping enable_skills on a fleet larger than a subscriber's
// buffer (defaultEventBuffer, 256) must not overflow it. The exposure path
// sends ONE skills.changed listing every affected server, after the commit's
// own events and the coalesced skills-marked review.changed, so a subscriber
// that is not drained still holds all of them.
//
// Every server genuinely supports skills: each is a connected managed client
// to the skillstest fixture (one fixture process serves all 300 names, which
// is the same upstream protocol path as 300 fixtures), and support and the
// held record come from a real RefreshServerSkills.
func TestSkillsExposureFlipOnAFleetLargerThanTheEventBuffer(t *testing.T) {
	const fleet = 300
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	fx := skillstest.New(t)
	fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"SKILL.md": skillMD("pdf", "reads pdf", "body")}})

	var servers []*config.ServerConfig
	var names []string
	for i := 0; i < fleet; i++ {
		name := fmt.Sprintf("fleet-%03d", i)
		names = append(names, name)
		servers = append(servers, explicitServer(&config.ServerConfig{
			Name: name, URL: fx.URL(), Protocol: "streamable-http", Enabled: true,
		}))
	}
	sort.Strings(names)
	rt, _ := newSpec116Runtime(t, func(c *config.Config) { c.EnableSkills = true }, servers...)
	rt.skills.noAutoTrigger.Store(true)
	for _, name := range names {
		rt.discoveryInProgress.Store(name, struct{}{})
		require.NoError(t, rt.upstreamManager.AddServerAt(name, spec116Live(rt, name), storedStamp(t, rt, name).Stamp.Gen))
	}
	for _, name := range names {
		c, ok := rt.upstreamManager.GetClient(name)
		require.True(t, ok)
		require.Eventually(t, c.IsConnected, 20*time.Second, 5*time.Millisecond, name)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		require.NoError(t, rt.RefreshServerSkills(ctx, name))
		cancel()
		require.True(t, rt.SkillsStatus(name).Supported, name)
	}
	rt.skills.wait()

	// One subscriber, never drained while the two commits run.
	ch := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(ch)
	for _, enable := range []bool{false, true} {
		_, err := rt.Commit("test", func(tx *CommitTx) error {
			return tx.MutateGlobal(func(c *config.Config) error { c.EnableSkills = enable; return nil })
		})
		require.NoError(t, err)
	}

	var saved, reviews, lists, perServer int
	for drained := false; !drained; {
		select {
		case evt := <-ch:
			switch evt.Type {
			case EventTypeConfigSaved, EventTypeConfigReloaded:
				saved++
			case EventTypeReviewChanged:
				if marked, _ := evt.Payload[ReviewChangedSkillsKey].(bool); marked {
					reviews++
					assert.Len(t, evt.Payload["servers"], fleet)
				}
			case EventTypeSkillsChanged:
				if _, one := evt.Payload["server"]; one {
					perServer++
					continue
				}
				lists++
				assert.Equal(t, names, evt.Payload["servers"], "one list event names every affected server")
			}
		case <-time.After(500 * time.Millisecond):
			drained = true
		}
	}
	assert.Equal(t, 2, saved, "each commit's config.saved survives")
	assert.Equal(t, 2, reviews, "each commit's skills-marked review.changed survives")
	assert.Equal(t, 2, lists, "one skills.changed list per commit")
	assert.Zero(t, perServer, "the exposure path sends no per-server skills.changed")
}
