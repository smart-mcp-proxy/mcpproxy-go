package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// newProfileGateTestServer builds a Server whose logger is observed, with two
// configured servers and two single-server profiles, so profileMiddleware
// can be driven directly with a hand-built AuthContext (auth already ran).
func newProfileGateTestServer(t *testing.T) (*Server, *observer.ObservedLogs) {
	t.Helper()

	core, logs := observer.New(zap.DebugLevel)
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.Servers = []*config.ServerConfig{{Name: "research-srv"}, {Name: "deploy-srv"}}
	cfg.Profiles = []config.ProfileConfig{
		{Name: "research", Servers: []string{"research-srv"}},
		{Name: "deploy", Servers: []string{"deploy-srv"}},
	}

	srv, err := NewServer(cfg, zap.New(core))
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })
	return srv, logs
}

// TestProfileMiddleware_ScopedRefusalIsLoggedForOperator (Spec 105 PR D
// critique round 1, finding S1): the uniform FR-004 refusal is deliberately
// silent towards the AGENT, but it must not be silent towards the OPERATOR.
// The gate answers before the request reaches the logging handler mounted
// inside it, so without its own log line a scoped token walking the slug
// space of /mcp/p/ leaves no trace at all. One structured line per refusal,
// naming the agent, the slug it asked for and where it came from — and none
// on admission.
func TestProfileMiddleware_ScopedRefusalIsLoggedForOperator(t *testing.T) {
	srv, logs := newProfileGateTestServer(t)

	reached := false
	handler := srv.profileMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		reached = true
	}))
	agent := &auth.AuthContext{Type: auth.AuthTypeAgent, AgentName: "a-only", AllowedServers: []string{"research-srv"}}

	req := httptest.NewRequest(http.MethodPost, "/mcp/p/deploy", http.NoBody)
	req.RemoteAddr = "203.0.113.7:4242"
	req = req.WithContext(auth.WithAuthContext(req.Context(), agent))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.False(t, reached, "a non-selectable slug must not reach the MCP handler")

	refusals := logs.FilterMessage("profile URL refused for scoped caller").All()
	require.Len(t, refusals, 1, "exactly one operator-facing line per refusal")
	fields := refusals[0].ContextMap()
	require.Equal(t, "a-only", fields["agent_name"])
	require.Equal(t, "deploy", fields["profile"])
	require.Equal(t, "203.0.113.7:4242", fields["remote_addr"])

	// Admission through a selectable profile is not a refusal and logs none.
	logs.TakeAll()
	req = httptest.NewRequest(http.MethodPost, "/mcp/p/research", http.NoBody)
	req = req.WithContext(auth.WithAuthContext(req.Context(), agent))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.True(t, reached, "the selectable profile must be admitted")
	require.Empty(t, logs.FilterMessage("profile URL refused for scoped caller").All())
}

// profileGateFleetConfig builds a config over a fleet of 1+n profiles: "pin"
// (reaching "pin-srv") followed by n profiles "p0".."p<n-1>" that reach only
// "other-srv". With n == -1 the fleet has no profiles at all. hidden further
// servers "hidden0".."hidden<hidden-1>" are configured but declared by no
// profile and granted to no test token — the server population an operator
// runs and a scoped token must not be able to measure (codex round 4).
func profileGateFleetConfig(n, hidden int) *config.Config {
	cfg := &config.Config{Servers: []*config.ServerConfig{{Name: "pin-srv"}, {Name: "other-srv"}}}
	for i := 0; i < hidden; i++ {
		cfg.Servers = append(cfg.Servers, &config.ServerConfig{Name: fmt.Sprintf("hidden%d", i)})
	}
	if n >= 0 {
		cfg.Profiles = []config.ProfileConfig{{Name: "pin", Servers: []string{"pin-srv"}}}
		for i := 0; i < n; i++ {
			cfg.Profiles = append(cfg.Profiles, config.ProfileConfig{Name: fmt.Sprintf("p%d", i), Servers: []string{"other-srv"}})
		}
	}
	return cfg
}

// profileGateFleet is one fleet shape driven through serveProfileURL — the
// whole gate after the snapshot read — on a bare Server. No runtime stands
// behind it on purpose: a live runtime over thousands of profiles spends the
// test building per-profile indexes in the background, which both inflates
// allocation readings and races the TempDir cleanup.
type profileGateFleet struct {
	srv *Server
	cfg *config.Config
}

