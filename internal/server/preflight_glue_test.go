package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// preflightFixture is a proxy wired to real storage + a real Bleve index, plus
// an INSTRUMENTED upstream: every HTTP request an upstream client would make
// increments upstreamHits. FR-006 says a preflight performs zero upstream I/O,
// and that is asserted here as a hard count, not as a threshold.
type preflightFixture struct {
	proxy        *MCPProxyServer
	storage      *storage.Manager
	index        *index.Manager
	cfg          *config.Config
	upstreamHits *int64
}

func newPreflightFixture(t *testing.T, mutate func(cfg *config.Config)) *preflightFixture {
	t.Helper()

	tmpDir := t.TempDir()
	logger := zap.NewNop()

	sm, err := storage.NewManager(tmpDir, logger.Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { sm.Close() })

	idx, err := index.NewManager(tmpDir, logger)
	require.NoError(t, err)
	t.Cleanup(func() { idx.Close() })

	var hits int64
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstreamSrv.Close)

	cfg := config.DefaultConfig()
	cfg.DataDir = tmpDir
	if mutate != nil {
		mutate(cfg)
	}

	um := upstream.NewManager(logger, cfg, nil, secret.NewResolver(), nil)
	cm, err := cache.NewManager(sm.GetDB(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { cm.Close() })

	tr := truncate.NewTruncator(0)
	proxy := NewMCPProxyServer(sm, idx, um, cm, func() *truncate.Truncator { return tr }, logger, nil, false, cfg, nil)

	// Register the instrumented upstream so a stray connect/ListTools would be
	// visible in the hit counter rather than silently unreachable.
	require.NoError(t, um.AddServerConfig("gh", &config.ServerConfig{
		Name:     "gh",
		URL:      upstreamSrv.URL,
		Protocol: "http",
		Enabled:  true,
	}))

	return &preflightFixture{proxy: proxy, storage: sm, index: idx, cfg: cfg, upstreamHits: &hits}
}

func (f *preflightFixture) addServer(t *testing.T, sc *config.ServerConfig) {
	t.Helper()
	require.NoError(t, f.storage.SaveUpstreamServer(sc))
	// Profiles resolve their members against the live config (EffectiveServers
	// drops names that are not configured servers), so the fixture keeps the two
	// in sync exactly as the real config/storage pair does.
	f.cfg.Servers = append(f.cfg.Servers, sc)
}

func (f *preflightFixture) indexTool(t *testing.T, serverName, toolName string) {
	t.Helper()
	require.NoError(t, f.index.IndexTool(&config.ToolMetadata{
		Name:        serverName + ":" + toolName,
		ServerName:  serverName,
		Description: "fixture tool",
		ParamsJSON:  `{"type":"object"}`,
		Hash:        "hash-" + toolName,
		Created:     time.Now(),
		Updated:     time.Now(),
	}))
}

// stateSnapshot is the observable proxy state a preflight must leave untouched:
// upstream records, tool approvals, the shared index contents, the per-profile
// index directories (ForProfile would create one) and the live config.
type stateSnapshot struct {
	Servers     string
	Approvals   string
	IndexedGH   string
	IndexCount  uint64
	ProfileDirs []string
	Config      string
}

func (f *preflightFixture) snapshot(t *testing.T) stateSnapshot {
	t.Helper()

	servers, err := f.storage.ListUpstreams()
	require.NoError(t, err)
	serversJSON, err := json.Marshal(servers)
	require.NoError(t, err)

	approvals, err := f.storage.ListToolApprovals("gh")
	require.NoError(t, err)
	approvalsJSON, err := json.Marshal(approvals)
	require.NoError(t, err)

	tools, err := f.index.GetToolsByServer("gh")
	require.NoError(t, err)
	toolsJSON, err := json.Marshal(tools)
	require.NoError(t, err)

	count, err := f.index.GetDocumentCount()
	require.NoError(t, err)

	dirs, err := f.index.ExistingProfileDirs()
	require.NoError(t, err)
	sort.Strings(dirs)

	cfgJSON, err := json.Marshal(f.cfg)
	require.NoError(t, err)

	return stateSnapshot{
		Servers:     string(serversJSON),
		Approvals:   string(approvalsJSON),
		IndexedGH:   string(toolsJSON),
		IndexCount:  count,
		ProfileDirs: dirs,
		Config:      string(cfgJSON),
	}
}

func resultByID(t *testing.T, out preflight.Outcome, id string) preflight.Result {
	t.Helper()
	for _, res := range out.Results {
		if res.ID == id {
			return res
		}
	}
	t.Fatalf("no result for id %q in %+v", id, out.Results)
	return preflight.Result{}
}

