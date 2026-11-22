package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/mcplogin"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

const (
	oauthTestOrigin   = "http://127.0.0.1:8080"
	oauthTestRedirect = "http://127.0.0.1:5555/cb"
)

var oauthTestKey = []byte("oauth-login-test-key-0123456789ab")

type oauthTestClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *oauthTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *oauthTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// oauthFx is the browser-login services over a real storage manager.
type oauthFx struct {
	t       *testing.T
	sm      *storage.Manager
	cfg     *config.Config
	clock   *oauthTestClock
	svc     *OAuthLoginService
	clients *ClientsService
	creds   *CredentialsService
	reqs    *mcplogin.Store
	guard   BindingGuard

	mu     sync.Mutex
	rows   []*storage.ActivityRecord
	events []Event
}

func newOAuthFx(t *testing.T) *oauthFx {
	t.Helper()
	dir, err := os.MkdirTemp("", "oauth-login-svc-*")
	require.NoError(t, err)
	sm, err := storage.NewManager(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = sm.Close(); _ = os.RemoveAll(dir) })

	fx := &oauthFx{t: t, sm: sm, clock: &oauthTestClock{t: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}, guard: stubGuard{}}
	fx.cfg = &config.Config{
		MCPOAuthEnabled: true, RequireMCPAuth: true,
		Profiles: []config.ProfileConfig{{Name: "ro", Servers: []string{"a"}}, {Name: "rw", Servers: []string{"a"}}},
	}
	fx.reqs = mcplogin.NewStore(oauthTestKey, fx.clock.Now)
	var shared sync.Mutex
	keyf := func() ([]byte, error) { return oauthTestKey, nil }
	cfgf := func() *config.Config { return fx.cfg }
	guardf := func() BindingGuard { return fx.guard }
	act := func(r *storage.ActivityRecord) error {
		fx.mu.Lock()
		defer fx.mu.Unlock()
		fx.rows = append(fx.rows, r)
		return nil
	}
	pub := func(e Event) {
		fx.mu.Lock()
		defer fx.mu.Unlock()
		fx.events = append(fx.events, e)
	}
	fx.svc = NewOAuthLoginService(OAuthLoginDeps{
		Store: sm, HMACKey: keyf, Config: cfgf, Guard: guardf, Activity: act, Publish: pub,
		Now: fx.clock.Now, Mu: &shared, Requests: fx.reqs,
	})
	fx.clients = NewClientsService(ClientsServiceDeps{
		Store: sm, HMACKey: keyf, Config: cfgf, Guard: guardf, Activity: act, Publish: pub,
		Now: fx.clock.Now, Mu: &shared,
	})
	fx.clients.SetLoginCanceler(func(id string) { fx.svc.CancelForClient(id) })
	fx.creds = fx.clients.CredentialsServiceOver(sm)
	fx.creds.SetOAuthLogin(fx.svc)
	return fx
}

// stubGuard never refuses (the same shape config_funnel_test uses); tests that
// need the real conservative evaluator set fx.guard.
func (fx *oauthFx) ctx() context.Context {
	return ContextWithActor(context.Background(), Actor{Kind: "api_key", Name: "admin", Surface: profile.SurfaceAPI})
}

func (fx *oauthFx) actor() Actor {
	return Actor{Kind: "api_key", Name: "admin", Surface: profile.SurfaceAPI}
}

// mintClient creates an API-key client with a binding.
func (fx *oauthFx) mintClient(id, pin, mode string) {
	fx.t.Helper()
	_, err := fx.sm.MintClientCredential(id, "mcp_cli_"+id+"_oauth_test_secret_000000", oauthTestKey, mode, pin, fx.clock.Now().Add(24*time.Hour))
	require.NoError(fx.t, err)
}

func (fx *oauthFx) register() string {
	fx.t.Helper()
	resp, rerr, err := fx.svc.Register(mcplogin.RegistrationRequest{
		RedirectURIs: []string{oauthTestRedirect}, ClientName: "Cursor IDE",
	})
	require.NoError(fx.t, err)
	require.Nil(fx.t, rerr)
	return resp["client_id"].(string)
}

var oauthTestVerifier = strings.Repeat("v", 43)

func oauthTestChallenge() string {
	sum := sha256.Sum256([]byte(oauthTestVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (fx *oauthFx) authInput(reg, resource string) AuthorizeInput {
	return AuthorizeInput{
		Origin: oauthTestOrigin, ClientID: reg, RedirectURI: oauthTestRedirect, State: "st-1",
		CodeChallenge: oauthTestChallenge(), Method: "S256", Resource: resource, ResponseType: "code",
	}
}

// authorize registers nothing: it begins an authorization for reg at the
// per-client resource of client ("" = the generic address).
func (fx *oauthFx) authorize(reg, client string) string {
	fx.t.Helper()
	id, aerr := fx.svc.BeginAuthorization(fx.authInput(reg, oauthTestOrigin+mcplogin.ResourcePath(client)))
	require.Nil(fx.t, aerr)
	require.NotEmpty(fx.t, id)
	return id
}

func oauthCodeOf(t *testing.T, redirect string) string {
	t.Helper()
	u, err := url.Parse(redirect)
	require.NoError(t, err)
	code := u.Query().Get("code")
	require.NotEmpty(t, code, redirect)
	return code
}

func (fx *oauthFx) tokenInput(reg, code string) TokenInput {
	return TokenInput{Origin: oauthTestOrigin, Code: code, RedirectURI: oauthTestRedirect, ClientID: reg, CodeVerifier: oauthTestVerifier}
}

func oauthStrp(s string) *string { return &s }

// login runs register -> authorize -> approve -> exchange for a new client.
func (fx *oauthFx) loginNew(client, profileName string) (reg string, res TokenResult) {
	fx.t.Helper()
	reg = fx.register()
	id := fx.authorize(reg, client)
	redirect, err := fx.svc.Approve(fx.ctx(), id, ApprovalChoice{Client: ClientChoice{ID: client}, Profile: oauthStrp(profileName)})
	require.NoError(fx.t, err)
	var terr *TokenError
	res, terr = fx.svc.Exchange(fx.tokenInput(reg, oauthCodeOf(fx.t, redirect)))
	require.Nil(fx.t, terr)
	return reg, res
}

func (fx *oauthFx) changeRows(change string) []map[string]interface{} {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	var out []map[string]interface{}
	for _, r := range fx.rows {
		if r.Type == storage.ActivityTypeProfileChange && r.Metadata["change"] == change {
			out = append(out, r.Metadata)
		}
	}
	return out
}

func (fx *oauthFx) eventsOf(typ EventType) []Event {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	var out []Event
	for _, e := range fx.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func (fx *oauthFx) grants() []storage.OAuthGrant {
	fx.t.Helper()
	gs, err := fx.sm.ListOAuthGrants()
	require.NoError(fx.t, err)
	return gs
}

// nothingStored asserts that no login, binding or marker exists.
func (fx *oauthFx) nothingStored() {
	fx.t.Helper()
	require.Empty(fx.t, fx.grants())
	b, err := fx.sm.ListOAuthClientBindings()
	require.NoError(fx.t, err)
	require.Empty(fx.t, b)
	used, err := fx.sm.OAuthLoginEverUsed()
	require.NoError(fx.t, err)
	require.False(fx.t, used)
}

var _ = auth.KindClient
