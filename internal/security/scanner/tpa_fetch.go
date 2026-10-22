package scanner

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// The fetch adapter (Spec 087 FR-003/FR-009 to FR-015, Spec 101 US3 tasks
// B4 to B12). It turns the configured channel into a source-neutral Candidate
// and hands it to the existing verifier; it never verifies, parses or compiles
// a bundle itself. Everything here is standard library, performs its network
// I/O with no lock held, and never lets a URL path, query or redirect target
// reach a log line, a status field or an error string (B12).

// FetchResolver is the DNS seam of the fetch adapter, satisfied by
// *net.Resolver. Tests inject a fake to exercise the egress rules without the
// network.
type FetchResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// fetchSettings is everything one fetch attempt needs, captured by the
// refresher from the live configuration before the (lock-free) network I/O
// starts.
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
	// Watermark returns the sequence the active root already satisfies, for the
	// pre-download hint skip (B4, B7); nil means no hint skip. A release at or
	// below it is not downloaded.
	Watermark func(authority string) (uint64, bool)
	// ETag is the validator cached for URL from a previous cycle ("" if none).
	ETag string
	// Client, Resolver and Now are the injected seams; nil selects the
	// defaults. A supplied Client is used as is, except that its redirect
	// policy is replaced by the egress rules.
	Client   *http.Client
	Resolver FetchResolver
	Now      func() time.Time
	// Proxy selects the proxy for a request of the production transport; nil
	// selects http.ProxyFromEnvironment. A test seam: the standard function
	// reads the environment once per process.
	Proxy func(*http.Request) (*url.URL, error)
	// Log, when set, receives host-only diagnostics (redirect hops by host).
	Log *zap.Logger
}

// fetchOutcomeCandidate is the internal outcome of an attempt that downloaded a
// pair to offer to the pipeline. It is not part of the closed status
// vocabulary: the refresher turns it into activated, unchanged or rejected.
const fetchOutcomeCandidate = "candidate"

// fetchResult is the outcome of one fetch attempt. Outcome and Reason use the
// RefreshOutcome* and RefreshReason* vocabularies (plus
// fetchOutcomeCandidate); Candidate is set only when a pair was downloaded and
// should be offered to the pipeline.
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
	// Detail is a short host-only note for logs; it never carries a URL, a
	// path or a query (B12).
	Detail string
}

const (
	// fetchRedirectLimit is the number of redirect hops a request may follow.
	fetchRedirectLimit = 5
	// maxReleasesJSONBytes caps the Releases-API response body (B4).
	maxReleasesJSONBytes = 1 << 20
	// fetchHeaderTimeout bounds the wait for response headers; a trickled body
	// is cut by the per-cycle deadline the refresher owns.
	fetchHeaderTimeout = 10 * time.Second
)

// errCycleDeadline is the cancellation cause of a refresh cycle that ran out of
// its total time. fetchCandidate reports it as the timeout reason.
var errCycleDeadline = errors.New("refresh cycle deadline exceeded")

var (
	releasesPathRE = regexp.MustCompile(`^/repos/[^/]+/[^/]+/releases/latest$`)
	releaseAssetRE = regexp.MustCompile(`^scanner-bundle-([0-9]{1,20})\.json$`)
)

// errInvalidFetchURL carries no detail on purpose: the URL is never echoed.
var errInvalidFetchURL = errors.New("the fetch URL is not acceptable")

// isLoopbackHost reports whether host is localhost or a loopback IP literal.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// parseFetchURL validates the configured channel URL (B1, B11): https, or http
// only for a loopback literal or localhost; a host; no userinfo, query or
// fragment. The error never echoes the URL.
func parseFetchURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errInvalidFetchURL
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return nil, errInvalidFetchURL
		}
	default:
		return nil, errInvalidFetchURL
	}
	return u, nil
}

