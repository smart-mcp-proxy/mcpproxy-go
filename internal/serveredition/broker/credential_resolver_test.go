//go:build server

package broker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// --- test doubles -----------------------------------------------------------

// fakeStore is an in-memory CredentialStore keyed by (userID, serverKey),
// matching the real backend's keying so resolver key derivation is exercised.
type fakeStore struct {
	mu      sync.Mutex
	enabled bool
	data    map[string]*UpstreamCredential
	getErr  error
}

func newFakeStore() *fakeStore {
	return &fakeStore{enabled: true, data: map[string]*UpstreamCredential{}}
}

func storeKey(userID, serverKey string) string { return userID + "\x00" + serverKey }

func (s *fakeStore) Enabled() bool { return s.enabled }

func (s *fakeStore) Get(userID, serverKey string) (*UpstreamCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return nil, s.getErr
	}
	c, ok := s.data[storeKey(userID, serverKey)]
	if !ok {
		return nil, ErrNotFound
	}
	return c, nil
}

func (s *fakeStore) Put(userID, serverKey string, cred *UpstreamCredential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[storeKey(userID, serverKey)] = cred
	return nil
}

func (s *fakeStore) Delete(userID, serverKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, storeKey(userID, serverKey))
	return nil
}

func (s *fakeStore) List(userID string) ([]CredentialEntry, error) { return nil, nil }

func (s *fakeStore) seed(userID, serverKey string, cred *UpstreamCredential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[storeKey(userID, serverKey)] = cred
}

// fakeExchanger records calls and returns a programmed credential/error.
type fakeExchanger struct {
	calls   int32
	cred    *UpstreamCredential
	err     error
	delay   time.Duration
	startWG *sync.WaitGroup
}

func (e *fakeExchanger) Exchange(_ context.Context, userID, serverKey string, _ *config.AuthBrokerConfig) (*UpstreamCredential, error) {
	if e.startWG != nil {
		e.startWG.Done()
	}
	if e.delay > 0 {
		time.Sleep(e.delay)
	}
	atomic.AddInt32(&e.calls, 1)
	if e.err != nil {
		return nil, e.err
	}
	return e.cred, nil
}

// fakeConnector implements Connector for connect-flow paths.
type fakeConnector struct {
	serverKey    string
	authURL      string
	buildErr     error
	refreshCred  *UpstreamCredential
	refreshErr   error
	buildCalls   int32
	refreshCalls int32
}

func (c *fakeConnector) ServerKey() string { return c.serverKey }

func (c *fakeConnector) BuildAuthorizationURL(_ string) (string, string, error) {
	atomic.AddInt32(&c.buildCalls, 1)
	if c.buildErr != nil {
		return "", "", c.buildErr
	}
	return c.authURL, "state-xyz", nil
}

func (c *fakeConnector) Refresh(_ context.Context, _ string) (*UpstreamCredential, error) {
	atomic.AddInt32(&c.refreshCalls, 1)
	if c.refreshErr != nil {
		return nil, c.refreshErr
	}
	return c.refreshCred, nil
}

type fakeConnectorProvider struct {
	conn *fakeConnector
	err  error
}

func (p *fakeConnectorProvider) ConnectorFor(_ *config.ServerConfig) (Connector, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.conn, nil
}

// --- fixtures ----------------------------------------------------------------

func httpServer(name string, broker *config.AuthBrokerConfig) *config.ServerConfig {
	return &config.ServerConfig{
		Name:       name,
		URL:        "https://" + name + ".example.com/mcp",
		Protocol:   "http",
		AuthBroker: broker,
	}
}

func tokenExchangeBroker() *config.AuthBrokerConfig {
	b := &config.AuthBrokerConfig{Mode: config.AuthBrokerModeTokenExchange, TokenEndpoint: "https://idp/token", Scopes: []string{"api"}}
	b.ApplyDefaults()
	return b
}

func connectBroker() *config.AuthBrokerConfig {
	b := &config.AuthBrokerConfig{
		Mode:                  config.AuthBrokerModeOAuthConnect,
		TokenEndpoint:         "https://idp/token",
		AuthorizationEndpoint: "https://idp/authorize",
		ClientID:              "client",
	}
	b.ApplyDefaults()
	return b
}

func validCred() *UpstreamCredential {
	return &UpstreamCredential{Type: "oauth2", AccessToken: "cached-token", ExpiresAt: time.Now().Add(time.Hour), ObtainedVia: "token_exchange"}
}

