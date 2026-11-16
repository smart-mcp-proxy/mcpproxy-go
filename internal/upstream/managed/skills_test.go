package managed

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/skillstest"
)

func TestManagedListSkillsCaptured(t *testing.T) {
	fx := skillstest.New(t)
	fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"a.md": []byte("A")}})
	mc := connectedManagedTestClient(t, fx.URL())
	require.True(t, mc.SupportsSkills())

	before := captureSeq.Load()
	entries, errs, capture, err := mc.ListSkillsCaptured(context.Background())
	require.NoError(t, err)
	assert.Empty(t, errs)
	require.Len(t, entries, 1)
	assert.Greater(t, capture.Seq, before, "the capture is a fresh sequence")
	assert.Same(t, mc, capture.Client)
	assert.True(t, mc.CaptureIsCurrent(capture))

	_, _, capture2, err := mc.ListSkillsCaptured(context.Background())
	require.NoError(t, err)
	assert.Greater(t, capture2.Seq, capture.Seq)

	content, isText, _, err := mc.ReadSkillFile(context.Background(), entries[0].Files[0])
	require.NoError(t, err)
	assert.True(t, isText)
	assert.NotEmpty(t, content)
}

func TestManagedListSkillsCapturedStampedBeforeRequest(t *testing.T) {
	fx := skillstest.New(t)
	fx.AddSkill(skillstest.Skill{Path: "pdf"})
	mc := connectedManagedTestClient(t, fx.URL())

	// A reconnect while the request is in flight must make the capture stale:
	// the epoch is read before the request goes out.
	fx.SetDelay(50_000_000)
	done := make(chan InventoryCapture, 1)
	go func() {
		_, _, c, _ := mc.ListSkillsCaptured(context.Background())
		done <- c
	}()
	for fx.Count("skills/list") == 0 {
		time.Sleep(time.Millisecond)
	}
	mc.connectionEpoch.Add(1)
	c := <-done
	assert.False(t, mc.CaptureIsCurrent(c), "capture taken before the epoch moved is not current")
}

func TestManagedListSkillsTruncatedPassesThroughWithPartial(t *testing.T) {
	fx := skillstest.New(t, skillstest.WithPageSize(1))
	for i := 0; i < skills.MaxListPages+5; i++ {
		fx.AddSkill(skillstest.Skill{Path: fmt.Sprintf("s%03d", i)})
	}
	mc := connectedManagedTestClient(t, fx.URL())

	entries, _, capture, err := mc.ListSkillsCaptured(context.Background())
	var tr *skills.TruncatedError
	require.ErrorAs(t, err, &tr)
	assert.Len(t, entries, skills.MaxListPages)
	assert.NotZero(t, capture.Seq)
}

func TestManagedSkillsNotConnected(t *testing.T) {
	mc := newTestDisconnectedClient(t)
	assert.False(t, mc.SupportsSkills())
	_, _, _, err := mc.ListSkillsCaptured(context.Background())
	require.Error(t, err)
	_, _, _, err = mc.ReadSkillFile(context.Background(), skills.FileRef{})
	require.Error(t, err)
}

func TestManagedListSkillsNoRequestWithoutSupport(t *testing.T) {
	fx := skillstest.New(t, skillstest.WithoutExtension())
	mc := connectedManagedTestClient(t, fx.URL())
	entries, _, _, err := mc.ListSkillsCaptured(context.Background())
	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.Zero(t, fx.Count("skills/list"))
}

// A hot-reloaded expose_skills: false must stop skill requests on the live
// connection, and removing it must allow them again, without a reconnect.
func TestManagedSetConfigPushesExposeSkills(t *testing.T) {
	fx := skillstest.New(t)
	fx.AddSkill(skillstest.Skill{Path: "pdf"})
	mc := connectedManagedTestClient(t, fx.URL())

	off := false
	excluded := *mc.GetConfig()
	excluded.ExposeSkills = &off
	mc.SetConfig(&excluded)

	entries, _, _, err := mc.ListSkillsCaptured(context.Background())
	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.Zero(t, fx.Count("skills/list"), "an excluded server is not asked for skills")

	included := excluded
	included.ExposeSkills = nil
	mc.SetConfig(&included)

	entries, _, _, err = mc.ListSkillsCaptured(context.Background())
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}
