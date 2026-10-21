package scanner

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// T322: the freshness fields of BundleInfo (tasks B15).

func TestRefreshStatus_LastCheckedAtAdvancesOnNoOpRuns(t *testing.T) {
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	setBundleClock(t, func() time.Time { return now })
	f := newRTFixture(t, rtOpts{})
	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "a"}))
	commits := f.store.commitCount()
	n := countCompiles(t)

	first := f.run()
	require.NotNil(t, first.LastCheckedAt)
	assert.Equal(t, now, *first.LastCheckedAt)

	now = now.Add(6 * time.Hour)
	second := f.run() // the canSkipLocked short-circuit: nothing to decide or record

	require.NotNil(t, second.LastCheckedAt)
	assert.Equal(t, now, *second.LastCheckedAt, "a no-op run still counts as a check")
	assert.Equal(t, commits, f.store.commitCount(), "and commits nothing")
	assert.Zero(t, *n, "and compiles nothing")
}

func TestRefreshStatus_LastActivatedAt(t *testing.T) {
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	setBundleClock(t, func() time.Time { return now })
	f := newRTFixture(t, rtOpts{})

	info := f.run()
	assert.Equal(t, BundleSourceEmbedded, info.Source)
	assert.Nil(t, info.LastActivatedAt, "the embedded default was never activated")

	now = now.Add(time.Hour)
	info = f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "a"}))
	st := f.store.persisted(t)
	require.NotNil(t, st.Active)
	require.NotNil(t, info.LastActivatedAt)
	assert.Equal(t, st.Active.ActivatedAt.UTC(), info.LastActivatedAt.UTC())
	assert.Equal(t, now, info.LastActivatedAt.UTC())

	now = now.Add(time.Hour)
	f.restart()
	info = f.run()
	require.NotNil(t, info.LastActivatedAt, "the restored bundle keeps its activation time")
	assert.Equal(t, st.Active.ActivatedAt.UTC(), info.LastActivatedAt.UTC())
}

func TestRefreshStatus_StaleAtReadTime(t *testing.T) {
	generated := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	now := generated
	setBundleClock(t, func() time.Time { return now })
	f := newRTFixture(t, rtOpts{})
	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "a", GeneratedAt: generated.Format(time.RFC3339)}))

	statusAt := func(days int) BundleInfo {
		now = generated.Add(time.Duration(days) * 24 * time.Hour)
		return BundleStatus()
	}

	for _, tc := range []struct {
		days  int
		stale bool
	}{{29, false}, {30, true}, {31, true}} {
		info := statusAt(tc.days)
		require.NotNil(t, info.StalenessDays)
		assert.Equal(t, tc.days, *info.StalenessDays)
		assert.Equal(t, tc.stale, info.Stale, "age %d days, threshold 30", tc.days)
		require.NotNil(t, info.StaleAfterDays)
		assert.Equal(t, 30, *info.StaleAfterDays)
	}

	t.Run("a zero threshold never reports stale", func(t *testing.T) {
		f.sec.TPAStaleAfterDays = intp(0)
		f.run()
		info := statusAt(400)
		assert.False(t, info.Stale)
		assert.Nil(t, info.StaleAfterDays, "the flag is not evaluated")
		f.sec.TPAStaleAfterDays = nil
		f.run()
	})

	t.Run("a threshold edit applies on the next read without rerunning the pipeline", func(t *testing.T) {
		require.True(t, statusAt(31).Stale)
		commits := f.store.commitCount()
		n := countCompiles(t)
		f.sec.TPAStaleAfterDays = intp(60)

		f.run()

		info := BundleStatus()
		assert.False(t, info.Stale)
		require.NotNil(t, info.StaleAfterDays)
		assert.Equal(t, 60, *info.StaleAfterDays)
		assert.Equal(t, commits, f.store.commitCount())
		assert.Zero(t, *n)
	})
}