func (f profileGateFleet) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.srv.serveProfileURL(w, r, f.cfg, next)
	})
}

// profileGateFleets builds the fleet shapes the gate tests replay: the
// profile population (none / the pin alone / 4 096 hidden profiles) and the
// server population (two servers / 4 096 hidden servers behind the same two).
func profileGateFleets() map[string]profileGateFleet {
	fleets := map[string]profileGateFleet{}
	for name, shape := range map[string][2]int{
		"no profiles":         {-1, 0},
		"pin only":            {0, 0},
		"4096 others":         {4096, 0},
		"4096 hidden servers": {0, 4096},
	} {
		fleets[name] = profileGateFleet{srv: &Server{logger: zap.NewNop()}, cfg: profileGateFleetConfig(shape[0], shape[1])}
	}
	return fleets
}

// profileGateRefusalCases enumerates every scoped refusal branch of the
// /mcp/p/<slug> gate. Each one is a refusal in EVERY fleet shape the tests
// below build (p0 is absent in a one-profile fleet, present but disjoint or
// pin-mismatched in a larger one), so the same table can be replayed against
// fleets of different population and the results compared.
var profileGateRefusalCases = map[string]struct {
	agent *auth.AuthContext
	path  string
}{
	"pin mismatch, absent slug":    {&auth.AuthContext{Type: auth.AuthTypeAgent, ProfilePin: "pin", AllowedServers: []string{"pin-srv"}}, "/mcp/p/nope"},
	"pin mismatch, existing slug":  {&auth.AuthContext{Type: auth.AuthTypeAgent, ProfilePin: "pin", AllowedServers: []string{"pin-srv"}}, "/mcp/p/p0"},
	"deleted pin":                  {&auth.AuthContext{Type: auth.AuthTypeAgent, ProfilePin: "gone", AllowedServers: []string{"pin-srv"}}, "/mcp/p/gone"},
	"zero-reach pin":               {&auth.AuthContext{Type: auth.AuthTypeAgent, ProfilePin: "pin", AllowedServers: []string{"other-srv"}}, "/mcp/p/pin"},
	"scoped, absent slug":          {&auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"pin-srv"}}, "/mcp/p/nope"},
	"scoped, disjoint slug":        {&auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"pin-srv"}}, "/mcp/p/p0"},
	"scoped, empty allowlist":      {&auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{}}, "/mcp/p/pin"},
	"pinned, slug-less /mcp/p":     {&auth.AuthContext{Type: auth.AuthTypeAgent, ProfilePin: "pin", AllowedServers: []string{"pin-srv"}}, "/mcp/p"},
	"scoped wildcard, absent slug": {&auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"*"}}, "/mcp/p/nope"},
}

// profileGateRefusal drives one request through the gate and returns the
// recorder, asserting the uniform refusal shape.
func profileGateRefusal(t *testing.T, handler http.Handler, agent *auth.AuthContext, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, http.NoBody)
	req = req.WithContext(auth.WithAuthContext(req.Context(), agent))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code, "%s must be refused", path)
	return rec
}

