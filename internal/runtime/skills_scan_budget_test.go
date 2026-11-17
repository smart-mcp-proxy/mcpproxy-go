package runtime

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/skillstest"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// The scanner needs about a second for 48 KiB of text, so one refresh may
// only spend a bounded time scanning. What it does not get to is scanned by
// the refreshes that follow; nothing scanned is lost in between.

func TestSkillsRefresh_ScanBudgetSpreadsScansOverRefreshes(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true, trust: "scan"})
	var n atomic.Int32
	g.rt.skills.scanFunc = countingScan(&n, "clean", true)
	g.rt.skills.limits.scanPerRefresh = time.Nanosecond // spent by the first skill
	for _, p := range []string{"a", "b", "c"} {
		g.fx.AddSkill(skillstest.Skill{Path: p})
	}

	require.NoError(t, g.refresh(t))
	assert.Equal(t, int32(1), n.Load(), "the budget covers one skill; a refresh always scans at least one")
	scanned, pending := 0, 0
	for _, rec := range g.records(t) {
		if rec.Scan != nil {
			scanned++
			assert.Equal(t, storage.SkillStatusApproved, rec.Status, "clean and complete in scan mode")
		} else {
			pending++
			assert.Equal(t, storage.SkillStatusPending, rec.Status, "not scanned yet is not clean: held")
		}
	}
	assert.Equal(t, 1, scanned)
	assert.Equal(t, 2, pending)
	assert.Equal(t, 2, g.rt.SkillsStatus(skillsSrv).ScansPending)

	require.NoError(t, g.refresh(t))
	require.NoError(t, g.refresh(t))
	assert.Equal(t, int32(3), n.Load(), "each skill is scanned exactly once across the refreshes")
	for p, rec := range g.records(t) {
		require.NotNil(t, rec.Scan, p)
		assert.Equal(t, storage.SkillStatusApproved, rec.Status, p)
	}
	assert.Zero(t, g.rt.SkillsStatus(skillsSrv).ScansPending)
}

func TestSkillsRefresh_ScanBudgetSchedulesAFollowUp(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true, trust: "scan", autoTrig: true})
	var n atomic.Int32
	g.rt.skills.scanFunc = countingScan(&n, "clean", true)
	g.rt.skills.limits.scanPerRefresh = time.Nanosecond
	g.rt.skills.limits.followUpDelay = time.Millisecond
	for _, p := range []string{"a", "b", "c"} {
		g.fx.AddSkill(skillstest.Skill{Path: p})
	}
	require.NoError(t, g.refresh(t))
	require.Eventually(t, func() bool {
		recs := g.records(t)
		if len(recs) != 3 {
			return false
		}
		for _, rec := range recs {
			if rec.Scan == nil {
				return false
			}
		}
		return true
	}, 20*time.Second, 20*time.Millisecond, "the refresh that ran out of scan time is followed by another without anyone asking")
	assert.Equal(t, int32(3), n.Load())
}

func TestScanSkillText_TimeLimitIsRecordedAndNotRepeated(t *testing.T) {
	var n int
	rt := newScanRT(func(string, string, string) (string, []scanner.ScanFinding, bool) {
		n++
		time.Sleep(20 * time.Millisecond)
		return "clean", nil, true
	})
	rt.skills.limits.scanPerSkill = 5 * time.Millisecond
	line := strings.Repeat("y", 99) + "\n"
	res := rt.scanSkillText(context.Background(), "srv", "p", []skillTextFile{{"SKILL.md", []byte(strings.Repeat(line, 2000))}})
	assert.Equal(t, 1, n, "the limit is checked between chunks")
	assert.False(t, res.Complete)
	assert.Equal(t, storage.SkillScanIncompleteTimeLimit, res.Incomplete)

	rec := &storage.SkillApprovalRecord{Scan: res}
	res.Hash = "h"
	assert.True(t, reusableSkillScan(rec, "h"), "a scan that ran out of time is kept for the same content")
}

// A person's decision made while a refresh is still reading must not delete
// the blobs that refresh has staged and not yet referenced from a record.
func TestApproveSkills_KeepsBlobsARunningRefreshStaged(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	rec := g.records(t)["pdf"]

	const staged = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	require.NoError(t, g.rt.storageManager.PutSkillFile(skillsSrv, "sha256:"+staged, []byte("staged by a refresh")))
	before := g.blobCount(t)

	n, err := g.rt.ApproveSkillsReviewedAt(context.Background(), skillsSrv, []string{"pdf"}, map[string]string{"pdf": rec.CurrentHash}, "tester", 0)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	assert.Equal(t, before, g.blobCount(t), "an approval collects nothing")
	ok, err := g.rt.storageManager.HasSkillFile(skillsSrv, "sha256:"+staged)
	require.NoError(t, err)
	assert.True(t, ok)
}
