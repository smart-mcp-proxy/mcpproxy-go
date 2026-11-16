package core

import (
	"context"
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

// listChangedStream is one running listener.
type listChangedStream struct {
	cancel context.CancelFunc
	done   chan struct{}
	run    func(context.Context) proxytransport.ListenEnd
	ctx    context.Context
}

// listChangedStreamStopTimeout bounds how long a stop waits for the listener
// goroutine (it only has to see its context cancelled).
const listChangedStreamStopTimeout = 5 * time.Second

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

// endListChangedStream ends the running listener but keeps the connection
// claimed: a failed re-initialize leaves the next one free to restart it.
func (c *Client) endListChangedStream() {
	c.listenMu.Lock()
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
	opts := listChangedStreamOptions
	opts.ProtocolVersion = info.ProtocolVersion
	opts.Logger = c.logger
	opts.OnNotification = c.dispatchNotification
	ls := cfg.NewListenStream(sh, opts)

	ctx, cancel := context.WithCancel(c.Gate().Context())
	return &listChangedStream{cancel: cancel, done: make(chan struct{}), run: ls.Run, ctx: ctx}
}

// runListChangedStream starts s's goroutine. A nil s is a no-op.
func (c *Client) runListChangedStream(s *listChangedStream) {
	if s == nil {
		return
	}
	serverName := c.config.Name
	c.logger.Debug("Opening the upstream's standalone GET stream for tools/list_changed",
		zap.String("server", serverName))
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
