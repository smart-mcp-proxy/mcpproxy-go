package oauth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Cloudflare's workers-oauth-provider (graphql.mcp.cloudflare.com and the other
// Cloudflare MCP servers) answers an authorization request for a client it has
// deleted with exactly this body, and never redirects back.
const cloudflareInvalidClientBody = `{"error":"invalid_request","error_description":"Invalid client_id"}`

func TestClassifyAuthorizeClientRejection(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"cloudflare 400 invalid client_id", 400, cloudflareInvalidClientBody, true},
		{"cloudflare 400 different case", 400, `{"error":"INVALID_REQUEST","error_description":"invalid CLIENT_ID"}`, true},
		{"token-endpoint style 401 invalid_client", 401, `{"error":"invalid_client","error_description":"Client not found"}`, true},
		{"400 invalid_client", 400, `{"error":"invalid_client"}`, true},
		{"400 unauthorized_client is a policy error, not a dead registration", 400, `{"error":"unauthorized_client","error_description":"client is not allowed"}`, false},
		{"invalid_request unknown client", 400, `{"error":"invalid_request","error_description":"Unknown client"}`, true},
		{"invalid_request client not found", 400, `{"error":"invalid_request","error_description":"Client not found"}`, true},
		{"invalid_request client_id is invalid", 400, `{"error":"invalid_request","error_description":"The client_id is invalid"}`, true},
		{"invalid_request client does not exist", 400, `{"error":"invalid_request","error_description":"client does not exist"}`, true},

		{"bad redirect_uri 400 must not trigger", 400, `{"error":"invalid_request","error_description":"Invalid redirect_uri"}`, false},
		{"redirect_uri not registered for client", 400, `{"error":"invalid_request","error_description":"redirect_uri is not registered for this client"}`, false},
		{"invalid client redirect_uri", 400, `{"error":"invalid_request","error_description":"Invalid redirect URI for client"}`, false},
		{"bad scope must not trigger", 400, `{"error":"invalid_scope","error_description":"Invalid scope"}`, false},
		{"invalid scope for client", 400, `{"error":"invalid_request","error_description":"invalid scope for client"}`, false},
		{"missing client_id is not a stale client", 400, `{"error":"invalid_request","error_description":"Missing client_id"}`, false},
		{"client sent invalid code_challenge", 400, `{"error":"invalid_request","error_description":"client sent an invalid code_challenge"}`, false},
		{"client requested unknown response_type", 400, `{"error":"invalid_request","error_description":"Client requested an unknown response_type"}`, false},
		{"client provided invalid code_challenge_method", 400, `{"error":"invalid_request","error_description":"The client provided an invalid code_challenge_method"}`, false},
		{"invalid request for client", 400, `{"error":"invalid_request","error_description":"invalid state parameter from client"}`, false},
		{"client id was not found", 400, `{"error":"invalid_request","error_description":"Client ID was not found"}`, true},
		{"no such client", 400, `{"error":"invalid_request","error_description":"No such client"}`, true},
		{"missing code_challenge", 400, `{"error":"invalid_request","error_description":"code_challenge required"}`, false},
		{"302", 302, cloudflareInvalidClientBody, false},
		{"200 consent page", 200, `<html>consent</html>`, false},
		{"500", 500, cloudflareInvalidClientBody, false},
		{"403", 403, cloudflareInvalidClientBody, false},
		{"400 html", 400, `<html>Invalid client_id</html>`, false},
		{"400 empty", 400, ``, false},
		{"400 text", 400, `invalid_client: Unknown client_id`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, _ := ClassifyAuthorizeClientRejection(tc.status, []byte(tc.body))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestProbeAuthorizationClient_CloudflareRejection(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "dead-client", r.URL.Query().Get("client_id"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(cloudflareInvalidClientBody))
	}))
	defer srv.Close()

	res := ProbeAuthorizationClient(context.Background(), nil, srv.URL+"/oauth/authorize?client_id=dead-client&response_type=code")
	assert.True(t, res.ClientRejected)
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	assert.Equal(t, "invalid_request", res.OAuthError)
	assert.Equal(t, "Invalid client_id", res.ErrorDescription)
	assert.NoError(t, res.Err)
	assert.EqualValues(t, 1, hits.Load())
}

func TestProbeAuthorizationClient_DoesNotFollowRedirects(t *testing.T) {
	var followed atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			followed.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(cloudflareInvalidClientBody))
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()

	res := ProbeAuthorizationClient(context.Background(), nil, srv.URL+"/authorize?client_id=x")
	assert.False(t, res.ClientRejected)
	assert.Equal(t, http.StatusFound, res.StatusCode)
	assert.EqualValues(t, 0, followed.Load(), "the probe must not follow redirects")
}

func TestProbeAuthorizationClient_FailOpen(t *testing.T) {
	t.Run("200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>login</html>"))
		}))
		defer srv.Close()
		res := ProbeAuthorizationClient(context.Background(), nil, srv.URL+"/authorize")
		assert.False(t, res.ClientRejected)
		assert.Equal(t, http.StatusOK, res.StatusCode)
	})
	t.Run("500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(cloudflareInvalidClientBody))
		}))
		defer srv.Close()
		res := ProbeAuthorizationClient(context.Background(), nil, srv.URL+"/authorize")
		assert.False(t, res.ClientRejected)
	})
	t.Run("connection refused", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := ln.Addr().String()
		require.NoError(t, ln.Close())
		res := ProbeAuthorizationClient(context.Background(), nil, "http://"+addr+"/authorize")
		assert.False(t, res.ClientRejected)
		assert.Error(t, res.Err)
	})
	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			<-release
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(cloudflareInvalidClientBody))
		}))
		defer srv.Close()
		defer close(release)
		hc := &http.Client{Timeout: 100 * time.Millisecond}
		start := time.Now()
		res := ProbeAuthorizationClient(context.Background(), hc, srv.URL+"/authorize")
		assert.False(t, res.ClientRejected)
		assert.Error(t, res.Err)
		assert.Less(t, time.Since(start), 3*time.Second)
	})
	t.Run("non-http scheme is not probed", func(t *testing.T) {
		res := ProbeAuthorizationClient(context.Background(), nil, "file:///etc/passwd")
		assert.False(t, res.ClientRejected)
		assert.Error(t, res.Err)
	})
	t.Run("transport error", func(t *testing.T) {
		hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("boom")
		})}
		res := ProbeAuthorizationClient(context.Background(), hc, "https://auth.example.com/authorize?client_id=x")
		assert.False(t, res.ClientRejected)
		assert.Error(t, res.Err)
	})
}

func TestProbeAuthorizationClient_CapsBodyRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		// A huge body: valid JSON prefix padded far past the cap, so the
		// capped read is unparsable and the probe fails open.
		_, _ = w.Write([]byte(`{"error":"invalid_client","pad":"` + strings.Repeat("a", 1<<20) + `"}`))
	}))
	defer srv.Close()
	res := ProbeAuthorizationClient(context.Background(), nil, srv.URL+"/authorize")
	assert.False(t, res.ClientRejected, "an over-long body is truncated and fails open")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