// FR-006: a preflight must perform zero upstream calls AND mutate nothing —
// including the per-profile Bleve indexes, which index.Manager.ForProfile would
// lazily create (which is exactly why the glue never calls it).
func TestRunPreflightPerformsNoUpstreamIOAndNoMutation(t *testing.T) {
	fixture := newPreflightFixture(t, func(cfg *config.Config) {
		cfg.Profiles = []config.ProfileConfig{{Name: "ops", Servers: []string{"gh"}}}
	})
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	fixture.addServer(t, &config.ServerConfig{Name: "locked", Enabled: true, Quarantined: true, Protocol: "http"})
	fixture.indexTool(t, "gh", "create_issue")
	require.NoError(t, fixture.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:        "gh",
		ToolName:          "create_issue",
		Status:            storage.ToolApprovalStatusApproved,
		CurrentHash:       "abc123",
		HashSchemaVersion: 2,
	}))

	before := fixture.snapshot(t)

	out, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
		Tools: []preflight.ToolRef{
			{ID: "gh:create_issue"},
			{ID: "gh:missing_tool"},
			{ID: "locked:anything"},
			{ID: "nosuch:tool"},
			{ID: "malformed-id"},
		},
		Profile: "ops",
	})
	require.NoError(t, err)

	// Sanity: the evaluation really ran (otherwise "no mutation" is vacuous).
	require.Len(t, out.Results, 5)
	assert.Equal(t, preflight.StatusReady, resultByID(t, out, "gh:create_issue").Status)
	assert.Equal(t, preflight.ReasonNotFound, resultByID(t, out, "gh:missing_tool").Reason)
	// "locked" is outside the requested profile, so scope wins over quarantine
	// per the FR-004 precedence chain.
	assert.Equal(t, preflight.ReasonServerNotInScope, resultByID(t, out, "locked:anything").Reason)
	assert.Equal(t, preflight.ReasonServerNotConfigured, resultByID(t, out, "nosuch:tool").Reason)
	assert.Equal(t, preflight.ReasonNotFound, resultByID(t, out, "malformed-id").Reason)
	assert.Equal(t, preflight.VerdictUnknownIDs, out.Verdict)

	assert.Equal(t, int64(0), atomic.LoadInt64(fixture.upstreamHits),
		"a preflight must never touch an upstream server")

	after := fixture.snapshot(t)
	assert.Equal(t, before, after, "a preflight must not mutate runtime, index, config or approval state")
	assert.Empty(t, after.ProfileDirs, "preflight must never call ForProfile (it creates a per-profile index)")
}

// The operator tier names the scope failure; the agent-token tier must not be
// able to tell "exists but hidden" from "does not exist" (FR-013).
func TestRunPreflightScopeDisclosureByTier(t *testing.T) {
	fixture := newPreflightFixture(t, func(cfg *config.Config) {
		cfg.Profiles = []config.ProfileConfig{{Name: "ops", Servers: []string{"gh"}}}
	})
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	fixture.addServer(t, &config.ServerConfig{Name: "secret", Enabled: true, Protocol: "http"})
	fixture.indexTool(t, "gh", "create_issue")
	fixture.indexTool(t, "secret", "exfiltrate")

	operator, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
		Tools:   []preflight.ToolRef{{ID: "secret:exfiltrate"}},
		Profile: "ops",
	})
	require.NoError(t, err)
	assert.Equal(t, preflight.ReasonServerNotInScope, operator.Results[0].Reason)

	agent, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
		Tools: []preflight.ToolRef{{ID: "secret:exfiltrate"}},
		Tier:  preflight.TierAgentToken,
		// The token is scoped to gh only; no profile requested.
		TokenServers: []string{"gh"},
	})
	require.NoError(t, err)
	assert.Equal(t, preflight.ReasonNotFound, agent.Results[0].Reason)
	assert.Empty(t, agent.Results[0].Hash)
	for _, suggestion := range agent.Results[0].DidYouMean {
		assert.NotContains(t, suggestion, "secret:", "did_you_mean must never cross the scope boundary")
	}
}

