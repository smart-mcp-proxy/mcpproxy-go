package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// Sol round 5 (reprotect): every ACCEPTED cached job is remembered for its
// (server, digest) pair, not only the ones that produced a verdict. A resume
// of the same digest (here: after the kill switch went off before bravo) must
// not register a second job for alpha when alpha's accepted job failed, timed
// out or left no readable verdict; it re-inspects the remembered job and, while
// that has no verdict, the sweep stays incomplete and the marker is withheld.

// switchOffAfterAlpha wires the kill switch off right after alpha's job was
// accepted, so bravo's scan is skipped and the sweep ends failed-suspended.
func switchOffAfterAlpha(fake *fakeSecurityScanner, on *bool) {
	fake.onCachedScan = func(server string) {
		if server == "alpha" {
			*on = false
		}
	}
}

func newR5Server(t *testing.T, fake securityScannerService, on *bool) (*Server, *reprotectProbe) {
	t.Helper()
	s, probe := newReprotectServer(t, fake, &config.SecurityConfig{AutoBaselineScan: on},
		enabledServer("alpha", config.TrustModeManual),
		enabledServer("bravo", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))
	seedToolRecords(t, s, "bravo", approvedOnly("b1"))
	s.bundleReprotect.activeDigest = func() string { return reprotectDigestA }
	return s, probe
}

func TestReprotect_R5_ResumeDoesNotRepeatAJobThatSettledFailed(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.cachedScanResult["alpha"] = &scanner.ScanSummary{Status: "failed"}
	on := true
	s, probe := newR5Server(t, fake, &on)
	switchOffAfterAlpha(fake, &on)

	s.handleBundleActivation(activation(reprotectDigestA))
	require.Equal(t, []string{"alpha@" + reprotectDigestA}, fake.cachedScans(), "precondition: alpha's job failed, bravo was skipped")

	on = true
	s.resumeSuspendedReprotect()

	assert.Equal(t, []string{"alpha@" + reprotectDigestA, "bravo@" + reprotectDigestA}, fake.cachedScans(),
		"bravo runs on resume; alpha's accepted job is not registered a second time")
	assert.Equal(t, 1, countOf(fake.cachedScanAttempts(), "alpha"))
	assert.Equal(t, scanner.ReprotectStateFailed, probe.lastStatus(t).State, "the failed job keeps the sweep incomplete")
	assert.Empty(t, reprotectMarker(t, s).FullDigest, "the marker stays withheld")
}

func TestReprotect_R5_ResumeDoesNotRepeatAJobThatTimedOut(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.cachedScanResult["alpha"] = &scanner.ScanSummary{Status: "scanning"}
	on := true
	s, probe := newR5Server(t, fake, &on)
	fake.onCachedJob = func(server string, finish func()) {
		if server == "alpha" {
			on = false
			time.AfterFunc(100*time.Millisecond, finish) // outlives the 30ms settle timeout
			return
		}
		finish()
	}

	s.handleBundleActivation(activation(reprotectDigestA))
	require.Equal(t, 1, countOf(fake.cachedScanAttempts(), "alpha"), "precondition: alpha's job was accepted once")

	on = true
	s.resumeSuspendedReprotect()

	assert.Equal(t, 1, countOf(fake.cachedScanAttempts(), "alpha"), "a timed-out job is not repeated by the resume")
	assert.Equal(t, 1, countOf(fake.cachedScanAttempts(), "bravo"))
	assert.Equal(t, scanner.ReprotectStateFailed, probe.lastStatus(t).State)
	assert.Empty(t, reprotectMarker(t, s).FullDigest)
}

// A job that left no readable verdict at first, but has one by the time the
// sweep resumes, is read from its job ID: counted once, marker written, still
// one job.
func TestReprotect_R5_ResumeReadsTheRememberedJobInsteadOfRegisteringAnother(t *testing.T) {
	base := newFakeSecurityScanner()
	fake := &jobSummaryFake{fakeSecurityScanner: base, byJob: map[string]*scanner.ScanSummary{
		"job-bravo": {Status: "clean"},
	}}
	on := true
	s, probe := newR5Server(t, fake, &on)
	switchOffAfterAlpha(base, &on)

	s.handleBundleActivation(activation(reprotectDigestA))
	require.Equal(t, scanner.ReprotectStateFailed, probe.lastStatus(t).State, "precondition: alpha has no verdict of its own")
	require.Empty(t, reprotectMarker(t, s).FullDigest)

	fake.mu.Lock()
	fake.byJob["job-alpha"] = &scanner.ScanSummary{Status: "dangerous", FindingCounts: &scanner.FindingCounts{Total: 1, Dangerous: 1}}
	fake.mu.Unlock()
	on = true
	s.resumeSuspendedReprotect()

	assert.Equal(t, 1, countOf(base.cachedScanAttempts(), "alpha"), "alpha's job is read, not repeated")
	last := probe.lastStatus(t)
	assert.Equal(t, scanner.ReprotectStateIdle, last.State)
	assert.Equal(t, 2, last.ServersScanned)
	assert.Equal(t, 1, last.ServersWithHits)
	assert.Equal(t, 1, probe.hitCount())
	assert.Equal(t, reprotectDigestA, reprotectMarker(t, s).FullDigest)

	s.handleBundleActivation(activation(reprotectDigestA))
	assert.Equal(t, 1, countOf(base.cachedScanAttempts(), "alpha"), "still one job for the pair")
}

func countOf(all []string, name string) int {
	n := 0
	for _, a := range all {
		if a == name {
			n++
		}
	}
	return n
}
