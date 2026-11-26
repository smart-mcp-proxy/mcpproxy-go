package oauth

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
)

// GH #1827: a refresh must leave a record in the server's own log that says
// which flight it was, what started it and how it ended, so a provider-side
// rejection can be matched to one client attempt.
func TestRefreshCoordinator_LogsNumberedFlightsToRequestLogger(t *testing.T) {
	f := newCoordFixture(t, "cid")
	c := newTestCoordinator()
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	var seen []RefreshFlightInfo
	lostResponse := &net.OpError{Op: "read", Net: "tcp", Err: io.ErrUnexpectedEOF}
	req := f.req(RefreshTriggerProactive, "rt-0")
	req.Logger = logger
	inner := req.Refresh
	req.Refresh = func(ctx context.Context, rec *storage.OAuthTokenRecord) (*client.Token, error) {
		info, ok := RefreshFlightInfoFrom(ctx)
		require.True(t, ok, "the token request context names its flight")
		seen = append(seen, info)
		return inner(ctx, rec)
	}

	f.failErr = lostResponse
	_, _, err := c.Do(context.Background(), req)
	require.Error(t, err)
	f.failErr = nil
	_, _, err = c.Do(context.Background(), req)
	require.NoError(t, err)

	require.Len(t, seen, 2)
	assert.Equal(t, uint64(1), seen[0].Flight)
	assert.Equal(t, uint64(2), seen[1].Flight)
	assert.Equal(t, RefreshTriggerProactive, seen[0].Trigger)

	started := logs.FilterMessage("OAuth refresh flight started").All()
	require.Len(t, started, 2, "each flight that may send a request is logged before it does")
	assert.Equal(t, uint64(1), started[0].ContextMap()["flight"])
	assert.Equal(t, "proactive", started[0].ContextMap()["trigger"])
	assert.Equal(t, int64(1), started[1].ContextMap()["prior_transient_failures"])

	finished := logs.FilterMessage("OAuth refresh flight finished").All()
	require.Len(t, finished, 2, "the outcome goes to the request's logger, not only the global one")
	assert.Equal(t, uint64(1), finished[0].ContextMap()["flight"])
	assert.Equal(t, string(RefreshClassNetwork), finished[0].ContextMap()["outcome"])
	assert.Equal(t, uint64(2), finished[1].ContextMap()["flight"])
	assert.Equal(t, string(RefreshClassSuccess), finished[1].ContextMap()["outcome"])
	for _, e := range logs.All() {
		for k, v := range e.ContextMap() {
			assert.NotContains(t, toString(v), "rt-", "field %s carries no refresh token", k)
		}
	}
}

// GH #1827: every token-endpoint request is logged after it returns, with its
// grant type, the flight it belongs to and its status, even when no
// extra_params are configured (the old marker only fired with extra_params,
// before the request was sent, and named nothing).
func TestOAuthTransportWrapper_LogsTokenRequestOutcome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "rt-secret-1", r.PostForm.Get("refresh_token"), "the body still reaches the server")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
	}))
	t.Cleanup(srv.Close)

	for _, extra := range []map[string]string{nil, {"resource": "https://r.example"}} {
		core, logs := observer.New(zapcore.DebugLevel)
		w := NewOAuthTransportWrapper(http.DefaultTransport, extra, zap.New(core))
		ctx := withRefreshFlightInfo(context.Background(), RefreshFlightInfo{Flight: 3, Trigger: RefreshTriggerReactive})
		body := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"rt-secret-1"}}.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/token", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := w.RoundTrip(req)
		require.NoError(t, err)
		_ = resp.Body.Close()

		entries := logs.FilterMessage("OAuth token request finished").All()
		require.Len(t, entries, 1, "extra params %v", extra)
		m := entries[0].ContextMap()
		assert.Equal(t, "refresh_token", m["grant_type"])
		assert.Equal(t, uint64(3), m["flight"])
		assert.Equal(t, "reactive", m["trigger"])
		assert.Equal(t, int64(http.StatusBadRequest), m["http_status"])
		assert.Equal(t, "http_response", m["outcome"])
		for k, v := range m {
			assert.NotContains(t, toString(v), "rt-secret", "field %s carries no refresh token", k)
		}
	}
}

