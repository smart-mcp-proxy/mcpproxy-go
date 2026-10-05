package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
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
	// Key is the server key (GenerateServerKey); all flights are keyed by it.
	Key string
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
}

// RefreshOutcome is passed to the completion hook for a terminal flight
// outcome whose generation still matches the stored record.
type RefreshOutcome struct {
	ServerName string
	Key        string
	Trigger    RefreshTrigger
	Class      RefreshErrorClass
	Err        error
}

type refreshFlight struct {
	done    chan struct{}
	waiters int
	tok     *client.Token
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
	cooldownGen       string
	cooldownUntil     time.Time
	cooldownErr       error
	transientFailures int

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
	keys   map[string]*refreshKeyState
	hook   func(RefreshOutcome)
	now    func() time.Time
	logger *zap.Logger
}

// NewRefreshCoordinator returns an empty coordinator.
func NewRefreshCoordinator() *RefreshCoordinator {
	return &RefreshCoordinator{
		keys: make(map[string]*refreshKeyState),
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

func (c *RefreshCoordinator) log() *zap.Logger {
	if c.logger != nil {
		return c.logger
	}
	return zap.L().Named("oauth-refresh")
}

func (c *RefreshCoordinator) stateLocked(key string) *refreshKeyState {
	st := c.keys[key]
	if st == nil {
		st = &refreshKeyState{}
		c.keys[key] = st
	}
	return st
}

// waiters reports how many callers are attached to the key's current flight (tests).
func (c *RefreshCoordinator) waiters(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.keys[key]; st != nil && st.flight != nil {
		return st.flight.waiters
	}
	return 0
}

// Do runs at most one refresh flight per key. A caller arriving while a flight
// is running waits for it and gets the same token or the same error. skipped
// reports that the flight made no network request (another flight had already
// rotated the token, or the latch/cooldown answered). A caller whose own ctx
// ends returns ctx.Err(); the flight continues for the others.
func (c *RefreshCoordinator) Do(ctx context.Context, req RefreshRequest) (*client.Token, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	st := c.stateLocked(req.Key)
	f := st.flight
	if f == nil {
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
		return f.tok, f.skipped, f.err
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
}

func (c *RefreshCoordinator) run(callerCtx context.Context, req RefreshRequest, st *refreshKeyState, f *refreshFlight) {
	fctx, cancel := context.WithTimeout(context.WithoutCancel(callerCtx), refreshFlightTimeout)
	defer cancel()

	start := c.now()
	tok, skipped, err := c.flight(fctx, req, st)

	cls, status := ClassifyRefreshError(err)
	c.log().Info("OAuth refresh flight finished",
		zap.String("server", req.ServerName),
		zap.String("trigger", string(req.Trigger)),
		zap.String("outcome", string(cls)),
		zap.Int("http_status", status),
		zap.Bool("network_skipped", skipped),
		zap.Duration("duration", c.now().Sub(start)))

	c.mu.Lock()
	f.tok, f.skipped, f.err = tok, skipped, err
	st.flight = nil
	c.mu.Unlock()
	close(f.done)
}

func (c *RefreshCoordinator) flight(ctx context.Context, req RefreshRequest, st *refreshKeyState) (*client.Token, bool, error) {
	rec, err := req.Load()
	if err != nil || rec == nil || (rec.AccessToken == "" && rec.RefreshToken == "") {
		return nil, true, &RefreshFailure{Class: RefreshClassTerminalOther, Message: "no stored OAuth token; sign in again", Err: ErrNoRefreshToken}
	}
	// FR-003: another flight (or a login) already rotated the token.
	if rec.RefreshToken != req.ObservedRefreshToken && rec.AccessToken != "" {
		return tokenFromRecord(rec), true, nil
	}
	if rec.RefreshToken == "" {
		return nil, true, &RefreshFailure{Class: RefreshClassTerminalOther, Message: "no refresh token stored; sign in again", Err: ErrNoRefreshToken}
	}

	gen := generationOf(rec)
	c.mu.Lock()
	// SC-001: a proactive caller that read the record only after another
	// flight (or a login) rotated it observes the new refresh token, so the
	// check above cannot tell. A token saved moments ago is not due.
	if req.Trigger == RefreshTriggerProactive && rec.AccessToken != "" && !st.lastSavedAt.IsZero() &&
		c.now().Sub(st.lastSavedAt) < proactiveFreshWindow &&
		(rec.ExpiresAt.IsZero() || c.now().Before(rec.ExpiresAt)) {
		c.mu.Unlock()
		return tokenFromRecord(rec), true, nil
	}
	if st.latchErr != nil && st.latchGen == gen {
		latched := st.latchErr
		c.mu.Unlock()
		return nil, true, latched
	}
	if req.Trigger == RefreshTriggerReactive && st.cooldownGen == gen && c.now().Before(st.cooldownUntil) {
		until, last := st.cooldownUntil, st.cooldownErr
		c.mu.Unlock()
		return nil, true, fmt.Errorf("%w (retry after %s): %w", ErrTokenRefreshTransient, until.Format(time.RFC3339), last)
	}
	c.mu.Unlock()

	tok, refreshErr := req.Refresh(withFlightGeneration(ctx, gen), rec)
	cur, loadErr := req.Load()
	if loadErr != nil {
		cur = nil
	}

	if refreshErr == nil {
		c.mu.Lock()
		c.resetLocked(st)
		st.lastSavedAt = c.now()
		c.mu.Unlock()
		// FR-006a: the flight's persist was discarded because something newer
		// was saved meanwhile; hand out the stored token instead.
		if tok == nil || (cur != nil && cur.AccessToken != "" && cur.AccessToken != tok.AccessToken) {
			if cur == nil {
				return nil, false, &RefreshFailure{Class: RefreshClassOther, Message: "refresh succeeded but no token is stored", Err: ErrRefreshFailed}
			}
			return tokenFromRecord(cur), false, nil
		}
		return tok, false, nil
	}

	cls, _ := ClassifyRefreshError(refreshErr)

	// FR-006a: a login or another writer superseded this flight's generation.
	// Its failure has no side effects; a newer grant is handed out.
	if cur == nil || generationOf(cur) != gen {
		if cur != nil && cur.AccessToken != "" && cur.RefreshToken != rec.RefreshToken {
			return tokenFromRecord(cur), false, nil
		}
		return nil, false, refreshErr
	}

	if cls == RefreshClassInvalidClient {
		refreshErr = c.handleInvalidClient(req, rec, st, refreshErr)
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
					return tokenFromRecord(again), false, nil
				}
				return nil, false, refreshErr
			}
			latchRec = again // after a DCR clear the generation changed
		}
		c.mu.Lock()
		st.latchGen = generationOf(latchRec)
		st.latchErr = refreshErr
		hook := c.hook
		c.mu.Unlock()
		if hook != nil {
			hook(RefreshOutcome{ServerName: req.ServerName, Key: req.Key, Trigger: req.Trigger, Class: cls, Err: refreshErr})
		}
		return nil, false, refreshErr
	}

	c.mu.Lock()
	st.transientFailures++
	st.cooldownGen = gen
	st.cooldownUntil = c.now().Add(refreshBackoff(st.transientFailures - 1))
	st.cooldownErr = refreshErr
	c.mu.Unlock()
	return nil, false, refreshErr
}

