package core

import (
	"context"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/clientrelay"
)

// rootsChangedTimeout bounds sending notifications/roots/list_changed upstream.
const rootsChangedTimeout = 10 * time.Second

// rootsChangedGateWait bounds waiting for a session re-initialize (and the
// calls it waits for) before sending notifications/roots/list_changed. It is
// longer than a tool call may run, so the notification is not dropped while a
// re-initialize waits behind one.
const rootsChangedGateWait = 5 * time.Minute

// relayFeatures returns which of the calling MCP client's features this server
// may use. A change to client_features or the quarantine builds a new Client,
// so the answer holds for this one's lifetime.
func (c *Client) relayFeatures() []string {
	return c.globalConfig.ClientFeaturesFor(c.config)
}

// relayOptions installs the clientrelay handler on an mcp-go client, so the
// upstream's elicitation, sampling and roots requests reach the MCP client
// whose tool call caused them (#1964, #1965, #1966). With no granted feature it
// returns none: the upstream sees no capability, exactly as before.
func (c *Client) relayOptions() []client.ClientOption {
	if len(c.relayFeatures()) == 0 {
		return nil
	}
	return c.relayHandler().Options()
}

// relayHandler also answers the requests on the upstream's GET stream, which
// mcpproxy reads itself (list_changed_stream.go).
func (c *Client) relayHandler() clientrelay.Handler {
	return clientrelay.Handler{
		Server:     c.config.Name,
		Features:   c.relayFeatures(),
		InFlight:   &c.relayInFlight,
		Caller:     c.callerContext,
		LastCaller: c.calls.lastCaller,
		URLs:       &c.urlElicitations,
		Audit:      c.auditClientRequest,
	}
}

// SetClientRequestObserver installs the sink every relayed request is reported
// to (the activity log). nil removes it.
func (c *Client) SetClientRequestObserver(fn func(clientrelay.Event)) {
	if fn == nil {
		c.requestObserver.Store(nil)
		return
	}
	c.requestObserver.Store(&fn)
}

func (c *Client) auditClientRequest(e clientrelay.Event) {
	if fn := c.requestObserver.Load(); fn != nil {
		(*fn)(e)
	}
}

// callerContext returns the context of the call an upstream request belongs
// to. A streamable-HTTP upstream sends it on the call's own response stream,
// so ctx already is that call's context. A stdio upstream's requests carry no
// call, so the request is credited to the only call in flight; with none, or
// several, nobody can be asked and ctx is returned as is.
func (c *Client) callerContext(ctx context.Context) context.Context {
	if clientrelay.HasClient(ctx) {
		return ctx
	}
	if only, ok := c.calls.only(); ok {
		return only
	}
	return ctx
}

// beginCall records a tool call in flight until the returned func is called.
// A call from a client other than the last one tells the upstream its roots
// changed, so it asks the new client for them (#1966).
func (c *Client) beginCall(callCtx context.Context) (done func()) {
	done, callerChanged := c.calls.add(callCtx)
	if callerChanged {
		c.notifyRootsChanged()
	}
	return done
}

// NotifyClientRootsChanged forwards a client's notifications/roots/list_changed
// to this upstream when that client is the last one that called it (#1966).
func (c *Client) NotifyClientRootsChanged(sessionID string) {
	if sessionID != "" && c.calls.lastSession() == sessionID {
		c.notifyRootsChanged()
	}
}

// notifyRootsChanged sends notifications/roots/list_changed upstream, in the
// background, when the connection declared roots.
func (c *Client) notifyRootsChanged() {
	c.mu.RLock()
	cl := c.client
	c.mu.RUnlock()
	if cl == nil || !c.IsConnected() {
		return
	}
	go func() {
		// A session re-initialize rewrites mcp-go's client state, the declared
		// capabilities included: read them, and send, only between two.
		gateCtx, cancelGate := context.WithTimeout(context.Background(), rootsChangedGateWait)
		release, err := c.sessionGate.acquireShared(gateCtx)
		cancelGate()
		if err != nil {
			c.logger.Debug("Not sending roots/list_changed upstream: session busy",
				zap.String("server", c.config.Name), zap.Error(err))
			return
		}
		defer release()
		if cl.GetClientCapabilities().Roots == nil {
			return
		}
		sendTimeout := rootsChangedTimeout
		if c.rootsChangedSendTimeout > 0 {
			sendTimeout = c.rootsChangedSendTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		if err := cl.RootListChanges(ctx); err != nil {
			c.logger.Debug("Sending roots/list_changed upstream failed",
				zap.String("server", c.config.Name), zap.Error(err))
		}
	}()
}

// inflightCalls is the set of tool-call contexts in flight on one client, and
// the last MCP client that made a call.
type inflightCalls struct {
	mu     sync.Mutex
	next   uint64
	ctxs   map[uint64]context.Context
	last   context.Context // the last client's call, without its cancellation
	lastID string          // that client's session id
}

// add records ctx until done is called. callerChanged reports a call from an
// MCP client other than the last one.
func (f *inflightCalls) add(ctx context.Context) (done func(), callerChanged bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ctxs == nil {
		f.ctxs = make(map[uint64]context.Context)
	}
	f.next++
	id := f.next
	f.ctxs[id] = ctx
	if sid := clientrelay.SessionID(ctx); sid != "" {
		callerChanged = sid != f.lastID
		f.last, f.lastID = context.WithoutCancel(ctx), sid
	}
	return func() {
		f.mu.Lock()
		delete(f.ctxs, id)
		f.mu.Unlock()
	}, callerChanged
}

// only returns the call in flight when there is exactly one.
func (f *inflightCalls) only() (context.Context, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ctxs) != 1 {
		return nil, false
	}
	for _, ctx := range f.ctxs {
		return ctx, true
	}
	return nil, false
}

// lastCaller returns the context of the last MCP client's call, or nil.
func (f *inflightCalls) lastCaller() context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

func (f *inflightCalls) lastSession() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastID
}
