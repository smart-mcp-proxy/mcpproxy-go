package scanner

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// T326 (Spec 087 FR-012, SC-005): the fetch leg issues ZERO requests unless
// every B10 precondition holds, and says why it did not.
func TestFetchGate_ZeroRequestsUnlessEverythingHolds(t *testing.T) {
	ch := newTestChannel(t)
	ch.publishRelease(2, "never-fetched")
	good := ch.srv.URL + releasesPath

	type tc struct {
		name   string
		mutate func(*fetchSettings)
		sec    func(*config.SecurityConfig)
		reason string
	}
	tests := []tc{
		{name: "fetch is off", mutate: func(s *fetchSettings) { s.Enabled = false }, reason: RefreshReasonFetchOff},
		{name: "an empty URL", mutate: func(s *fetchSettings) { s.URL = "" }, reason: RefreshReasonNoURL},
		{name: "a relative URL", mutate: func(s *fetchSettings) { s.URL = "/repos/a/b/releases/latest" }, reason: RefreshReasonInvalidURL},
		{name: "not a URL at all", mutate: func(s *fetchSettings) { s.URL = "%%%" }, reason: RefreshReasonInvalidURL},
		{name: "an ftp URL", mutate: func(s *fetchSettings) { s.URL = "ftp://mirror.test/x" }, reason: RefreshReasonInvalidURL},
		{name: "http to a non-loopback host", mutate: func(s *fetchSettings) { s.URL = "http://mirror.test/repos/a/b/releases/latest" }, reason: RefreshReasonInvalidURL},
		{name: "userinfo", mutate: func(s *fetchSettings) { s.URL = "https://user:pw@mirror.test/x" }, reason: RefreshReasonInvalidURL},
		{name: "a query", mutate: func(s *fetchSettings) { s.URL = "https://mirror.test/x?a=b" }, reason: RefreshReasonInvalidURL},
		{name: "a fragment", mutate: func(s *fetchSettings) { s.URL = "https://mirror.test/x#frag" }, reason: RefreshReasonInvalidURL},
		{name: "no host", mutate: func(s *fetchSettings) { s.URL = "https:///x" }, reason: RefreshReasonInvalidURL},
		{
			name:   "the publisher root with the empty built-in signer set",
			sec:    func(s *config.SecurityConfig) { s.TPAActiveRoot = config.TPARootPublisher },
			reason: RefreshReasonNoTrustedSigner,
		},
		{
			name:   "the operator root with no operator signers",
			sec:    func(s *config.SecurityConfig) { s.TPAOperatorSigners = nil },
			reason: RefreshReasonNoTrustedSigner,
		},
		{
			name:   "the operator root whose signers are all invalid",
			sec:    func(s *config.SecurityConfig) { s.TPAOperatorSigners = []string{"not-a-key"} },
			reason: RefreshReasonNoTrustedSigner,
		},
		{
			name:   "an unrecognised root",
			sec:    func(s *config.SecurityConfig) { s.TPAActiveRoot = "root-of-all-evil" },
			reason: RefreshReasonNoTrustedSigner,
		},
		{name: "an unreadable state store", mutate: func(s *fetchSettings) { s.StateUnavailable = true }, reason: RefreshReasonStateUnavailable},
		{name: "no policy", mutate: func(s *fetchSettings) { s.Policy = nil }, reason: RefreshReasonNoTrustedSigner},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := ch.count()
			settings := fetchTestSettings(t, good, tt.mutate)
			if tt.sec != nil {
				sec := &config.SecurityConfig{
					TPAActiveRoot: config.TPARootOperator, TPAOperatorSigners: []string{keyOperator.b64},
					TPAOperatorBaseline: u64p(1), TPAFetchEnabled: true, TPAFetchURL: good,
				}
				tt.sec(sec)
				settings.Policy = policyFromConfig(sec)
			}

			res := fetchCandidate(fetchCtx(t), settings)

			assert.Equal(t, RefreshOutcomeDisabled, res.Outcome)
			assert.Equal(t, tt.reason, res.Reason)
			assert.Nil(t, res.Candidate)
			assert.Equal(t, before, ch.count(), "a disabled leg must issue zero requests")
		})
	}
}

func TestFetchGate_LoopbackHostsMayUseHTTP(t *testing.T) {
	for _, url := range []string{
		"http://127.0.0.1:8080/repos/a/b/releases/latest",
		"http://localhost:8080/repos/a/b/releases/latest",
		"http://[::1]:8080/repos/a/b/releases/latest",
		"https://mirror.test/repos/a/b/releases/latest",
	} {
		_, err := parseFetchURL(url)
		assert.NoError(t, err, url)
	}
}

// Zero requests across a full simulated day, whatever the cycle count, when the
// fetch is disabled; and the same for an unreadable state store.
func TestFetchGate_RefresherIssuesNoRequestWhileDisabled(t *testing.T) {
	ch := newTestChannel(t)
	ch.publishRelease(2, "never-fetched")

	t.Run("fetch disabled for a simulated day", func(t *testing.T) {
		f := newFetchFixture(t, func(s *config.SecurityConfig) {
			s.TPAFetchEnabled = false
			s.TPAFetchURL = ch.srv.URL + releasesPath
		})
		h := newRefresherHarness(t, f, nil)
		h.start()
		for i := 0; i < 24; i++ {
			h.clock.Advance(time.Hour)
			h.tick()
		}
		assert.Zero(t, ch.count())
		info := BundleStatus()
		require.NotNil(t, info.Refresh)
		assert.Equal(t, RefreshOutcomeDisabled, info.Refresh.LastOutcome)
		assert.Equal(t, RefreshReasonFetchOff, info.Refresh.LastReason)
	})

	t.Run("an unreadable state store", func(t *testing.T) {
		f := newFetchFixture(t, func(s *config.SecurityConfig) { s.TPAFetchURL = ch.srv.URL + releasesPath })
		f.store.failLoad = assert.AnError
		h := newRefresherHarness(t, f, nil)
		h.start()
		h.tick()
		assert.Zero(t, ch.count())
		info := BundleStatus()
		require.NotNil(t, info.Refresh)
		assert.Equal(t, RefreshOutcomeDisabled, info.Refresh.LastOutcome)
		assert.Equal(t, RefreshReasonStateUnavailable, info.Refresh.LastReason)
	})

	t.Run("the publisher root with the empty signer set", func(t *testing.T) {
		f := newFetchFixture(t, func(s *config.SecurityConfig) {
			s.TPAActiveRoot = config.TPARootPublisher
			s.TPAFetchURL = ch.srv.URL + releasesPath
		})
		h := newRefresherHarness(t, f, nil)
		h.start()
		h.tick()
		assert.Zero(t, ch.count())
		assert.Equal(t, RefreshReasonNoTrustedSigner, BundleStatus().Refresh.LastReason)
	})
}
