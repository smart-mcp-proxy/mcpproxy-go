package oauth

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
)

// Spec 116 PR4, T4.9 (review R1.4, FR-024a): the OAuth preflight, resource
// auto-detection, metadata discovery and cache fetch run on the instance's
// transport. Parked before they send and released after the instance is
// retired, they reach nothing.

// oauthCounter serves a 401 with resource_metadata, PRM and AS metadata, and
// counts every request (and token-endpoint request) it receives.
type oauthCounter struct {
	srv    *httptest.Server
	hits   atomic.Int64
	tokens atomic.Int64
	bodies atomic.Pointer[string]
}

func newOAuthCounter(t *testing.T) *oauthCounter {
	t.Helper()
	c := &oauthCounter{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.hits.Add(1)
		base := "http://" + r.Host
		switch {
		case r.URL.Path == "/token":
			c.tokens.Add(1)
			_ = r.ParseForm()
			body := r.PostForm.Encode()
			c.bodies.Store(&body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"access_token":"new","token_type":"Bearer","expires_in":3600,"refresh_token":"rt2"}`)
		case strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"resource":%q,"authorization_servers":[%q],"scopes_supported":["read"]}`, base+"/mcp", base)
		case strings.HasPrefix(r.URL.Path, "/.well-known/oauth-authorization-server"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"registration_endpoint":%q,"scopes_supported":["read"]}`,
				base, base+"/authorize", base+"/token", base+"/register")
		default:
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q`, base+"/.well-known/oauth-protected-resource"))
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	t.Cleanup(c.srv.Close)
	return c
}

// parkedEnv returns an Env on a fresh gate whose first admitted dial parks.
func parkedEnv() (env Env, gate *dialgate.Gate, parked <-chan struct{}, release func()) {
	gate = dialgate.New()
	p := make(chan struct{})
	r := make(chan struct{})
	var once atomic.Bool
	gate.SetHooks(&dialgate.Hooks{BeforeAdmit: func() {
		if once.CompareAndSwap(false, true) {
			close(p)
			<-r
		}
	}})
	return Env{Transport: gate.HTTPTransport()}, gate, p, func() { close(r) }
}

func TestRetirePreflight_ParkedHelpersReachNothing(t *testing.T) {
	cases := map[string]func(env Env, sc *config.ServerConfig) error{
		"preflightPRMScopes": func(env Env, sc *config.ServerConfig) error {
			_, err := preflightPRMScopes(env, sc, overridesFromConfig(sc), zap.NewNop())
			return err
		},
		"autoDetectResource": func(env Env, sc *config.ServerConfig) error {
			_ = autoDetectResource(env, sc, zap.NewNop())
			return nil
		},
		"httpFetchRaw": func(env Env, sc *config.ServerConfig) error {
			_, err := httpFetchRaw(env, strings.TrimSuffix(sc.URL, "/mcp")+"/.well-known/oauth-authorization-server", 2*time.Second)
			return err
		},
		"ValidateOAuthMetadataWithEnv": func(env Env, sc *config.ServerConfig) error {
			_, err := ValidateOAuthMetadataWithEnv(env, sc.URL, sc.Name, 2*time.Second)
			return err
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			up := newOAuthCounter(t)
			sc := &config.ServerConfig{Name: "retire-" + name, URL: up.srv.URL + "/mcp"}
			InvalidateDiscovery(sc.URL)
			env, gate, parked, release := parkedEnv()
			done := make(chan error, 1)
			go func() { done <- run(env, sc) }()
			<-parked
			gate.Retire()
			release()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("helper did not return")
			}
			assert.Zero(t, up.hits.Load(), "a retired instance's OAuth helper reaches nothing")
		})
	}
}

// Control: the same helper on an open gate reaches the fixture.
func TestRetirePreflight_Control(t *testing.T) {
	up := newOAuthCounter(t)
	sc := &config.ServerConfig{Name: "retire-control", URL: up.srv.URL + "/mcp"}
	InvalidateDiscovery(sc.URL)
	res, err := preflightPRMScopes(Env{Transport: dialgate.New().HTTPTransport()}, sc, overridesFromConfig(sc), zap.NewNop())
	require.NoError(t, err)
	assert.Equal(t, []string{"read"}, res.scopes)
	assert.Positive(t, up.hits.Load())
}

