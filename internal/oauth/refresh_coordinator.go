package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
)

// RefreshTrigger names what started a refresh flight (Spec 113 FR-011).
type RefreshTrigger string

const (
	// RefreshTriggerReactive: mcp-go asked the token store for a token that is
	// inside its refresh margin.
	RefreshTriggerReactive RefreshTrigger = "reactive"
	// RefreshTriggerProactive: the RefreshManager's schedule fired.
	RefreshTriggerProactive RefreshTrigger = "proactive"
)

// refreshFlightTimeout bounds one refresh flight. The flight runs on a context
// detached from any single caller (FR-004).
const refreshFlightTimeout = 30 * time.Second

// RefreshFunc performs one network refresh for the given record and persists
// the result. It MUST persist through a compare-and-swap against the
// generation of the record it was given (see FlightGeneration), so a login
// that superseded the flight is never overwritten (FR-006a).
type RefreshFunc func(ctx context.Context, current *storage.OAuthTokenRecord) (*client.Token, error)

// RefreshRequest describes one caller's request to refresh a server's token.
type RefreshRequest struct {
	// Key is the server key (GenerateServerKey).
	Key string
	// Binding is the caller's committed credential binding (Spec 116 PR2b,
	// review R7.1). Flights, the terminal latch, the transient cooldown and
	// lastSavedAt are keyed by (Key, Binding): an instance a commit removed,
	// replaced or re-bound never joins its replacement's flight (and gets
	// its token), and the replacement never inherits the old instance's
	// failure, latch or cooldown. Load must be the caller's bound loader.
	Binding commitview.CredentialBinding
	// ServerName is the display name (logs, flow-active checks, hook).
	ServerName string
	// ObservedRefreshToken is the refresh token the caller read. It is only
	// compared, never logged.
	ObservedRefreshToken string
	Trigger              RefreshTrigger
	// Load re-reads the persisted record.
	Load func() (*storage.OAuthTokenRecord, error)
	// Refresh performs the network refresh (one of the two sub-paths).
	Refresh RefreshFunc
	// StaticClient is true when the client id comes from oauth.client_id.
	StaticClient bool
	// ClearClient compare-and-clears the stored DCR registration (FR-009)
	// while the stored client id and refresh token are still the ones the
	// failed request used (FR-006a).
	// nil means there is nothing to clear (e.g. no storage).
	ClearClient func(expectedClientID, expectedRefreshToken string) (bool, error)
	// ClearClientAt, when set, replaces ClearClient (Spec 116 PR2b, review
	// R7.3): it clears the DCR registration of exactly the record the flight
	// read (its revision), bound to Binding.
	ClearClientAt func(rec *storage.OAuthTokenRecord) (bool, error)
	// Logger, when set, receives the flight's start and finish lines (GH
	// #1827): the server's OAuth logger, so they reach the server's own log
	// file as well as the main log. nil uses the coordinator's logger.
	Logger *zap.Logger
}

// RefreshFlightInfo identifies the refresh flight a token request belongs to
// (GH #1827). Flight numbers count, per server key and credential binding,
// the flights that went on to call the refresh function; 0 means the request
// was not sent by a refresh flight (an authorization-code exchange, say).
type RefreshFlightInfo struct {
	Flight  uint64
	Trigger RefreshTrigger
}

type refreshFlightInfoKey struct{}

func withRefreshFlightInfo(ctx context.Context, info RefreshFlightInfo) context.Context {
	return context.WithValue(ctx, refreshFlightInfoKey{}, info)
}

// RefreshFlightInfoFrom returns the flight a token request's context was
// created by, if any.
func RefreshFlightInfoFrom(ctx context.Context) (RefreshFlightInfo, bool) {
	if ctx == nil {
		return RefreshFlightInfo{}, false
	}
	info, ok := ctx.Value(refreshFlightInfoKey{}).(RefreshFlightInfo)
	return info, ok
}

// RefreshOutcome is passed to the completion hook for a terminal flight
// outcome whose generation still matches the stored record.
type RefreshOutcome struct {
	ServerName string
	Key        string
	// Binding is the flight's credential binding (Spec 116 PR2b): the
	// RefreshManager applies the outcome only while it is the binding of
	// the instance that serves the server now.
	Binding commitview.CredentialBinding
	Trigger RefreshTrigger
	Class   RefreshErrorClass
	Err     error
	// ExpiresAt is the access-token expiry of the record the flight failed
	// on. The RefreshManager only applies the outcome to a schedule built
	// for that token, so a login that replaced the schedule after the
	// flight's last generation check is not failed by it (FR-006a).
	ExpiresAt time.Time
}

type refreshFlight struct {
	done    chan struct{}
	waiters int
	tok     *client.Token
	// rev is the revision of the stored record whose token the flight
	// handed out, as read by the flight itself (Sol r3 on #1621); zero when
	// it handed out no token or the store has no revisions.
	rev     uint64
	skipped bool
	err     error
}

