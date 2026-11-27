package core

import (
	"context"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	proxytransport "github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// GH #1910: lifecycle of the standalone GET stream of a streamable-HTTP
// upstream (the mechanics, and why mcp-go's own listener is not used, are in
// internal/transport/listen_stream.go).
//
// One stream per connection, and only for an upstream whose initialize result
// advertises tools.listChanged: any other upstream gets no GET and holds no
// extra connection. Its context derives from the instance's gate, so a
// retirement (server removal) ends it. Disconnect and a reconnect end it and
// release the connection (listenClient), so nothing can start another one for
// that connection afterwards; a session re-initialize replaces it with one on
// the new session, waiting for the old one first, so it never ends with two.
//
// The listener never runs the notification handlers itself: it queues each
// notification for a separate dispatch goroutine and goes back to reading. The
// managed layer holds its own lock across core Disconnect, and its
// tools/prompts-changed callbacks take that lock, so a stop that waited on a
// dispatch could block on its own caller until the stop timeout. A stop waits
// only for the reader; the dispatcher exits on the same context, once any
// handler call already under way returns.

// listChangedStream is one running listener.
type listChangedStream struct {
	cancel context.CancelFunc
	done   chan struct{} // closed once the reader goroutine has exited
	run    func(context.Context) proxytransport.ListenEnd
	ctx    context.Context
	queue  *notificationQueue
}

// listChangedStreamStopTimeout bounds how long a stop waits for the listener
// goroutine (it only has to see its context cancelled).
const listChangedStreamStopTimeout = 5 * time.Second

// maxQueuedNotificationMethods bounds the dispatch queue: notifications are
// coalesced per method, so it holds at most this many distinct methods.
const maxQueuedNotificationMethods = 8

// notificationQueue hands the listener's notifications to the dispatch
// goroutine without blocking the listener. A notification whose method is
// already queued is coalesced into it: a list_changed notification carries no
// payload, and one re-list answers any number of them.
type notificationQueue struct {
	mu      sync.Mutex
	pending []mcp.JSONRPCNotification
	wake    chan struct{}
}

func newNotificationQueue() *notificationQueue {
	return &notificationQueue{wake: make(chan struct{}, 1)}
}

// push queues n. It never blocks. Only the methods dispatchNotification acts
// on are queued, so notifications it would ignore (logging, progress,
// resources, ...) can never fill the queue and crowd out a list_changed one.
func (q *notificationQueue) push(n mcp.JSONRPCNotification) {
	if !dispatchedNotification(n.Method) {
		return
	}
	q.mu.Lock()
	queued := false
	for _, p := range q.pending {
		if p.Method == n.Method {
			queued = true
			break
		}
	}
	if !queued && len(q.pending) < maxQueuedNotificationMethods {
		q.pending = append(q.pending, n)
	}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// dispatchedNotification reports whether dispatchNotification acts on method.
func dispatchedNotification(method string) bool {
	switch method {
	case string(mcp.MethodNotificationToolsListChanged), string(mcp.MethodNotificationPromptsListChanged):
		return true
	default:
		return false
	}
}

// take removes and returns everything queued, oldest first.
func (q *notificationQueue) take() []mcp.JSONRPCNotification {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.pending
	q.pending = nil
	return out
}

// listChangedStreamOptions is a test seam for the backoff timings.
var listChangedStreamOptions = proxytransport.ListenStreamOptions{}

// advertisesToolsListChanged reports whether initialize said the server sends
// notifications/tools/list_changed.
func advertisesToolsListChanged(info *mcp.InitializeResult) bool {
	return info != nil && info.Capabilities.Tools != nil && info.Capabilities.Tools.ListChanged
}

// startListChangedStream claims the connection cl just established (info is
// its initialize result) and starts its GET stream when it is a streamable-HTTP
// transport whose upstream advertises tools.listChanged.
//
// Connect calls it with c.mu held, so it neither takes c.mu nor waits for a
// previous listener (whose notification dispatch takes c.mu.RLock); there is
// normally none, the reconnect path having stopped it.
func (c *Client) startListChangedStream(cl *client.Client, info *mcp.InitializeResult) {
	s := c.newListChangedStream(cl, info)
	c.listenMu.Lock()
	old := c.listen
	c.listenClient = cl
	c.listen = s
	c.listenMu.Unlock()
	if old != nil {
		old.cancel()
	}
	c.runListChangedStream(s)
}

// restartListChangedStream moves the GET stream of cl to the session a
// re-initialize just established (info is its initialize result), or drops it
// if the upstream no longer advertises tools.listChanged. It does nothing once
// cl's connection was released by Disconnect or a reconnect. The previous
// listener has exited before the new one sends anything. Must not be called
// with c.mu held.
func (c *Client) restartListChangedStream(cl *client.Client, info *mcp.InitializeResult) {
	s := c.newListChangedStream(cl, info)
	c.listenMu.Lock()
	if cl == nil || c.listenClient != cl {
		c.listenMu.Unlock()
		if s != nil {
			s.cancel()
		}
		return
	}
	old := c.listen
	c.listen = s
	c.listenMu.Unlock()
	c.awaitListChangedStream(old)
	c.runListChangedStream(s)
}

// stopListChangedStream ends the running listener and releases the connection,
// so a racing re-initialize cannot start another one for it. With wait it
// returns once the listener goroutine has exited (bounded); callers holding
// c.mu must pass false.
func (c *Client) stopListChangedStream(wait bool) {
	c.listenMu.Lock()
	s := c.listen
	c.listen = nil
	c.listenClient = nil
	c.listenMu.Unlock()
	if s == nil {
		return
	}
	if wait {
		c.awaitListChangedStream(s)
	} else {
		s.cancel()
	}
}

// endListChangedStream ends cl's running listener but keeps the connection
// claimed: a failed re-initialize leaves the next one free to restart it. It
// does nothing once cl's connection was released, so a late failure on an old
// connection cannot end the stream of the connection that replaced it.
func (c *Client) endListChangedStream(cl *client.Client) {
	c.listenMu.Lock()
	if cl == nil || c.listenClient != cl {
		c.listenMu.Unlock()
		return
	}
	s := c.listen
	c.listen = nil
	c.listenMu.Unlock()
	c.awaitListChangedStream(s)
}

// newListChangedStream builds the listener for cl, or returns nil when cl
// should have none. It does not start it.
func (c *Client) newListChangedStream(cl *client.Client, info *mcp.InitializeResult) *listChangedStream {
	sh, ok := innerTransport(cl).(*transport.StreamableHTTP)
	if !ok || !advertisesToolsListChanged(info) {
		return nil
	}
	cfg := c.httpTransportConfig(c.config, nil)
	// The stream authenticates like the transport it belongs to: the OAuth
	// bearer comes from sh itself; static headers ride along except on the
	// no-auth strategy, which connected without them.
	if c.AuthStrategy() == "no-auth" {
		cfg.Headers = nil
	}
	queue := newNotificationQueue()
	opts := listChangedStreamOptions
	opts.ProtocolVersion = info.ProtocolVersion
	opts.Logger = c.logger
	opts.OnNotification = func(n mcp.JSONRPCNotification) {
		// Not through the queue: it coalesces per method, and each completion
		// is for a different elicitation. Complete never blocks.
		if n.Method == string(mcp.MethodNotificationElicitationComplete) {
			c.urlElicitations.Complete(n)
			return
		}
		// GH #1967: an upstream whose server library hands one session's
		// notifications to whichever stream reads first (mcp-go does) sends a
		// call's progress here too. Each carries its call's own token, so it
		// must not be coalesced either; Dispatch never blocks.
		if n.Method == string(mcp.MethodNotificationProgress) {
			c.progress.Dispatch(n)
			return
		}
		queue.push(n)
	}
	if len(c.relayFeatures()) > 0 {
		opts.OnRequest = c.relayHandler().Answer
	}
	ls := cfg.NewListenStream(sh, opts)

	ctx, cancel := context.WithCancel(c.Gate().Context())
	return &listChangedStream{cancel: cancel, done: make(chan struct{}), run: ls.Run, ctx: ctx, queue: queue}
}

// runListChangedStream starts s's goroutine. A nil s is a no-op.
func (c *Client) runListChangedStream(s *listChangedStream) {
	if s == nil {
		return
	}
	serverName := c.config.Name
	c.logger.Debug("Opening the upstream's standalone GET stream for tools/list_changed",
		zap.String("server", serverName))
	go c.dispatchListChangedNotifications(s)
	go func() {
		defer close(s.done)
		defer s.cancel()
		end := s.run(s.ctx)
		switch end.Reason {
		case proxytransport.ListenUnsupported:
			c.logger.Info("Upstream does not serve the standalone GET stream; tool changes are picked up by the discovery poll",
				zap.String("server", serverName), zap.Int("status", end.Status), zap.String("detail", end.Detail))
		case proxytransport.ListenSessionTerminated:
			c.logger.Debug("Upstream GET stream: session not found; a session re-initialize or reconnect re-opens it",
				zap.String("server", serverName))
		}
		c.listenMu.Lock()
		if c.listen == s {
			c.listen = nil
		}
		c.listenMu.Unlock()
	}()
}

// dispatchListChangedNotifications runs the handlers for what s's listener
// queued, until s's context ends. Nothing waits for it (see the file comment).
func (c *Client) dispatchListChangedNotifications(s *listChangedStream) {
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.queue.wake:
		}
		for _, n := range s.queue.take() {
			if s.ctx.Err() != nil {
				return
			}
			c.dispatchNotification(n)
		}
	}
}

// awaitListChangedStream cancels s and waits (bounded) for its goroutine.
func (c *Client) awaitListChangedStream(s *listChangedStream) {
	if s == nil {
		return
	}
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(listChangedStreamStopTimeout):
		c.logger.Warn("Upstream GET stream listener did not stop in time",
			zap.String("server", c.config.Name))
	}
}

// listChangedStreamRunning reports whether a listener is running (tests).
func (c *Client) listChangedStreamRunning() bool {
	c.listenMu.Lock()
	defer c.listenMu.Unlock()
	return c.listen != nil
}