// parseAssetURL validates a download URL taken from the (untrusted) release
// listing: the same scheme rule, no userinfo; a query is allowed.
func parseAssetURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, errInvalidFetchURL
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return nil, errInvalidFetchURL
		}
	default:
		return nil, errInvalidFetchURL
	}
	return u, nil
}

// isReleasesPath reports whether path selects the GitHub Releases channel
// shape (/repos/<owner>/<repo>/releases/latest); any other path is a direct
// URL (B4). Detection looks at the path only, so httptest can exercise both.
func isReleasesPath(path string) bool { return releasesPathRE.MatchString(path) }

func disabledResult(reason string) fetchResult {
	return fetchResult{Outcome: RefreshOutcomeDisabled, Reason: reason}
}

// fetchGate applies the B10 preconditions: the parsed channel URL when the leg
// may issue requests, otherwise the RefreshReason* that says why not. The
// refresher also asks it for a cycle that skips the leg, so the reason stays
// visible.
func fetchGate(settings fetchSettings) (*url.URL, string) {
	if !settings.Enabled {
		return nil, RefreshReasonFetchOff
	}
	if settings.URL == "" {
		return nil, RefreshReasonNoURL
	}
	u, err := parseFetchURL(settings.URL)
	if err != nil {
		return nil, RefreshReasonInvalidURL
	}
	if settings.Policy == nil || settings.Policy.ActiveRootKeys == 0 {
		return nil, RefreshReasonNoTrustedSigner
	}
	if settings.StateUnavailable {
		return nil, RefreshReasonStateUnavailable
	}
	return u, ""
}

// fetchCandidate performs one fetch attempt against the configured channel and
// returns what to do with it. It issues zero requests unless every B10
// precondition holds.
func fetchCandidate(ctx context.Context, settings fetchSettings) fetchResult {
	u, reason := fetchGate(settings)
	if reason != "" {
		return disabledResult(reason)
	}
	a := &fetchAttempt{s: settings, u: u, host: u.Hostname(), now: settings.Now, resolver: settings.Resolver, log: settings.Log}
	if a.now == nil {
		a.now = time.Now
	}
	if a.resolver == nil {
		a.resolver = net.DefaultResolver
	}
	if a.log == nil {
		a.log = zap.NewNop()
	}
	return a.run(ctx)
}

// fetchAttempt is the working state of one fetchCandidate call.
type fetchAttempt struct {
	s        fetchSettings
	u        *url.URL
	host     string
	now      func() time.Time
	resolver FetchResolver
	log      *zap.Logger
	class    egressClass
	client   *http.Client
	// retry is the latest backoff any response of this attempt announced. One
	// attempt can make several requests (listing, bundle, sidecar); a later
	// failure or a success must not discard what an earlier response asked for
	// (B6), so every return path folds it into the result.
	retry time.Time
}

func (a *fetchAttempt) run(ctx context.Context) fetchResult {
	res := a.runOnce(ctx)
	if a.retry.After(res.RetryAt) {
		res.RetryAt = a.retry
	}
	return res
}

func (a *fetchAttempt) runOnce(ctx context.Context) fetchResult {
	class, err := a.configuredClass(ctx)
	if err != nil {
		return a.failure(ctx, err)
	}
	a.class = class
	a.client = a.buildClient()
	if isReleasesPath(a.u.Path) {
		return a.releases(ctx)
	}
	return a.direct(ctx)
}

// --- egress -------------------------------------------------------------------

// egressClass classifies the address the configured host resolved to, for the
// egress predicate (B11).
type egressClass int

const (
	// egressPublic: the configured host resolved to a public address.
	egressPublic egressClass = iota
	// egressLoopback: the configured host is a loopback literal or localhost.
	egressLoopback
	// egressPrivate: the configured host resolved to RFC1918, ULA or CGNAT
	// space (a LAN mirror).
	egressPrivate
)

// egressError is a refused connection or hop. Its text carries the host only.
type egressError struct{ host, why string }

func (e *egressError) Error() string { return "connection to " + e.host + " refused: " + e.why }

