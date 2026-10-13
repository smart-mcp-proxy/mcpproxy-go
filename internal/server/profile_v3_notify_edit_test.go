//go:build !server

package server

import (
	"context"
	"sync"
	"testing"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 108-f T067a/T072a (FR-027, FR-038): editing a profile - through the
// profiles service or by hand - notifies exactly the live sessions it governs
// on every routing-mode instance, once, after the new snapshot is published;
// the profiles.changed event follows the same rule.

type surfaceFn = func() (func(context.Context, *notifyingSession) error, func(context.Context, *notifyingSession) context.Context, func(context.Context, []byte))

func instanceSurface(srv func() *mcpserver.MCPServer) surfaceFn {
	return func() (func(context.Context, *notifyingSession) error, func(context.Context, *notifyingSession) context.Context, func(context.Context, []byte)) {
		return func(ctx context.Context, s *notifyingSession) error { return srv().RegisterSession(ctx, s) },
			func(ctx context.Context, s *notifyingSession) context.Context { return srv().WithContext(ctx, s) },
			func(ctx context.Context, msg []byte) { srv().HandleMessage(ctx, msg) }
	}
}

// fourSurfaces returns the routing-mode instances (retrieve, direct, call, code).
func fourSurfaces(p *MCPProxyServer) map[string]surfaceFn {
	return map[string]surfaceFn{
		"retrieve": instanceSurface(func() *mcpserver.MCPServer { return p.server }),
		"direct":   instanceSurface(func() *mcpserver.MCPServer { return p.directServer }),
		"call":     instanceSurface(func() *mcpserver.MCPServer { return p.callToolServer }),
		"code":     instanceSurface(func() *mcpserver.MCPServer { return p.codeExecServer }),
	}
}

type notifyHarness struct {
	t       *testing.T
	srv     *Server
	proxy   *MCPProxyServer
	cursor  map[string]*notifyingSession // locked to ro, one session per instance
	windsrf *notifyingSession            // locked to full
	anon    *notifyingSession            // no credential
	admin   *notifyingSession            // admin key
	counts  map[*notifyingSession]int
	mu      sync.Mutex
}

func newNotifyHarness(t *testing.T) *notifyHarness {
	t.Helper()
	srv, _ := newNotifyServer(t)
	h := &notifyHarness{t: t, srv: srv, proxy: srv.mcpProxy, cursor: map[string]*notifyingSession{}, counts: map[*notifyingSession]int{}}
	mintClient(t, srv, "cursor", "ro")
	mintClient(t, srv, "windsurf", "full")
	for name, surf := range fourSurfaces(h.proxy) {
		require.NotNil(t, surf, name)
		h.cursor[name] = initSession(t, surf, "cursor-"+name, clientCtx("cursor", "ro", auth.ProfileModeLocked))
	}
	h.windsrf = initSession(t, defaultSurface(h.proxy), "windsurf-1", clientCtx("windsurf", "full", auth.ProfileModeLocked))
	h.anon = initSession(t, defaultSurface(h.proxy), "anon-1", context.Background())
	h.admin = initSession(t, defaultSurface(h.proxy), "admin-1", auth.WithAuthContext(context.Background(), auth.AdminContext()))
	h.drainAll()
	return h
}

func (h *notifyHarness) all() []*notifyingSession {
	out := []*notifyingSession{h.windsrf, h.anon, h.admin}
	for _, s := range h.cursor {
		out = append(out, s)
	}
	return out
}

func (h *notifyHarness) drainAll() {
	for _, s := range h.all() {
		s.drainListChanged()
	}
	h.mu.Lock()
	h.counts = map[*notifyingSession]int{}
	h.mu.Unlock()
}

// settle waits until the expected sessions got at least one notification, then
// waits a little longer so a duplicate would show, and returns the totals.
func (h *notifyHarness) settle(expect ...*notifyingSession) map[*notifyingSession]int {
	h.t.Helper()
	collect := func() {
		for _, s := range h.all() {
			if n := s.drainListChanged(); n > 0 {
				h.mu.Lock()
				h.counts[s] += n
				h.mu.Unlock()
			}
		}
	}
	require.Eventually(h.t, func() bool {
		collect()
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, s := range expect {
			if h.counts[s] == 0 {
				return false
			}
		}
		return true
	}, 5*time.Second, 5*time.Millisecond, "the governed sessions were never notified")
	time.Sleep(200 * time.Millisecond)
	collect()
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[*notifyingSession]int{}
	for k, v := range h.counts {
		out[k] = v
	}
	return out
}

func (h *notifyHarness) actor() runtime.Actor {
	return runtime.Actor{Kind: "api_key", Surface: profile.SurfaceAPI}
}

func (h *notifyHarness) profiles() *runtime.ProfilesService { return h.srv.runtime.ProfilesService() }

func (h *notifyHarness) cursorSessions() []*notifyingSession {
	var out []*notifyingSession
	for _, s := range h.cursor {
		out = append(out, s)
	}
	return out
}

func TestProfileV3Notify_ServiceEditNotifiesGovernedSessionsOnEveryInstance(t *testing.T) {
	h := newNotifyHarness(t)
	_, err := h.profiles().Update(context.Background(), h.actor(), "ro", config.ProfileConfig{Name: "ro", Servers: []string{"a"}, MaxTier: "read"})
	require.NoError(t, err)

	counts := h.settle(h.cursorSessions()...)
	for name, s := range h.cursor {
		assert.Equal(t, 1, counts[s], "exactly one list_changed on the %s instance", name)
	}
	assert.Zero(t, counts[h.windsrf], "a session of another profile is not notified")
	assert.Zero(t, counts[h.anon])
	assert.Zero(t, counts[h.admin])
}

func TestProfileV3Notify_HandEditViaConfigReloadNotifiesToo(t *testing.T) {
	h := newNotifyHarness(t)
	// A hand edit reaches the runtime through the config reload path: a new
	// snapshot with the profile's max_tier changed, no service involved.
	cfg, err := h.srv.runtime.GetDesiredConfig()
	require.NoError(t, err)
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name == "full" {
			cfg.Profiles[i].MaxTier = "write"
		}
	}
	h.srv.runtime.UpdateConfig(cfg, "")

	counts := h.settle(h.windsrf)
	assert.Equal(t, 1, counts[h.windsrf])
	for name, s := range h.cursor {
		assert.Zero(t, counts[s], "cursor is on ro; the %s instance must not be notified", name)
	}
}

