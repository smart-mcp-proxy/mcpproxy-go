package transport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type limitTestRT func(*http.Request) (*http.Response, error)

func (f limitTestRT) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func limitedGet(t *testing.T, rt http.RoundTripper, url string, ctx context.Context) ([]byte, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return nil, err // declared length already over the limit
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// A chunked body has no declared length, so the reader must catch it.
func TestResponseLimit_ChunkedBodyCaughtWhileReading(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f := w.(http.Flusher)
		for i := 0; i < 10; i++ {
			_, _ = w.Write([]byte(strings.Repeat("a", 100)))
			f.Flush()
		}
	}))
	defer srv.Close()
	rt := newResponseLimitTransport(http.DefaultTransport)
	_, err := limitedGet(t, rt, srv.URL, WithResponseLimit(context.Background(), 500))
	assert.ErrorIs(t, err, ErrResponseTooLarge)
}

func serveBytes(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestResponseLimit_FailsAfterLimit(t *testing.T) {
	srv := serveBytes(t, strings.Repeat("a", 1000))
	rt := newResponseLimitTransport(http.DefaultTransport)

	_, err := limitedGet(t, rt, srv.URL, WithResponseLimit(context.Background(), 999))
	assert.ErrorIs(t, err, ErrResponseTooLarge)

	body, err := limitedGet(t, rt, srv.URL, WithResponseLimit(context.Background(), 1000))
	require.NoError(t, err, "a body of exactly n bytes is accepted")
	assert.Len(t, body, 1000)
}

func TestResponseLimit_NoLimitIsUntouched(t *testing.T) {
	srv := serveBytes(t, strings.Repeat("a", 5000))
	var seen *http.Response
	rt := newResponseLimitTransport(limitTestRT(func(r *http.Request) (*http.Response, error) {
		resp, err := http.DefaultTransport.RoundTrip(r)
		seen = resp
		return resp, err
	}))
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Same(t, seen, resp, "response must not be replaced without a limit")
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Len(t, b, 5000)
}

func TestResponseLimit_AcceptsFullyEscapedBody(t *testing.T) {
	size := 1000
	srv := serveBytes(t, strings.Repeat(`A`, size)) // six bytes per source byte
	rt := newResponseLimitTransport(http.DefaultTransport)
	_, err := limitedGet(t, rt, srv.URL, WithResponseLimit(context.Background(), int64(6*size+64<<10)))
	require.NoError(t, err)
}