// An agent token pinned to a profile cannot widen its scope by naming another
// one: the evaluation scope is the intersection (review finding 11).
func TestRunPreflightTokenPinIntersectsRequestedProfile(t *testing.T) {
	fixture := newPreflightFixture(t, func(cfg *config.Config) {
		cfg.Profiles = []config.ProfileConfig{
			{Name: "ops", Servers: []string{"gh"}},
			{Name: "wide", Servers: []string{"gh", "secret"}},
		}
	})
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	fixture.addServer(t, &config.ServerConfig{Name: "secret", Enabled: true, Protocol: "http"})
	fixture.indexTool(t, "gh", "create_issue")
	fixture.indexTool(t, "secret", "exfiltrate")

	out, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
		Tools:           []preflight.ToolRef{{ID: "gh:create_issue"}, {ID: "secret:exfiltrate"}},
		Tier:            preflight.TierAgentToken,
		TokenProfilePin: "ops",
		Profile:         "wide",
	})
	require.NoError(t, err)
	assert.Equal(t, preflight.StatusReady, resultByID(t, out, "gh:create_issue").Status)
	assert.Equal(t, preflight.ReasonNotFound, resultByID(t, out, "secret:exfiltrate").Reason)
}

func TestRunPreflightUnknownProfileIsACallerError(t *testing.T) {
	fixture := newPreflightFixture(t, nil)
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})

	_, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
		Tools:   []preflight.ToolRef{{ID: "gh:create_issue"}},
		Profile: "nope",
	})
	require.ErrorIs(t, err, preflight.ErrUnknownProfile)
}

// A degraded process must refuse rather than emit reduced-fidelity verdicts.
func TestRunPreflightRuntimeUnavailable(t *testing.T) {
	fixture := newPreflightFixture(t, nil)

	noIndex := fixture.proxy
	savedIndex := noIndex.index
	noIndex.index = nil
	t.Cleanup(func() { noIndex.index = savedIndex })
	_, err := noIndex.RunPreflight(context.Background(), preflight.Params{Tools: []preflight.ToolRef{{ID: "gh:x"}}})
	require.ErrorIs(t, err, preflight.ErrRuntimeUnavailable)

	var nilServer *Server
	_, err = nilServer.RunPreflight(context.Background(), preflight.Params{})
	require.ErrorIs(t, err, preflight.ErrRuntimeUnavailable)
}

// An unreadable upstream record is NOT the same as an absent one. Reporting a
// failed read as server_not_configured would put a storage failure into the
// reason taxonomy — the caller would "learn" a server was removed and go fix
// their config (FR-005/FR-008: infra read failures are 503, never a reason).
func TestRunPreflightStorageReadFailureIsAnErrorNotAVerdict(t *testing.T) {
	fixture := newPreflightFixture(t, nil)
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	fixture.indexTool(t, "gh", "create_issue")

	// A genuinely absent server stays a verdict...
	out, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
		Tools: []preflight.ToolRef{{ID: "nosuch:tool"}},
	})
	require.NoError(t, err)
	assert.Equal(t, preflight.ReasonServerNotConfigured, out.Results[0].Reason)

	// ...while a broken store is an error. Closing the DB is the cheapest
	// faithful stand-in for the read failures BBolt actually produces.
	require.NoError(t, fixture.storage.Close())

	_, err = fixture.proxy.RunPreflight(context.Background(), preflight.Params{
		Tools: []preflight.ToolRef{{ID: "gh:create_issue"}},
	})
	require.Error(t, err, "a failed upstream-record read must not resolve to a reason code")
	assert.Contains(t, err.Error(), "upstream record read for")
	assert.NotErrorIs(t, err, storage.ErrUpstreamNotFound)
}

// A proxy WITH a runtime but without a usable connection-state view is the
// degraded process state FR-006 names: refuse, rather than evaluate blind and
// call every tool ready. (A proxy with no runtime at all is the pure-unit
// construction the rest of this file uses, and keeps evaluating.)
func TestRunPreflightRefusesWhenTheRuntimeHasNoStateView(t *testing.T) {
	fixture := newPreflightFixture(t, nil)
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	fixture.indexTool(t, "gh", "create_issue")

	params := preflight.Params{Tools: []preflight.ToolRef{{ID: "gh:create_issue"}}}

	out, err := fixture.proxy.RunPreflight(context.Background(), params)
	require.NoError(t, err)
	require.Equal(t, preflight.StatusReady, out.Results[0].Status)

	fixture.proxy.mainServer = &Server{runtime: &internalRuntime.Runtime{}}
	t.Cleanup(func() { fixture.proxy.mainServer = nil })

	_, err = fixture.proxy.RunPreflight(context.Background(), params)
	require.ErrorIs(t, err, preflight.ErrRuntimeUnavailable)
}