// T4.4 (OAuth part of FR-024): the OAuth config built in an instance's
// environment carries an HTTP client on the instance transport. mcp-go's
// registration, code exchange and refresh go through it, so after
// retirement the token endpoint receives no request, no client secret and
// no refresh token.
func TestRetireOAuthConfig_TokenRequestsRefused(t *testing.T) {
	up := newOAuthCounter(t)
	gate := dialgate.New()
	sc := &config.ServerConfig{
		Name: "retire-oauth",
		URL:  up.srv.URL + "/mcp",
		OAuth: &config.OAuthConfig{
			ClientID:     "static-client",
			ClientSecret: "static-secret",
		},
	}
	InvalidateDiscovery(sc.URL)
	oc, _, err := CreateOAuthConfigWithEnv(Env{Transport: gate.HTTPTransport()}, sc, setupTestStorage(t), zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, oc.HTTPClient)

	gate.Retire()
	hitsBefore := up.hits.Load()
	resp, err := oc.HTTPClient.Post(up.srv.URL+"/token", "application/x-www-form-urlencoded",
		strings.NewReader("grant_type=refresh_token&refresh_token=rt&client_secret=static-secret"))
	if resp != nil {
		resp.Body.Close()
	}
	require.Error(t, err)
	assert.True(t, errors.Is(err, dialgate.ErrRetired), "got %v", err)
	assert.Equal(t, hitsBefore, up.hits.Load())
	assert.Zero(t, up.tokens.Load(), "no refresh token or client secret reaches /token")
}

// A discovery flight that ran on a retired instance is not cached: the next
// instance's discovery runs its own fetch instead of inheriting the refusal.
func TestDiscoveryCache_RetiredFlightNotCached(t *testing.T) {
	up := newOAuthCounter(t)
	sc := &config.ServerConfig{Name: "retire-cache", URL: up.srv.URL + "/mcp"}
	InvalidateDiscovery(sc.URL)

	retired := dialgate.New()
	retired.Retire()
	_, err := preflightPRMScopes(Env{Transport: retired.HTTPTransport()}, sc, overridesFromConfig(sc), zap.NewNop())
	require.Error(t, err)
	key := makeDiscoveryKey("prm-scopes", sc.URL, overridesFromConfig(sc))
	_, err = cachedDiscover(globalDiscoveryCache, key, sc.URL, func() (prmScopeResult, error) {
		return preflightPRMScopes(Env{Transport: retired.HTTPTransport()}, sc, overridesFromConfig(sc), zap.NewNop())
	})
	require.Error(t, err)

	res, err := cachedDiscover(globalDiscoveryCache, key, sc.URL, func() (prmScopeResult, error) {
		return preflightPRMScopes(Env{Transport: dialgate.New().HTTPTransport()}, sc, overridesFromConfig(sc), zap.NewNop())
	})
	require.NoError(t, err, "the retired flight's refusal was not cached")
	assert.Equal(t, []string{"read"}, res.scopes)
}

// Review round 1: a discovery fetch whose response read is cut off by the
// instance's retirement (the gate closes the connection under the body read:
// a socket error, not a refused dial) is not cached as a failure either. A
// replacement instance with the same discovery key runs its own fetch.
func TestDiscoveryCache_RetiredMidReadNotCached(t *testing.T) {
	var hold atomic.Bool
	hold.Store(true)
	var full atomic.Int64
	started := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if hold.Load() {
			w.Header().Set("Content-Length", "512")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"resource":`)
			w.(http.Flusher).Flush()
			once.Do(func() { close(started) })
			<-r.Context().Done()
			return
		}
		full.Add(1)
		_, _ = io.WriteString(w, `{"resource":"`+"http://"+r.Host+`/mcp","scopes_supported":["read"]}`)
	}))
	defer srv.Close()
	serverURL := srv.URL + "/mcp"
	prmURL := srv.URL + "/.well-known/oauth-protected-resource"
	InvalidateDiscovery(serverURL)
	t.Cleanup(func() { InvalidateDiscovery(serverURL) })
	ov := discoveryOverrides{}

	gate := dialgate.New()
	errc := make(chan error, 1)
	go func() {
		_, err := cachedPRM(Env{Transport: gate.HTTPTransport()}, serverURL, ov, prmURL, 10*time.Second)
		errc <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the PRM fetch never reached the server")
	}
	gate.Retire()
	var err error
	select {
	case err = <-errc:
	case <-time.After(5 * time.Second):
		t.Fatal("retirement did not end the fetch")
	}
	require.Error(t, err)
	assert.True(t, dialgate.IsRetired(err), "a read cut off by retirement is a retirement, got %v", err)

	hold.Store(false)
	prm, err := cachedPRM(Env{Transport: dialgate.New().HTTPTransport()}, serverURL, ov, prmURL, 10*time.Second)
	require.NoError(t, err, "the cut-off fetch was not cached as a failure")
	assert.Equal(t, []string{"read"}, prm.ScopesSupported)
	assert.Equal(t, int64(1), full.Load())
}
