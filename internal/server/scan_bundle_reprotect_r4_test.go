package server

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Sol round 4 (reprotect): a failure on one leg must not mask the kill-switch
// suspension of another. The operator switches auto_baseline_scan off after
// alpha's held-evidence refresh failed, so bravo's cached scan is skipped. The
// sweep is failed AND has deferred work; once automatic scans are back on the
// resume must run bravo (and retry alpha) even though the digest is still the
// active one and no new activation will ever fire.
func TestReprotect_R4_AFailureDoesNotMaskKillSwitchSuspension(t *testing.T) {
	fake := newFakeSecurityScanner()
	on := true
	s, probe := newReprotectServer(t, fake, &config.SecurityConfig{AutoBaselineScan: &on},
		enabledServer("alpha", config.TrustModeManual),
		enabledServer("bravo", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", map[string]string{"h1": storage.ToolApprovalStatusPending})
	seedToolRecords(t, s, "bravo", approvedOnly("b1"))
	s.bundleReprotect.activeDigest = func() string { return reprotectDigestA }

	refreshes := 0
	real := s.bundleReprotect.refreshHeld
	s.bundleReprotect.refreshHeld = func(server string) error {
		refreshes++
		if refreshes == 1 {
			on = false // switched off right after alpha's refresh fails
			return errors.New("evidence refresh failed")
		}
		return real(server)
	}

	s.handleBundleActivation(activation(reprotectDigestA))
	require.Empty(t, fake.cachedScans(), "precondition: bravo was skipped by the kill switch")
	require.Equal(t, scanner.ReprotectStateFailed, probe.lastStatus(t).State, "precondition: the failure is what is reported")
	require.Empty(t, reprotectMarker(t, s).FullDigest)

	on = true
	s.resumeSuspendedReprotect()

	assert.Equal(t, []string{"bravo@" + reprotectDigestA}, fake.cachedScans(), "the skipped scan runs on resume")
	assert.Equal(t, 2, refreshes, "the failed leg is retried by the resume")
	assert.Equal(t, scanner.ReprotectStateIdle, probe.lastStatus(t).State)
	assert.Equal(t, reprotectDigestA, reprotectMarker(t, s).FullDigest)
}

// The completion marker stays withheld while a failure remains, even when the
// deferred work ran on resume; and the settled scan is not repeated by the
// next retry.
func TestReprotect_R4_MarkerWithheldUntilEveryFailureIsResolved(t *testing.T) {
	fake := newFakeSecurityScanner()
	on := true
	s, probe := newReprotectServer(t, fake, &config.SecurityConfig{AutoBaselineScan: &on},
		enabledServer("alpha", config.TrustModeManual),
		enabledServer("bravo", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", map[string]string{"h1": storage.ToolApprovalStatusPending})
	seedToolRecords(t, s, "bravo", approvedOnly("b1"))
	s.bundleReprotect.activeDigest = func() string { return reprotectDigestA }

	alphaFails := 2
	real := s.bundleReprotect.refreshHeld
	s.bundleReprotect.refreshHeld = func(server string) error {
		if alphaFails > 0 {
			alphaFails--
			on = false
			return errors.New("evidence refresh failed")
		}
		return real(server)
	}

	s.handleBundleActivation(activation(reprotectDigestA))
	on = true
	s.resumeSuspendedReprotect() // alpha fails again (and switches off again); bravo is not scanned yet
	require.Empty(t, fake.cachedScans())
	require.Empty(t, reprotectMarker(t, s).FullDigest, "a leg still failing withholds the marker")
	require.Equal(t, scanner.ReprotectStateFailed, probe.lastStatus(t).State)

	on = true
	s.resumeSuspendedReprotect() // alpha now succeeds; bravo runs

	assert.Equal(t, []string{"bravo@" + reprotectDigestA}, fake.cachedScans())
	assert.Equal(t, scanner.ReprotectStateIdle, probe.lastStatus(t).State)
	assert.Equal(t, reprotectDigestA, reprotectMarker(t, s).FullDigest)
}
