package transport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync"
)

// RequestGate lets the owner of an upstream HTTP/SSE client bar every request
// that has not reached the wire once the client is retired (removed, replaced
// or quarantined by a newer configuration commit).
//
// Admit is consulted once per request and its release is called as soon as the
// request has been written (or the round trip ended). While a request is
// between admission and the first byte on the wire the owner's Retire waits for
// it, exactly as it waits for a process spawn, after cancelling RetireCtx so
// that wait is short: a pending dial or TLS handshake is aborted rather than
// completed. After Retire returns no request starts and none is half-issued
// (UX-01 r9).
type RequestGate struct {
	// Admit refuses (non-nil error) once the client is retired. When it admits,
	// release MUST be called once the request has been issued.
	Admit func() (release func(), err error)
	// RetireCtx is cancelled when the client is retired; it aborts requests that
	// are admitted but not yet written. May be nil.
	RetireCtx context.Context
}

// BeforeUpstreamRequestHook is a test seam fired after a request was admitted
// and before it is handed to the network, with the admission still held.
var BeforeUpstreamRequestHook func(url string)

type gateRoundTripper struct {
	base http.RoundTripper
	gate *RequestGate
}

// newGateRoundTripper wraps base with the gate; a nil gate returns base.
func newGateRoundTripper(base http.RoundTripper, gate *RequestGate) http.RoundTripper {
	if gate == nil || gate.Admit == nil {
		return base
	}
	return &gateRoundTripper{base: base, gate: gate}
}

func (g *gateRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	release, err := g.gate.Admit()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(req.Context())
	stop := func() bool { return true }
	if g.gate.RetireCtx != nil {
		stop = context.AfterFunc(g.gate.RetireCtx, cancel)
	}
	var issued sync.Once
	markIssued := func() {
		issued.Do(func() {
			// From here the request is on the wire: retirement no longer
			// cancels it (the owner's Disconnect tears the connection down).
			stop()
			if release != nil {
				release()
			}
		})
	}
	trace := &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) { markIssued() },
	}
	req = req.WithContext(httptrace.WithClientTrace(ctx, trace))
	if BeforeUpstreamRequestHook != nil {
		BeforeUpstreamRequestHook(req.URL.String())
	}
	resp, err := g.base.RoundTrip(req)
	markIssued()
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}