// TestProfileMiddleware_RefusalWorkIndependentOfFleet (Spec 105 PR D codex
// round 2, finding 1): the uniform refusal must not cost work proportional to
// the number of OTHER profiles the operator has configured. A gate that
// computed the whole selectable-profile list before refusing did zero
// iterations over an empty fleet and one EffectiveServers per profile over a
// populated one — same status and body, fleet-sized difference in work, so a
// scoped token could learn whether hidden profiles exist from how long its
// own refusal took (FR-004; spec Definitions: non-disclosing = status, body
// AND timing class).
//
// The witness is deterministic, not wall-clock: the allocation profile of one
// refusal is identical over a fleet with no profiles, one profile and 4 097
// profiles, for every refusal branch. (HEAD before the fix: 31 allocations
// over the empty fleet, ~4 129 over the large one — 1.7 µs vs 0.9 ms at
// 10 000 profiles.) AllocsPerRun counts every goroutine's mallocs and the
// package's other tests may leave background work behind, so a reading is
// retried into a quiet window — noise only ever adds, and a fleet-
// proportional gate is off by thousands, so it can never pass. The pure
// predicate is pinned at zero allocations without any retry in
// TestProfileIndex_SelectableAllocatesNothing.
func TestProfileMiddleware_RefusalWorkIndependentOfFleet(t *testing.T) {
	fleets := profileGateFleets()
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("%s must not reach the MCP handler", r.URL.Path)
	})
	// Build every index up front: the first request after a snapshot change
	// pays the one-off index build, which is not part of a refusal's cost.
	for _, f := range fleets {
		f.srv.profileIndexes.For(f.cfg)
	}

	for name, c := range profileGateRefusalCases {
		t.Run(name, func(t *testing.T) {
			var allocs map[string]float64
			for attempt := 0; attempt < 10; attempt++ {
				allocs = map[string]float64{}
				for fleet, f := range fleets {
					handler := f.handler(next)
					allocs[fleet] = testing.AllocsPerRun(20, func() { profileGateRefusal(t, handler, c.agent, c.path) })
				}
				same := true
				for fleet := range fleets {
					same = same && allocs[fleet] == allocs["no profiles"]
				}
				if same {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("%s must allocate exactly like the empty fleet on every fleet: %v", name, allocs)
		})
	}
}

// TestProfileMiddleware_GateTouchesOnlyRequestedSlugAndPin is the traversal-
// counter seam behind the allocation parity above: through the index's lookup
// hook, every scoped request to the gate — refused or admitted, over any fleet
// — resolves at most the slug it asked for and the caller's pin, never a
// third profile. (Admission resolves the slug twice: once to decide, once to
// build the scope.)
func TestProfileMiddleware_GateTouchesOnlyRequestedSlugAndPin(t *testing.T) {
	fleets := profileGateFleets()
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})

	cases := map[string]struct {
		agent *auth.AuthContext
		path  string
	}{}
	for name, c := range profileGateRefusalCases {
		cases[name] = c
	}
	cases["admitted pin"] = struct {
		agent *auth.AuthContext
		path  string
	}{&auth.AuthContext{Type: auth.AuthTypeAgent, ProfilePin: "pin", AllowedServers: []string{"pin-srv"}}, "/mcp/p/pin"}
	cases["admitted scoped"] = struct {
		agent *auth.AuthContext
		path  string
	}{&auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"*"}}, "/mcp/p/pin"}

	for fleet, f := range fleets {
		var touched []string
		idx := newProfileIndex(f.cfg)
		idx.lookupHook = func(slug string) { touched = append(touched, slug) }
		f.srv.profileIndexes.warm.Store(idx)
		handler := f.handler(next)

		for name, c := range cases {
			touched = nil
			req := httptest.NewRequest(http.MethodPost, c.path, http.NoBody)
			req = req.WithContext(auth.WithAuthContext(req.Context(), c.agent))
			handler.ServeHTTP(httptest.NewRecorder(), req)

			slug := strings.Trim(strings.TrimPrefix(c.path, "/mcp/p"), "/")
			allowed := map[string]bool{slug: true}
			if c.agent.ProfilePin != "" {
				allowed[c.agent.ProfilePin] = true
			}
			require.NotEmpty(t, touched, "%s/%s: the gate must resolve through the index", fleet, name)
			require.LessOrEqual(t, len(touched), 3, "%s/%s: at most slug, pin and the admission re-lookup: %v", fleet, name, touched)
			for _, got := range touched {
				require.True(t, allowed[got], "%s/%s: the gate touched profile %q, outside {slug, pin}: %v", fleet, name, got, touched)
			}
		}
		require.Same(t, idx, f.srv.profileIndexes.For(f.cfg), "%s: the cached index must be reused for the same snapshot", fleet)
	}
}

