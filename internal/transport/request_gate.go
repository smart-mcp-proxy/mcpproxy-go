package transport

import (
	"context"
	"io"
	"net/http"
)

// RequestGate lets the owner of an upstream HTTP/SSE client bar every request
// that has not reached the wire once the client is retired (removed, replaced
// or quarantined by a newer configuration commit).
//
// Admit is consulted once per request and its release is called when the round
// trip returns (response headers or an error). While a request is in that
// window the owner's Retire waits for it, exactly as it waits for a process
// spawn, after cancelling RetireCtx so that wait is short: the request, and any
// retry the HTTP transport would make, is aborted rather than completed. After
// Retire returns no request starts or retries (UX-01 r9, r10).
type RequestGate struct {
	// Admit refuses (non-nil error) once the client is retired. When it admits,
	// release MUST be called once the round trip has returned.
	Admit func() (release func(), err error)
	// RetireCtx is cancelled when the client is retired; it aborts requests that
	// are admitted and still awaiting response headers. May be nil.
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
	// The admission is held, and retirement keeps cancelling the request, until
	// the underlying round trip RETURNS. net/http retries a request internally
	// (a reused connection that failed its write, an idempotent request whose
	// connection closed) and httptrace.WroteRequest fires for every attempt, so
	// "the first write completed" is not a point after which nothing more can
	// reach the endpoint. A retry starts only if its context is alive, which a
	// retired client's never is (UX-01 r10).
	if BeforeUpstreamRequestHook != nil {
		BeforeUpstreamRequestHook(req.URL.String())
	}
	resp, err := g.base.RoundTrip(req.WithContext(ctx))
	// Response headers (or a failure) are in: nothing further is sent for this
	// request. A streaming body is not cancelled by retirement; the owner's
	// Disconnect tears the connection down.
	stop()
	if release != nil {
		release()
	}
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

// GateHTTPClient routes every request of hc through the gate (once), so OAuth
// discovery, registration and token traffic is barred after retirement like the
// MCP traffic. A nil client or gate is returned unchanged.
func GateHTTPClient(hc *http.Client, gate *RequestGate) *http.Client {
	if hc == nil || gate == nil || gate.Admit == nil {
		return hc
	}
	if _, already := hc.Transport.(*gateRoundTripper); already {
		return hc
	}
	base := hc.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	gated := *hc
	gated.Transport = newGateRoundTripper(base, gate)
	return &gated
}