// A token pinned to a profile that has since been deleted must not fall back to
// the token's own (wider) server scope: the pin is a narrowing the operator
// applied, and losing the profile it names cannot hand the agent a view it was
// never granted. It resolves to deny-all, which at this tier reads as not_found.
func TestRunPreflightStaleTokenPinDeniesRatherThanWidens(t *testing.T) {
	fixture := newPreflightFixture(t, func(cfg *config.Config) {
		cfg.Profiles = []config.ProfileConfig{{Name: "ops", Servers: []string{"gh"}}}
	})
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	fixture.addServer(t, &config.ServerConfig{Name: "secret", Enabled: true, Protocol: "http"})
	fixture.indexTool(t, "gh", "create_issue")
	fixture.indexTool(t, "secret", "exfiltrate")

	tools := []preflight.ToolRef{{ID: "gh:create_issue"}, {ID: "secret:exfiltrate"}}
	params := preflight.Params{
		Tools:           tools,
		Tier:            preflight.TierAgentToken,
		TokenProfilePin: "ops",
		TokenServers:    []string{"gh", "secret"},
	}

	// While the pinned profile exists it narrows the token to gh.
	out, err := fixture.proxy.RunPreflight(context.Background(), params)
	require.NoError(t, err)
	assert.Equal(t, preflight.StatusReady, resultByID(t, out, "gh:create_issue").Status)
	assert.Equal(t, preflight.ReasonNotFound, resultByID(t, out, "secret:exfiltrate").Reason)

	// The operator deletes the profile. The token's view must not grow.
	fixture.cfg.Profiles = nil

	out, err = fixture.proxy.RunPreflight(context.Background(), params)
	require.NoError(t, err)
	assert.Equal(t, preflight.ReasonNotFound, resultByID(t, out, "gh:create_issue").Reason,
		"a removed pin must deny, not degrade to the token's own scope")
	assert.Equal(t, preflight.ReasonNotFound, resultByID(t, out, "secret:exfiltrate").Reason)
	for _, res := range out.Results {
		assert.Empty(t, res.DidYouMean, "a deny-all scope has no visible corpus to suggest from")
	}
}

// Hash disclosure is operator-tier only, and a stale pin fails closed
// (FR-011/FR-013).
func TestRunPreflightHashDisclosureAndPins(t *testing.T) {
	fixture := newPreflightFixture(t, nil)
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	fixture.indexTool(t, "gh", "create_issue")
	require.NoError(t, fixture.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:        "gh",
		ToolName:          "create_issue",
		Status:            storage.ToolApprovalStatusApproved,
		CurrentHash:       "abc123",
		HashSchemaVersion: 2,
	}))

	operator, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
		Tools: []preflight.ToolRef{{ID: "gh:create_issue"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "sha256/v2:abc123", operator.Results[0].Hash)

	agent, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
		Tools: []preflight.ToolRef{{ID: "gh:create_issue"}},
		Tier:  preflight.TierAgentToken,
	})
	require.NoError(t, err)
	assert.Empty(t, agent.Results[0].Hash)

	stale, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
		Tools: []preflight.ToolRef{{ID: "gh:create_issue", PinHash: "sha256/v2:deadbeef"}},
	})
	require.NoError(t, err)
	assert.Equal(t, preflight.ReasonHashMismatch, stale.Results[0].Reason)
	assert.Equal(t, preflight.VerdictBlocked, stale.Verdict)
	assert.Equal(t, int64(0), atomic.LoadInt64(fixture.upstreamHits))
}