// handleInvalidClient implements FR-009 and returns the error to hand out.
func (c *RefreshCoordinator) handleInvalidClient(req RefreshRequest, rec *storage.OAuthTokenRecord, st *refreshKeyState, err error) error {
	if req.StaticClient {
		return &RefreshFailure{Class: RefreshClassInvalidClient, Err: err,
			Message: "the authorization server rejected the configured OAuth client (invalid_client); fix oauth.client_id / oauth.client_secret for this server, then sign in again"}
	}
	c.mu.Lock()
	alreadyCleared := st.dcrCleared
	c.mu.Unlock()
	if alreadyCleared {
		return &RefreshFailure{Class: RefreshClassInvalidClient, Err: err, Message: rejectsNewRegistrationsMessage}
	}
	// No "login flow active" exemption: the clear is a compare-and-clear on
	// the client id AND refresh token this request used, so a login that
	// saved a new registration or a new grant is never touched, while a
	// connect attempt that is itself running this refresh (tryOAuthAuth
	// holds a flow for its whole duration) still gets the rejected
	// registration removed and can re-register on the next sign-in.
	if req.ClearClient != nil && rec.ClientID != "" {
		cleared, clearErr := req.ClearClient(rec.ClientID, rec.RefreshToken)
		if clearErr != nil {
			c.log().Warn("Failed to clear rejected DCR client registration",
				zap.String("server", req.ServerName), zap.Error(clearErr))
		}
		if cleared {
			c.mu.Lock()
			st.dcrCleared = true
			st.clearedClientID = rec.ClientID
			c.mu.Unlock()
			c.log().Info("Cleared DCR client registration rejected by the authorization server",
				zap.String("server", req.ServerName))
		}
	}
	return &RefreshFailure{Class: RefreshClassInvalidClient, Err: err,
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
	if err == nil || staticClient {
		return err
	}
	if cls, _ := ClassifyRefreshError(err); cls != RefreshClassInvalidClient {
		return err
	}
	c.mu.Lock()
	cleared, clearedID := false, ""
	if st := c.keys[key]; st != nil {
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
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.stateLocked(key)
	c.resetLocked(st)
	st.lastSavedAt = c.now()
}

func (c *RefreshCoordinator) resetLocked(st *refreshKeyState) {
	st.latchGen, st.latchErr = "", nil
	st.cooldownGen, st.cooldownUntil, st.cooldownErr = "", time.Time{}, nil
	st.transientFailures = 0
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
