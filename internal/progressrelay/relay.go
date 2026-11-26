// Package progressrelay forwards an upstream's notifications/progress to the
// MCP client whose tool call asked for progress (GH #1967).
//
// The client's progressToken is never sent upstream. Each upstream call that
// has a sink gets a token of its own, minted by the Registry of the upstream
// client it runs on; the Registry maps that token back to the sink until the
// call ends. Two clients that pick the same token therefore never see each
// other's progress, and an upstream cannot address a call it was not given.
package progressrelay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const (
	// MaxMessageBytes caps the upstream-written message of one notification.
	MaxMessageBytes = 512
	// MinInterval is the shortest gap between two notifications forwarded for
	// one call. Notifications that arrive sooner are dropped, except the first
	// one that reports completion (progress >= total).
	MinInterval = 100 * time.Millisecond
)

// Update is one progress report, as the upstream sent it, message capped.
type Update struct {
	Progress float64
	Total    *float64
	Message  string
}

// Sink receives the forwarded updates of one call. It must not block.
type Sink func(Update)

type sinkKey struct{}

// WithSink returns ctx carrying the sink an upstream call started with it
// reports progress to.
func WithSink(ctx context.Context, s Sink) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, sinkKey{}, s)
}

// SinkFrom returns the sink in ctx, or nil.
func SinkFrom(ctx context.Context) Sink {
	s, _ := ctx.Value(sinkKey{}).(Sink)
	return s
}

// SessionSink returns a sink that sends each update to the MCP client session
// in ctx under the client's own token, or nil when progress cannot be
// forwarded: the client sent no token, or ctx carries no session that can
// receive notifications. clean, when not nil, is applied to every message; a
// message it empties is left out and the progress numbers are still sent.
// Sending never blocks: an update the session cannot take now is dropped.
func SessionSink(ctx context.Context, clientToken mcp.ProgressToken, clean func(string) string) Sink {
	if clientToken == nil {
		return nil
	}
	session := mcpserver.ClientSessionFromContext(ctx)
	if session == nil || !session.Initialized() {
		return nil
	}
	return func(u Update) {
		params := map[string]any{
			"progressToken": clientToken,
			"progress":      u.Progress,
		}
		if u.Total != nil {
			params["total"] = *u.Total
		}
		msg := u.Message
		if msg != "" && clean != nil {
			msg = clean(msg)
		}
		if msg != "" {
			params["message"] = msg
		}
		n := mcp.JSONRPCNotification{
			JSONRPC: mcp.JSONRPC_VERSION,
			Notification: mcp.Notification{
				Method: string(mcp.MethodNotificationProgress),
				Params: mcp.NotificationParams{AdditionalFields: params},
			},
		}
		if up, ok := session.(mcpserver.SessionWithStreamableHTTPConfig); ok {
			up.UpgradeToSSEWhenReceiveNotification()
		}
		select {
		case session.NotificationChannel() <- n:
		default:
		}
	}
}

// Registry maps the tokens one upstream client minted to the calls they
// belong to. The zero value is ready to use.
type Registry struct {
	seq atomic.Uint64

	mu    sync.Mutex
	calls map[string]*call

	now func() time.Time // tests
}

type call struct {
	mu       sync.Mutex
	sink     Sink
	closed   bool
	sent     bool
	doneSent bool // the completion bypass was used
	last     float64
	lastSent time.Time
}

func (r *Registry) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// Begin registers the call whose context is ctx. With no sink in ctx it
// returns a nil token and registers nothing. Otherwise the token goes into
// the upstream request's _meta.progressToken, and end must be called when
// the call returns; after end returns the sink is never called again.
func (r *Registry) Begin(ctx context.Context) (token mcp.ProgressToken, end func()) {
	sink := SinkFrom(ctx)
	if sink == nil {
		return nil, func() {}
	}
	tok := newToken(r.seq.Add(1))
	c := &call{sink: sink}
	r.mu.Lock()
	if r.calls == nil {
		r.calls = make(map[string]*call)
	}
	r.calls[tok] = c
	r.mu.Unlock()
	return tok, func() {
		// Closed first: a Dispatch that already found c sees it closed, and
		// a Clear that no longer finds c cannot return while c may still fire.
		c.close()
		r.mu.Lock()
		if r.calls[tok] == c {
			delete(r.calls, tok)
		}
		r.mu.Unlock()
	}
}

func (c *call) close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}

// newToken mints an unguessable token: an upstream that saw one token cannot
// name a call it has not been sent yet.
func newToken(seq uint64) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("mcpproxy-%d-%s", seq, hex.EncodeToString(b[:]))
}

// Clear forgets every call; their sinks are never called again. It runs when
// the upstream connection goes away.
func (r *Registry) Clear() {
	// Held while closing, so an overlapping Clear cannot return before every
	// call is closed. Lock order is r.mu then c.mu; nothing takes them the
	// other way round.
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		c.close()
	}
	r.calls = nil
}

// Len reports how many calls are registered.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// Dispatch forwards one notifications/progress to the call its token names.
// It reports whether the update was forwarded. An unknown token, progress
// that does not increase, a malformed notification, or one that arrives
// sooner than MinInterval after the last forwarded one is dropped; the first
// update that reports completion (progress >= total) skips the interval.
func (r *Registry) Dispatch(n mcp.JSONRPCNotification) bool {
	f := n.Params.AdditionalFields
	tok, ok := f["progressToken"].(string)
	if !ok {
		return false
	}
	r.mu.Lock()
	c := r.calls[tok]
	r.mu.Unlock()
	if c == nil {
		return false
	}
	progress, ok := number(f["progress"])
	if !ok {
		return false
	}
	u := Update{Progress: progress}
	if t, ok := number(f["total"]); ok {
		u.Total = &t
	} else if _, present := f["total"]; present {
		return false
	}
	if m, ok := f["message"].(string); ok {
		u.Message = capMessage(m)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	if c.sent && progress <= c.last {
		return false
	}
	now := r.clock()
	done := u.Total != nil && progress >= *u.Total && !c.doneSent
	if c.sent && !done && now.Sub(c.lastSent) < MinInterval {
		return false
	}
	c.sent, c.last, c.lastSent = true, progress, now
	c.doneSent = c.doneSent || done
	c.sink(u)
	return true
}

// number reads a finite JSON number.
func number(v any) (float64, bool) {
	var x float64
	switch n := v.(type) {
	case float64:
		x = n
	case float32:
		x = float64(n)
	case int:
		x = float64(n)
	case int64:
		x = float64(n)
	default:
		return 0, false
	}
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0, false
	}
	return x, true
}

// capMessage cuts m to at most MaxMessageBytes on a rune boundary.
func capMessage(m string) string {
	if len(m) <= MaxMessageBytes {
		return m
	}
	m = m[:MaxMessageBytes]
	for len(m) > 0 && !utf8.ValidString(m) {
		m = m[:len(m)-1]
	}
	return m
}