type refreshKeyState struct {
	flight *refreshFlight

	// Terminal latch (FR-004): while the stored generation equals latchGen,
	// every caller gets latchErr without a network request.
	latchGen string
	latchErr error

	// Transient cooldown for reactive callers (FR-004).
	cooldownGen   string
	cooldownUntil time.Time
	cooldownErr   error
	// transientPending: the last flight failed transiently and none has
	// succeeded since. Unlike the cooldown it survives ClearTransientCooldown,
	// so the connectivity watcher still knows the credential needs a refresh.
	transientPending  bool
	transientFailures int
	// flightSeq numbers the flights that called the refresh function (GH
	// #1827), so each token request in the log can be told apart.
	flightSeq uint64
	// savesInProgress counts token responses being written for this key
	// (BeginTokenSaveFor). A successful token response settles the
	// transient state, but the write commits before NoteTokenSavedFor runs
	// and other goroutines can already use the new token; while a save is
	// in progress the credential is not reported as transiently failing
	// (GH #1929 Windows CI: a slow bbolt fsync held that window open).
	savesInProgress int

	// dcrCleared is set when the DCR registration was cleared after
	// invalid_client and no successful token response has been seen since
	// (FR-009 "exactly one re-registration").
	dcrCleared bool
	// clearedClientID is the client id that clear removed. A login that
	// still exchanges its code with it was built before the clear (e.g. the
	// connect attempt whose refresh hit invalid_client) and is told to sign
	// in again, not that the AS rejects new registrations.
	clearedClientID string

	// lastSavedAt is when a token was last saved for this key (a refresh
	// flight or a login). A proactive refresh within proactiveFreshWindow of
	// it is answered from storage (FR-003, SC-001).
	lastSavedAt time.Time
	// saveSeq counts saved tokens for the key; a flight that cleared the DCR
	// registration only records that while no token was saved since it read
	// the state (a login that re-registered in between wins).
	saveSeq uint64
}

// proactiveFreshWindow: a proactive refresh that arrives this soon after a
// token was saved for the key was decided from the pre-rotation state (the
// RefreshManager timer fired while a reactive flight or a login was
// rotating the token, before OnTokenSaved rescheduled it). It must not rotate
// the just-minted grant again. The RefreshManager never schedules a refresh
// sooner than MinRefreshInterval after a save, so a genuine proactive refresh
// is never inside the window.
const proactiveFreshWindow = MinRefreshInterval

// RefreshCoordinator serializes refresh flights per server key across the
// reactive (token store) and proactive (RefreshManager) triggers (Spec 113
// FR-001). It is process-wide; see DefaultRefreshCoordinator.
type RefreshCoordinator struct {
	mu     sync.Mutex
	keys   map[flightKey]*refreshKeyState
	hook   func(RefreshOutcome)
	audit  func(RefreshAttemptFailure)
	now    func() time.Time
	logger *zap.Logger
}

// NewRefreshCoordinator returns an empty coordinator.
func NewRefreshCoordinator() *RefreshCoordinator {
	return &RefreshCoordinator{
		keys: make(map[flightKey]*refreshKeyState),
		now:  time.Now,
	}
}

var defaultRefreshCoordinator = NewRefreshCoordinator()

// DefaultRefreshCoordinator returns the process-wide coordinator used by the
// token stores and core.Client.RefreshOAuthTokenDirect.
func DefaultRefreshCoordinator() *RefreshCoordinator { return defaultRefreshCoordinator }

// SetCompletionHook installs the function called once per flight for a
// terminal outcome that still applies to the stored record. nil removes it.
func (c *RefreshCoordinator) SetCompletionHook(hook func(RefreshOutcome)) {
	c.mu.Lock()
	c.hook = hook
	c.mu.Unlock()
}

// RefreshAttemptFailure is one token refresh that was sent and failed
// (Spec 124 D5).
type RefreshAttemptFailure struct {
	ServerName string
	Class      RefreshErrorClass
	Trigger    RefreshTrigger
	// At is when the failure was observed.
	At time.Time
	// Code is the diagnostics code (MCPX_*) of the failure when the error
	// classifies to one in the catalogue, else empty. Only the code crosses
	// this boundary, never the error text.
	Code string
}

// refreshFailureCode classifies a refresh error to a catalogue code.
func refreshFailureCode(serverName string, err error) string {
	code := diagnostics.Classify(err, diagnostics.ClassifierHints{ServerID: serverName, Transport: "http"})
	if code == "" || code == diagnostics.UnknownUnclassified {
		return ""
	}
	if _, ok := diagnostics.Get(code); !ok {
		return ""
	}
	return string(code)
}

// auditAttempt reports one failed token request to the audit observer. A
// failure before anything was sent (no client id, no token endpoint, a
// retired instance) is not a failed token request and is not reported.
func (c *RefreshCoordinator) auditAttempt(req RefreshRequest, err error, at time.Time) {
	if err == nil || RefreshWasNotSent(err) || dialgate.IsRetired(err) {
		return
	}
	c.mu.Lock()
	audit := c.audit
	c.mu.Unlock()
	if audit == nil {
		return
	}
	cls, _ := ClassifyRefreshError(err)
	audit(RefreshAttemptFailure{
		ServerName: req.ServerName, Class: cls, Trigger: req.Trigger, At: at,
		Code: refreshFailureCode(req.ServerName, err),
	})
}