var (
	errTooManyRedirects  = errors.New("too many redirects")
	errRedirectDowngrade = errors.New("redirect from https to http refused")
)

var cgnatNet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// alwaysRefused reports whether ip may never be connected to, whatever the
// configured host is: link-local (169.254/16 including cloud metadata,
// fe80::/10), unspecified, multicast, "this network" and reserved space.
func alwaysRefused(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
		if v4[0] == 0 || v4[0] >= 240 {
			return true
		}
	}
	return ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast()
}

func isPrivateAddr(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		return v4.IsPrivate() || cgnatNet.Contains(v4)
	}
	return ip.IsPrivate()
}

// classOf is the egress class of an address a configured host may legitimately
// resolve to; false for addresses that are never acceptable.
func classOf(ip net.IP) (egressClass, bool) {
	switch {
	case ip == nil || alwaysRefused(ip):
		return 0, false
	case ip.IsLoopback():
		return egressLoopback, true
	case isPrivateAddr(ip):
		return egressPrivate, true
	}
	return egressPublic, true
}

// egressAllowed reports whether a connection (including a redirect hop) to ip
// is permitted given the class of the configured host: link-local (169.254/16,
// fe80::/10, metadata), unspecified and multicast addresses are always refused;
// loopback and private ranges only when the configured host itself resolved to
// that class (B11).
func egressAllowed(ip net.IP, configuredClass egressClass) bool {
	c, ok := classOf(ip)
	if !ok {
		return false
	}
	if c == egressPublic {
		return true
	}
	return c == configuredClass
}

// egressDialControl returns the net.Dialer.Control function enforcing the
// egress predicate at connect time, after name resolution, so DNS rebinding
// cannot slip an inward address past the application-layer check (B11).
func egressDialControl(class egressClass) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return &egressError{host: "unknown", why: "unparsable address"}
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return &egressError{host: "unknown", why: "unparsable address"}
		}
		if !egressAllowed(ip, class) {
			return &egressError{host: host, why: "address class not permitted"}
		}
		return nil
	}
}

// resolveHost returns the addresses host stands for: the literal itself, the
// loopback pair for localhost, or the resolver's answer.
func (a *fetchAttempt) resolveHost(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	if strings.EqualFold(host, "localhost") {
		return []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}, nil
	}
	addrs, err := a.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, ad := range addrs {
		ips = append(ips, ad.IP)
	}
	return ips, nil
}

// configuredClass classifies the configured host: public if any address is
// public, else private if any is, else loopback. Every address is then still
// checked against that class by checkTarget and by the dial control, so a
// mixed answer that includes an inward address is refused, never trusted.
func (a *fetchAttempt) configuredClass(ctx context.Context) (egressClass, error) {
	ips, err := a.resolveHost(ctx, a.host)
	if err != nil {
		return 0, err
	}
	var have [3]bool
	for _, ip := range ips {
		if c, ok := classOf(ip); ok {
			have[c] = true
		}
	}
	switch {
	case have[egressPublic]:
		return egressPublic, nil
	case have[egressPrivate]:
		return egressPrivate, nil
	case have[egressLoopback]:
		return egressLoopback, nil
	}
	return 0, &egressError{host: a.host, why: "no acceptable address"}
}

// checkTarget is the application-layer egress check for a request or redirect
// hop to u: the scheme rule, then every address u's host stands for must be
// permitted. It applies equally when a proxy carries the connection, where the
// dial-time control only ever sees the proxy.
func (a *fetchAttempt) checkTarget(ctx context.Context, u *url.URL) error {
	host := u.Hostname()
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(host) {
			return &egressError{host: host, why: "plain http is only allowed for loopback"}
		}
	default:
		return &egressError{host: host, why: "unsupported scheme"}
	}
	ips, err := a.resolveHost(ctx, host)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return &egressError{host: host, why: "no address"}
	}
	for _, ip := range ips {
		if !egressAllowed(ip, a.class) {
			return &egressError{host: host, why: "address class not permitted"}
		}
	}
	return nil
}

