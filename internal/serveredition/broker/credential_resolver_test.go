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
	calls     int32
	cred      *UpstreamCredential
	err       error
	delay     time.Duration
	startWG   *sync.WaitGroup
	gotCtxErr error
}

func (e *fakeExchanger) Exchange(ctx context.Context, userID, serverKey string, _ *config.AuthBrokerConfig) (*UpstreamCredential, error) {
	e.gotCtxErr = ctx.Err()
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

// TestResolve_SingleFlight_DetachesCallerCancellation proves the must-fix from
// review: the in-flight acquisition must not inherit the calling request's
// cancellation, or a cancelled caller would broadcast its ctx error to every
// co-pending acquisition for the same (user, server).
func TestResolve_SingleFlight_DetachesCallerCancellation(t *testing.T) {
	store := newFakeStore()
	server := httpServer("grafana", tokenExchangeBroker())
	ex := &fakeExchanger{cred: &UpstreamCredential{AccessToken: "exchanged"}}
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: ex})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // caller's request is already cancelled before acquisition runs

	got, err := r.Resolve(ctx, "alice", server)
	if err != nil {
		t.Fatalf("acquisition should run despite caller cancellation, got error: %v", err)
	}
	if got.AccessToken != "exchanged" {
		t.Fatalf("expected exchanged token, got %q", got.AccessToken)
	}
	if ex.gotCtxErr != nil {
		t.Fatalf("flight context must be detached from caller cancellation, got ctx.Err()=%v", ex.gotCtxErr)
	}
}

// TestResolve_TokenExchange_NearExpiry_NoDoubleExchangeOnFailure proves the
// advisory fix: a near-expiry token-exchange credential whose re-mint fails must
// surface that single error, not retry Exchange a second time.
func TestResolve_TokenExchange_NearExpiry_NoDoubleExchangeOnFailure(t *testing.T) {
	store := newFakeStore()
	server := httpServer("grafana", tokenExchangeBroker())
	key := oauth.GenerateServerKey(server.Name, server.URL)
	store.seed("alice", key, &UpstreamCredential{AccessToken: "old", ExpiresAt: time.Now().Add(5 * time.Second)})

	ex := &fakeExchanger{err: errors.New("token exchange failed: status 401, error \"invalid_grant\"")}
	r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: ex})

	_, err := r.Resolve(context.Background(), "alice", server)
	if err == nil {
		t.Fatal("expected the exchange error to propagate, got nil")
	}
	if c := atomic.LoadInt32(&ex.calls); c != 1 {
		t.Fatalf("near-expiry exchange failure must not double-call Exchange, got %d calls", c)
	}
}

// TestResolve_ConnectFlow_RefreshFails_ReturnsReconnectError proves the advisory
// fix: an already-connected user whose refresh fails gets an actionable
// reconnect error (with the connect URL), not a misleading "never connected".
func TestResolve_ConnectFlow_RefreshFails_ReturnsReconnectError(t *testing.T) {
	store := newFakeStore()
	server := httpServer("github", connectBroker())
	key := oauth.GenerateServerKey(server.Name, server.URL)
	store.seed("alice", key, &UpstreamCredential{AccessToken: "old", RefreshToken: "rt", ExpiresAt: time.Now().Add(5 * time.Second)})

	conn := &fakeConnector{
		serverKey:  key,
		authURL:    "https://idp/authorize?client_id=client&state=state-xyz",
		refreshErr: errors.New("oauth connector: token endpoint returned 400: invalid_grant"),
	}
	r := NewCredentialResolver(ResolverDeps{Store: store, Connectors: &fakeConnectorProvider{conn: conn}})

	_, err := r.Resolve(context.Background(), "alice", server)
	var nce *NotConnectedError
	if !errors.As(err, &nce) {
		t.Fatalf("expected *NotConnectedError, got %T: %v", err, err)
	}
	if nce.Reason == "" || !strings.Contains(nce.Reason, "reconnect") {
		t.Fatalf("expected a reconnect reason, got %q", nce.Reason)
	}
	if nce.ConnectURL != conn.authURL {
		t.Fatalf("expected connect URL %q, got %q", conn.authURL, nce.ConnectURL)
	}
	if c := atomic.LoadInt32(&conn.refreshCalls); c != 1 {
		t.Fatalf("expected exactly 1 refresh attempt, got %d", c)
	}
	if c := atomic.LoadInt32(&conn.buildCalls); c != 1 {
		t.Fatalf("expected the connect URL to be built once, got %d", c)
	}
}

