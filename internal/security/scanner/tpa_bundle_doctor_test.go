package scanner

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The B16 truth table (Spec 101 US3 tasks): which BundleInfo states raise a
// doctor warning, and that the strings carry no digest, path, URL or host.

var doctorNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func doctorIntP(n int) *int { return &n }

// doctorStaleInfo is a stale bundle from the given source, with 81 days of age
// against the default 30-day threshold.
func doctorStaleInfo(source string) BundleInfo {
	return BundleInfo{
		Source:         source,
		Path:           "/etc/mcpproxy/private/bundle.json",
		FullDigest:     strings.Repeat("ab", 32),
		Fingerprint:    strings.Repeat("ab", 6),
		StalenessDays:  doctorIntP(81),
		Stale:          true,
		StaleAfterDays: doctorIntP(30),
		LoadedAt:       doctorNow.Add(-72 * time.Hour),
	}
}

func doctorRejection(reason string, at time.Time) *BundleRejection {
	return &BundleRejection{
		Reason: reason,
		// Detail is deliberately full of things the warning must never echo.
		Detail: "digest " + strings.Repeat("cd", 32) + " at /var/lib/tpa/private-name.json via https://secret-host.example.test/s3cr3t-path",
		At:     at,
	}
}

func TestBundleDoctorWarnings_TruthTable(t *testing.T) {
	activated := doctorNow.Add(-48 * time.Hour)
	newer := doctorNow.Add(-1 * time.Hour)
	older := doctorNow.Add(-96 * time.Hour)

	cases := []struct {
		name string
		info func() BundleInfo
		// want is a substring each returned warning must match, in order;
		// len(want) is the exact expected count.
		want []string
	}{
		{
			name: "healthy embedded default raises nothing",
			info: func() BundleInfo { return BundleInfo{Source: BundleSourceEmbedded, LoadedAt: doctorNow} },
		},
		{
			name: "stale external file bundle",
			info: func() BundleInfo { return doctorStaleInfo(BundleSourceFile) },
			want: []string{"stale"},
		},
		{
			name: "stale fetched bundle",
			info: func() BundleInfo { return doctorStaleInfo(BundleSourceFetch) },
			want: []string{"stale"},
		},
		{
			name: "stale embedded corpus on an install that never configured a refresh stays status-only",
			info: func() BundleInfo { return doctorStaleInfo(BundleSourceEmbedded) },
		},
		{
			name: "stale embedded corpus with a refresh block but fetch off stays status-only",
			info: func() BundleInfo {
				i := doctorStaleInfo(BundleSourceEmbedded)
				i.Refresh = &BundleRefreshStatus{FetchEnabled: false, IntervalSeconds: 86400, LastOutcome: RefreshOutcomeUnchanged}
				return i
			},
		},
		{
			name: "stale embedded corpus with fetch enabled raises the stale warning",
			info: func() BundleInfo {
				i := doctorStaleInfo(BundleSourceEmbedded)
				i.Refresh = &BundleRefreshStatus{FetchEnabled: true, FetchHost: "mirror.example.test", IntervalSeconds: 86400, LastOutcome: RefreshOutcomeUnchanged}
				return i
			},
			want: []string{"stale"},
		},
		{
			name: "fresh file bundle raises nothing",
			info: func() BundleInfo {
				return BundleInfo{Source: BundleSourceFile, StalenessDays: doctorIntP(3), StaleAfterDays: doctorIntP(30), LastActivatedAt: &activated, LoadedAt: activated}
			},
		},
		{
			name: "stuck: rejection newer than the last activation",
			info: func() BundleInfo {
				return BundleInfo{
					Source: BundleSourceFile, LastActivatedAt: &activated, LoadedAt: activated,
					LastRejection: doctorRejection(RejectTamper, newer),
				}
			},
			want: []string{"tamper"},
		},
		{
			name: "rejection older than the last activation is history, not a problem",
			info: func() BundleInfo {
				return BundleInfo{
					Source: BundleSourceFile, LastActivatedAt: &activated, LoadedAt: activated,
					LastRejection: doctorRejection(RejectTamper, older),
				}
			},
		},
		{
			name: "stuck: gate_failure is reported",
			info: func() BundleInfo {
				return BundleInfo{
					Source: BundleSourceFetch, LastActivatedAt: &activated, LoadedAt: activated,
					LastRejection: doctorRejection(RejectGateFailure, newer),
				}
			},
			want: []string{"gate_failure"},
		},
		{
			name: "quiet set: a fresh downgrade refusal is quiet",
			info: func() BundleInfo {
				return BundleInfo{Source: BundleSourceFile, LastActivatedAt: &activated, LoadedAt: activated, LastRejection: doctorRejection(RejectDowngrade, newer)}
			},
		},
		{
			name: "quiet set: a fresh deny_listed refusal is quiet",
			info: func() BundleInfo {
				return BundleInfo{Source: BundleSourceFile, LastActivatedAt: &activated, LoadedAt: activated, LastRejection: doctorRejection(RejectDenyListed, newer)}
			},
		},
		{
			name: "embedded serving: a rejection newer than process start is stuck on last-known-good",
			info: func() BundleInfo {
				return BundleInfo{
					Source: BundleSourceEmbedded, LoadedAt: activated,
					LastRejection: doctorRejection(RejectSignatureRequired, newer),
				}
			},
			want: []string{"signature_required"},
		},
		{
			name: "embedded serving: a rejection older than process start is not reported",
			info: func() BundleInfo {
				return BundleInfo{
					Source: BundleSourceEmbedded, LoadedAt: activated,
					LastRejection: doctorRejection(RejectSignatureRequired, older),
				}
			},
		},
		{
			name: "three consecutive fetch failures",
			info: func() BundleInfo {
				return BundleInfo{
					Source: BundleSourceFile, LoadedAt: activated, LastActivatedAt: &activated,
					Refresh: &BundleRefreshStatus{
						FetchEnabled: true, FetchHost: "mirror.example.test", IntervalSeconds: 86400,
						LastOutcome: RefreshOutcomeFetchUnavailable, LastReason: RefreshReasonNetworkError,
						ConsecutiveFailures: 3,
					},
				}
			},
			want: []string{"3"},
		},
		{
			name: "two consecutive fetch failures are not yet a warning",
			info: func() BundleInfo {
				return BundleInfo{
					Source: BundleSourceFile, LoadedAt: activated, LastActivatedAt: &activated,
					Refresh: &BundleRefreshStatus{
						FetchEnabled: true, FetchHost: "mirror.example.test", IntervalSeconds: 86400,
						LastOutcome: RefreshOutcomeFetchUnavailable, LastReason: RefreshReasonTimeout,
						ConsecutiveFailures: 2,
					},
				}
			},
		},
		{
			name: "fetch enabled but no URL",
			info: func() BundleInfo {
				return BundleInfo{Source: BundleSourceEmbedded, LoadedAt: doctorNow, Refresh: &BundleRefreshStatus{
					FetchEnabled: true, IntervalSeconds: 86400, LastOutcome: RefreshOutcomeDisabled, LastReason: RefreshReasonNoURL, FetchState: RefreshOutcomeDisabled, FetchReason: RefreshReasonNoURL,
				}}
			},
			want: []string{"tpa_fetch_url"},
		},
		{
			name: "fetch enabled with an invalid URL",
			info: func() BundleInfo {
				return BundleInfo{Source: BundleSourceEmbedded, LoadedAt: doctorNow, Refresh: &BundleRefreshStatus{
					FetchEnabled: true, IntervalSeconds: 86400, LastOutcome: RefreshOutcomeDisabled, LastReason: RefreshReasonInvalidURL, FetchState: RefreshOutcomeDisabled, FetchReason: RefreshReasonInvalidURL,
				}}
			},
			want: []string{"tpa_fetch_url"},
		},
		{
			name: "fetch enabled with no trusted signer",
			info: func() BundleInfo {
				return BundleInfo{Source: BundleSourceEmbedded, LoadedAt: doctorNow, Refresh: &BundleRefreshStatus{
					FetchEnabled: true, IntervalSeconds: 86400, LastOutcome: RefreshOutcomeDisabled, LastReason: RefreshReasonNoTrustedSigner, FetchState: RefreshOutcomeDisabled, FetchReason: RefreshReasonNoTrustedSigner,
				}}
			},
			want: []string{"signer"},
		},
		{
			name: "a file activation owns the cycle outcome but the inactive fetch leg is still reported",
			info: func() BundleInfo {
				return BundleInfo{Source: BundleSourceFile, LoadedAt: doctorNow, Refresh: &BundleRefreshStatus{
					FetchEnabled: true, IntervalSeconds: 86400, LastOutcome: RefreshOutcomeActivated,
					FetchState: RefreshOutcomeDisabled, FetchReason: RefreshReasonNoTrustedSigner,
				}}
			},
			want: []string{"signer"},
		},
		{
			name: "fetch disabled by configuration is not a problem",
			info: func() BundleInfo {
				return BundleInfo{Source: BundleSourceEmbedded, LoadedAt: doctorNow, Refresh: &BundleRefreshStatus{
					FetchEnabled: false, IntervalSeconds: 86400, LastOutcome: RefreshOutcomeDisabled, LastReason: RefreshReasonFetchOff, FetchState: RefreshOutcomeDisabled, FetchReason: RefreshReasonFetchOff,
				}}
			},
		},
		{
			name: "state_unavailable is not a configuration error and is not reported by this source",
			info: func() BundleInfo {
				return BundleInfo{Source: BundleSourceEmbedded, LoadedAt: doctorNow, Refresh: &BundleRefreshStatus{
					FetchEnabled: true, IntervalSeconds: 86400, LastOutcome: RefreshOutcomeDisabled, LastReason: RefreshReasonStateUnavailable, FetchState: RefreshOutcomeDisabled, FetchReason: RefreshReasonStateUnavailable,
				}}
			},
		},
		{
			name: "all three conditions at once give exactly three warnings, stale first",
			info: func() BundleInfo {
				i := doctorStaleInfo(BundleSourceFetch)
				i.LastActivatedAt = &activated
				i.LastRejection = doctorRejection(RejectKeyNotTrusted, newer)
				i.Refresh = &BundleRefreshStatus{
					FetchEnabled: true, FetchHost: "mirror.example.test", IntervalSeconds: 86400,
					LastOutcome: RefreshOutcomeDisabled, LastReason: RefreshReasonNoTrustedSigner, FetchState: RefreshOutcomeDisabled, FetchReason: RefreshReasonNoTrustedSigner,
					ConsecutiveFailures: 5,
				}
				return i
			},
			want: []string{"stale", "key_not_trusted", "signer"},
		},
	}

	hex12 := regexp.MustCompile(`[0-9a-f]{12,}`)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := tc.info()
			got := BundleDoctorWarnings(info, doctorNow)
			require.Len(t, got, len(tc.want), "warnings: %q", got)
			for i, sub := range tc.want {
				assert.Contains(t, strings.ToLower(got[i]), strings.ToLower(sub), "warning %d", i)
			}
			for _, w := range got {
				assert.LessOrEqual(t, len(w), 240, "a doctor warning is one short line: %q", w)
				assert.NotRegexp(t, hex12, w, "no digest or fingerprint")
				assert.NotContains(t, w, "/", "no path or URL")
				assert.NotContains(t, w, "\n")
				if info.Path != "" {
					assert.NotContains(t, w, info.Path)
				}
				assert.NotContains(t, w, "example.test", "no host")
				assert.NotContains(t, w, "s3cr3t")
				assert.NotContains(t, w, "private-name")
			}
		})
	}
}