func TestOAuthTransportWrapper_LogsTokenRequestTransportFailure(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	failing := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "read", Net: "tcp", Err: io.ErrUnexpectedEOF}
	})
	w := NewOAuthTransportWrapper(failing, nil, zap.New(core))
	req, err := http.NewRequest(http.MethodPost, "https://as.example/oauth/token", strings.NewReader("grant_type=authorization_code&code=c-secret"))
	require.NoError(t, err)
	_, err = w.RoundTrip(req)
	require.Error(t, err)
	entries := logs.FilterMessage("OAuth token request finished").All()
	require.Len(t, entries, 1)
	m := entries[0].ContextMap()
	assert.Equal(t, "authorization_code", m["grant_type"])
	assert.Equal(t, string(RefreshClassNetwork), m["outcome"])
	assert.Equal(t, uint64(0), m["flight"], "a request outside a refresh flight has flight 0")
	for k, v := range m {
		assert.NotContains(t, toString(v), "c-secret", "field %s carries no code", k)
	}
}

func TestTokenRequestGrantTypeIsClosedSet(t *testing.T) {
	assert.Equal(t, "refresh_token", tokenRequestGrantType("refresh_token"))
	assert.Equal(t, "other", tokenRequestGrantType("urn:attacker:rt-secret"))
	assert.Equal(t, "unknown", tokenRequestGrantType(""))
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// Review round 1: the logged status is the provider's (before 201→200), the
// logged grant is the one sent (an extra_params grant_type wins), and a
// request the retired gate refused is "not_sent".
func TestOAuthTransportWrapper_TokenRequestLogMatchesWhatWasSent(t *testing.T) {
	created := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusCreated, Status: "201 Created", Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})
	core, logs := observer.New(zapcore.DebugLevel)
	w := NewOAuthTransportWrapper(created, map[string]string{"grant_type": "client_credentials"}, zap.New(core))
	req, err := http.NewRequest(http.MethodPost, "https://as.example/token", strings.NewReader("grant_type=refresh_token&refresh_token=x"))
	require.NoError(t, err)
	resp, err := w.RoundTrip(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the response is still normalized for mcp-go")
	m := logs.FilterMessage("OAuth token request finished").All()[0].ContextMap()
	assert.Equal(t, int64(http.StatusCreated), m["http_status"])
	assert.Equal(t, "client_credentials", m["grant_type"])

	core, logs = observer.New(zapcore.DebugLevel)
	retired := roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, dialgate.ErrRetired })
	w = NewOAuthTransportWrapper(retired, nil, zap.New(core))
	req, err = http.NewRequest(http.MethodPost, "https://as.example/token", strings.NewReader("grant_type=refresh_token"))
	require.NoError(t, err)
	_, err = w.RoundTrip(req)
	require.Error(t, err)
	assert.Equal(t, "not_sent", logs.FilterMessage("OAuth token request finished").All()[0].ContextMap()["outcome"])
}

type failingBody struct {
	closed bool
	n      int
}

func (b *failingBody) Read(p []byte) (int, error) {
	if b.n == 0 {
		b.n++
		return copy(p, "grant_type=refresh"), nil
	}
	return 0, errors.New("upload broke")
}

func (b *failingBody) Close() error { b.closed = true; return nil }

// Review round 1: a body that fails while being peeked still fails the
// upload (its prefix is not sent as a complete form), and closing the
// outgoing body closes the caller's.
func TestPeekTokenGrantType_ReadErrorStillFailsUpload(t *testing.T) {
	body := &failingBody{}
	req, err := http.NewRequest(http.MethodPost, "https://as.example/token", nil)
	require.NoError(t, err)
	req.Body = body
	out, grant := peekTokenGrantType(req)
	assert.Equal(t, "unknown", grant)
	assert.Same(t, body, req.Body, "the caller's request is not modified")
	got, rerr := io.ReadAll(out.Body)
	assert.Equal(t, "grant_type=refresh", string(got))
	assert.EqualError(t, rerr, "upload broke")
	require.NoError(t, out.Body.Close())
	assert.True(t, body.closed)
}
