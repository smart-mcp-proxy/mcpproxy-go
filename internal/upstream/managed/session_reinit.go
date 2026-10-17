package managed

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
)

// Spec 113-e (G8): when a Streamable HTTP upstream answers HTTP 404 for a
// session id it no longer knows (mcp-go: transport.ErrSessionTerminated), the
// managed client re-initializes the MCP session in place and retries the
// request once, instead of flipping the whole server into Error and tearing
// the connection down. The Streamable HTTP rule that an unknown session is
// rejected before the method executes is what makes a retry safe; because
// mcp-go maps EVERY 404 to ErrSessionTerminated, tools/call is additionally
// retried only for read-only tools whose identity hash is unchanged (FR-081).

// ErrSessionReestablished is returned for a tools/call that hit a terminated
// session when the call was NOT repeated (write/destructive tool, unknown or
// changed tool identity). The session itself was re-established, so the next
// call works. It is not a connection failure and never marks the server Error.
var ErrSessionReestablished = errors.New("upstream session was lost and re-established; the call was not repeated")

// sessionReestablishedError is the ErrSessionReestablished returned for a
// call that was not repeated. It also matches transport.ErrSessionTerminated,
// its cause, so call-error classification (Spec 113-c) still reports
// session_terminated rather than the bare 404 status.
type sessionReestablishedError struct{ detail string }

func (e *sessionReestablishedError) Error() string {
	return ErrSessionReestablished.Error() + " (" + e.detail + ")"
}

func (e *sessionReestablishedError) Is(target error) bool {
	return target == ErrSessionReestablished || target == transport.ErrSessionTerminated
}

// reinitTimeout bounds one re-init flight (initialize + tools/list). The
// flight is detached from any single caller's context so one caller going
// away cannot fail the callers waiting on it.
const reinitTimeout = 30 * time.Second

type reinitFlight struct {
	done chan struct{}
	err  error
}

type toolIdentity struct {
	hash     string
	readOnly bool
}

// sessionReinit is the per-client re-init state. The zero value is ready.
type sessionReinit struct {
	mu       sync.Mutex
	known    string // last session id seen on the transport
	flight   *reinitFlight
	baseline map[string]toolIdentity // raw tool name -> identity from the last good listing
	count    atomic.Int64
	// verifiedGen is the core client's session generation whose toolset the
	// baseline was last verified against (a tools/list on that session). A
	// re-initialize whose verifying tools/list failed leaves it behind the
	// current generation, and no tools/call is sent until a listing catches
	// it up (sendVerified).
	verifiedGen uint64

	// beforeSend, when set (tests only), runs in sendVerified after the
	// checks and right before the send, i.e. inside the window a concurrent
	// re-initialize can overtake.
	beforeSend func()
}

// SessionReinitCount reports how many in-place session re-initializations this
// client has performed (FR-085).
func (mc *Client) SessionReinitCount() int64 { return mc.sess.count.Load() }

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// publishBaseline remembers the identity hash and read-only-ness of every tool
// in a successful listing taken on session generation gen, the reference a
// post-re-init listing is compared with, and marks gen verified. The baseline
// and its generation are published together under sess.mu, and a listing a
// re-initialize has overtaken (the generation moved since it started) is
// dropped without touching either, so an old session's listing can never
// replace a newer session's verified baseline. changed reports whether the
// published toolset differs from the previous baseline.
func (mc *Client) publishBaseline(tools []*config.ToolMetadata, gen uint64) (changed, published bool) {
	next := make(map[string]toolIdentity, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		next[t.Name] = toolIdentity{hash: t.Hash, readOnly: isReadOnlyTool(t)}
	}
	mc.sess.mu.Lock()
	defer mc.sess.mu.Unlock()
	if mc.coreClient != nil && mc.coreClient.SessionGeneration() != gen {
		return false, false
	}
	prev := mc.sess.baseline
	changed = len(prev) != len(next)
	for name, id := range next {
		if p, ok := prev[name]; !ok || p.hash != id.hash {
			changed = true
		}
	}
	mc.sess.baseline = next
	mc.sess.verifiedGen = gen
	return changed, true
}

func (mc *Client) sessionGeneration() uint64 {
	if mc.coreClient == nil {
		return 0
	}
	return mc.coreClient.SessionGeneration()
}

