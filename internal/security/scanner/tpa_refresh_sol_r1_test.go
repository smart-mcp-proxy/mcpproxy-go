package scanner

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Sol round-1 findings on the US3 refresh scheduler and runtime (group
// "refresh"): the file leg must not re-apply a policy a newer reload replaced,
// the supersession check lives inside the offer, a file activation owns the
// cycle, the failure counter follows the reported outcome, a display-only edit
// survives a reload, an unsigned serving pair never short-cuts the MustSign
// gate, and the fetch leg's own state is reported next to the merged outcome.

// The cycle captured config A, a reload then applied config B (the operator
// signer is gone), and only then did the file leg run. Applying A would
// activate a bundle B forbids and leave the stale policy serving.
func TestSolR1_FileLegDoesNotReapplyAPolicyAReloadReplaced(t *testing.T) {
	f := newFileAndFetchFixture(t, nil, false)
	h := newRefresherHarness(t, f, nil)
	c := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "stale-policy"})
	f.put(c) // dropped after the construction-time apply: only the cycle sees it
	h.ref.afterCapture = func() {
		h.reload(func(s *config.SecurityConfig) { s.TPAOperatorSigners = nil })
	}
	h.start()

	h.tick()

	info := BundleStatus()
	assert.NotEqual(t, digestOf(c.Bundle), info.FullDigest, "the stale policy must not activate a bundle the live policy forbids")
	assert.Equal(t, BundleSourceEmbedded, info.Source)
	h.mu.Lock()
	live := policyFromConfig(h.cfg)
	h.mu.Unlock()
	assert.Equal(t, live.Fingerprint, servingPolicy().Fingerprint, "the runtime keeps the newer policy")
	require.NotNil(t, info.Refresh)
	assert.Equal(t, RefreshOutcomeSuperseded, info.Refresh.LastOutcome)
	assert.Empty(t, f.store.historyRows())
}

// The supersession check must be atomic with the offer: a Nudge that lands
// after the check but before the pipeline takes its lock still discards the
// download.
func TestSolR1_SupersessionIsCheckedInsideTheOffer(t *testing.T) {
	ch := newTestChannel(t)
	pair := ch.publishRelease(2, "late-supersede")
	f := newChannelFixture(t, ch)
	obs := recordActivations(t)
	h := newRefresherHarness(t, f, nil)
	h.ref.beforeOffer = func() {
		h.reload(func(s *config.SecurityConfig) { s.TPAFetchEnabled = false })
	}
	h.start()

	h.tick()

	info := BundleStatus()
	assert.NotEqual(t, digestOf(pair.Bundle), info.FullDigest, "the download was superseded before it was offered")
	require.NotNil(t, info.Refresh)
	assert.Equal(t, RefreshOutcomeSuperseded, info.Refresh.LastOutcome)
	assert.Equal(t, 0, obs.count())
	assert.Empty(t, f.store.historyRows())
}

// B7: both candidates valid and above the watermark: the file wins this cycle,
// the fetch wins at the next one.
func TestSolR1_FileActivationSkipsTheFetchLegThisCycle(t *testing.T) {
	ch := newTestChannel(t)
	fetched := ch.publishRelease(3, "fetched")
	f := newFileAndFetchFixture(t, ch, true)
	file := signedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "file"})
	obs := recordActivations(t)
	h := newRefresherHarness(t, f, nil)
	f.put(file) // dropped after the construction-time apply: only the cycle sees it
	h.start()

	require.Equal(t, RefreshClassActivated, h.tick())

	assert.Equal(t, digestOf(file.Bundle), BundleStatus().FullDigest, "the file activated")
	assert.Zero(t, ch.count(), "the fetch leg waits for the next cycle")
	assert.Equal(t, 1, obs.count())
	assert.Len(t, f.store.historyRows(), 1)

	require.Equal(t, RefreshClassActivated, h.tick())

	assert.Equal(t, digestOf(fetched.Bundle), BundleStatus().FullDigest, "the fetch wins the next cycle")
	assert.Equal(t, 2, obs.count())
}

// B6: consecutive_failures follows the outcome the cycle reports, not the fetch
// leg's own answer.
func TestSolR1_ConsecutiveFailuresFollowTheMergedOutcome(t *testing.T) {
	ch := newTestChannel(t)
	ch.serve(releasesPath, http.StatusInternalServerError, []byte("boom"), nil)
	f := newFileAndFetchFixture(t, ch, true)
	h := newRefresherHarness(t, f, nil)
	h.start()
	require.Equal(t, RefreshClassUnavailable, h.tick())
	require.Equal(t, RefreshClassUnavailable, h.tick())
	require.Equal(t, 2, BundleStatus().Refresh.ConsecutiveFailures)

	// A new file refusal takes precedence over the fetch failure: the cycle
	// reports "rejected", which resets the counter.
	bad := signedCandidate(t, keyOperator, bundleOpts{Seq: 6, Tag: "bad"})
	bad.Bundle = flipByte(t, bad.Bundle, "bundle-marker")
	f.put(bad)

	assert.Equal(t, RefreshClassRejected, h.tick())

	info := BundleStatus()
	assert.Equal(t, RefreshOutcomeRejected, info.Refresh.LastOutcome)
	assert.Zero(t, info.Refresh.ConsecutiveFailures, "a rejected cycle resets the failure counter")

	// A valid dropped file activates over a failing channel: also a reset.
	require.Equal(t, RefreshClassUnavailable, h.tick())
	require.Equal(t, 1, BundleStatus().Refresh.ConsecutiveFailures)
	good := signedCandidate(t, keyOperator, bundleOpts{Seq: 7, Tag: "good"})
	f.put(good)

	assert.Equal(t, RefreshClassActivated, h.tick())

	assert.Zero(t, BundleStatus().Refresh.ConsecutiveFailures)
}