type attemptFailureReporterKey struct{}

// WithAttemptFailureReporter returns a context whose refresh function can
// report a failed token request that it follows with another one. The
// coordinator installs it for every flight.
func WithAttemptFailureReporter(ctx context.Context, report func(error, time.Time)) context.Context {
	return context.WithValue(ctx, attemptFailureReporterKey{}, report)
}

// ReportRefreshAttemptFailure records a token request that was sent and
// failed and that the refresh function is about to follow with another one
// (Spec 124 D5). The failure a refresh function RETURNS is recorded by the
// coordinator and must not be reported here as well. at is when the request
// failed. Outside a coordinator flight it does nothing.
func ReportRefreshAttemptFailure(ctx context.Context, err error, at time.Time) {
	if report, ok := ctx.Value(attemptFailureReporterKey{}).(func(error, time.Time)); ok && report != nil {
		report(err, at)
	}
}

// SetAuditObserver installs the function called once for every refresh that
// was actually sent and failed. nil removes it.
//
// It is separate from the completion hook, which reports only terminal
// outcomes that still apply to the stored record. An activity log needs every
// failed attempt, including a transient one that the next retry repairs, and
// needs none of the returns that sent nothing: a fresh record, a latched
// terminal error, a cooldown, a retired instance. Waiters on the same flight
// and a caller that stopped waiting report nothing, because the observer is
// called by the flight itself.
func (c *RefreshCoordinator) SetAuditObserver(observer func(RefreshAttemptFailure)) {
	c.mu.Lock()
	c.audit = observer
	c.mu.Unlock()
}

func (c *RefreshCoordinator) logFor(req RefreshRequest) *zap.Logger {
	if req.Logger != nil {
		return req.Logger
	}
	return c.log()
}

func (c *RefreshCoordinator) log() *zap.Logger {
	if c.logger != nil {
		return c.logger
	}
	return zap.L().Named("oauth-refresh")
}

// flightKey is one credential's coordinator state: the server key and the
// committed credential binding of the instances using it (Spec 116 PR2b,
// review R7.1).
type flightKey struct {
	serverKey string
	binding   commitview.CredentialBinding
}

func (c *RefreshCoordinator) stateLocked(key flightKey) *refreshKeyState {
	st := c.keys[key]
	if st == nil {
		st = &refreshKeyState{}
		c.keys[key] = st
	}
	return st
}

// waiters reports how many unbound callers are attached to the key's current
// flight (tests).
func (c *RefreshCoordinator) waiters(key string) int {
	return c.waitersFor(key, commitview.CredentialBinding{})
}

// waitersFor reports how many callers bound to b are attached to the key's
// current flight (tests).
func (c *RefreshCoordinator) waitersFor(key string, b commitview.CredentialBinding) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.keys[flightKey{key, b}]; st != nil && st.flight != nil {
		return st.flight.waiters
	}
	return 0
}

// Forget drops the coordinator state of one credential binding of serverKey
// (Spec 116 PR2b): the removal purge, the OAuth-config clear and a logout
// call it for the binding they made obsolete, so the map stays bounded by the
// live bindings plus in-flight stale ones. A flight still running keeps its
// own state value and finishes for its joiners; its result is recorded on
// that orphaned value only.
func (c *RefreshCoordinator) Forget(serverKey string, b commitview.CredentialBinding) {
	c.mu.Lock()
	delete(c.keys, flightKey{serverKey, b})
	c.mu.Unlock()
}

// ForgetServer drops the coordinator state of every binding of serverKey
// (a logout, which clears the credential whatever binding wrote it).
func (c *RefreshCoordinator) ForgetServer(serverKey string) {
	c.mu.Lock()
	for k := range c.keys {
		if k.serverKey == serverKey {
			delete(c.keys, k)
		}
	}
	c.mu.Unlock()
}

// ClearTransientCooldown lifts the transient-failure cooldown of one
// credential binding of serverKey and restarts its backoff, reporting whether
// one was set. The upstream manager's connectivity watcher calls it when the
// network demonstrably came back (GH #1929): refreshes that failed while the
// machine was offline must not make the reconnect wait up to five minutes for
// a refresh that would now succeed. The terminal latch (a revoked grant) is
// left alone, so a rejected credential is still not retried.
func (c *RefreshCoordinator) ClearTransientCooldown(serverKey string, b commitview.CredentialBinding) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.keys[flightKey{serverKey, b}]
	if st == nil || (st.cooldownUntil.IsZero() && st.transientFailures == 0) {
		return false
	}
	st.cooldownGen, st.cooldownUntil, st.cooldownErr = "", time.Time{}, nil
	st.transientFailures = 0
	return true
}