// checkRedirect is http.Client.CheckRedirect: at most fetchRedirectLimit hops,
// no https-to-http downgrade, and the egress check on every hop. Hops may cross
// hosts (GitHub asset downloads redirect off api.github.com); integrity comes
// from the signature, not the channel.
func (a *fetchAttempt) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > fetchRedirectLimit {
		return errTooManyRedirects
	}
	if prev := via[len(via)-1]; prev.URL.Scheme == "https" && req.URL.Scheme != "https" {
		return errRedirectDowngrade
	}
	if err := a.checkTarget(req.Context(), req.URL); err != nil {
		return err
	}
	a.log.Debug("TPA bundle fetch redirect", zap.String("host", req.URL.Hostname()), zap.Int("hop", len(via)))
	return nil
}

// buildClient returns the client for this attempt. The supplied client is
// copied so the egress redirect policy can be attached without touching it.
func (a *fetchAttempt) buildClient() *http.Client {
	var c http.Client
	if a.s.Client != nil {
		c = *a.s.Client
	} else {
		c.Transport = a.buildTransport()
	}
	c.CheckRedirect = a.checkRedirect
	// Every response of the chain passes through the observer, redirects
	// included: http.Client.Do hands back only the last one, so a backoff a
	// 3xx announced would otherwise be lost, and one on a redirect the client
	// then refused to follow even more so (B6).
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.Transport = backoffObserver{base: base, a: a}
	return &c
}

// backoffObserver is the RoundTripper that records the rate-limit headers of
// every response, intermediate redirects included, into the attempt.
type backoffObserver struct {
	base http.RoundTripper
	a    *fetchAttempt
}

func (o backoffObserver) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := o.base.RoundTrip(req)
	if err == nil && resp != nil {
		o.a.noteBackoff(resp)
	}
	return resp, err
}

// noteBackoff keeps the latest backoff any response of this attempt announced.
func (a *fetchAttempt) noteBackoff(resp *http.Response) {
	if at := a.retryAt(resp); at.After(a.retry) {
		a.retry = at
	}
}

// fetchHop records where the transport's most recent proxy decision sent the
// connection of one request chain. It rides the request context, so the dial
// that serves a hop can tell whether its address is that hop's proxy.
type fetchHop struct {
	mu        sync.Mutex
	proxyAddr string // host:port the current hop dials as its proxy; "" for a direct hop
}

func (h *fetchHop) set(addr string) {
	h.mu.Lock()
	h.proxyAddr = addr
	h.mu.Unlock()
}

func (h *fetchHop) isProxy(addr string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.proxyAddr != "" && h.proxyAddr == strings.ToLower(addr)
}

type fetchHopKey struct{}

// fetchHopContext returns ctx carrying a fresh hop record. Every request of the
// production transport is issued on such a context; a dial without one is a
// direct dial and is always guarded.
func fetchHopContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, fetchHopKey{}, &fetchHop{})
}

func fetchHopFrom(ctx context.Context) *fetchHop {
	h, _ := ctx.Value(fetchHopKey{}).(*fetchHop)
	return h
}