func TestProfileV3Notify_RenameAndDeleteNotify(t *testing.T) {
	h := newNotifyHarness(t)
	_, err := h.profiles().Rename(context.Background(), h.actor(), "ro", "ro2")
	require.NoError(t, err)
	counts := h.settle(h.cursorSessions()...)
	for name, s := range h.cursor {
		assert.Equal(t, 1, counts[s], "rename: %s instance", name)
	}
	assert.Zero(t, counts[h.windsrf])

	h.drainAll()
	_, err = h.profiles().Delete(context.Background(), h.actor(), "full", "ro2", false)
	require.NoError(t, err)
	counts = h.settle(h.windsrf)
	assert.Equal(t, 1, counts[h.windsrf], "delete with reassign notifies the sessions it moved")
}

func TestProfileV3Notify_UnrelatedConfigEditSendsNone(t *testing.T) {
	h := newNotifyHarness(t)
	cfg, err := h.srv.runtime.GetDesiredConfig()
	require.NoError(t, err)
	cfg.ToolsLimit = 21
	h.srv.runtime.UpdateConfig(cfg, "")

	time.Sleep(400 * time.Millisecond)
	counts := h.settleNone()
	for s, n := range counts {
		assert.Zero(t, n, "%v", s.id)
	}
}

// settleNone drains without expecting anything.
func (h *notifyHarness) settleNone() map[*notifyingSession]int {
	out := map[*notifyingSession]int{}
	for _, s := range h.all() {
		out[s] = s.drainListChanged()
	}
	return out
}

