package server

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Sol round 2, finding 2: a sweep that waited on the informational scan queue
// must recheck supersession once it has the lock. Otherwise it starts a job
// labelled with its (old) digest that scans the serving (newer) bundle.
func TestReprotect_R2_SupersededSweepStartsNothingAfterWaitingForTheScanQueue(t *testing.T) {
	fake := newFakeSecurityScanner()
	s, probe := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))

	s.infoScanRunMu.Lock() // another informational scan holds the queue
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.handleBundleActivation(activation(reprotectDigestA)) }()
	require.Eventually(t, func() bool {
		probe.mu.Lock()
		defer probe.mu.Unlock()
		return len(probe.statuses) > 0
	}, 5*time.Second, time.Millisecond, "sweep A must be under way")
	time.Sleep(150 * time.Millisecond) // past its own checks, blocked on the queue

	// Bundle C activates while A waits; C's sweep queues behind A's runMu.
	cAct := activation(reprotectDigestC)
	cAct.Seq = 2
	go func() { defer wg.Done(); s.handleBundleActivation(cAct) }()
	require.Eventually(t, func() bool {
		s.bundleReprotect.mu.Lock()
		defer s.bundleReprotect.mu.Unlock()
		return s.bundleReprotect.latest == reprotectDigestC
	}, 5*time.Second, time.Millisecond, "the newer activation must be registered while A waits")

	s.infoScanRunMu.Unlock()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	awaitClosed(t, done)

	assert.Equal(t, []string{"alpha@" + reprotectDigestC}, fake.cachedScans(),
		"the superseded sweep must not start a job once it gets the scan queue; only the newer digest scans")
}

// Sol round 2, finding 3: the operator re-enables automatic scans while a
// sweep that skipped a server is still refreshing held evidence. The reload's
// resume sees the "suspended" flag unset (a sweep sets it only when it returns)
// and does nothing; the sweep then ends suspended. That must not be permanent.
func TestReprotect_R2_ReEnablingScansDuringASuspendingSweepStillResumesIt(t *testing.T) {
	fake := newFakeSecurityScanner()
	off := false
	s, probe := newReprotectServer(t, fake, &config.SecurityConfig{AutoBaselineScan: &off},
		enabledServer("alpha", config.TrustModeManual),
		enabledServer("bravo", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))
	seedToolRecords(t, s, "bravo", map[string]string{"b1": storage.ToolApprovalStatusPending})
	s.bundleReprotect.activeDigest = func() string { return reprotectDigestA }

	refresh := s.bundleReprotect.refreshHeld
	var flipped sync.Once
	s.bundleReprotect.refreshHeld = func(server string) error {
		err := refresh(server)
		flipped.Do(func() {
			// alpha was skipped (switch off); now, still inside the sweep, the
			// operator turns scanning back on and the reload hook fires.
			off = true
			s.resumeSuspendedReprotect()
		})
		return err
	}

	s.handleBundleActivation(activation(reprotectDigestA))

	assert.Equal(t, []string{"alpha@" + reprotectDigestA}, fake.cachedScans(),
		"the skipped server is re-protected once the switch is back on, with no further reload")
	assert.Equal(t, scanner.ReprotectStateIdle, probe.lastStatus(t).State)
	assert.Equal(t, reprotectDigestA, reprotectMarker(t, s).FullDigest)
}
