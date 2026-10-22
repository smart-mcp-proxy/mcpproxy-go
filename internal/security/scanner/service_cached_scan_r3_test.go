package scanner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sol round 3 (reprotect), finding 1: a bundle that activates after the sweep
// passed its supersession check, while the service collects contracts and peers
// or waits for the approval lock, must not get a job labelled with the stale
// digest while the detector reads the newer active bundle.

const (
	r3DigestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	r3DigestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// r3MarkerBundle has one runnable rule that matches r3Marker.
	r3Marker       = "zzpinmarkerzz"
	r3MarkerRuleID = "TPA-2099-0001"
	r3MarkerBundle = `{"bundle_version":"0.1.0","schema_version":"0.1.0","signature_count":1,"rules":[` +
		`{"id":"TPA-2099-0001","detector":"pin","engine":"regex","target":"tool_description","pattern":"zzpinmarkerzz","category":"prompt-injection","level":"high","confidence":0.9}],"skipped":[]}`
)

func r3PublishBundle(t *testing.T, check *BundleCheck, digest string) {
	t.Helper()
	prevCheck, prevInfo := snapshotBundle()
	storeBundle(check, BundleInfo{Source: BundleSourceEmbedded, FullDigest: digest})
	t.Cleanup(func() { storeBundle(prevCheck, prevInfo) })
}

func r3MarkerCheck(t *testing.T) *BundleCheck {
	t.Helper()
	check, _, err := loadBundleCheck([]byte(r3MarkerBundle))
	require.NoError(t, err)
	return check
}

func TestStartScanFromCachedContracts_R3_BundleSupersededWhileContractsAreReadGetsNoJob(t *testing.T) {
	embedded, _, err := loadEmbeddedBundleCheck()
	require.NoError(t, err)
	r3PublishBundle(t, embedded, r3DigestA)

	provider := &cachedScanProvider{info: &ServerInfo{Name: "srv", Protocol: "stdio"}, approved: approvedPair()}
	svc, mock, _ := newCachedScanService(t, provider)
	svc.bundleSnapshotFn = nil // the real active bundle
	flip := &flipProvider{cachedScanProvider: provider, lock: &r2ApprovalLockUnquarantiner{}}
	// Bundle B activates after the caller's own supersession check, while the
	// contracts are being collected.
	flip.onRead = func() { storeBundle(embedded, BundleInfo{Source: BundleSourceEmbedded, FullDigest: r3DigestB}) }
	svc.SetServerInfoProvider(flip)

	job, err := svc.StartScanFromCachedContracts(context.Background(), "srv", r3DigestA)

	require.ErrorIs(t, err, ErrBundleSuperseded)
	assert.Nil(t, job)
	jobs, _ := mock.ListScanJobs("srv")
	assert.Empty(t, jobs, "a superseded request must leave no job record behind")
}

func TestStartScanFromCachedContracts_R3_DetectorUsesTheBundleTheJobIsLabelledWith(t *testing.T) {
	marker := r3MarkerCheck(t)
	embedded, _, err := loadEmbeddedBundleCheck()
	require.NoError(t, err)
	// The pinned bundle (digest A) carries the marker rule. By the time the
	// asynchronous detector reads "the active bundle", bundle B (no marker
	// rule) is serving.
	provider := &cachedScanProvider{
		info:     &ServerInfo{Name: "srv", Protocol: "stdio"},
		approved: []map[string]interface{}{rt06Tool("Summarizes text. " + r3Marker)},
	}
	svc, mock, _ := newCachedScanService(t, provider)
	svc.bundleSnapshotFn = func() (*BundleCheck, BundleInfo) {
		return marker, BundleInfo{Source: BundleSourceEmbedded, FullDigest: r3DigestA}
	}
	r3PublishBundle(t, embedded, r3DigestB)

	job, err := svc.StartScanFromCachedContracts(context.Background(), "srv", r3DigestA)
	require.NoError(t, err)
	require.NotNil(t, job.ScanContext)
	assert.Equal(t, r3DigestA, job.ScanContext.BundleDigest)

	findings := waitCachedScan(t, mock, job.ID)
	var sawMarker bool
	for _, f := range findings {
		if strings.Contains(f.RuleID, r3MarkerRuleID) {
			sawMarker = true
		}
	}
	assert.True(t, sawMarker, "the job labelled with bundle A must have been scanned with bundle A, not the bundle that serves now: %+v", findings)
}

func TestStartScanFromCachedContracts_R3_EmptyDigestIsLabelledWithTheBundleUsed(t *testing.T) {
	embedded, _, err := loadEmbeddedBundleCheck()
	require.NoError(t, err)
	r3PublishBundle(t, embedded, r3DigestA)
	provider := &cachedScanProvider{info: &ServerInfo{Name: "srv", Protocol: "stdio"}, approved: approvedPair()}
	svc, _, _ := newCachedScanService(t, provider)
	svc.bundleSnapshotFn = nil

	job, err := svc.StartScanFromCachedContracts(context.Background(), "srv", "")
	require.NoError(t, err)
	assert.Equal(t, r3DigestA, job.ScanContext.BundleDigest)
}

// Sol round 3 (reprotect), finding 2: the verdict of a cached job is read by
// the job's own ID, never from the server-wide latest summary.
func TestGetScanJobSummary_R3_SettlesByJobIDNotByLatestScan(t *testing.T) {
	provider := &cachedScanProvider{
		info:     &ServerInfo{Name: "srv", Protocol: "stdio"},
		approved: []map[string]interface{}{rt06Tool(rt06Poisoned)},
	}
	svc, mock, _ := newCachedScanService(t, provider)
	job, err := svc.StartScanFromCachedContracts(context.Background(), "srv", cachedDigest)
	require.NoError(t, err)
	waitCachedScan(t, mock, job.ID)
	<-job.Done()

	// A newer scan of the same server (a manual scan does not take the sweep's
	// queue) is now the server's latest job and is still running.
	newer := &ScanJob{ID: "newer", ServerName: "srv", Status: ScanJobStatusRunning, ScanPass: ScanPassSecurityScan, StartedAt: time.Now().Add(time.Minute)}
	require.NoError(t, mock.SaveScanJob(newer))
	svc.invalidateScanSummaryCache("srv")

	got := svc.GetScanJobSummary(context.Background(), "srv", job.ID)
	require.NotNil(t, got)
	assert.Equal(t, "dangerous", got.Status, "the job's own hard finding, whatever the server's newest job is")

	assert.Nil(t, svc.GetScanJobSummary(context.Background(), "srv", "newer"), "a job that has not settled has no verdict")
	assert.Nil(t, svc.GetScanJobSummary(context.Background(), "other", job.ID), "a job of another server is not this server's verdict")
	assert.Nil(t, svc.GetScanJobSummary(context.Background(), "srv", "no-such-job"))
}