func TestProfileV3Notify_AnonymousProfileChangeNotifiesOnlyAnonymousSessions(t *testing.T) {
	h := newNotifyHarness(t)
	require.NoError(t, h.profiles().SetAnonymous(context.Background(), h.actor(), "ro"))

	counts := h.settle(h.anon)
	assert.Equal(t, 1, counts[h.anon])
	assert.Zero(t, counts[h.windsrf])
	assert.Zero(t, counts[h.admin], "an administrator session is not anonymous")
	for name, s := range h.cursor {
		assert.Zero(t, counts[s], "%s", name)
	}
}

func TestProfileV3Notify_EventFollowsPublicationAndNamesEachProfileOnce(t *testing.T) {
	h := newNotifyHarness(t)
	events := h.srv.runtime.SubscribeEvents()
	defer h.srv.runtime.UnsubscribeEvents(events)

	_, err := h.profiles().Create(context.Background(), h.actor(), config.ProfileConfig{Name: "fresh", Servers: []string{"a"}})
	require.NoError(t, err)

	var got []map[string]any
	deadline := time.After(5 * time.Second)
loop:
	for {
		select {
		case evt := <-events:
			if evt.Type != runtime.EventTypeProfilesChanged {
				continue
			}
			// The snapshot that contains the profile is ALREADY the current one
			// when the event is observable.
			found := false
			for _, p := range h.proxy.currentConfig().Profiles {
				found = found || p.Name == "fresh"
			}
			assert.True(t, found, "profiles.changed must follow publication")
			got = append(got, evt.Payload)
			break loop
		case <-deadline:
			t.Fatal("no profiles.changed event")
		}
	}
	require.Len(t, got, 1)
	assert.Equal(t, "fresh", got[0]["name"])
	assert.Equal(t, "create", got[0]["change"])
	assert.NotContains(t, got[0], "previous_name")

	// And no second event for the same write.
	select {
	case evt := <-events:
		assert.NotEqual(t, runtime.EventTypeProfilesChanged, evt.Type, "one event per changed profile")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestProfileV3Notify_BurstOfPublishesLosesNoDelta(t *testing.T) {
	h := newNotifyHarness(t)
	ctx := context.Background()
	_, err := h.profiles().Update(ctx, h.actor(), "ro", config.ProfileConfig{Name: "ro", Servers: []string{"a"}, MaxTier: "read"})
	require.NoError(t, err)
	_, err = h.profiles().Update(ctx, h.actor(), "full", config.ProfileConfig{Name: "full", Servers: []string{"a", "b"}, MaxTier: "write"})
	require.NoError(t, err)

	expect := append(h.cursorSessions(), h.windsrf)
	counts := h.settle(expect...)
	for _, s := range expect {
		assert.GreaterOrEqual(t, counts[s], 1, s.id)
		assert.LessOrEqual(t, counts[s], 2, s.id)
	}
	assert.Zero(t, counts[h.admin])
	assert.Zero(t, counts[h.anon])
}

func TestProfileV3Notify_DeltaComputation(t *testing.T) {
	a := &config.Config{Servers: []*config.ServerConfig{{Name: "a"}, {Name: "b"}}, Profiles: []config.ProfileConfig{
		{Name: "p", Servers: []string{"a"}, Title: "P"},
		{Name: "q", Servers: []string{"b"}},
	}}
	b := &config.Config{Servers: a.Servers, Profiles: []config.ProfileConfig{
		{Name: "p", Servers: []string{"a"}, Title: "Renamed title"}, // display-only
		{Name: "q", Servers: []string{"a", "b"}},                    // servers
		{Name: "r", Servers: []string{"a"}},                         // new
	}}
	d := computeProfileDelta(a, b)
	assert.False(t, d.notify["p"], "a display-only edit re-lists nobody")
	assert.True(t, d.notify["q"])
	assert.True(t, d.notify["r"])
	byName := map[string]string{}
	for _, e := range d.events {
		byName[e.name] = e.change
	}
	assert.Equal(t, map[string]string{"p": "update", "q": "update", "r": "create"}, byName, "a display edit still invalidates UIs")
}