// TransientlyFailing reports whether the last refresh of one credential
// binding of serverKey failed transiently (network, timeout, 5xx) and none has
// succeeded since, with no terminal latch (a revoked grant). The connectivity
// watcher uses it to find a CONNECTED server whose expired token cannot be
// refreshed only because the network or the provider was briefly unavailable
// (GH #1929).
func (c *RefreshCoordinator) TransientlyFailing(serverKey string, b commitview.CredentialBinding) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.keys[flightKey{serverKey, b}]
	return st != nil && st.transientPending && st.savesInProgress == 0 && st.latchErr == nil
}

// BeginTokenSaveFor marks that a successful token response for one credential
// binding of key is being persisted; the returned function ends it and must be
// called once the save finished (after NoteTokenSavedFor on success). Until
// then TransientlyFailing and TransientRefreshStatus report no transient
// failure, so no reader that already sees the new token is told the refresh
// is still failing. A save that fails or is discarded leaves the earlier
// transient state in place once it ends.
func (c *RefreshCoordinator) BeginTokenSaveFor(key string, b commitview.CredentialBinding) (end func()) {
	c.mu.Lock()
	st := c.stateLocked(flightKey{key, b})
	st.savesInProgress++
	c.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			st.savesInProgress--
			c.mu.Unlock()
		})
	}
}

// TransientRefreshInfo describes a refresh that is failing transiently.
type TransientRefreshInfo struct {
	Failures  int       // transient failures in a row (0 after ClearTransientCooldown)
	RetryAt   time.Time // end of the reactive cooldown; zero when none
	LastError string
}

// TransientRefreshStatus is TransientlyFailing with the details health
// reporting shows: a connected server whose expired token is being refreshed
// again is "Token refresh pending", not "sign in" (GH #1929).
func (c *RefreshCoordinator) TransientRefreshStatus(serverKey string, b commitview.CredentialBinding) (TransientRefreshInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.keys[flightKey{serverKey, b}]
	if st == nil || !st.transientPending || st.savesInProgress > 0 || st.latchErr != nil {
		return TransientRefreshInfo{}, false
	}
	info := TransientRefreshInfo{Failures: st.transientFailures, RetryAt: st.cooldownUntil}
	if st.cooldownErr != nil {
		info.LastError = LogSafeErrorText(st.cooldownErr)
	}
	return info, true
}

// Do runs at most one refresh flight per key. A caller arriving while a flight
// is running waits for it and gets the same token or the same error. skipped
// reports that the flight made no network request (another flight had already
// rotated the token, or the latch/cooldown answered). A caller whose own ctx
// ends returns ctx.Err(); the flight continues for the others.
func (c *RefreshCoordinator) Do(ctx context.Context, req RefreshRequest) (*client.Token, bool, error) {
	tok, _, skipped, err := c.DoServed(ctx, req)
	return tok, skipped, err
}

// DoServed is Do that also returns the revision of the stored record whose
// token it handed out, as the flight read it (Sol r3 on #1621). A token store
// guards its later 5xx-recovery clear with that revision: a record a
// same-binding replacement rewrote after the flight's read, even with the
// same access token, carries a newer revision and is not erased.
func (c *RefreshCoordinator) DoServed(ctx context.Context, req RefreshRequest) (*client.Token, uint64, bool, error) {
	// Spec 116 PR4: a flight that ran on a retired upstream instance (its
	// gate refused the token request, or closed the connection under the
	// response) says nothing about the grant. A caller that only joined such
	// a flight tries again until it gets an answer that is not a retirement
	// or leads a flight of its own, on its own transport: the retired
	// initiator may start a new (also refused) flight first, and a single
	// retry would join that one (review round 1). A retired caller's own
	// flight is refused at once, so the loop ends; ctx bounds it.
	//
	// A nil ctx is normalized here, before the loop reads ctx.Err()
	// (review round 2).
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		tok, rev, skipped, led, err := c.do(ctx, req)
		if !dialgate.IsRetired(err) || led || ctx.Err() != nil {
			return tok, rev, skipped, err
		}
		if hook := refreshRetiredRetryHook; hook != nil {
			hook(req)
		}
	}
}

// refreshRetiredRetryHook is a test seam run before a caller that joined a
// retired instance's flight tries again. Nil in production.
var refreshRetiredRetryHook func(RefreshRequest)

// do joins the key's flight or starts one. led reports that this call
// started the flight it returns the result of.
func (c *RefreshCoordinator) do(ctx context.Context, req RefreshRequest) (tok *client.Token, rev uint64, skipped, led bool, err error) {
	c.mu.Lock()
	st := c.stateLocked(flightKey{req.Key, req.Binding})
	f := st.flight
	if f == nil {
		led = true
		f = &refreshFlight{done: make(chan struct{})}
		st.flight = f
		go c.run(ctx, req, st, f)
	}
	f.waiters++
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		f.waiters--
		c.mu.Unlock()
	}()

	select {
	case <-f.done:
		return f.tok, f.rev, f.skipped, led, f.err
	case <-ctx.Done():
		return nil, 0, false, led, ctx.Err()
	}
}