// TestResolve_CrossUserIsolation_NeverReturnsAnotherUsersCredential is the
// direct cross-user isolation guard (MCP-2578, backlog follow-up to MCP-1039 /
// #688). #688 verified isolation only structurally (every lookup is
// store.Get(userID, serverKey)-keyed with no shared fallback); this asserts the
// behaviour end-to-end: seed user B's credential, Resolve as user A for the
// SAME serverKey, and prove user A gets the fail-closed path and NEVER user B's
// token.
//
// The seeded user B credential is deliberately VALID and not near expiry, so a
// regression that dropped the per-user keying (a shared/static fallback) would
// make acquire() return it directly from cache (FR-014). With correct keying,
// store.Get(userA, key) misses and user A falls through to its own — absent —
// acquisition path, which fails closed.
func TestResolve_CrossUserIsolation_NeverReturnsAnotherUsersCredential(t *testing.T) {
	const userBToken = "userB-secret-token-MUST-NOT-LEAK"

	t.Run("token_exchange mode falls closed, not to user B's cache", func(t *testing.T) {
		store := newFakeStore()
		server := httpServer("grafana", tokenExchangeBroker())
		key := oauth.GenerateServerKey(server.Name, server.URL)

		// Seed user B with a valid, long-lived credential for the same serverKey.
		store.seed("userB", key, &UpstreamCredential{
			Type:        "oauth2",
			AccessToken: userBToken,
			ExpiresAt:   time.Now().Add(time.Hour),
			ObtainedVia: "token_exchange",
		})

		// User A has no credential and its own acquisition path fails (fail closed).
		ex := &fakeExchanger{err: errors.New("token exchange failed: status 401, error \"invalid_grant\"")}
		r := NewCredentialResolver(ResolverDeps{Store: store, Exchanger: ex})

		got, err := r.Resolve(context.Background(), "userA", server)
		if err == nil {
			t.Fatal("expected user A to fail closed (no per-user credential), got nil error")
		}
		if got != nil {
			t.Fatalf("user A must receive no credential, got %+v", got)
		}
		// The acquisition path must have been exercised — proving user A did NOT
		// short-circuit to user B's cached credential (which a shared fallback
		// would do, skipping Exchange entirely).
		if c := atomic.LoadInt32(&ex.calls); c != 1 {
			t.Fatalf("expected user A to go through its own acquisition (1 Exchange call), got %d — a shared cache fallback would skip it", c)
		}

		// User B's credential is still intact and retrievable as user B, proving
		// the seed was real and the miss for user A is isolation, not absence.
		bCred, bErr := store.Get("userB", key)
		if bErr != nil || bCred == nil || bCred.AccessToken != userBToken {
			t.Fatalf("user B's credential should be intact; got %+v err=%v", bCred, bErr)
		}
	})

	t.Run("oauth_connect mode falls closed to NotConnectedError, not user B's cache", func(t *testing.T) {
		store := newFakeStore()
		server := httpServer("github", connectBroker())
		key := oauth.GenerateServerKey(server.Name, server.URL)

		// Seed user B with a valid connect-flow credential for the same serverKey.
		store.seed("userB", key, &UpstreamCredential{
			Type:         "oauth2",
			AccessToken:  userBToken,
			RefreshToken: "userB-refresh",
			ExpiresAt:    time.Now().Add(time.Hour),
			ObtainedVia:  "oauth_connect",
		})

		conn := &fakeConnector{serverKey: key, authURL: "https://idp/authorize?client_id=client&state=state-xyz"}
		r := NewCredentialResolver(ResolverDeps{Store: store, Connectors: &fakeConnectorProvider{conn: conn}})

		got, err := r.Resolve(context.Background(), "userA", server)
		if got != nil {
			t.Fatalf("user A must receive no credential, got %+v", got)
		}
		var nce *NotConnectedError
		if !errors.As(err, &nce) {
			t.Fatalf("expected user A to fail closed with *NotConnectedError, got %T: %v", err, err)
		}
		if nce.ConnectURL != conn.authURL {
			t.Fatalf("expected the actionable connect URL %q, got %q", conn.authURL, nce.ConnectURL)
		}
		// User A must be steered into its own connect flow, never handed user B's
		// existing connection (no refresh against user B's cached credential).
		if c := atomic.LoadInt32(&conn.refreshCalls); c != 0 {
			t.Fatalf("user A must not refresh against user B's cached credential, got %d refresh calls", c)
		}
		if !strings.Contains(err.Error(), conn.authURL) || strings.Contains(err.Error(), userBToken) {
			t.Fatalf("error must surface user A's connect URL and never user B's token, got %q", err.Error())
		}
	})
}
