package server

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// jobSummaryFake is a scan service that, like the real one, settles a cached
// scan by its job ID: the job's own verdict lives in byJob, while the
// server-wide latest summary (GetScanSummary) belongs to whatever scan of the
// server started last.
type jobSummaryFake struct {
	*fakeSecurityScanner
	mu    sync.Mutex
	byJob map[string]*scanner.ScanSummary
	asked []string
}

func (f *jobSummaryFake) StartScanFromCachedContracts(ctx context.Context, server, digest string) (*scanner.ScanJob, error) {
	job, err := f.fakeSecurityScanner.StartScanFromCachedContracts(ctx, server, digest)
	if job != nil {
		job.ID = "job-" + server
	}
	return job, err
}

func (f *jobSummaryFake) GetScanJobSummary(_ context.Context, _, jobID string) *scanner.ScanSummary {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, jobID)
	return f.byJob[jobID]
}

// Sol round 3, finding 2 (reprotect): completion bookkeeping must use the
// cached job's own verdict. A newer scan of the same server (a manual scan
// does not take the sweep's queue) makes the server-wide summary read
// "scanning" or carry that scan's verdict instead.
func TestReprotect_R3_HitIsTakenFromTheCachedJobNotTheServersLatestSummary(t *testing.T) {
	base := newFakeSecurityScanner()
	// The server-wide summary is a newer clean scan's.
	base.cachedScanResult["alpha"] = &scanner.ScanSummary{Status: "clean"}
	fake := &jobSummaryFake{
		fakeSecurityScanner: base,
		byJob: map[string]*scanner.ScanSummary{
			"job-alpha": {Status: "dangerous", FindingCounts: &scanner.FindingCounts{Total: 1, Dangerous: 1}},
		},
	}
	s, probe := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))

	s.handleBundleActivation(activation(reprotectDigestA))

	assert.Equal(t, []string{"job-alpha"}, fake.asked)
	assert.Equal(t, 1, probe.hitCount(), "the cached job's hard finding is a hit even though a newer clean scan displaced it as the latest")
	assert.Equal(t, 1, probe.lastStatus(t).ServersWithHits)
}

func TestReprotect_R3_ASettledJobIsNotFailedBecauseANewerScanIsRunning(t *testing.T) {
	base := newFakeSecurityScanner()
	// The server-wide summary reads "scanning": a newer scan is in flight.
	base.cachedScanResult["alpha"] = &scanner.ScanSummary{Status: "scanning"}
	fake := &jobSummaryFake{
		fakeSecurityScanner: base,
		byJob:               map[string]*scanner.ScanSummary{"job-alpha": {Status: "clean"}},
	}
	s, probe := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))

	s.handleBundleActivation(activation(reprotectDigestA))

	last := probe.lastStatus(t)
	assert.Equal(t, 1, last.ServersScanned)
	assert.Equal(t, scanner.ReprotectStateIdle, last.State, "the completed job is a verdict; the other scan is not this sweep's")
	assert.Equal(t, reprotectDigestA, reprotectMarker(t, s).FullDigest)
}

// A job the service cannot summarise (replaced server, vanished record) has no
// verdict: a failure, never a success.
func TestReprotect_R3_AJobWithoutItsOwnSummaryIsNoVerdict(t *testing.T) {
	base := newFakeSecurityScanner()
	base.cachedScanResult["alpha"] = &scanner.ScanSummary{Status: "clean"} // someone else's
	fake := &jobSummaryFake{fakeSecurityScanner: base, byJob: map[string]*scanner.ScanSummary{}}
	s, probe := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))

	s.handleBundleActivation(activation(reprotectDigestA))

	last := probe.lastStatus(t)
	assert.Equal(t, scanner.ReprotectStateFailed, last.State)
	assert.Empty(t, reprotectMarker(t, s).FullDigest, "a sweep that read no verdict of its own is not complete")
}

// Sol round 3, finding 1 (reprotect): a service-side supersession refusal ends
// the superseded sweep quietly; it is neither retried nor a failure.
func TestReprotect_R3_ServiceSideSupersessionAbortsWithoutRetry(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.cachedScanErrs["alpha"] = []error{scanner.ErrBundleSuperseded, scanner.ErrBundleSuperseded, scanner.ErrBundleSuperseded}
	s, probe := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))

	s.handleBundleActivation(activation(reprotectDigestA))

	require.Len(t, fake.cachedScanAttempts(), 1, "a superseded request is not retried")
	assert.NotEqual(t, scanner.ReprotectStateFailed, probe.lastStatus(t).State)
	assert.Empty(t, reprotectMarker(t, s).FullDigest, "an abandoned sweep is not complete")
}