// Spec 105 FR-009 gap G3 (task T006): preflightApprovalReader.ToolApproval
// reads the approval record under the EXACT raw name only and reports "no
// record" as the implicit-approved default (nil, nil). For a raw tool whose
// approval identity was collapsed by the pre-105 discovery producer — a
// pending record filed under "erase" for "ns:erase" — preflight therefore
// answers `ready` for a:ns:erase while retrieve dispatch refuses it as pending
// (evaluateExactToolGate merges the collapsed key).
//
// FR-009: "a pending approval for ns:erase is a pending approval for
// ns:erase"; "while the quarantine gate is active for a server, 'no approval
// record' for a tool its snapshot contains is pending, never ready, at every
// gate site" — the preflight reader is one of those sites. Both layers are
// asserted: the reader seam the gap map names, and the outcome the served
// surface would return, so a fix landing at either layer is measured where it
// is observable and a fix at neither cannot pass.
func TestPreflight_CollapsedPendingRecordIsPendingNotReady(t *testing.T) {
	fixture := newPreflightFixture(t, nil)
	// trust_mode manual keeps the quarantine gate active for "a"
	// (IsQuarantineSkipped is false; DefaultConfig leaves quarantine_enabled
	// nil, which IsQuarantineEnabled reads as on).
	fixture.addServer(t, &config.ServerConfig{Name: "a", Enabled: true, Protocol: "http", TrustMode: string(config.TrustModeManual)})
	require.True(t, fixture.cfg.IsQuarantineEnabled(), "precondition: the quarantine gate is active")
	fixture.indexTool(t, "a", "erase")
	fixture.indexTool(t, "a", "ns:erase")

	// The connection snapshot "a" is Ready and contains both raw names — the
	// FR-009 wording is about a tool "its snapshot contains", so the snapshot
	// is supplied at the one seam production reads it from.
	fixture.proxy.preflightStateSource = func() (preflight.StateReader, func(serverName, toolName string) *config.ToolAnnotations, error) {
		return stubState{state: preflight.RuntimeStateReady}, func(serverName, toolName string) *config.ToolAnnotations {
			if serverName != "a" {
				return nil
			}
			switch toolName {
			case "erase":
				return &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}
			case "ns:erase":
				return &config.ToolAnnotations{DestructiveHint: boolPtr(true)}
			}
			return nil
		}, nil
	}
	t.Cleanup(func() { fixture.proxy.preflightStateSource = nil })

	// The pre-105 producer state: the raw tool "ns:erase" has its pending
	// record collapsed onto "erase", and nothing under its own name.
	require.NoError(t, fixture.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:         "a",
		ToolName:           "erase",
		Status:             storage.ToolApprovalStatusPending,
		CurrentDescription: "Erase for real",
	}))
	require.NoError(t, fixture.storage.DeleteToolApproval("a", "ns:erase"))
	_, err := fixture.storage.GetToolApproval("a", "ns:erase")
	require.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "precondition: no exact-name record for ns:erase")

	// Control (passes on HEAD): the retrieve-surface gate already reads the
	// collapsed record as a pending lock, which is the disagreement G3 names.
	gate := fixture.proxy.evaluateExactToolGate("a", "ns:erase")
	require.NotNil(t, gate.approval)
	require.Equal(t, storage.ToolApprovalStatusPending, gate.lockStatus, "control: retrieve dispatch refuses a:ns:erase as pending")

	t.Run("reader: ToolApproval for ns:erase is the pending record, not nil", func(t *testing.T) {
		reader := &preflightApprovalReader{storage: fixture.storage}
		state, err := reader.ToolApproval("a", "ns:erase")
		require.NoError(t, err)
		require.NotNil(t, state, "no exact-name record must not read as implicit-approved while the collapsed record is pending")
		assert.Equal(t, storage.ToolApprovalStatusPending, state.Status)
		assert.False(t, state.Disabled)
	})

	t.Run("outcome: a:ns:erase is tool_pending_approval, not ready", func(t *testing.T) {
		out, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
			Tools: []preflight.ToolRef{{ID: "a:ns:erase"}, {ID: "a:erase"}},
		})
		require.NoError(t, err)
		require.Len(t, out.Results, 2)

		nsErase := resultByID(t, out, "a:ns:erase")
		assert.Equal(t, preflight.StatusUnavailable, nsErase.Status, "a:ns:erase must not be ready: %+v", nsErase)
		assert.Equal(t, preflight.ReasonToolPendingApproval, nsErase.Reason)
		assert.Empty(t, nsErase.Hash, "a pending tool carries no pin")

		// The exact-name record keeps binding its own tool (control; passes
		// on HEAD).
		erase := resultByID(t, out, "a:erase")
		assert.Equal(t, preflight.StatusUnavailable, erase.Status)
		assert.Equal(t, preflight.ReasonToolPendingApproval, erase.Reason)

		assert.Equal(t, int64(0), atomic.LoadInt64(fixture.upstreamHits), "a preflight must never touch an upstream server")
	})

	// A record approved under the exact name keeps binding only its own raw
	// name (FR-009 conservative legacy rule), so approving "erase" must not
	// make ns:erase ready.
	t.Run("approving the collapsed name does not ready the namespaced tool", func(t *testing.T) {
		require.NoError(t, fixture.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
		}))
		t.Cleanup(func() {
			require.NoError(t, fixture.storage.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusPending, CurrentDescription: "Erase for real",
			}))
		})

		out, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
			Tools: []preflight.ToolRef{{ID: "a:ns:erase"}, {ID: "a:erase"}},
		})
		require.NoError(t, err)
		assert.Equal(t, preflight.StatusReady, resultByID(t, out, "a:erase").Status, "control: the exact-name approval readies its own tool")
		nsErase := resultByID(t, out, "a:ns:erase")
		assert.NotEqual(t, preflight.StatusReady, nsErase.Status,
			"a:ns:erase has no approval under its own name and must stay pending: %+v", nsErase)
	})
}