// TestProfileMiddleware_RefusalReachCostsTheGrantNotTheFleet (Spec 105 PR D
// codex round 4, finding 1): the reach test behind every scoped refusal must
// cost the READER's grant, never the fleet. A reach that walked every
// configured server and ran the credential check on each did one iteration
// over a fleet of one server and 4 096 over an otherwise identical fleet with
// 4 095 hidden servers behind it — same 404, same zero allocations (so the
// allocation-parity test above was blind to it), 36 ns vs 25 µs — a timing
// oracle on the number of servers the operator runs (FR-004; spec
// Definitions: non-disclosing = status, body AND timing class).
//
// Traversal-counter seam, not a clock: through the index's reach hook, every
// scoped refusal branch over every fleet shape performs exactly one
// membership test per entry of the token's own allowed_servers — a size the
// agent controls and already knows — and the count is identical across the
// two-server and the 4 098-server fleet.
func TestProfileMiddleware_RefusalReachCostsTheGrantNotTheFleet(t *testing.T) {
	fleets := profileGateFleets()
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("%s must not reach the MCP handler", r.URL.Path)
	})

	for name, c := range profileGateRefusalCases {
		t.Run(name, func(t *testing.T) {
			steps := map[string]int{}
			for fleet, f := range fleets {
				idx := newProfileIndex(f.cfg)
				idx.reachHook = func() { steps[fleet]++ }
				f.srv.profileIndexes.warm.Store(idx)
				profileGateRefusal(t, f.handler(next), c.agent, c.path)
			}
			for fleet, got := range steps {
				require.Equal(t, len(c.agent.AllowedServers), got,
					"%s over fleet %q: reach must test exactly one membership per granted server, never per configured server: %v", name, fleet, steps)
			}
			require.Equal(t, steps["no profiles"], steps["4096 hidden servers"],
				"%s: 4 096 hidden servers must cost exactly what an empty fleet costs: %v", name, steps)
		})
	}
}

// TestProfileMiddleware_RefusesThroughTheSnapshotSeam pins the production
// wiring the fleet tests bypass: profileMiddleware over a live runtime reaches
// the same gate (serveProfileURL) with the runtime's current snapshot — a
// scoped refusal and an admission behave identically through either entry.
func TestProfileMiddleware_RefusesThroughTheSnapshotSeam(t *testing.T) {
	srv, _ := newProfileGateTestServer(t)
	agent := &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"research-srv"}}
	reached := 0
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { reached++ })

	for _, entry := range []struct {
		name    string
		handler http.Handler
	}{
		{"profileMiddleware", srv.profileMiddleware(next)},
		{"serveProfileURL", profileGateFleet{srv: srv, cfg: srv.runtime.Config()}.handler(next)},
	} {
		rec := profileGateRefusal(t, entry.handler, agent, "/mcp/p/deploy")
		require.JSONEq(t, `{"error":"unknown profile 'deploy'"}`, rec.Body.String(), entry.name)

		before := reached
		req := httptest.NewRequest(http.MethodPost, "/mcp/p/research", http.NoBody)
		req = req.WithContext(auth.WithAuthContext(req.Context(), agent))
		rec = httptest.NewRecorder()
		entry.handler.ServeHTTP(rec, req)
		require.Equal(t, before+1, reached, "%s must admit the selectable profile", entry.name)
	}
}

// TestProfileIndex_WarmedBeforeFirstRequest (Spec 105 PR D codex round 3,
// prior item): the per-snapshot index must not be built by the first request
// after startup or a hot reload — that request would pay one insertion per
// configured profile (4 096 over a hidden fleet, none over an empty one),
// the fleet-population cost the index exists to remove (FR-004). The Server
// builds it when it is constructed and again on every config event (and,
// since round 5, before every publication — TestProfileIndex_BuiltBefore-
// Publication), so the gate's lazy build never serves a live runtime.
func TestProfileIndex_WarmedBeforeFirstRequest(t *testing.T) {
	srv, _ := newProfileGateTestServer(t)

	// Construction indexes the constructor's snapshot; background
	// initialization then publishes its own (followed by its config event),
	// so "covers the current snapshot" is reached, never requested.
	require.NotNil(t, srv.profileIndexes.warm.Load(), "the index must be built at construction, not by the first request")
	covered := func() bool {
		idx := srv.profileIndexes.warm.Load()
		return idx != nil && idx.cfg == srv.runtime.Config()
	}
	require.Eventually(t, covered, 5*time.Second, 10*time.Millisecond, "the startup snapshot must be indexed without a request")
	first := srv.runtime.Config()

	// A hot reload publishes a new snapshot; its config event rebuilds the
	// index before any request arrives.
	next := *first
	next.Profiles = append(slices.Clone(first.Profiles), config.ProfileConfig{Name: "extra", Servers: []string{"research-srv"}})
	_, err := srv.ApplyConfig(&next, filepath.Join(t.TempDir(), "mcp_config.json"))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return srv.runtime.Config() != first && covered() },
		5*time.Second, 10*time.Millisecond, "the reloaded snapshot must be indexed without a request")
	require.NotNil(t, srv.profileIndexes.warm.Load().lookup("extra"))
}