// --- tests -------------------------------------------------------------------

func TestResolve_ValidCachedCredential(t *testing.T) {
	store := newFakeStore()
	server := httpServer("grafana", tokenExchangeBroker())
	key := oauth.GenerateServerKey(server.Name, server.URL)
	store.seed("alice", key, validCred())

	ex := &fakeExchanger{cred: &UpstreamCredential{AccessToken: "fresh"}}
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: ex})

	got, err := r.Resolve(context.Background(), "alice", server)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.AccessToken != "cached-token" {
		t.Fatalf("expected cached token, got %q", got.AccessToken)
	}
	if c := atomic.LoadInt32(&ex.calls); c != 0 {
		t.Fatalf("expected no exchange calls for valid cache, got %d", c)
	}
}

func TestResolve_NearExpiryRefresh_TokenExchange(t *testing.T) {
	store := newFakeStore()
	server := httpServer("grafana", tokenExchangeBroker())
	key := oauth.GenerateServerKey(server.Name, server.URL)
	nearExpiry := &UpstreamCredential{AccessToken: "old", ExpiresAt: time.Now().Add(10 * time.Second)}
	store.seed("alice", key, nearExpiry)

	ex := &fakeExchanger{cred: &UpstreamCredential{AccessToken: "refreshed"}}
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: ex})

	got, err := r.Resolve(context.Background(), "alice", server)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.AccessToken != "refreshed" {
		t.Fatalf("expected refreshed token, got %q", got.AccessToken)
	}
	if c := atomic.LoadInt32(&ex.calls); c != 1 {
		t.Fatalf("expected 1 exchange call, got %d", c)
	}
}

func TestResolve_NearExpiryRefresh_ConnectFlow(t *testing.T) {
	store := newFakeStore()
	server := httpServer("github", connectBroker())
	key := oauth.GenerateServerKey(server.Name, server.URL)
	store.seed("alice", key, &UpstreamCredential{AccessToken: "old", RefreshToken: "rt", ExpiresAt: time.Now().Add(5 * time.Second)})

	conn := &fakeConnector{serverKey: key, refreshCred: &UpstreamCredential{AccessToken: "refreshed-connect"}}
	r := NewCredentialResolver(ResolverDeps{Store: store, Connectors: &fakeConnectorProvider{conn: conn}})

	got, err := r.Resolve(context.Background(), "alice", server)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.AccessToken != "refreshed-connect" {
		t.Fatalf("expected refreshed-connect, got %q", got.AccessToken)
	}
	if c := atomic.LoadInt32(&conn.refreshCalls); c != 1 {
		t.Fatalf("expected 1 refresh call, got %d", c)
	}
}

func TestResolve_NoCache_TokenExchange(t *testing.T) {
	store := newFakeStore()
	server := httpServer("grafana", tokenExchangeBroker())
	ex := &fakeExchanger{cred: &UpstreamCredential{AccessToken: "exchanged"}}
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: ex})

	got, err := r.Resolve(context.Background(), "alice", server)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.AccessToken != "exchanged" {
		t.Fatalf("expected exchanged token, got %q", got.AccessToken)
	}
}

func TestResolve_NoCache_EntraOBO(t *testing.T) {
	store := newFakeStore()
	b := &config.AuthBrokerConfig{Mode: config.AuthBrokerModeEntraOBO, TokenEndpoint: "https://login.microsoftonline.com/token"}
	b.ApplyDefaults()
	server := httpServer("graph", b)
	ex := &fakeExchanger{cred: &UpstreamCredential{AccessToken: "obo"}}
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: ex})

	got, err := r.Resolve(context.Background(), "alice", server)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.AccessToken != "obo" {
		t.Fatalf("expected obo token, got %q", got.AccessToken)
	}
}

func TestResolve_ConnectUnconnected_ReturnsActionableConnectURL(t *testing.T) {
	store := newFakeStore()
	server := httpServer("github", connectBroker())
	key := oauth.GenerateServerKey(server.Name, server.URL)
	conn := &fakeConnector{serverKey: key, authURL: "https://idp/authorize?client_id=client&state=state-xyz"}
	r := NewCredentialResolver(ResolverDeps{Store: store, Connectors: &fakeConnectorProvider{conn: conn}})

	_, err := r.Resolve(context.Background(), "alice", server)
	if err == nil {
		t.Fatal("expected NotConnectedError, got nil")
	}
	var nce *NotConnectedError
	if !errors.As(err, &nce) {
		t.Fatalf("expected *NotConnectedError, got %T: %v", err, err)
	}
	if nce.ConnectURL != conn.authURL {
		t.Fatalf("expected connect URL %q in error, got %q", conn.authURL, nce.ConnectURL)
	}
	if !strings.Contains(err.Error(), conn.authURL) {
		t.Fatalf("error message must surface the connect URL, got %q", err.Error())
	}
}