func TestRefreshStatus_FutureGeneratedAtIsNeverStale(t *testing.T) {
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	setBundleClock(t, func() time.Time { return now })
	f := newRTFixture(t, rtOpts{})
	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "future", GeneratedAt: now.Add(48 * time.Hour).Format(time.RFC3339)}))

	info := BundleStatus()

	require.NotNil(t, info.StalenessDays)
	assert.Equal(t, 0, *info.StalenessDays, "never a negative age")
	assert.False(t, info.Stale)

	f.sec.TPAStaleAfterDays = intp(1)
	f.run()
	assert.False(t, BundleStatus().Stale)
}

func TestRefreshStatus_LastTriggerStartupThenConfig(t *testing.T) {
	f := newRTFixture(t, rtOpts{})

	first := f.run()
	require.NotNil(t, first.Refresh)
	assert.Equal(t, RefreshTriggerStartup, first.Refresh.LastTrigger)

	second := f.run()
	require.NotNil(t, second.Refresh)
	assert.Equal(t, RefreshTriggerConfig, second.Refresh.LastTrigger)

	f.restart()
	assert.Equal(t, RefreshTriggerStartup, f.run().Refresh.LastTrigger, "a new process starts again")
}

func TestRefreshStatus_RefreshSettingsAreReported(t *testing.T) {
	f := newFetchFixture(t, func(s *config.SecurityConfig) {
		s.TPARefreshInterval = config.Duration(6 * time.Hour)
		s.TPAFetchURL = "https://mirror.test:8443/repos/acme/tpa-db/releases/latest"
	})

	info := f.run()

	require.NotNil(t, info.Refresh)
	assert.True(t, info.Refresh.FetchEnabled)
	assert.Equal(t, "mirror.test", info.Refresh.FetchHost, "host only: no port, no path")
	assert.Equal(t, int((6 * time.Hour).Seconds()), info.Refresh.IntervalSeconds)

	f.sec.TPAFetchEnabled = false
	f.sec.TPARefreshInterval = 0
	info = f.run()
	assert.False(t, info.Refresh.FetchEnabled)
	assert.Equal(t, int((24 * time.Hour).Seconds()), info.Refresh.IntervalSeconds)
}

func TestRefreshStatus_MergedAtReadNotPublished(t *testing.T) {
	f := newFetchFixture(t, nil)
	f.activate0(t)
	checkBefore := servingCheck()
	SetReprotectStatus(BundleReprotectStatus{State: ReprotectStateSuspended, Reason: ReprotectReasonAutoBaselineScanOff, ServersScanned: 2, ServersWithHits: 1})
	t.Cleanup(func() { SetReprotectStatus(BundleReprotectStatus{}) })

	viaStatus := BundleStatus()
	viaSnapshot := statusFromSnapshot()

	for name, info := range map[string]BundleInfo{"BundleStatus": viaStatus, "statusFromSnapshot": viaSnapshot} {
		require.NotNil(t, info.Refresh, name)
		require.NotNil(t, info.Reprotect, name)
		assert.Equal(t, ReprotectStateSuspended, info.Reprotect.State, name)
		assert.Equal(t, ReprotectReasonAutoBaselineScanOff, info.Reprotect.Reason, name)
		assert.Equal(t, 1, info.Reprotect.ServersWithHits, name)
		require.NotNil(t, info.LastCheckedAt, name)
	}

	_, published := snapshotBundle()
	assert.Nil(t, published.Refresh, "refresh status is merged into read copies; cycles never churn the published pair")
	assert.Nil(t, published.Reprotect)
	assert.Nil(t, published.LastCheckedAt)
	assert.Same(t, checkBefore, servingCheck())
}

// activate0 activates a small operator-signed fetched bundle.
func (f *rtFixture) activate0(t *testing.T) {
	t.Helper()
	c := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "status"})
	require.Equal(t, digestOf(c.Bundle), runOffered(c, f.log).FullDigest)
}
