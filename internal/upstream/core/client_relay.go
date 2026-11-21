package core

import (
	"context"
	"sync"

	"github.com/mark3labs/mcp-go/client"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/clientrelay"
)

// relayOptions installs the clientrelay handler on an mcp-go client, so the
// upstream's elicitation, sampling and roots requests reach the MCP client
// whose tool call caused them (#1964, #1965, #1966).
func (c *Client) relayOptions() []client.ClientOption {
	return c.relayHandler().Options()
}

// relayHandler also answers the requests on the upstream's GET stream, which
// mcpproxy reads itself (list_changed_stream.go).
func (c *Client) relayHandler() clientrelay.Handler {
	return clientrelay.Handler{Caller: c.callerContext, URLs: &c.urlElicitations, Logger: c.logger}
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

// inflightCalls is the set of tool-call contexts in flight on one client.
type inflightCalls struct {
	mu   sync.Mutex
	next uint64
	ctxs map[uint64]context.Context
}

// add records ctx until the returned func is called.
func (f *inflightCalls) add(ctx context.Context) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ctxs == nil {
		f.ctxs = make(map[uint64]context.Context)
	}
	f.next++
	id := f.next
	f.ctxs[id] = ctx
	return func() {
		f.mu.Lock()
		delete(f.ctxs, id)
		f.mu.Unlock()
	}
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
