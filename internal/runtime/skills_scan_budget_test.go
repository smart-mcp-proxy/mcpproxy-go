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

// A scan with degraded coverage has to be repeated, so it is not progress: a
// follow-up refresh would scan the same skills again and never get further.
func TestSkillsRefresh_DegradedScansDoNotScheduleAFollowUp(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true, trust: "scan", autoTrig: true})
	var n atomic.Int32
	g.rt.skills.scanFunc = countingScan(&n, "clean", false)
	g.rt.skills.limits.scanPerRefresh = time.Nanosecond
	g.rt.skills.limits.followUpDelay = time.Millisecond
	for _, p := range []string{"a", "b", "c"} {
		g.fx.AddSkill(skillstest.Skill{Path: p})
	}
	// The connect may have started a refresh of its own; let it finish.
	require.NoError(t, g.refresh(t))
	g.rt.skills.wait()
	lists := g.fx.Count("skills/list")
	require.NoError(t, g.refresh(t))
	time.Sleep(300 * time.Millisecond)
	g.rt.skills.wait()
	assert.Equal(t, lists+1, g.fx.Count("skills/list"), "no refresh follows one that settled nothing")
	assert.Equal(t, 2, g.rt.SkillsStatus(skillsSrv).ScansPending)
}

// A background trigger that arrives while a refresh runs is not lost by
// joining it: that refresh may already have read the configuration, so one
// more refresh follows it.
func TestSkillsRefresh_TriggerDuringARefreshRunsOneMore(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true, autoTrig: true})
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	g.rt.skills.wait()
	lists := g.fx.Count("skills/list")

	inList := make(chan struct{})
	release := make(chan struct{})
	var once atomic.Bool
	g.rt.skills.afterList = func(string) {
		if once.CompareAndSwap(false, true) {
			close(inList)
			<-release
		}
	}
	done := make(chan error, 1)
	go func() { done <- g.refresh(t) }()
	<-inList
	g.rt.triggerSkillsRefresh(skillsSrv) // e.g. the trust mode changed
	g.rt.triggerSkillsRefresh(skillsSrv) // twice is still one more
	close(release)
	require.NoError(t, <-done)
	require.Eventually(t, func() bool { return g.fx.Count("skills/list") == lists+2 }, 10*time.Second, 10*time.Millisecond)
	g.rt.skills.wait()
	assert.Equal(t, lists+2, g.fx.Count("skills/list"), "exactly one more refresh")
}