// The stale warning names the age and the threshold so the operator knows how
// far past the line the corpus is.
func TestBundleDoctorWarnings_StaleNamesAgeAndThreshold(t *testing.T) {
	got := BundleDoctorWarnings(doctorStaleInfo(BundleSourceFile), doctorNow)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "81")
	assert.Contains(t, got[0], "30")
	assert.Contains(t, got[0], "mcpproxy security bundle refresh", "the warning names the fix")
}

// A threshold of 0 disables the flag (info.Stale is false), so nothing is raised
// whatever the age.
func TestBundleDoctorWarnings_ThresholdZeroNeverWarnsStale(t *testing.T) {
	info := doctorStaleInfo(BundleSourceFile)
	info.Stale = false
	info.StaleAfterDays = doctorIntP(0)
	assert.Empty(t, BundleDoctorWarnings(info, doctorNow))
}

// Calling the registered source on a runtime that was never published installs
// the embedded default lazily WITHOUT holding bundleRT.mu first. The server
// registers exactly this composition with AddRuntimeWarningSource, so it must
// return, not deadlock.
func TestBundleDoctorWarnings_SourceCompositionOnNeverPublishedRuntimeDoesNotHang(t *testing.T) {
	resetBundleRuntimeForTest(t)

	done := make(chan []string, 1)
	go func() { done <- BundleDoctorWarnings(BundleStatus(), time.Now()) }()
	select {
	case got := <-done:
		assert.Empty(t, got, "a fresh embedded default raises no warning")
	case <-time.After(10 * time.Second):
		t.Fatal("BundleDoctorWarnings(BundleStatus(), now) hung on a never-published runtime")
	}
}