// buildTransport builds the production transport: TLS 1.2+, bounded handshake
// and header waits, no connection reuse between daily cycles, and the dial-time
// egress control.
//
// The control is always on. The one exemption is the operator's own proxy: a
// dial whose address is the proxy the Proxy function selected for the hop being
// dialed is not an egress to the target, so it is not judged by the target's
// rules (the proxy is often a private address). Every other dial, including a
// redirect hop or an asset host that NO_PROXY exempts and that therefore goes
// direct, is checked against the configured host's class after name
// resolution, so DNS rebinding cannot slip an inward address past the
// application-layer check. The decision follows each hop's actual next hop and
// is overwritten by the next hop's own decision (it lives on the request
// context, see fetchHop): a proxy chosen earlier never exempts a later direct
// dial to the same host:port, and a dial with no hop on its context is guarded.
func (a *fetchAttempt) buildTransport() *http.Transport {
	selectProxy := a.s.Proxy
	if selectProxy == nil {
		selectProxy = http.ProxyFromEnvironment
	}
	plain := &net.Dialer{Timeout: fetchHeaderTimeout, KeepAlive: -1}
	guarded := &net.Dialer{Timeout: fetchHeaderTimeout, KeepAlive: -1, Control: egressDialControl(a.class)}
	return &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			pu, err := selectProxy(req)
			if hop := fetchHopFrom(req.Context()); hop != nil {
				if err == nil && pu != nil {
					hop.set(proxyDialAddr(pu))
				} else {
					hop.set("")
				}
			}
			return pu, err
		},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if hop := fetchHopFrom(ctx); hop != nil && hop.isProxy(addr) {
				return plain.DialContext(ctx, network, addr)
			}
			return guarded.DialContext(ctx, network, addr)
		},
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   fetchHeaderTimeout,
		ResponseHeaderTimeout: fetchHeaderTimeout,
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     true,
	}
}

// proxyDialAddr is the host:port the transport dials for proxy pu: the proxy's
// own port, or the scheme default, lower-cased like the transport's key.
func proxyDialAddr(pu *url.URL) string {
	port := pu.Port()
	if port == "" {
		switch pu.Scheme {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	return strings.ToLower(net.JoinHostPort(pu.Hostname(), port))
}

// --- requests -----------------------------------------------------------------

func (a *fetchAttempt) get(ctx context.Context, target *url.URL, accept, etag string) (*http.Response, error) {
	if err := a.checkTarget(ctx, target); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(fetchHopContext(ctx), http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "mcpproxy/"+a.s.Version)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := a.client.Do(req)
	if err == nil {
		a.noteBackoff(resp)
	}
	return resp, err
}

// readLimited reads at most limit bytes of the body.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit))
}

// candidateOrigin is the only part of the URL a candidate remembers (B5, B12).
func (a *fetchAttempt) candidateOrigin() string { return a.u.Scheme + "://" + a.host }

// failure maps a transport error to fetch_unavailable with a host-only detail.
func (a *fetchAttempt) failure(ctx context.Context, err error) fetchResult {
	reason := RefreshReasonNetworkError
	var ne net.Error
	switch {
	case errors.Is(context.Cause(ctx), errCycleDeadline), errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		reason = RefreshReasonTimeout
	}
	return fetchResult{Outcome: RefreshOutcomeFetchUnavailable, Reason: reason, Detail: a.host + ": " + classifyFetchError(err)}
}

// classifyFetchError names a transport failure from a closed vocabulary. The
// error's own text is never copied: net/http and the URL parser embed the
// failing request or redirect URL (path, query, a malformed Location) in it
// (B12). The one exception is an egressError, whose text the egress check built
// from a host it had just judged and a fixed reason.
func classifyFetchError(err error) string {
	var (
		ee  *egressError
		dns *net.DNSError
		ua  x509.UnknownAuthorityError
		hn  x509.HostnameError
		ci  x509.CertificateInvalidError
		ne  net.Error
		ue  *url.Error
	)
	switch {
	case errors.As(err, &ee):
		return ee.Error()
	case errors.Is(err, errTooManyRedirects):
		return errTooManyRedirects.Error()
	case errors.Is(err, errRedirectDowngrade):
		return errRedirectDowngrade.Error()
	case errors.Is(err, errCycleDeadline), errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.As(err, &dns):
		return "name resolution failed"
	case errors.As(err, &ua), errors.As(err, &hn), errors.As(err, &ci):
		return "TLS certificate not accepted"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.As(err, &ne) && ne.Timeout():
		return "timed out"
	case errors.As(err, &ue) && isMalformedRedirect(ue.Err):
		return "malformed redirect"
	}
	return "request failed"
}

// isMalformedRedirect recognises net/http's two refusals of an unusable
// redirect response by their fixed prefixes. Only the classification leaves
// this function; the text, which embeds the Location, does not.
func isMalformedRedirect(err error) bool {
	m := err.Error()
	return strings.HasPrefix(m, "failed to parse Location header") || strings.HasPrefix(m, "missing Location header")
}

// statusFailure maps a non-2xx response to fetch_unavailable. 403, 429, a
// Retry-After header and an exhausted X-RateLimit-Remaining all mean "rate
// limited"; none of them says anything about the bundle, so none is a rejection.
func (a *fetchAttempt) statusFailure(resp *http.Response) fetchResult {
	res := fetchResult{
		Outcome: RefreshOutcomeFetchUnavailable,
		Reason:  RefreshReasonHTTPStatus,
		Detail:  a.host + ": HTTP " + strconv.Itoa(resp.StatusCode),
	}
	limited := resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests ||
		resp.Header.Get("Retry-After") != "" || resp.Header.Get("X-RateLimit-Remaining") == "0"
	if limited {
		res.Reason = RefreshReasonRateLimited
		res.RetryAt = a.retryAt(resp)
	}
	return res
}

// retryAt is the earliest time the server asked us back: Retry-After (seconds
// or an HTTP date) and the X-RateLimit-Reset epoch, whichever is later; zero
// when neither is present or both already passed.
func (a *fetchAttempt) retryAt(resp *http.Response) time.Time {
	now := a.now()
	limit := now.Add(refreshRetryCap)
	maxSecs := int64(refreshRetryCap / time.Second)
	var at time.Time
	// consider keeps the latest future instant, never past the refresher's cap.
	// Server-supplied numbers are clamped before any arithmetic so that a huge
	// value cannot wrap time.Duration or time.Unix negative and be discarded as
	// already passed.
	consider := func(t time.Time) {
		if t.After(limit) {
			t = limit
		}
		if t.After(now) && t.After(at) {
			at = t
		}
	}
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && secs > 0 {
			consider(now.Add(time.Duration(min(secs, maxSecs)) * time.Second))
		} else if t, err := http.ParseTime(v); err == nil {
			consider(t)
		}
	}
	if v := resp.Header.Get("X-RateLimit-Reset"); v != "" && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		if epoch, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && epoch > 0 {
			epoch = min(epoch, now.Unix()+maxSecs)
			consider(time.Unix(epoch, 0).UTC())
		}
	}
	return at
}

