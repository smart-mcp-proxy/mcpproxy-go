package server

// Spec 108 (Profiles v3) PR 108-bd-tests, T046a/T046b: the REST doors
// (`/api/v1/tools/call`, `/api/v1/code/exec`, `/api/v1/tool-calls/{id}/replay`)
// driven by REAL stored agent tokens pinned to a profile, through the real
// httpapi router and auth middleware.
//
// These live in package server rather than internal/httpapi because httpapi
// cannot import server (server imports httpapi): a real pinned token, a real
// CallToolDirect/ReplayToolCall and the warmed profile index can only be wired
// together from here (the consistency_crosssurface_test.go pattern).

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

const (
	restV3AdminKey   = "t108-rest-admin"
	restV3ReadonlyID = "work-readonly"
)

// restV3Fixture is the enforcement-matrix fixture (work-readonly / work-full /
// legacy over github, notion, filesystem) fronted by the real REST router.
type restV3Fixture struct {
	t         *testing.T
	proxy     *MCPProxyServer
	rt        *runtime.Runtime
	srv       *Server
	api       *httpapi.Server
	router    http.Handler
	cfg       *config.Config
	upstreams map[string]*countingUpstream
	hmacKey   []byte
}

// newProfilesV3RESTFixture builds the fixture and starts the activity service
// so refusals persist as activity rows. configure overrides the config on top
// of the fixture profiles.
func newProfilesV3RESTFixture(t *testing.T, configure func(*config.Config)) *restV3Fixture {
	t.Helper()
	proxy, rt, upstreams := newProfilesV3FixtureUpstreams(t, func(cfg *config.Config) {
		cfg.APIKey = restV3AdminKey
		if configure != nil {
			configure(cfg)
		}
	})
	// The Server that already holds the warmed profile indexes is also the
	// REST controller, so REST dispatch resolves profiles the way production
	// does (one Server, one proxy).
	srv := proxy.mainServer
	srv.mcpProxy = proxy

	api := httpapi.NewServer(srv, zap.NewNop().Sugar(), nil)
	api.SetTokenStore(rt.StorageManager(), rt.Config().DataDir)

	hmacKey, err := auth.GetOrCreateHMACKey(rt.Config().DataDir)
	require.NoError(t, err)

	go rt.ActivityService().Start(rt.AppContext(), rt)
	require.Eventually(t, rt.ActivityService().Started, 5*time.Second, time.Millisecond,
		"activity service must subscribe before the first refusal is emitted")

	return &restV3Fixture{
		t: t, proxy: proxy, rt: rt, srv: srv, api: api, router: api.Router(),
		cfg: rt.Config(), upstreams: upstreams, hmacKey: hmacKey,
	}
}

// mint stores an agent token (all tiers, every server) pinned to pin and
// returns its raw secret. An empty pin mints an unpinned token.
func (f *restV3Fixture) mint(name, pin string) string {
	f.t.Helper()
	return f.mintWith(name, pin, []string{"*"}, []string{auth.PermRead, auth.PermWrite, auth.PermDestructive})
}

func (f *restV3Fixture) mintWith(name, pin string, allowed, perms []string) string {
	f.t.Helper()
	raw, err := auth.GenerateToken()
	require.NoError(f.t, err)
	require.NoError(f.t, f.rt.StorageManager().CreateAgentToken(auth.AgentToken{
		Name:           name,
		AllowedServers: allowed,
		Permissions:    perms,
		ProfilePin:     pin,
		ExpiresAt:      time.Now().Add(time.Hour),
	}, raw, f.hmacKey))
	return raw
}

// mintClient stores a real mcp_cli_ client credential bound to (mode, pin).
func (f *restV3Fixture) mintClient(clientID, pin, mode string) string {
	f.t.Helper()
	raw, err := auth.GenerateClientToken()
	require.NoError(f.t, err)
	_, err = f.rt.StorageManager().MintClientCredential(clientID, raw, f.hmacKey, mode, pin, time.Now().Add(time.Hour))
	require.NoError(f.t, err)
	return raw
}