func (c *RefreshCoordinator) run(callerCtx context.Context, req RefreshRequest, st *refreshKeyState, f *refreshFlight) {
	fctx, cancel := context.WithTimeout(context.WithoutCancel(callerCtx), refreshFlightTimeout)
	defer cancel()

	start := c.now()
	var seq uint64
	tok, rev, skipped, err := c.flight(fctx, req, st, &seq)

	cls, status := ClassifyRefreshError(err)
	c.logFor(req).Info("OAuth refresh flight finished",
		zap.String("server", req.ServerName),
		zap.String("trigger", string(req.Trigger)),
		zap.Uint64("flight", seq),
		zap.String("outcome", string(cls)),
		zap.Int("http_status", status),
		zap.Bool("network_skipped", skipped),
		zap.Duration("duration", c.now().Sub(start)))

	c.mu.Lock()
	f.tok, f.rev, f.skipped, f.err = tok, rev, skipped, err
	st.flight = nil
	c.mu.Unlock()
	close(f.done)
}

// flight runs one refresh flight. *seq is set to the flight's number once it
// goes on to call the refresh function (0 when it answered without one).
func (c *RefreshCoordinator) flight(ctx context.Context, req RefreshRequest, st *refreshKeyState, seq *uint64) (*client.Token, uint64, bool, error) {
	rec, err := req.Load()
	if err != nil || rec == nil || (rec.AccessToken == "" && rec.RefreshToken == "") {
		return nil, 0, true, &RefreshFailure{Class: RefreshClassTerminalOther, Message: "no stored OAuth token; sign in again", Err: ErrNoRefreshToken}
	}
	// FR-003: another flight (or a login) already rotated the token.
	if rec.RefreshToken != req.ObservedRefreshToken && rec.AccessToken != "" {
		return tokenFromRecord(rec), rec.Revision, true, nil
	}
	if rec.RefreshToken == "" {
		return nil, 0, true, &RefreshFailure{Class: RefreshClassTerminalOther, Message: "no refresh token stored; sign in again", Err: ErrNoRefreshToken}
	}

	gen := generationOf(rec)
	c.mu.Lock()
	// SC-001: a proactive caller that read the record only after another
	// flight (or a login) rotated it observes the new refresh token, so the
	// check above cannot tell. A token saved moments ago is not due.
	// A token with no more than the window left is genuinely due (the
	// RefreshManager schedules very short tokens at expiry minus
	// MinRefreshInterval, possibly within the window of their save).
	if req.Trigger == RefreshTriggerProactive && rec.AccessToken != "" && !st.lastSavedAt.IsZero() &&
		c.now().Sub(st.lastSavedAt) < proactiveFreshWindow &&
		(rec.ExpiresAt.IsZero() || rec.ExpiresAt.Sub(c.now()) > proactiveFreshWindow) {
		c.mu.Unlock()
		return tokenFromRecord(rec), rec.Revision, true, nil
	}
	if st.latchErr != nil && st.latchGen == gen {
		latched := st.latchErr
		c.mu.Unlock()
		return nil, 0, true, latched
	}
	if req.Trigger == RefreshTriggerReactive && st.cooldownGen == gen && c.now().Before(st.cooldownUntil) {
		until, last := st.cooldownUntil, st.cooldownErr
		c.mu.Unlock()
		return nil, 0, true, fmt.Errorf("%w (retry after %s): %w", ErrTokenRefreshTransient, until.Format(time.RFC3339), last)
	}
	st.flightSeq++
	*seq = st.flightSeq
	priorFailures := st.transientFailures
	c.mu.Unlock()

	// GH #1827: logged before any token request is sent, so a request the
	// provider saw can be matched to the flight that sent it even when the
	// flight never finishes (a crash, a sleeping laptop).
	c.logFor(req).Info("OAuth refresh flight started",
		zap.String("server", req.ServerName),
		zap.String("trigger", string(req.Trigger)),
		zap.Uint64("flight", *seq),
		zap.Int("prior_transient_failures", priorFailures))

	saved := &atomic.Uint64{}
	// A refresh function that sends more than one token request (the replay
	// of a lost response) reports each failed request that is followed by
	// another through this context; the flight reports the last one itself.
	refreshCtx := WithAttemptFailureReporter(ctx, func(attemptErr error, at time.Time) {
		c.auditAttempt(req, attemptErr, at)
	})
	refreshCtx = withRefreshFlightInfo(refreshCtx, RefreshFlightInfo{Flight: *seq, Trigger: req.Trigger})
	tok, refreshErr := req.Refresh(withFlightSavedRevision(withFlightGeneration(refreshCtx, gen), saved), rec)
	// The time of the outcome, before the storage reads below: an audit row
	// must not be placed after calls that only waited for those reads.
	refreshedAt := c.now()
	cur, loadErr := req.Load()
	if loadErr != nil {
		cur = nil
	}

	if refreshErr == nil {
		c.mu.Lock()
		c.resetLocked(st)
		st.lastSavedAt = c.now()
		st.saveSeq++
		c.mu.Unlock()
		// FR-006a: the flight's persist was discarded because something newer
		// was saved meanwhile; hand out the stored token instead.
		// No stored record after the flight (logout / token cleared while it
		// ran, or an unreadable store): the persist was discarded, so the
		// flight's token must not be handed out either.
		if cur == nil || cur.AccessToken == "" {
			return nil, 0, false, &RefreshFailure{Class: RefreshClassOther, Message: "refresh succeeded but no token is stored", Err: ErrRefreshFailed}
		}
		if tok == nil || cur.AccessToken != tok.AccessToken {
			return tokenFromRecord(cur), cur.Revision, false, nil
		}
		// The served record is the one the flight's own persist wrote
		// (Sol r3 on #1621): a replacement that rewrote it since, even
		// with the same access token, has a newer revision and is not the
		// served one. A store that reports no revision falls back to the
		// record read here.
		if rev := saved.Load(); rev != 0 {
			return tok, rev, false, nil
		}
		return tok, cur.Revision, false, nil
	}

	// Spec 116 PR4: the instance that ran the refresh was retired and its
	// gate refused the request. No token request was sent, so the failure
	// has no side effects: no latch, no cooldown, no client clear.
	if dialgate.IsRetired(refreshErr) {
		return nil, 0, false, refreshErr
	}

	cls, _ := ClassifyRefreshError(refreshErr)

	// Spec 124 D5: a token request was sent and failed. Reported here, past
	// the returns that sent nothing, and before supersession can turn this
	// flight's failure into a successful return of a newer grant.
	c.auditAttempt(req, refreshErr, refreshedAt)

	// FR-006a: a login or another writer superseded this flight's generation.
	// Its failure has no side effects; a newer grant is handed out.
	if cur == nil || generationOf(cur) != gen {
		if cur != nil && cur.AccessToken != "" && cur.RefreshToken != rec.RefreshToken {
			return tokenFromRecord(cur), cur.Revision, false, nil
		}
		return nil, 0, false, refreshErr
	}

	clearedByFlight := false
	if cls == RefreshClassInvalidClient {
		// Sol r5 on #1621: a writer rewrote the record after the request
		// read it (e.g. a same-binding replacement that kept the client id
		// and refresh token but corrected the secret, callback port or
		// redirect URI). The rejection was of the registration the request
		// sent, not of the stored one: it supersedes this flight, so the
		// failure has no side effects (no clear, no latch).
		if !req.StaticClient && rec.Revision != 0 && cur.Revision != rec.Revision {
			return nil, 0, false, supersededRefreshError(refreshErr)
		}
		// The clear acts on exactly the record the failed request used, by
		// its revision (Spec 116 PR2b), never on a record re-read after it.
		superseded, cleared, annotated := c.handleInvalidClient(req, rec, st, refreshErr)
		if superseded {
			// Sol r6 on #1621: the revision-checked clear refused because a
			// writer rewrote the record (or re-bound the server) between the
			// check above and the clear. The rejection was of the
			// registration the request sent, not of the stored one: no
			// latch, no terminal outcome, and the next refresh tries the
			// rewritten registration.
			return nil, 0, false, supersededRefreshError(refreshErr)
		}
		refreshErr = annotated
		clearedByFlight = cleared
	}

	if cls.IsTerminal() {
		latchRec := cur
		if again, err := req.Load(); err == nil && again != nil {
			// FR-006a: a login that saved a new grant (or a new client)
			// since cur was read supersedes this flight; its failure must
			// not latch or fail the newer grant. Only the flight's own DCR
			// clear (same refresh token, client id emptied) may change the
			// generation here.
			if again.RefreshToken != rec.RefreshToken || (again.ClientID != "" && again.ClientID != rec.ClientID) {
				if again.AccessToken != "" {
					return tokenFromRecord(again), again.Revision, false, nil
				}
				return nil, 0, false, refreshErr
			}
			if clearedByFlight && again.ClientID != "" {
				// Sol r7 on #1621: the flight's revision-checked clear
				// succeeded, yet the stored record has a client id again: a
				// same-binding writer re-saved a registration after the clear
				// (e.g. the same client id with a corrected secret, callback
				// port or redirect URI). That record has the flight's
				// generation but is not the one the flight cleared, so the
				// rejection must not latch against it or emit a terminal
				// outcome; the next refresh tries the rewritten registration.
				c.mu.Lock()
				if st.clearedClientID == rec.ClientID {
					st.dcrCleared, st.clearedClientID = false, ""
				}
				c.mu.Unlock()
				return nil, 0, false, supersededRefreshError(refreshErr)
			}
			latchRec = again // after a DCR clear the generation changed
		}
		c.mu.Lock()
		st.latchGen = generationOf(latchRec)
		st.latchErr = refreshErr
		hook := c.hook
		c.mu.Unlock()
		if hook != nil {
			hook(RefreshOutcome{ServerName: req.ServerName, Key: req.Key, Binding: req.Binding, Trigger: req.Trigger, Class: cls, Err: refreshErr, ExpiresAt: latchRec.ExpiresAt})
		}
		return nil, 0, false, refreshErr
	}

	c.mu.Lock()
	st.transientFailures++
	st.transientPending = true
	st.cooldownGen = gen
	st.cooldownUntil = c.now().Add(refreshBackoff(st.transientFailures - 1))
	st.cooldownErr = refreshErr
	c.mu.Unlock()
	return nil, 0, false, refreshErr
}

