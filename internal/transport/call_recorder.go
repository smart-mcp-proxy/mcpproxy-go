package transport

// call_recorder.go: a per-call, context-scoped record of what the upstream
// HTTP exchange looked like (Spec 113-c, FR-042).
//
// mcp-go flattens a non-2xx, non-JSON response into an untyped
// fmt.Errorf("request failed with status %d: ...") and an unmapped JSON-RPC
// code into errors.New(message), so by the time a tool-call error reaches the
// server the status is only recoverable from a string. The one layer that
// still holds the *http.Response is the RoundTripper beneath the MCP client.
// A dispatch site puts a recorder on the ctx it hands to the upstream call;
// the round tripper below fills it for requests carrying that ctx only, so
// concurrent calls never see each other's responses.

import (
	"context"
	"net/http"
	"sync"
)

type callRecorderKeyType struct{}

var callRecorderKey callRecorderKeyType

// CallRecorder accumulates the HTTP facts of ONE tool call.
type CallRecorder struct {
	mu         sync.Mutex
	dispatched bool
	requests   int
	responses  int
	lastStatus int
}

// CallRecorderSnapshot is an immutable copy of a recorder's state.
type CallRecorderSnapshot struct {
	// Dispatched is true once the call was handed to the mcp-go client
	// (MarkDispatched), for every transport, including stdio.
	Dispatched bool
	// Requests is the number of HTTP requests that carried the recorder.
	Requests int
	// Responses is how many of them produced an HTTP response.
	Responses int
	// LastStatus is the status of the most recent response; 0 if none.
	LastStatus int
}

// WithCallRecorder returns a ctx carrying a fresh recorder.
func WithCallRecorder(ctx context.Context) (context.Context, *CallRecorder) {
	rec := &CallRecorder{}
	return context.WithValue(ctx, callRecorderKey, rec), rec
}

// CallRecorderFrom returns the recorder on ctx, or nil.
func CallRecorderFrom(ctx context.Context) *CallRecorder {
	if ctx == nil {
		return nil
	}
	rec, _ := ctx.Value(callRecorderKey).(*CallRecorder)
	return rec
}

// MarkDispatched records that the call was handed to the transport layer. It
// is a no-op when ctx carries no recorder.
func MarkDispatched(ctx context.Context) {
	if rec := CallRecorderFrom(ctx); rec != nil {
		rec.mu.Lock()
		rec.dispatched = true
		rec.mu.Unlock()
	}
}

// Snapshot returns a copy of the recorder's state. A nil recorder yields the
// zero snapshot.
func (r *CallRecorder) Snapshot() CallRecorderSnapshot {
	if r == nil {
		return CallRecorderSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return CallRecorderSnapshot{
		Dispatched: r.dispatched,
		Requests:   r.requests,
		Responses:  r.responses,
		LastStatus: r.lastStatus,
	}
}

func (r *CallRecorder) noteRequest() {
	r.mu.Lock()
	r.requests++
	r.mu.Unlock()
}

func (r *CallRecorder) noteResponse(status int) {
	r.mu.Lock()
	r.responses++
	r.lastStatus = status
	r.mu.Unlock()
}

// callRecorderTransport fills the recorder carried by each request's ctx.
type callRecorderTransport struct {
	next http.RoundTripper
}

// NewCallRecorderTransport wraps next so requests whose ctx carries a
// CallRecorder fill it.
func NewCallRecorderTransport(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return &callRecorderTransport{next: next}
}

func (t *callRecorderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := CallRecorderFrom(req.Context())
	if rec == nil {
		return t.next.RoundTrip(req)
	}
	// mcp-go reuses the tools/call context for the reply POSTs to
	// server-initiated requests received inside the call's SSE stream. Those
	// are not the call's own request, so their status must not overwrite it.
	// An unreadable or oversize body is counted: it cannot be told apart.
	clone := req.Clone(req.Context())
	if body, ok := readGateBody(clone); ok {
		if !isToolsCallBody(body) {
			return t.next.RoundTrip(clone)
		}
	}
	req = clone
	rec.noteRequest()
	resp, err := t.next.RoundTrip(req)
	if err == nil && resp != nil {
		rec.noteResponse(resp.StatusCode)
	}
	return resp, err
}
