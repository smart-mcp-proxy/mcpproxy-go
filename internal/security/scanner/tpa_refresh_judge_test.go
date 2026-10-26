package scanner

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// US3 judge findings 2 to 4 (T321, T327): cycle-level behaviour of the ETag
// cache and of the merged cycle outcome.

// A pair refused at the pipeline must be re-offered on a later cycle. In direct
// mode the ETag belongs to the bundle only, so remembering it after a refusal
// would turn every later cycle into a 304 that never looks at the sidecar again.
func TestRefreshScheduler_RefusedDirectPairIsReofferedWhenTheSidecarAppears(t *testing.T) {
	const path = "/mirror/scanner-bundle.json"
	ch := newTestChannel(t)
	pair := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "direct"})
	ch.handle(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"b1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"b1"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pair.Bundle)
	})
	// scanner-bundle.json.sig is not uploaded yet: 404.
	f := newFetchFixture(t, func(s *config.SecurityConfig) { s.TPAFetchURL = ch.srv.URL + path })
	h := newRefresherHarness(t, f, nil)
	h.start()

	assert.Equal(t, RefreshClassRejected, h.tick(), "the unsigned pair is refused as signature_required")
	require.NotNil(t, BundleStatus().LastRejection)
	assert.Equal(t, RejectSignatureRequired, BundleStatus().LastRejection.Reason)

	h.tick() // still no sidecar: the same pair is offered again (negative cache)

	ch.serve(path+".sig", http.StatusOK, pair.Sidecar, nil) // the mirror operator finishes the upload

	assert.Equal(t, RefreshClassActivated, h.tick(), "the sidecar is fetched again and the pair now activates")
	assert.Equal(t, digestOf(pair.Bundle), BundleStatus().FullDigest)
}

// A persistent file-leg refusal must not mask what the fetch leg reports.
func TestRefreshScheduler_PersistentFileRefusalDoesNotMaskTheFetchLeg(t *testing.T) {
	ch := newTestChannel(t)
	f := newFileAndFetchFixture(t, ch, true)
	h := newRefresherHarness(t, f, nil)
	h.start()
	ch.publishRelease(5, "fetched")
	require.Equal(t, RefreshClassActivated, h.tick())

	f.put(signedCandidate(t, keyOperator, bundleOpts{Seq: 3, Tag: "stale-file"}))
	assert.Equal(t, RefreshClassRejected, h.tick(), "the stale file is a NEW refusal on this cycle")
	require.NotNil(t, BundleStatus().LastRejection)
	require.Equal(t, RejectDowngrade, BundleStatus().LastRejection.Reason)

	// The same refusal again: the fetch leg's own answer (the listing's
	// sequence is at the watermark already) is the cycle's.
	assert.Equal(t, RefreshClassUnchanged, h.tick())
	assert.Equal(t, RefreshOutcomeUnchanged, BundleStatus().Refresh.LastOutcome)

	// A fetch failure is visible while the stale file is still refused.
	ch.serve(releasesPath, http.StatusInternalServerError, []byte("boom"), nil)
	assert.Equal(t, RefreshClassUnavailable, h.tick())
	assert.Equal(t, RefreshOutcomeFetchUnavailable, BundleStatus().Refresh.LastOutcome)
	assert.Equal(t, RefreshReasonHTTPStatus, BundleStatus().Refresh.LastReason)
}

// A cycle already in flight when the settings change must not write its stale
// validator back after the Nudge cleared the cache.
func TestRefreshScheduler_NudgeDuringACycleKeepsTheStaleETagOut(t *testing.T) {
	ch := newTestChannel(t)
	f := newChannelFixture(t, ch)
	h := newRefresherHarness(t, f, nil)
	h.start()
	url := ch.srv.URL + releasesPath
	h.ref.rememberETag(url, `"old"`, h.ref.currentGeneration())

	reached, release := make(chan struct{}), make(chan struct{})
	ch.handle(releasesPath, func(w http.ResponseWriter, r *http.Request) {
		close(reached)
		<-release
		w.WriteHeader(http.StatusNotModified)
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ref.Refresh(t.Context(), f.log)
	}()
	<-reached

	h.reload(func(s *config.SecurityConfig) { s.TPAOperatorBaseline = u64p(2) }) // fingerprint input: bumps the generation, clears the cache
	require.Empty(t, h.ref.etagFor(url), "the nudge cleared the validators")
	close(release)
	<-done

	assert.Empty(t, h.ref.etagFor(url), "the 304 of the superseded cycle must not resurrect the old validator")
}