// supersededRefreshError re-classifies an invalid_client rejection that a
// same-binding registration rewrite superseded (Sol r9 on #1621). The
// rejection was of the registration the request sent, not of the stored one,
// so it must not read as terminal to any caller: a proactive attempt
// (RefreshManager.executeRefresh) keeps its schedule and retries with backoff,
// and a reactive one surfaces as transient instead of "authorization
// required". The original error stays in the chain for logs.
func supersededRefreshError(err error) error {
	return &RefreshFailure{
		Class:   RefreshClassOther,
		Message: "client registration was rewritten while the refresh ran; retrying with the rewritten registration",
		Err:     err,
	}
}

// handleInvalidClient implements FR-009 and returns the error to hand out.
// superseded is true when the revision-checked clear (ClearClientAt) ran
// without error and cleared nothing: the record the failed request used is
// no longer the stored one (a same-binding rewrite or a re-bind landed in the
// window before the clear), so the rejection must not latch against it.
// cleared is true when the flight's own clear removed the registration.
func (c *RefreshCoordinator) handleInvalidClient(req RefreshRequest, rec *storage.OAuthTokenRecord, st *refreshKeyState, err error) (superseded, cleared bool, annotated error) {
	if req.StaticClient {
		return false, false, &RefreshFailure{Class: RefreshClassInvalidClient, Err: err,
			Message: "the authorization server rejected the configured OAuth client (invalid_client); fix oauth.client_id / oauth.client_secret for this server, then sign in again"}
	}
	c.mu.Lock()
	alreadyCleared := st.dcrCleared
	seq := st.saveSeq
	c.mu.Unlock()
	if alreadyCleared {
		return false, false, &RefreshFailure{Class: RefreshClassInvalidClient, Err: err, Message: rejectsNewRegistrationsMessage}
	}
	// No "login flow active" exemption: the clear is a compare-and-clear on
	// the client id AND refresh token this request used, so a login that
	// saved a new registration or a new grant is never touched, while a
	// connect attempt that is itself running this refresh (tryOAuthAuth
	// holds a flow for its whole duration) still gets the rejected
	// registration removed and can re-register on the next sign-in.
	didClear := false
	if (req.ClearClient != nil || req.ClearClientAt != nil) && rec.ClientID != "" {
		var cleared bool
		var clearErr error
		if req.ClearClientAt != nil {
			cleared, clearErr = req.ClearClientAt(rec)
		} else {
			cleared, clearErr = req.ClearClient(rec.ClientID, rec.RefreshToken)
		}
		if clearErr != nil {
			c.log().Warn("Failed to clear rejected DCR client registration",
				zap.String("server", req.ServerName), zap.Error(clearErr))
		}
		if !cleared && clearErr == nil && req.ClearClientAt != nil {
			c.log().Debug("rejected DCR client registration not cleared: the record changed since the refresh read it",
				zap.String("server", req.ServerName))
			return true, false, err
		}
		if cleared {
			didClear = true
			c.mu.Lock()
			if st.saveSeq == seq {
				st.dcrCleared = true
				st.clearedClientID = rec.ClientID
			}
			c.mu.Unlock()
			c.log().Info("Cleared DCR client registration rejected by the authorization server",
				zap.String("server", req.ServerName))
		}
	}
	return false, didClear, &RefreshFailure{Class: RefreshClassInvalidClient, Err: err,
		Message: signInToReRegisterMessage}
}