// do drives the REST router. key is sent as X-API-Key, reqID (when set) as
// X-Request-Id so two requests can be compared byte-for-byte.
func (f *restV3Fixture) do(method, path, key string, body interface{}, reqID string) *httptest.ResponseRecorder {
	f.t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		require.NoError(f.t, err)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	if reqID != "" {
		req.Header.Set("X-Request-Id", reqID)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *restV3Fixture) callTool(key, tool string, args map[string]interface{}, reqID string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.do(http.MethodPost, "/api/v1/tools/call", key, map[string]interface{}{"tool_name": tool, "arguments": args}, reqID)
}

// restV3Error decodes the shared {"success":false,"error":...} envelope.
func restV3Error(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	require.False(t, body.Success, rec.Body.String())
	return body.Error
}

// activities lists every persisted activity row, oldest first is not
// guaranteed: callers filter.
func (f *restV3Fixture) activities() []*storage.ActivityRecord {
	f.t.Helper()
	records, _, err := f.rt.StorageManager().ListActivities(storage.ActivityFilter{Limit: 100})
	require.NoError(f.t, err)
	return records
}

// waitBlocked waits for a blocked policy_decision row on (server, tool) whose
// block_reason is want ("" = the key must be absent) and returns it.
func (f *restV3Fixture) waitBlocked(server, tool string, want profile.BlockReason) *storage.ActivityRecord {
	f.t.Helper()
	var found *storage.ActivityRecord
	require.Eventually(f.t, func() bool {
		for _, r := range f.activities() {
			if r.Type != storage.ActivityTypePolicyDecision || r.ServerName != server || r.ToolName != tool || r.Status != "blocked" {
				continue
			}
			got, _ := r.Metadata[storage.MetadataKeyBlockReason].(string)
			if got == string(want) {
				found = r
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "blocked policy_decision row %s:%s block_reason=%q", server, tool, want)
	return found
}

// settle waits until every activity event published so far has been written,
// so a "no row" assertion is not vacuous. It emits a marker refusal (a write
// tool under a work-readonly pin) and waits for its row: the activity service
// consumes one ordered event stream, so everything queued before the marker
// is persisted once the marker is.
func (f *restV3Fixture) settle() {
	f.t.Helper()
	marker := f.mint("settle-"+time.Now().Format("150405.000000000"), restV3ReadonlyID)
	before := f.countBlocked("github", "create_issue")
	rec := f.callTool(marker, "call_tool_write", map[string]interface{}{"name": "github:create_issue", "args_json": "{}"}, "")
	require.Equal(f.t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Eventually(f.t, func() bool { return f.countBlocked("github", "create_issue") > before },
		5*time.Second, 10*time.Millisecond, "settle marker row must be persisted")
}

func (f *restV3Fixture) countBlocked(server, tool string) int {
	n := 0
	for _, r := range f.activities() {
		if r.Type == storage.ActivityTypePolicyDecision && r.ServerName == server && r.ToolName == tool && r.Status == "blocked" {
			n++
		}
	}
	return n
}

// rowsFor returns the persisted policy_decision rows for one tool name
// (any server, including the server-less "").
func (f *restV3Fixture) rowsFor(tool string) []*storage.ActivityRecord {
	var out []*storage.ActivityRecord
	for _, r := range f.activities() {
		if r.Type == storage.ActivityTypePolicyDecision && r.ToolName == tool {
			out = append(out, r)
		}
	}
	return out
}

func (f *restV3Fixture) totalDispatched() int {
	n := 0
	for _, up := range f.upstreams {
		n += len(up.dispatched())
	}
	return n
}

// restV3ReplaceToolName swaps the tool name in an error so two refusals can be
// compared for uniform bytes (FR-018: a hidden built-in answers exactly like a
// tool that does not exist).
func restV3ReplaceToolName(s, from, to string) string { return strings.ReplaceAll(s, from, to) }

// ---------------------------------------------------------------------------
// T046a — REST /tools/call with a real work-readonly-pinned agent token
// ---------------------------------------------------------------------------

func TestRESTProfileV3_ToolsCall_PinnedAgentToken(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	indexEnforcementMatrixFixtureTools(t, f.proxy)
	ro := f.mint("ro-bot", restV3ReadonlyID)
	wf := f.mint("wf-bot", "work-full")

	t.Run("tier refusal is 403 with the contract text, before upstream I/O", func(t *testing.T) {
		rec := f.callTool(ro, "call_tool_write", map[string]interface{}{"name": "github:create_issue", "args_json": "{}"}, "")
		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		require.Equal(t, v3TierRefusal(t), restV3Error(t, rec))
		// Spec 108 D39: the pinned token's own profile is named.
		require.Contains(t, rec.Body.String(), "work-readonly")
		require.Empty(t, f.upstreams["github"].dispatched())
		row := f.waitBlocked("github", "create_issue", profile.BlockReasonTier)
		require.Equal(t, v3TierRefusal(t), row.Metadata["reason"], "the persisted refusal text is the caller-visible one")
	})

	t.Run("hidden built-ins answer exactly like an unknown tool and record profile_* reasons", func(t *testing.T) {
		const reqID = "rest-v3-uniform-refusal"
		unknown := f.callTool(ro, "no_such_tool", map[string]interface{}{}, reqID)
		require.Equal(t, http.StatusInternalServerError, unknown.Code, unknown.Body.String())
		unknownBody := unknown.Body.String()

		for _, tc := range []struct {
			tool   string
			args   map[string]interface{}
			reason profile.BlockReason
		}{
			{"upstream_servers", map[string]interface{}{"operation": "list"}, profile.BlockReasonManagement},
			{"quarantine_security", map[string]interface{}{"operation": "list_quarantined"}, profile.BlockReasonManagement},
			{"code_execution", map[string]interface{}{"code": "1"}, profile.BlockReasonCodeExecution},
		} {
			t.Run(tc.tool, func(t *testing.T) {
				rec := f.callTool(ro, tc.tool, tc.args, reqID)
				require.Equal(t, unknown.Code, rec.Code, rec.Body.String())
				require.Equal(t, unknownBody, restV3ReplaceToolName(rec.Body.String(), tc.tool, "no_such_tool"),
					"a profile-hidden %s must be byte-identical to an unknown tool once the name is substituted", tc.tool)
				row := f.waitBlocked("", tc.tool, tc.reason)
				if tc.reason == profile.BlockReasonManagement {
					require.Equal(t, "unknown tool: "+tc.tool, row.Metadata["reason"],
						"the row carries the non-disclosing text the caller already received")
				}
			})
		}
		require.Empty(t, f.totalDispatched(), "hidden built-ins must never reach an upstream")
	})

	t.Run("retrieve_tools matches the direct handler and names the pinned token's own profile (pin source)", func(t *testing.T) {
		rec := f.callTool(ro, "retrieve_tools", map[string]interface{}{"query": "create_issue"}, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var envelope struct {
			Success bool `json:"success"`
			Data    []struct {
				Text string `json:"text"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), rec.Body.String())
		require.True(t, envelope.Success)
		require.NotEmpty(t, envelope.Data)

		direct := callRetrieveToolsV3Raw(t, f.proxy, pinnedProfileCtx(restV3ReadonlyID), "create_issue")
		require.JSONEq(t, direct, envelope.Data[0].Text, "REST retrieve_tools must equal the direct handler under the same pin")

		var payload map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(envelope.Data[0].Text), &payload))
		require.JSONEq(t, "1", string(payload["hidden_by_profile"]))
		require.JSONEq(t, `"work-readonly"`, string(payload["profile"]), "a pinned caller learns its own profile in retrieve_tools (FR-011, Spec 108 D39)")
		require.NotContains(t, envelope.Data[0].Text, "github:create_issue", "create_issue is over the tier cap and must not be a hit")
	})

	t.Run("work-full pin admits the write and keeps pre-108 management visibility", func(t *testing.T) {
		rec := f.callTool(wf, "call_tool_write", map[string]interface{}{"name": "github:create_issue", "args_json": "{}"}, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, []string{"create_issue"}, f.upstreams["github"].dispatched())

		list := f.callTool(wf, "upstream_servers", map[string]interface{}{"operation": "list"}, "")
		require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	})

	t.Run("admin API key keeps today's outcomes", func(t *testing.T) {
		created := len(f.upstreams["github"].dispatched())
		write := f.callTool(restV3AdminKey, "call_tool_write", map[string]interface{}{"name": "github:create_issue", "args_json": "{}"}, "")
		require.Equal(t, http.StatusOK, write.Code, write.Body.String())
		require.Len(t, f.upstreams["github"].dispatched(), created+1)
		list := f.callTool(restV3AdminKey, "upstream_servers", map[string]interface{}{"operation": "list"}, "")
		require.Equal(t, http.StatusOK, list.Code, list.Body.String())
		quarantine := f.callTool(restV3AdminKey, "quarantine_security", map[string]interface{}{"operation": "list_quarantined"}, "")
		require.Equal(t, http.StatusOK, quarantine.Code, quarantine.Body.String())
	})
}

// TestRESTProfileV3_ClientCredentialRefusedOnREST: a real mcp_cli_ credential
// is valid on MCP endpoints only (FR-023), on every REST dispatch door, with no
// upstream dispatch and no activity row.
func TestRESTProfileV3_ClientCredentialRefusedOnREST(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	cli := f.mintClient("cursor", restV3ReadonlyID, auth.ProfileModeLocked)
	f.settle()
	rowsBefore := len(f.activities())

	for _, tc := range []struct {
		name, path string
		body       interface{}
	}{
		{"tools/call", "/api/v1/tools/call", map[string]interface{}{"tool_name": "retrieve_tools", "arguments": map[string]interface{}{"query": "issue"}}},
		{"code/exec", "/api/v1/code/exec", map[string]interface{}{"code": "1+1"}},
		{"tool-calls/replay", "/api/v1/tool-calls/x/replay", map[string]interface{}{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(http.MethodPost, tc.path, cli, tc.body, "")
			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			require.Equal(t, "client credentials are valid on MCP endpoints only", restV3Error(t, rec))
		})
	}
	require.Empty(t, f.totalDispatched())
	f.settle()
	require.Equal(t, rowsBefore+1, len(f.activities()), "a refused client credential writes no activity row (only the settle marker is new)")
}

// callRetrieveToolsV3Raw returns the raw retrieve_tools JSON text under ctx.
func callRetrieveToolsV3Raw(t *testing.T, proxy *MCPProxyServer, ctx context.Context, query string) string {
	t.Helper()
	req := quarantineRequest(map[string]interface{}{"query": query})
	result, err := proxy.handleRetrieveTools(ctx, req)
	require.NoError(t, err)
	require.False(t, result.IsError, "retrieve_tools returned an error: %v", result.Content)
	return resultText(t, result)
}

// ---------------------------------------------------------------------------
// T046b — REST /code/exec and /tool-calls/{id}/replay with real pinned tokens
// ---------------------------------------------------------------------------

// restV3CodeExec decodes the /code/exec envelope.
type restV3CodeExec struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (f *restV3Fixture) codeExec(key string) (int, restV3CodeExec) {
	f.t.Helper()
	rec := f.do(http.MethodPost, "/api/v1/code/exec", key, map[string]interface{}{"code": "1+1"}, "")
	var out restV3CodeExec
	require.NoError(f.t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return rec.Code, out
}

func TestRESTProfileV3_CodeExec_PinnedAgentToken(t *testing.T) {
	const blocked = "blocked by profile: code execution is disabled for this profile"

	t.Run("global flag on", func(t *testing.T) {
		f := newProfilesV3RESTFixture(t, func(cfg *config.Config) { cfg.EnableCodeExecution = true })
		ro, wf := f.mint("ro-bot", restV3ReadonlyID), f.mint("wf-bot", "work-full")

		status, out := f.codeExec(ro)
		require.Equal(t, http.StatusForbidden, status, "a profile refusal is a 403, not a 500 EXECUTION_FAILED")
		require.False(t, out.OK)
		require.NotNil(t, out.Error)
		require.Equal(t, "PROFILE_BLOCKED", out.Error.Code)
		require.Equal(t, blocked, out.Error.Message)
		f.waitBlocked("", "code_execution", profile.BlockReasonCodeExecution)
		require.Empty(t, f.totalDispatched(), "a refused /code/exec must not touch any upstream")

		status, out = f.codeExec(wf)
		require.Equal(t, http.StatusOK, status)
		require.True(t, out.OK)
		require.Contains(t, string(out.Result), "2")

		status, out = f.codeExec(restV3AdminKey)
		require.Equal(t, http.StatusOK, status, "the administrator is unchanged")
		require.True(t, out.OK)
	})

	t.Run("global flag off: the profile refusal still wins for the pinned read-only token", func(t *testing.T) {
		f := newProfilesV3RESTFixture(t, func(cfg *config.Config) { cfg.EnableCodeExecution = false })
		ro, wf := f.mint("ro-bot", restV3ReadonlyID), f.mint("wf-bot", "work-full")

		status, out := f.codeExec(ro)
		require.Equal(t, http.StatusForbidden, status)
		require.NotNil(t, out.Error)
		require.Equal(t, "PROFILE_BLOCKED", out.Error.Code, "the profile wins over the global gate")
		require.Equal(t, blocked, out.Error.Message)

		status, out = f.codeExec(wf)
		require.Equal(t, http.StatusForbidden, status)
		require.NotNil(t, out.Error)
		require.Equal(t, "FEATURE_DISABLED", out.Error.Code, "control: a permitted profile meets the global gate")
	})
}

// restV3Replay holds two replayable records seeded against the runtime's own
// upstream manager: create_issue on github (a write, inside work-readonly's
// server scope) and read_text_file on filesystem (outside it).
type restV3Replay struct {
	writeID, fsID string
	github, fs    *upstreamCalls
}

func seedRESTReplayRecords(t *testing.T, f *restV3Fixture) restV3Replay {
	t.Helper()
	ghURL, ghCalls := startRuntimeCountingUpstream(t, f.proxy, "github", "create_issue")
	fsURL, fsCalls := startRuntimeCountingUpstream(t, f.proxy, "filesystem", "read_text_file")
	writeID := seedReplayableCallWithID(t, f.proxy, f.srv, "replay-write-1", "github", "create_issue", ghURL,
		&config.ToolAnnotations{ReadOnlyHint: boolPtr(false)})
	fsID := seedReplayableCallWithID(t, f.proxy, f.srv, "replay-fs-1", "filesystem", "read_text_file", fsURL,
		&config.ToolAnnotations{ReadOnlyHint: boolPtr(true)})
	require.Eventually(t, func() bool {
		gh, ok1 := f.rt.UpstreamManager().GetClient("github")
		fs, ok2 := f.rt.UpstreamManager().GetClient("filesystem")
		return ok1 && ok2 && gh != nil && fs != nil && gh.IsConnected() && fs.IsConnected()
	}, 10*time.Second, 20*time.Millisecond, "the runtime's replay upstreams must connect")
	return restV3Replay{writeID: writeID, fsID: fsID, github: ghCalls, fs: fsCalls}
}

func TestRESTProfileV3_Replay_PinnedAgentToken(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	ids := seedRESTReplayRecords(t, f)
	ro, wf := f.mint("ro-bot", restV3ReadonlyID), f.mint("wf-bot", "work-full")
	replay := func(key, id, reqID string) *httptest.ResponseRecorder {
		return f.do(http.MethodPost, "/api/v1/tool-calls/"+id+"/replay", key, map[string]interface{}{}, reqID)
	}

	t.Run("in-scope write over the tier cap is a 403 with the tier text", func(t *testing.T) {
		rec := replay(ro, ids.writeID, "")
		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		require.Equal(t, v3TierRefusal(t), restV3Error(t, rec))
		require.Contains(t, rec.Body.String(), "work-readonly")
		require.Empty(t, ids.github.dispatched(), "a refused replay must not reach the upstream")
		f.waitBlocked("github", "create_issue", profile.BlockReasonTier)
	})

	t.Run("out-of-profile server is byte-identical to an unknown id", func(t *testing.T) {
		const reqID = "rest-v3-replay-uniform"
		out := replay(ro, ids.fsID, reqID)
		unknown := replay(ro, "no-such-id", reqID)
		require.Equal(t, http.StatusNotFound, out.Code, out.Body.String())
		require.Equal(t, unknown.Code, out.Code)
		require.Equal(t, unknown.Body.String(), out.Body.String(),
			"an out-of-profile record and an unknown id must be one response (FR-015)")
		require.Empty(t, ids.fs.dispatched())

		// D1: the out-of-profile replay writes the Spec 105-shaped row
		// (status=blocked, the non-disclosing 404 reason, NO block_reason),
		// never a profile_* one; an unknown id writes no row at all.
		row := f.waitBlocked("filesystem", "read_text_file", "")
		require.Equal(t, profile.ErrToolOutsideProfile.Error(), row.Metadata["reason"])
		f.settle()
		for _, r := range f.activities() {
			if r.Type != storage.ActivityTypePolicyDecision || r.ServerName != "filesystem" {
				continue
			}
			got, _ := r.Metadata[storage.MetadataKeyBlockReason].(string)
			require.Empty(t, got, "an out-of-scope replay never records a profile_* block reason")
		}
		require.Empty(t, f.rowsFor("no-such-id"), "an unknown id writes no activity row")
	})

	t.Run("work-full pin replays the write", func(t *testing.T) {
		rec := replay(wf, ids.writeID, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, []string{"create_issue"}, ids.github.dispatched())
	})

	t.Run("admin key replays the write", func(t *testing.T) {
		rec := replay(restV3AdminKey, ids.writeID, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Len(t, ids.github.dispatched(), 2)
	})
}
