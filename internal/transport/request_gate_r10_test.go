package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// retireGate is a minimal owner: Admit refuses once retired and counts live
// admissions; retire cancels the context and waits for them to drain.
type retireGate struct {
	mu      sync.RWMutex
	retired bool
	ctx     context.Context
	cancel  context.CancelFunc
}

func newRetireGate() *retireGate {
	g := &retireGate{}
	g.ctx, g.cancel = context.WithCancel(context.Background())
	return g
}

func (g *retireGate) gate() *RequestGate {
	return &RequestGate{
		RetireCtx: g.ctx,
		Admit: func() (func(), error) {
			g.mu.RLock()
			if g.retired {
				g.mu.RUnlock()
				return nil, errors.New("retired")
			}
			return g.mu.RUnlock, nil
		},
	}
}

// Retire in the owner's order: flag, cancel, then wait for admissions.
func (g *retireGate) retireOwner() {
	g.cancel()
	g.mu.Lock()
	g.retired = true
	g.mu.Unlock()
}

// retryingBase simulates net/http's internal retry: the first attempt's write
// reports (a failure or a success), the transport then pauses, and the retry
// reaches the endpoint only if the request context is still alive.
type retryingBase struct {
	writeErr  error
	paused    chan struct{}
	resume    chan struct{}
	endpoints atomic.Int32
}

func (b *retryingBase) RoundTrip(req *http.Request) (*http.Response, error) {
	if tr := httptrace.ContextClientTrace(req.Context()); tr != nil && tr.WroteRequest != nil {
		tr.WroteRequest(httptrace.WroteRequestInfo{Err: b.writeErr})
	}
	close(b.paused)
	<-b.resume
	// The retry: net/http checks the request context before every attempt.
	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	default:
	}
	b.endpoints.Add(1)
	return nil, errors.New("endpoint reached")
}

// UX-01 r10: a transport-level retry must not escape retirement, whether the
// first attempt's write failed or succeeded, and retirement must not hang.
func TestGate_RetryAfterWriteDoesNotEscapeRetirement(t *testing.T) {
	for name, writeErr := range map[string]error{"write_failed": errors.New("broken pipe"), "write_ok": nil} {
		t.Run(name, func(t *testing.T) {
			g := newRetireGate()
			base := &retryingBase{writeErr: writeErr, paused: make(chan struct{}), resume: make(chan struct{})}
			rt := newGateRoundTripper(base, g.gate())

			req, err := http.NewRequest(http.MethodGet, "http://old.example/sse", nil)
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() { _, err := rt.RoundTrip(req); done <- err }()
			<-base.paused

			retired := make(chan struct{})
			go func() { g.retireOwner(); close(retired) }()
			// Retirement is observable (context cancelled) and still waits on the
			// held admission before the parked request resumes.
			<-g.ctx.Done()
			select {
			case <-retired:
				t.Fatal("retirement completed while an admission was held")
			case <-time.After(100 * time.Millisecond):
			}
			close(base.resume)
			select {
			case <-retired:
			case <-time.After(5 * time.Second):
				t.Fatal("retirement hung on an admitted request")
			}
			require.Error(t, <-done)
			require.Zero(t, base.endpoints.Load(), "a retry reached the old endpoint after retirement")
		})
	}
}

// UX-01 r10: OAuth traffic (metadata, registration, token) leaves through the
// OAuth config's HTTP client; once gated it sends nothing after retirement.
func TestGateHTTPClient_RefusesAfterRetirement(t *testing.T) {
	var hits atomic.Int32
	srv := newCountingServer(&hits)
	t.Cleanup(srv.Close)

	g := newRetireGate()
	hc := GateHTTPClient(&http.Client{}, g.gate())
	require.Same(t, hc, GateHTTPClient(hc, g.gate()), "gating twice must not stack wrappers")

	resp, err := hc.Post(srv.URL+"/token", "application/x-www-form-urlencoded", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.EqualValues(t, 1, hits.Load())

	g.retireOwner()
	_, err = hc.Post(srv.URL+"/token", "application/x-www-form-urlencoded", nil)
	require.Error(t, err)
	require.EqualValues(t, 1, hits.Load(), "a retired client's OAuth request reached the old token endpoint")
}

func newCountingServer(hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
}
