package scanner

import (
	"context"
	"net"
	"net/http"
	"time"
)

// The fetch adapter (Spec 087 FR-003/FR-009 to FR-015, Spec 101 US3 tasks
// B4 to B12). It turns the configured channel into a source-neutral Candidate
// and hands it to the existing verifier; it never verifies, parses or compiles
// a bundle itself. The real implementation lands with T331.

// FetchResolver is the DNS seam of the fetch adapter, satisfied by
// *net.Resolver. Tests inject a fake to exercise the dial-time egress rules
// without the network.
type FetchResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// fetchSettings is everything one fetch attempt needs, captured by the
// refresher from the live configuration before the (lock-free) network I/O
// starts. The field set may grow with T331; the refresher is its only builder.
//
//nolint:unused // built by the refresher once T331 lands
type fetchSettings struct {
	// Enabled mirrors security.tpa_fetch_enabled (with its env override).
	Enabled bool
	// URL is the configured channel URL. It is never logged or reported; only
	// its host is (B12).
	URL string
	// Version is the mcpproxy version for the User-Agent header.
	Version string
	// Policy is the live bundle policy: its active root's trust set decides
	// whether the leg may issue requests at all (B10).
	Policy *bundlePolicy
	// StateUnavailable is true while the anti-downgrade state store could not
	// be read (B10 state_unavailable).
	StateUnavailable bool
	// Watermark returns the active root's published watermark snapshot for the
	// pre-download hint skip (B4, B7); nil means no hint skip.
	Watermark func(authority string) (uint64, bool)
	// ETag is the validator cached for URL from a previous cycle ("" if none).
	ETag string
	// Client, Resolver and Now are the injected seams; nil selects the
	// defaults built by NewBundleRefresher.
	Client   *http.Client
	Resolver FetchResolver
	Now      func() time.Time
}

// fetchResult is the outcome of one fetch attempt. Outcome and Reason use the
// RefreshOutcome* and RefreshReason* vocabularies; Candidate is set only when
// a pair was downloaded and should be offered to the pipeline.
//
//nolint:unused // produced by fetchCandidate once T331 lands
type fetchResult struct {
	Outcome string
	Reason  string
	// Candidate is the downloaded pair (Source BundleSourceFetch, Path
	// "<scheme>://<host>"); nil for every non-offer outcome.
	Candidate *Candidate
	// ETag is the validator to remember for the next cycle.
	ETag string
	// RetryAt, when non-zero, is the earliest time the next attempt may run
	// (rate limiting); the refresher caps it at 24h.
	RetryAt time.Time
}

// fetchCandidate performs one fetch attempt against the configured channel and
// returns what to do with it. It issues zero requests unless every B10
// precondition holds.
//
// STUB (Spec 101 US3 wave 0): reports disabled without any request.
//
//nolint:unused // called by the refresher once T331 lands
func fetchCandidate(ctx context.Context, settings fetchSettings) fetchResult {
	_, _ = ctx, settings
	return fetchResult{Outcome: RefreshOutcomeDisabled, Reason: RefreshReasonFetchOff}
}

// egressClass classifies the address the configured host resolved to, for the
// dial-time egress predicate (B11).
//
//nolint:unused // used by egressAllowed once T331 lands
type egressClass int

//nolint:unused // classes are assigned by the dial control once T331 lands
const (
	// egressPublic: the configured host resolved to a public address.
	egressPublic egressClass = iota
	// egressLoopback: the configured host is a loopback literal or localhost.
	egressLoopback
	// egressPrivate: the configured host resolved to RFC1918, ULA or CGNAT
	// space (a LAN mirror).
	egressPrivate
)

// egressAllowed reports whether a connection (including a redirect hop) to ip
// is permitted given the class of the configured host: link-local (169.254/16,
// fe80::/10, metadata), unspecified and multicast addresses are always refused;
// loopback and private ranges only when the configured host itself resolved to
// that class (B11).
//
// STUB (Spec 101 US3 wave 0): permits everything until T331.
//
//nolint:unused // called by the dial-time control once T331 lands
func egressAllowed(ip net.IP, configuredClass egressClass) bool {
	_, _ = ip, configuredClass
	return true
}