// --- the two channel shapes ---------------------------------------------------

func (a *fetchAttempt) releases(ctx context.Context) fetchResult {
	resp, err := a.get(ctx, a.u, "application/vnd.github+json", a.s.ETag)
	if err != nil {
		return a.failure(ctx, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified:
		return fetchResult{Outcome: RefreshOutcomeNotModified, ETag: a.s.ETag, RetryAt: a.retryAt(resp)}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return a.statusFailure(resp)
	}
	etag := resp.Header.Get("ETag")
	retryAt := a.retryAt(resp)
	invalid := func(why string) fetchResult {
		return fetchResult{Outcome: RefreshOutcomeChannelInvalid, Detail: a.host + ": " + why, RetryAt: retryAt}
	}

	body, err := readLimited(resp.Body, maxReleasesJSONBytes+1)
	if err != nil {
		return a.failure(ctx, err)
	}
	if len(body) > maxReleasesJSONBytes {
		return invalid("release listing over 1 MiB")
	}
	var doc struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return invalid("release listing is not JSON")
	}

	byName := make(map[string]string, len(doc.Assets))
	var bundles []string
	for _, as := range doc.Assets {
		byName[as.Name] = as.URL
		if releaseAssetRE.MatchString(as.Name) {
			bundles = append(bundles, as.Name)
		}
	}
	if len(bundles) != 1 {
		return invalid(fmt.Sprintf("%d candidate bundle assets, want exactly 1", len(bundles)))
	}
	name := bundles[0]
	seq, err := strconv.ParseUint(releaseAssetRE.FindStringSubmatch(name)[1], 10, 64)
	if err != nil {
		return invalid("asset sequence out of range")
	}
	sigURL, ok := byName[name+".sig"]
	if !ok {
		return invalid("no sidecar asset beside the bundle")
	}
	bundleURL, err1 := parseAssetURL(byName[name])
	sidecarURL, err2 := parseAssetURL(sigURL)
	if err1 != nil || err2 != nil {
		return invalid("asset download URL not acceptable")
	}

	// The filename sequence is an UNSIGNED hint: it only spares a download that
	// the watermark would refuse anyway. It is never trusted for activation.
	if a.s.Watermark != nil && a.s.Policy != nil {
		if w, ok := a.s.Watermark(a.s.Policy.ActiveRoot); ok && seq <= w {
			return fetchResult{Outcome: RefreshOutcomeUnchanged, ETag: etag, RetryAt: retryAt}
		}
	}

	bundle, res, ok := a.download(ctx, bundleURL, MaxBundleBytes+1)
	if !ok {
		return res
	}
	sidecar, res, ok := a.download(ctx, sidecarURL, MaxSidecarBytes+1)
	if !ok {
		return res
	}
	return fetchResult{
		Outcome:   fetchOutcomeCandidate,
		Candidate: &Candidate{Bundle: bundle, Sidecar: sidecar, SidecarPresent: true, Source: BundleSourceFetch, Path: a.candidateOrigin()},
		ETag:      etag,
		RetryAt:   retryAt,
	}
}