// A display-only edit (stale threshold, interval) is skipped by the idempotence
// short-circuit; the runtime must still retain the newer policy, or the next
// local reload re-applies the old values.
func TestSolR1_DisplayOnlyEditSurvivesReloadBundle(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "serving"}))

	f.sec.TPAStaleAfterDays = intp(60)
	f.sec.TPARefreshInterval = config.Duration(6 * time.Hour)
	f.run()

	info := ReloadBundle(f.log)

	require.NotNil(t, info.StaleAfterDays)
	assert.Equal(t, 60, *info.StaleAfterDays, "the threshold edit survives the reload")
	require.NotNil(t, info.Refresh)
	assert.Equal(t, int((6 * time.Hour).Seconds()), info.Refresh.IntervalSeconds)

	f.sec.TPAStaleAfterDays = intp(0)
	f.run()
	assert.Nil(t, ReloadBundle(f.log).StaleAfterDays, "a disabled threshold stays disabled after a reload")
}

// An unsigned file bundle serving must not short-cut an identical offered
// pair: the MustSign gate has to refuse it, and the fetch leg must not cache
// the validator of a pair that was refused.
func TestSolR1_UnsignedServingPairDoesNotBypassMustSign(t *testing.T) {
	f := newFileAndFetchFixture(t, nil, true)
	f.sec.TPAFetchURL = fetchChannelURL
	unsigned := unsignedCandidate(t, bundleOpts{Seq: 5, Tag: "unsigned-serving"})
	f.activate(unsigned)
	require.False(t, BundleStatus().SignatureVerified)
	f.run()

	offered := unsigned
	offered.Source = BundleSourceFetch
	offered.Path = "https://mirror.test"
	_, off := offerCandidate(offered, f.log)

	assert.Equal(t, RefreshOutcomeRejected, off.Outcome, "the signed-only gate refuses the unsigned pair")
	require.NotNil(t, off.Rejection)
	assert.Equal(t, RejectSignatureRequired, off.Rejection.Reason)
	assert.Equal(t, digestOf(unsigned.Bundle), BundleStatus().FullDigest, "the serving file bundle stays")

	// A verified serving pair still short-cuts.
	signed := signedCandidate(t, keyOperator, bundleOpts{Seq: 6, Tag: "signed-serving"})
	f.activate(signed)
	again := signed
	again.Source = BundleSourceFetch
	again.Path = "https://mirror.test"
	_, off = offerCandidate(again, f.log)
	assert.Equal(t, RefreshOutcomeUnchanged, off.Outcome)
	assert.False(t, off.Refused)
}

// A successful file leg owns the cycle outcome, but a fetch leg that cannot run
// must stay visible to the doctor.
func TestSolR1_FetchLegStateIsKeptApartFromTheMergedOutcome(t *testing.T) {
	f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorSigners = nil // no trusted signer: the fetch leg is disabled
		s.TPAFetchEnabled = true
		s.TPAFetchURL = fetchChannelURL
	}})
	f.run()
	h := newRefresherHarness(t, f, nil)
	h.start()
	f.put(unsignedCandidate(t, bundleOpts{Seq: 5, Tag: "day-1"}))

	require.Equal(t, RefreshClassActivated, h.tick())

	info := BundleStatus()
	require.NotNil(t, info.Refresh)
	assert.Equal(t, RefreshOutcomeActivated, info.Refresh.LastOutcome, "the cycle reports the file activation")
	assert.Equal(t, RefreshOutcomeDisabled, info.Refresh.FetchState)
	assert.Equal(t, RefreshReasonNoTrustedSigner, info.Refresh.FetchReason)
	warnings := BundleDoctorWarnings(info, time.Now())
	require.Len(t, warnings, 1, "%v", warnings)
	assert.Contains(t, warnings[0], "no trusted signer")

	f.put(unsignedCandidate(t, bundleOpts{Seq: 6, Tag: "day-2"}))
	require.Equal(t, RefreshClassActivated, h.tick())
	assert.Len(t, BundleDoctorWarnings(BundleStatus(), time.Now()), 1, "repeated file activations keep hiding nothing")
}

// B15: the construction-time configure is "startup". The scanner service
// re-applies the identical config right after it, which is not a reload.
func TestSolR1_IdenticalReapplyKeepsTheStartupTrigger(t *testing.T) {
	f := newRTFixture(t, rtOpts{})

	require.Equal(t, RefreshTriggerStartup, f.run().Refresh.LastTrigger)
	assert.Equal(t, RefreshTriggerStartup, f.run().Refresh.LastTrigger, "an identical re-apply is not a config reload")

	f.sec.TPAStaleAfterDays = intp(60)
	assert.Equal(t, RefreshTriggerConfig, f.run().Refresh.LastTrigger, "a changed config is a reload")
	assert.Equal(t, RefreshTriggerConfig, f.run().Refresh.LastTrigger)
}
