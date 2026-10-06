//go:build !server

package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Shared harness for the client-credential REST tests (both editions compile
// it; the routes it drives are personal-edition only).

const bindingAdminKey = "binding-admin-key"

type bindingController struct {
	baseController
	cfg *config.Config

	// The onboarding state persists in memory across requests (and across a
	// second REST server built over the same controller: a "restart"), and the
	// session page is whatever the test set.
	mu       sync.Mutex
	state    *storage.OnboardingState
	sessions []*contracts.MCPSession
}

func (c *bindingController) GetCurrentConfig() *config.Config   { return c.cfg }
func (c *bindingController) GetConfig() (*config.Config, error) { return c.cfg, nil }

func (c *bindingController) GetOnboardingState() (*storage.OnboardingState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == nil {
		return &storage.OnboardingState{}, nil
	}
	cp := *c.state
	return &cp, nil
}

func (c *bindingController) UpdateOnboardingState(fn func(*storage.OnboardingState) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == nil {
		c.state = &storage.OnboardingState{}
	}
	return fn(c.state)
}

func (c *bindingController) GetRecentSessions(f storage.SessionFilter) ([]*contracts.MCPSession, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.sessions
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, len(c.sessions), nil
}

type bindingHarness struct {
	t    *testing.T
	ctrl *bindingController
	srv  *Server
	sm   *storage.Manager
	svc  *internalRuntime.ClientsService
	key  []byte
	cfg  *config.Config
}

type refusingGuard struct {
	err *internalRuntime.BindingGuardError
}

func (g refusingGuard) BindingGuardDelta(_, _ internalRuntime.GuardState) []internalRuntime.BindingRef {
	return g.err.Bindings
}
func (g refusingGuard) BindingGuardFixes(internalRuntime.GuardState, []internalRuntime.BindingRef) []internalRuntime.GuardFix {
	return g.err.Fixes
}
func (g refusingGuard) BindingGuardActiveBindings() []internalRuntime.BindingRef { return nil }

func newBindingHarness(t *testing.T, guard internalRuntime.BindingGuard) *bindingHarness {
	t.Helper()
	dir := t.TempDir()
	sm, err := storage.NewManager(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = sm.Close() })
	key, err := auth.GetOrCreateHMACKey(dir)
	require.NoError(t, err)

	cfg := &config.Config{APIKey: bindingAdminKey, RequireMCPAuth: true, DataDir: dir,
		Profiles: []config.ProfileConfig{{Name: "ro", Servers: []string{"a"}}, {Name: "full", Servers: []string{"a", "b"}}}}
	svc := internalRuntime.NewClientsService(internalRuntime.ClientsServiceDeps{
		Store:    sm,
		HMACKey:  func() ([]byte, error) { return key, nil },
		Config:   func() *config.Config { return cfg },
		Guard:    func() internalRuntime.BindingGuard { return guard },
		Activity: sm.SaveActivity,
	})
	ctrl := &bindingController{cfg: cfg}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	srv.SetTokenStore(sm, dir)
	srv.SetClientsService(svc)
	return &bindingHarness{t: t, ctrl: ctrl, srv: srv, sm: sm, svc: svc, key: key, cfg: cfg}
}

func (h *bindingHarness) mint(clientID, prof string) string {
	h.t.Helper()
	m := h.svc.ConnectMinter()
	p := prof
	intent := connect.CredentialIntent{Profile: &p, ActorKind: "api_key", Surface: "api"}
	issued, err := m.Issue(clientID, intent)
	require.NoError(h.t, err)
	require.NoError(h.t, m.Commit(clientID, intent, issued))
	return issued.Secret
}

func (h *bindingHarness) put(clientID, body string, headers map[string]string, apiKey string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/clients/"+clientID+"/binding", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, req)
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out), w.Body.String())
	return out
}