func isReadOnlyTool(t *config.ToolMetadata) bool {
	a := t.Annotations
	if a == nil || a.ReadOnlyHint == nil || !*a.ReadOnlyHint {
		return false
	}
	return a.DestructiveHint == nil || !*a.DestructiveHint
}

func (mc *Client) toolIdentityOf(name string) (toolIdentity, bool) {
	mc.sess.mu.Lock()
	defer mc.sess.mu.Unlock()
	id, ok := mc.sess.baseline[name]
	return id, ok
}

// sessionState returns the session id a request would use now, whether the
// negotiated protocol is stateless, and the last known session id.
func (mc *Client) sessionState() (current string, known string, applicable bool) {
	if mc.coreClient == nil {
		return "", "", false
	}
	id, modern := mc.coreClient.SessionSnapshot()
	if modern {
		return "", "", false
	}
	mc.sess.mu.Lock()
	defer mc.sess.mu.Unlock()
	if id != "" {
		mc.sess.known = id
	}
	return id, mc.sess.known, true
}

// reinitSession re-initializes the session identified by stale (the id the
// failing request used, or the known id when the transport had none). It is
// single-flight: concurrent callers for the same stale id share one flight,
// and a caller that finds a newer session already in place does nothing.
func (mc *Client) reinitSession(ctx context.Context, stale, method string) error {
	s := &mc.sess
	s.mu.Lock()
	// A running flight wins over the known-id comparison: initialize installs
	// the new session id before the flight's tools/list has been verified, so
	// "newer session in place" must not let a caller slip past the flight
	// (FR-082, FR-083a).
	if f := s.flight; f != nil {
		s.mu.Unlock()
		return waitFlight(ctx, f)
	}
	if s.known != stale {
		s.mu.Unlock()
		return nil
	}
	f := &reinitFlight{done: make(chan struct{})}
	s.flight = f
	s.mu.Unlock()

	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reinitTimeout)
	defer cancel()
	err := mc.runReinit(fctx)

	newID, _ := mc.coreClient.SessionSnapshot()
	s.mu.Lock()
	if newID != "" || err == nil {
		s.known = newID
	}
	s.flight = nil
	s.mu.Unlock()
	f.err = err
	close(f.done)

	serverName := mc.GetConfig().Name
	if err != nil {
		mc.logger.Warn("Upstream session re-initialize failed",
			zap.String("server", serverName), zap.String("method", method),
			zap.String("stale_session", shortID(stale)), zap.Error(err))
		return err
	}
	s.count.Add(1)
	mc.logger.Info("Upstream session terminated (HTTP 404); re-initialized in place",
		zap.String("server", serverName), zap.String("method", method),
		zap.String("stale_session", shortID(stale)), zap.String("new_session", shortID(newID)),
		zap.Int64("reinit_count", s.count.Load()))
	return nil
}