func TestResolve_Unauthenticated_Rejected(t *testing.T) {
	store := newFakeStore()
	server := httpServer("grafana", tokenExchangeBroker())
	ex := &fakeExchanger{cred: validCred()}
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: ex})

	_, err := r.Resolve(context.Background(), "", server)
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}
	if c := atomic.LoadInt32(&ex.calls); c != 0 {
		t.Fatalf("expected no work for unauthenticated caller, got %d exchange calls", c)
	}
}

func TestResolve_StoreDisabled_DegradesGracefully(t *testing.T) {
	store := newFakeStore()
	store.enabled = false
	server := httpServer("grafana", tokenExchangeBroker())
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: &fakeExchanger{cred: validCred()}})

	_, err := r.Resolve(context.Background(), "alice", server)
	if !errors.Is(err, ErrStoreDisabled) {
		t.Fatalf("expected ErrStoreDisabled, got %v", err)
	}
}

func TestResolve_NoBrokerConfig_Rejected(t *testing.T) {
	store := newFakeStore()
	server := httpServer("plain", nil)
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: &fakeExchanger{}})

	_, err := r.Resolve(context.Background(), "alice", server)
	if err == nil {
		t.Fatal("expected error for server without auth_broker, got nil")
	}
}

func TestResolve_NoStaticFallback_OnExchangeFailure(t *testing.T) {
	store := newFakeStore()
	server := httpServer("grafana", tokenExchangeBroker())
	ex := &fakeExchanger{err: errors.New("token exchange failed: status 401, error \"invalid_grant\"")}
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: ex})

	got, err := r.Resolve(context.Background(), "alice", server)
	if err == nil {
		t.Fatal("expected the exchange error to propagate (no static fallback), got nil")
	}
	if got != nil {
		t.Fatalf("expected no credential on failure (FR-014, no shared fallback), got %+v", got)
	}
}

func TestResolve_PolicyHook_DeniesInjection(t *testing.T) {
	store := newFakeStore()
	server := httpServer("grafana", tokenExchangeBroker())
	key := oauth.GenerateServerKey(server.Name, server.URL)
	store.seed("alice", key, validCred())

	policy := PolicyHookFunc(func(_ context.Context, in PolicyInput) (PolicyDecision, error) {
		return PolicyDecision{Allow: false, Reason: "blocked by policy for " + in.ServerName}, nil
	})
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: &fakeExchanger{}, Policy: policy})

	_, err := r.Resolve(context.Background(), "alice", server)
	var pde *PolicyDeniedError
	if !errors.As(err, &pde) {
		t.Fatalf("expected *PolicyDeniedError, got %T: %v", err, err)
	}
	if !strings.Contains(pde.Reason, "grafana") {
		t.Fatalf("expected reason to include server name, got %q", pde.Reason)
	}
}

func TestResolve_SingleFlight_CoalescesConcurrentAcquisitions(t *testing.T) {
	store := newFakeStore()
	server := httpServer("grafana", tokenExchangeBroker())

	const n = 12
	var start sync.WaitGroup
	start.Add(1)
	ex := &fakeExchanger{cred: &UpstreamCredential{AccessToken: "exchanged"}, delay: 40 * time.Millisecond}
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: ex})

	var wg sync.WaitGroup
	errs := make([]error, n)
	toks := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			start.Wait()
			cred, err := r.Resolve(context.Background(), "alice", server)
			errs[idx] = err
			if cred != nil {
				toks[idx] = cred.AccessToken
			}
		}(i)
	}
	start.Done() // release all goroutines together
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d errored: %v", i, errs[i])
		}
		if toks[i] != "exchanged" {
			t.Fatalf("goroutine %d got %q", i, toks[i])
		}
	}
	if c := atomic.LoadInt32(&ex.calls); c != 1 {
		t.Fatalf("single-flight should coalesce to 1 upstream acquisition, got %d", c)
	}
}