// download fetches one release asset; ok is false when res is the result to return.
func (a *fetchAttempt) download(ctx context.Context, u *url.URL, limit int64) (data []byte, res fetchResult, ok bool) {
	resp, err := a.get(ctx, u, "application/octet-stream", "")
	if err != nil {
		return nil, a.failure(ctx, err), false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, a.statusFailure(resp), false
	}
	data, err = readLimited(resp.Body, limit)
	if err != nil {
		return nil, a.failure(ctx, err), false
	}
	return data, fetchResult{}, true
}

func (a *fetchAttempt) direct(ctx context.Context) fetchResult {
	resp, err := a.get(ctx, a.u, "application/octet-stream", a.s.ETag)
	if err != nil {
		return a.failure(ctx, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified:
		return fetchResult{Outcome: RefreshOutcomeNotModified, ETag: a.s.ETag, RetryAt: a.retryAt(resp)}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return a.statusFailure(resp)
	}
	etag := resp.Header.Get("ETag")
	retryAt := a.retryAt(resp)
	bundle, err := readLimited(resp.Body, MaxBundleBytes+1)
	if err != nil {
		return a.failure(ctx, err)
	}

	sig := *a.u
	sig.Path += ".sig"
	if sig.RawPath != "" {
		sig.RawPath += ".sig"
	}
	sresp, err := a.get(ctx, &sig, "application/octet-stream", "")
	if err != nil {
		return a.failure(ctx, err)
	}
	defer sresp.Body.Close()
	c := &Candidate{Bundle: bundle, Source: BundleSourceFetch, Path: a.candidateOrigin()}
	switch {
	case sresp.StatusCode == http.StatusNotFound:
		// No sidecar: the pipeline itself refuses it as signature_required.
	case sresp.StatusCode < 200 || sresp.StatusCode > 299:
		return a.statusFailure(sresp)
	default:
		sidecar, err := readLimited(sresp.Body, MaxSidecarBytes+1)
		if err != nil {
			return a.failure(ctx, err)
		}
		c.Sidecar, c.SidecarPresent = sidecar, true
	}
	return fetchResult{Outcome: fetchOutcomeCandidate, Candidate: c, ETag: etag, RetryAt: retryAt}
}