const signInToReRegisterMessage = "client registration rejected by the authorization server; sign in again to re-register"

const rejectsNewRegistrationsMessage = "the authorization server rejected the newly registered OAuth client (invalid_client) again: the authorization server rejects new client registrations; configure a static oauth.client_id for this server"

// AnnotateCodeExchangeError applies the FR-009 "exactly one re-registration"
// rule to the login flow's authorization-code exchange: an invalid_client
// after the registration was cleared, before any successful token response,
// is reported with an actionable terminal message.
// exchangeClientID is the client id the exchange used.
func (c *RefreshCoordinator) AnnotateCodeExchangeError(key string, staticClient bool, exchangeClientID string, err error) error {
	return c.AnnotateCodeExchangeErrorFor(key, commitview.CredentialBinding{}, staticClient, exchangeClientID, err)
}

// AnnotateCodeExchangeErrorFor is AnnotateCodeExchangeError for a login bound
// to the credential binding b (Spec 116 PR2b).
func (c *RefreshCoordinator) AnnotateCodeExchangeErrorFor(key string, b commitview.CredentialBinding, staticClient bool, exchangeClientID string, err error) error {
	if err == nil || staticClient {
		return err
	}
	if cls, _ := ClassifyRefreshError(err); cls != RefreshClassInvalidClient {
		return err
	}
	c.mu.Lock()
	cleared, clearedID := false, ""
	if st := c.keys[flightKey{key, b}]; st != nil {
		cleared, clearedID = st.dcrCleared, st.clearedClientID
	}
	c.mu.Unlock()
	if !cleared {
		return err
	}
	if exchangeClientID != "" && exchangeClientID == clearedID {
		return &RefreshFailure{Class: RefreshClassInvalidClient, Err: err, Message: signInToReRegisterMessage}
	}
	return &RefreshFailure{Class: RefreshClassInvalidClient, Err: err, Message: rejectsNewRegistrationsMessage}
}