func waitFlight(ctx context.Context, f *reinitFlight) error {
	select {
	case <-f.done:
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runReinit is the flight body: initialize on the same transport, then a
// synchronous tools/list to refresh the identity baseline. A changed toolset
// schedules the normal discovery path (differential update / quarantine
// semantics); connectionEpoch is never touched here (FR-083).
func (mc *Client) runReinit(ctx context.Context) error {
	if err := mc.coreClient.ReinitializeSession(ctx); err != nil {
		return err
	}
	if err := mc.verifyToolset(ctx); err != nil {
		return fmt.Errorf("tools/list after session re-initialize: %w", err)
	}
	return nil
}

// verifyToolset lists the tools on the current session and publishes the
// listing as the identity baseline of the generation it ran on (scheduling the
// discovery path when the toolset changed). A listing overtaken by another
// re-initialize publishes nothing; the newer session's own listing does.
func (mc *Client) verifyToolset(ctx context.Context) error {
	gen := mc.sessionGeneration()
	tools, err := mc.coreClient.ListTools(ctx)
	if err != nil {
		return err
	}
	if changed, _ := mc.publishBaseline(tools, gen); changed {
		mc.scheduleToolRefresh()
	}
	return nil
}

func (mc *Client) sessionVerified(gen uint64) bool {
	mc.sess.mu.Lock()
	defer mc.sess.mu.Unlock()
	return mc.sess.verifiedGen == gen
}

func (mc *Client) scheduleToolRefresh() {
	mc.mu.RLock()
	cb := mc.toolDiscoveryCallback
	mc.mu.RUnlock()
	if cb == nil {
		return
	}
	name := mc.GetConfig().Name
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := cb(ctx, name); err != nil {
			mc.logger.Warn("Tool refresh after session re-initialize failed",
				zap.String("server", name), zap.Error(err))
		}
	}()
}

// joinGap re-initializes before sending when the transport has no session id
// but one is known: another request already hit the 404 and mcp-go cleared it,
// so sending now would go out without a session (FR-080, FR-082).
func (mc *Client) joinGap(ctx context.Context, method string) error {
	// A flight in progress (initialize done, tools/list not yet verified) is
	// joined even though the transport already has a session id again.
	mc.sess.mu.Lock()
	f := mc.sess.flight
	mc.sess.mu.Unlock()
	if f != nil {
		return waitFlight(ctx, f)
	}
	cur, known, ok := mc.sessionState()
	if !ok || cur != "" || known == "" {
		return nil
	}
	return mc.reinitSession(ctx, known, method)
}

// withSession runs an idempotent request (list, get, ping) with one re-init and
// one retry on ErrSessionTerminated. Only the final error is returned.
func (mc *Client) withSession(ctx context.Context, method string, fn func() error) error {
	if err := mc.joinGap(ctx, method); err != nil {
		return err
	}
	cur, known, ok := mc.sessionState()
	stale := cur
	if stale == "" {
		stale = known
	}
	err := fn()
	if err == nil || !ok || stale == "" || !errors.Is(err, transport.ErrSessionTerminated) {
		return err
	}
	if rerr := mc.reinitSession(ctx, stale, method); rerr != nil {
		// Keep the original 404 (so the existing connection-error path still
		// matches) and surface why recovery failed (e.g. an auth error).
		return errors.Join(err, rerr)
	}
	return fn()
}

// probeLiveness is the health ping with session recovery.
func (mc *Client) probeLiveness(ctx context.Context, p livenessProber) error {
	if p != livenessProber(mc.coreClient) {
		return p.Ping(ctx)
	}
	return mc.withSession(ctx, string(mcp.MethodPing), func() error { return p.Ping(ctx) })
}

// listToolsUpstream is the upstream tools/list with session recovery.
func (mc *Client) listToolsUpstream(ctx context.Context) (tools []*config.ToolMetadata, err error) {
	gen := mc.sessionGeneration()
	err = mc.withSession(ctx, string(mcp.MethodToolsList), func() error {
		var e error
		tools, e = mc.coreClient.ListTools(ctx)
		return e
	})
	if err == nil {
		// Dropped when a re-initialize overtook this listing (including one
		// withSession ran for it): that re-init published its own listing.
		mc.publishBaseline(tools, gen)
	}
	return tools, err
}

// callToolWithSession is tools/call with session recovery (FR-081). invoker is
// the dispatch surface callTool already chose; recovery applies only when it is
// the real core client (test fakes bypass it).
func (mc *Client) callToolWithSession(ctx context.Context, invoker toolCaller, toolName string, args map[string]interface{}, expectedEpoch *int64) (*mcp.CallToolResult, error) {
	if mc.coreClient == nil || invoker != toolCaller(mc.coreClient) {
		return invoker.CallTool(ctx, toolName, args)
	}
	// The identity this call is held against, read before anything can re-init.
	pre, preKnown := mc.toolIdentityOf(toolName)
	method := string(mcp.MethodToolsCall)

	// stale is the session the send went out on (or the known id when the
	// transport had none); sent is false when nothing was sent, in which case
	// an error is never retried.
	var stale string
	var ok, sent bool
	result, err := mc.sendVerified(ctx, method, toolName, args, func() error {
		return mc.identityRefusal(toolName, pre, preKnown, expectedEpoch)
	}, func() {
		var cur, known string
		cur, known, ok = mc.sessionState()
		stale = cur
		if stale == "" {
			stale = known
		}
		sent = true
	})
	if err == nil || !sent || !ok || stale == "" || !errors.Is(err, transport.ErrSessionTerminated) ||
		errors.Is(err, ErrSessionReestablished) {
		return result, err
	}
	if rerr := mc.reinitSession(ctx, stale, method); rerr != nil {
		return nil, errors.Join(err, rerr)
	}
	// The retry is held to the re-listed toolset: the tool's identity must be
	// unchanged and both listings must say read-only (annotations are not part
	// of the identity hash). sendVerified re-runs these checks if yet another
	// re-init lands before the retry is sent.
	return mc.sendVerified(ctx, method, toolName, args, func() error {
		post, postKnown := mc.toolIdentityOf(toolName)
		if !preKnown || !postKnown || post.hash != pre.hash {
			if expectedEpoch != nil {
				return ErrConnectionGenerationChanged
			}
			return &sessionReestablishedError{detail: fmt.Sprintf("tool %q identity not confirmed after re-initialize", toolName)}
		}
		if !pre.readOnly || !post.readOnly {
			return &sessionReestablishedError{detail: fmt.Sprintf("tool %q is not read-only", toolName)}
		}
		if expectedEpoch != nil && !mc.generationIs(*expectedEpoch) {
			return ErrConnectionGenerationChanged
		}
		return nil
	}, nil)
}

// maxSessionAdmits bounds how many times one send re-validates after a session
// re-initialize overtook it. Each round waits for a whole re-init flight, so
// hitting the bound means the upstream keeps dropping sessions.
const maxSessionAdmits = 3

// sendVerified sends one tools/call only on a session whose toolset the checks
// in verify were run against (FR-082, FR-083a). It joins any re-init flight in
// progress, takes the core client's session generation, and runs verify; the
// send is pinned to that generation, so if a re-initialize lands between the
// checks and the send (the call queued behind it on the core client's session
// gate), the core client refuses it with nothing sent and the checks are run
// again against the re-listed toolset. observe, when set, runs right before
// each send to capture the session the send will use.
//
// The generation is read BEFORE the flight check: a flight is registered
// before its Initialize moves the generation and cleared only after its
// tools/list ran, so seeing no flight after the read means that any later
// re-initialize changes the generation and is caught at send, and that an
// earlier one has finished. If that earlier one's tools/list failed, the
// generation is not verified (verifiedGen) and the toolset is listed again
// before the checks run.
func (mc *Client) sendVerified(ctx context.Context, method, toolName string, args map[string]interface{}, verify func() error, observe func()) (*mcp.CallToolResult, error) {
	for attempt := 1; ; attempt++ {
		if err := mc.joinGap(ctx, method); err != nil {
			return nil, err
		}
		gen := mc.coreClient.SessionGeneration()
		mc.sess.mu.Lock()
		f := mc.sess.flight
		mc.sess.mu.Unlock()
		if f != nil {
			if err := waitFlight(ctx, f); err != nil {
				return nil, err
			}
			if attempt < maxSessionAdmits {
				continue
			}
			return nil, &sessionReestablishedError{detail: "session re-initialized repeatedly before the call could be sent"}
		}
		if !mc.sessionVerified(gen) {
			// A re-initialize installed this session but its verifying
			// tools/list failed, so the baseline still describes the old
			// session: list again before any check is run against it.
			if attempt >= maxSessionAdmits {
				return nil, &sessionReestablishedError{detail: "toolset of the re-initialized session could not be verified"}
			}
			if err := mc.verifyToolset(ctx); err != nil {
				return nil, fmt.Errorf("tools/list to verify the re-initialized session: %w", err)
			}
			continue
		}
		if err := verify(); err != nil {
			return nil, err
		}
		if observe != nil {
			observe()
		}
		if hook := mc.sess.beforeSend; hook != nil {
			hook()
		}
		// The caller's WithDispatchRecheck verdicts are re-read by the core
		// client after it admits the send, so a verdict that flipped during a
		// re-init refuses it with nothing sent.
		result, err := mc.coreClient.CallTool(core.WithSessionGeneration(ctx, gen), toolName, args)
		if !errors.Is(err, core.ErrSessionChanged) {
			return result, err
		}
		if attempt >= maxSessionAdmits {
			return nil, &sessionReestablishedError{detail: "session re-initialized repeatedly before the call could be sent"}
		}
	}
}

// identityRefusal re-runs, after waiting on a re-init flight, the generation
// checks a fresh call would (FR-083a): the pinned epoch and the tool's
// identity hash against the re-listed toolset.
func (mc *Client) identityRefusal(toolName string, pre toolIdentity, preKnown bool, expectedEpoch *int64) error {
	if expectedEpoch != nil && !mc.generationIs(*expectedEpoch) {
		return ErrConnectionGenerationChanged
	}
	post, postKnown := mc.toolIdentityOf(toolName)
	if preKnown && (!postKnown || post.hash != pre.hash) {
		return ErrConnectionGenerationChanged
	}
	return nil
}