// TestProfileIndex_BuiltBeforePublication (Spec 105 PR D codex round 5,
// prior item P): warming from the config EVENT left a window — snapshot
// stored, event not yet delivered — in which a request built the fleet-sized
// index inline, and a token with server-write permission can open that
// window itself (upstream_servers add, then probe). The index is now built
// by a configsvc pre-publish observer, on the exact snapshot pointer, before
// it is stored: across N reloads over a live runtime, the request that
// follows each publication immediately — no event delivered, no wait —
// finds its index ready, and the request-path build seam never fires.
func TestProfileIndex_BuiltBeforePublication(t *testing.T) {
	srv, _ := newProfileGateTestServer(t)
	agent := &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"research-srv"}}
	reached := 0
	handler := srv.profileMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { reached++ }))

	// Let background initialization publish its startup snapshots first, so
	// the loop below measures the reloads it drives, not startup.
	require.Eventually(t, func() bool {
		idx := srv.profileIndexes.warm.Load()
		return idx != nil && idx.cfg == srv.runtime.Config()
	}, 5*time.Second, 10*time.Millisecond)

	// Deterministic seam, since the event listener may win the race on a
	// quiet machine: observers run in registration order, so this one sees
	// the warm slot right after the Server's observer and before the snapshot
	// is stored — the index for the config being published must already
	// cover that exact pointer.
	var unindexed atomic.Int32
	srv.runtime.ConfigService().AddPrePublishObserver(func(cfg *config.Config) {
		if idx := srv.profileIndexes.warm.Load(); idx == nil || idx.cfg != cfg {
			unindexed.Add(1)
		}
	})

	cfgPath := filepath.Join(t.TempDir(), "mcp_config.json")
	for i := 0; i < 5; i++ {
		before := srv.runtime.Config()
		slug := fmt.Sprintf("extra-%d", i)
		next := *before
		next.Profiles = append(slices.Clone(before.Profiles), config.ProfileConfig{Name: slug, Servers: []string{"research-srv"}})
		_, err := srv.ApplyConfig(&next, cfgPath)
		require.NoError(t, err)
		require.NotSame(t, before, srv.runtime.Config(), "ApplyConfig publishes synchronously")

		req := httptest.NewRequest(http.MethodPost, "/mcp/p/"+slug, http.NoBody)
		req = req.WithContext(auth.WithAuthContext(req.Context(), agent))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, i+1, reached, "reload %d: the new profile must be admitted through the freshly published snapshot: %s", i, rec.Body.String())
	}
	require.Zero(t, unindexed.Load(), "every published snapshot must be indexed before it is stored")
	require.Zero(t, srv.profileIndexes.lazyBuilds.Load(), "no request may build the index")
}

// TestProfileIndexCache_StaleRequestCannotEvictTheWarmIndex (Spec 105 PR D
// codex round 5, finding 1): the single-slot cache could be rolled back by a
// request — R1 captures snapshot A and stalls; a reload publishes and warms
// B; R1 resumes, builds A and overwrites the cached B; the next request under
// B rebuilds the whole fleet inline. The warm slot is written only by the
// warm path; a request that captured an older snapshot builds into the lazy
// slot and leaves the warm index where it is.
func TestProfileIndexCache_StaleRequestCannotEvictTheWarmIndex(t *testing.T) {
	older := &config.Config{Profiles: []config.ProfileConfig{{Name: "a"}}}
	current := &config.Config{Profiles: []config.ProfileConfig{{Name: "b"}}}

	var c profileIndexCache
	warmed := c.warmPublishing(current)
	require.Same(t, warmed, c.For(current), "the warmed index serves the current snapshot")

	stale := c.For(older) // an in-flight request that captured the previous snapshot
	require.Same(t, older, stale.cfg)
	require.Equal(t, int64(1), c.lazyBuilds.Load(), "the stale request builds for itself")

	require.Same(t, warmed, c.For(current), "the stale request must not have evicted the warm index")
	require.Same(t, stale, c.For(older), "the stale request's own index is retained beside it")
	require.Equal(t, int64(1), c.lazyBuilds.Load(), "and nothing was rebuilt")
}