// NoteTokenSaved records a successful token response for key (a login or a
// refresh): the latch, cooldown and re-registration state are reset.
func (c *RefreshCoordinator) NoteTokenSaved(key string) {
	c.NoteTokenSavedFor(key, commitview.CredentialBinding{})
}

// NoteTokenSavedFor is NoteTokenSaved for a token saved under the credential
// binding b (Spec 116 PR2b): only that binding's state is reset.
func (c *RefreshCoordinator) NoteTokenSavedFor(key string, b commitview.CredentialBinding) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.stateLocked(flightKey{key, b})
	c.resetLocked(st)
	st.lastSavedAt = c.now()
	st.saveSeq++
}

func (c *RefreshCoordinator) resetLocked(st *refreshKeyState) {
	st.latchGen, st.latchErr = "", nil
	st.cooldownGen, st.cooldownUntil, st.cooldownErr = "", time.Time{}, nil
	st.transientFailures = 0
	st.transientPending = false
	st.dcrCleared = false
	st.clearedClientID = ""
}

// refreshBackoff mirrors RefreshManager.calculateBackoff (10 s doubling, 5 min cap).
func refreshBackoff(retry int) time.Duration {
	if retry < 0 {
		retry = 0
	}
	if retry > maxBackoffExponent {
		return MaxRetryBackoff
	}
	d := RetryBackoffBase * time.Duration(1<<uint(retry))
	if d <= 0 || d > MaxRetryBackoff {
		d = MaxRetryBackoff
	}
	return d
}

// generationOf fingerprints the refresh token and client id of a record. It
// identifies the grant a flight started from without keeping token values.
func generationOf(rec *storage.OAuthTokenRecord) string {
	if rec == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(rec.RefreshToken + "\x00" + rec.ClientID))
	return hex.EncodeToString(sum[:12])
}

type flightGenerationKey struct{}

func withFlightGeneration(ctx context.Context, gen string) context.Context {
	return context.WithValue(ctx, flightGenerationKey{}, gen)
}

type flightSavedRevisionKey struct{}

func withFlightSavedRevision(ctx context.Context, sink *atomic.Uint64) context.Context {
	return context.WithValue(ctx, flightSavedRevisionKey{}, sink)
}

// noteFlightSavedRevision records, for the refresh flight ctx belongs to, the
// revision of the record its persist wrote (Sol r3 on #1621). No-op outside a
// flight.
func noteFlightSavedRevision(ctx context.Context, rev uint64) {
	if ctx == nil {
		return
	}
	if sink, ok := ctx.Value(flightSavedRevisionKey{}).(*atomic.Uint64); ok && sink != nil {
		sink.Store(rev)
	}
}

// NoteFlightSavedRevision is noteFlightSavedRevision for a refresh function
// that persists outside PersistentTokenStore.SaveToken (core's stored-DCR
// sub-path, Sol r4 on #1621): every flight persist reports the revision it
// wrote, so the flight serves that record and never a later rewrite's.
func NoteFlightSavedRevision(ctx context.Context, rev uint64) {
	noteFlightSavedRevision(ctx, rev)
}

// FlightGeneration returns the generation of the refresh flight ctx belongs
// to. A token persisted under such a ctx MUST only be written while the stored
// record still has that generation (FR-006a).
func FlightGeneration(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	gen, ok := ctx.Value(flightGenerationKey{}).(string)
	return gen, ok
}

// GenerationMatches reports whether rec still has the generation gen.
func GenerationMatches(rec *storage.OAuthTokenRecord, gen string) bool {
	return generationOf(rec) == gen
}

func tokenFromRecord(rec *storage.OAuthTokenRecord) *client.Token {
	return &client.Token{
		AccessToken:  rec.AccessToken,
		RefreshToken: rec.RefreshToken,
		TokenType:    rec.TokenType,
		ExpiresAt:    rec.ExpiresAt,
		Scope:        strings.Join(rec.Scopes, " "),
	}
}

// errIsTerminal is a small helper for callers holding only an error.
func errIsTerminal(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	cls, _ := ClassifyRefreshError(err)
	return cls.IsTerminal()
}
