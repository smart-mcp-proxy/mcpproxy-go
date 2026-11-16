package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
)

// ErrResponseTooLarge is returned by a response body that carried more bytes
// than the limit the request's context asked for.
var ErrResponseTooLarge = errors.New("upstream response exceeds the per-request size limit")

type responseLimitKey struct{}

// WithResponseLimit returns a context that makes the upstream round tripper
// fail the response body once more than n bytes have been read. Requests
// without the value are untouched. Only a request of its own (streamable
// HTTP POST) is bounded: on a shared SSE stream the limit does not apply.
func WithResponseLimit(ctx context.Context, n int64) context.Context {
	return context.WithValue(ctx, responseLimitKey{}, n)
}

type responseLimitTransport struct {
	next http.RoundTripper
}

func newResponseLimitTransport(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return &responseLimitTransport{next: next}
}

func (t *responseLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	n, ok := req.Context().Value(responseLimitKey{}).(int64)
	if !ok {
		return resp, nil
	}
	if resp.ContentLength > n {
		// Declared too large: refuse without reading anything.
		_ = resp.Body.Close()
		return nil, ErrResponseTooLarge
	}
	resp.Body = &limitedBody{rc: resp.Body, left: n}
	return resp, nil
}

// limitedBody errors (never truncates silently) when the body outgrows its
// limit, so a caller cannot parse a cut-off document as a complete one.
type limitedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.left < 0 {
		return 0, ErrResponseTooLarge
	}
	// Read one byte past the limit so an oversize body is detected.
	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	if b.left < 0 {
		return n - int(-b.left), ErrResponseTooLarge
	}
	return n, err
}

func (b *limitedBody) Close() error { return b.rc.Close() }
