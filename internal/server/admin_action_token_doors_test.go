package server

// Spec 133 PR-1a door table test (FR-023): every agent-token door, driven
// through the real REST router and the real MCP `credentials` tool of an
// in-process daemon, writes exactly the admin_action lines the contract
// names, with the caller, source, client surface and request id of the door
// it came through, schema-valid, and nothing for a refusal or a no-op.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

const adminE2EAPIKey = "test-api-key-admin-action-0123456789abcdef"

type adminE2E struct {
	t    *testing.T
	env  *TestEnvironment
	sink *recordingAuditSink
	base string
}

func newAdminE2E(t *testing.T, extra ...func(*config.Config)) *adminE2E {
	t.Helper()
	e := &adminE2E{t: t, sink: &recordingAuditSink{}}
	e.env = NewTestEnvironmentWithOptions(t, TestEnvironmentOptions{
		Mutate: func(cfg *config.Config, _ string) {
			cfg.APIKey = adminE2EAPIKey
			cfg.RequireMCPAuth = true
			cfg.Profiles = append(cfg.Profiles, config.ProfileConfig{Name: "ro", Servers: []string{"*"}})
			for _, f := range extra {
				f(cfg)
			}
		},
		ServerOptions: []ServerOption{WithAuditSink(e.sink)},
	})
	t.Cleanup(e.env.Cleanup)
	e.base = strings.TrimSuffix(e.env.proxyAddr, "/mcp")
	return e
}

func (e *adminE2E) rest(method, path, requestID string, body any) (int, []byte) {
	e.t.Helper()
	return e.restAs("cli/0.77.0", method, path, requestID, body)
}

// restAs sends the request with the given X-MCPProxy-Client value.
func (e *adminE2E) restAs(client, method, path, requestID string, body any) (int, []byte) {
	e.t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(e.t, err)
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.base+path, rdr)
	require.NoError(e.t, err)
	req.Header.Set("X-API-Key", adminE2EAPIKey)
	req.Header.Set("X-MCPProxy-Client", client)
	if requestID != "" {
		req.Header.Set("X-Request-Id", requestID)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(e.t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// adminLines returns the admin_action lines written since the last call,
// each validated against the contract schema and the forbidden-key rule.
func (e *adminE2E) adminLines(seen *int) []map[string]interface{} {
	e.t.Helper()
	all := e.sink.decoded(e.t)
	// The recording sink does not sanitize: run the production whole-line
	// pass here so a builder leak is caught (it must be the identity).
	for i, raw := range e.sink.snapshot()[*seen:] {
		if out, hit := audit.SanitizeLine(raw); hit || !bytes.Equal(out, raw) {
			e.t.Fatalf("line %d: whole-line sanitizer fired on builder output: %s", *seen+i, raw)
		}
	}
	var out []map[string]interface{}
	for _, l := range all[*seen:] {
		if l["event"] == "admin_action" {
			out = append(out, l)
		}
	}
	*seen = len(all)
	return out
}

func (e *adminE2E) activityRequestIDs(requestID string) int {
	e.t.Helper()
	sm := e.env.proxyServer.runtime.StorageManager()
	rows, _, err := sm.ListActivities(storage.ActivityFilter{Types: []string{string(storage.ActivityTypeProfileChange)}, RequestID: requestID, Limit: 100})
	require.NoError(e.t, err)
	return len(rows)
}

func TestAdminActionDoors_RESTTokenDoors(t *testing.T) {
	e := newAdminE2E(t)
	seen := 0

	// POST /tokens → token.create
	code, body := e.rest(http.MethodPost, "/api/v1/tokens", "req-create-1", map[string]any{"name": "ci-bot", "expires_in": "1h"})
	require.Equal(t, http.StatusCreated, code, string(body))
	var created struct {
		Data struct {
			Token       string `json:"token"`
			TokenPrefix string `json:"token_prefix"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	lines := e.adminLines(&seen)
	require.Len(t, lines, 1, "token.create: exactly one line")
	l := lines[0]
	require.Equal(t, "token.create", l["action"])
	require.Equal(t, "api", l["source"])
	require.Equal(t, "local", l["origin"])
	require.Equal(t, map[string]interface{}{"kind": "api_key"}, l["caller"])
	require.Equal(t, "req-create-1", l["request_id"])
	require.Equal(t, 1, e.activityRequestIDs("req-create-1"), "the line joins the activity row by request_id")
	client := l["client"].(map[string]interface{})
	require.Equal(t, "cli", client["surface"])
	require.Equal(t, "cli/0.77.0", client["name"])
	tgt := l["target"].(map[string]interface{})
	require.Equal(t, "agent_token", tgt["kind"])
	require.Equal(t, "ci-bot", tgt["name"])
	ref := tgt["ref"].(string)
	raw, _ := json.Marshal(l)
	if created.Data.Token != "" {
		require.NotContains(t, string(raw), created.Data.Token, "the secret never reaches the line")
	}

	// POST /tokens/{name}/regenerate → token.rotate with both prefixes.
	code, body = e.rest(http.MethodPost, "/api/v1/tokens/ci-bot/regenerate", "req-rotate-1", nil)
	require.Equal(t, http.StatusOK, code, string(body))
	lines = e.adminLines(&seen)
	require.Len(t, lines, 1)
	require.Equal(t, "token.rotate", lines[0]["action"])
	require.Equal(t, ref, lines[0]["target"].(map[string]interface{})["ref"], "rotation keeps the ref")
	require.Equal(t, "ci-bot", lines[0]["target"].(map[string]interface{})["name"])
	require.Equal(t, "req-rotate-1", lines[0]["request_id"])
	require.Equal(t, map[string]interface{}{"kind": "api_key"}, lines[0]["caller"], "TCP with the API key is api_key, not socket")
	require.Equal(t, "local", lines[0]["origin"])
	require.Equal(t, "api", lines[0]["source"])
	require.Equal(t, "cli", lines[0]["client"].(map[string]interface{})["surface"], "the CLI client header classifies the surface")
	ch := lines[0]["changes"].([]interface{})[0].(map[string]interface{})
	require.Equal(t, "/token_prefix", ch["field"])
	createdPrefix := l["changes"].([]interface{})[0].(map[string]interface{})["after"]
	for _, c := range l["changes"].([]interface{}) {
		if m := c.(map[string]interface{}); m["field"] == "/token_prefix" {
			createdPrefix = m["after"]
		}
	}
	require.Equal(t, createdPrefix, ch["before"], "before is the prefix the create line recorded")
	var rotated struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &rotated))
	require.NotEmpty(t, rotated.Data.Token, "regenerate returns the new secret once")
	require.Equal(t, auth.TokenPrefix(rotated.Data.Token), ch["after"], "after is the display prefix of the secret the regenerate returned")
	require.Equal(t, 1, e.activityRequestIDs("req-rotate-1"))

	// DELETE /tokens/{name} → token.revoke; again → no line.
	code, _ = e.rest(http.MethodDelete, "/api/v1/tokens/ci-bot", "req-revoke-1", nil)
	require.Equal(t, http.StatusNoContent, code)
	lines = e.adminLines(&seen)
	require.Len(t, lines, 1)
	require.Equal(t, "token.revoke", lines[0]["action"])
	require.Equal(t, "req-revoke-1", lines[0]["request_id"])
	require.Equal(t, "api", lines[0]["source"])
	require.Equal(t, "local", lines[0]["origin"])
	require.Equal(t, "cli", lines[0]["client"].(map[string]interface{})["surface"])
	require.Equal(t, "ci-bot", lines[0]["target"].(map[string]interface{})["name"])
	require.Equal(t, map[string]interface{}{"kind": "api_key"}, lines[0]["caller"])
	require.Equal(t, ref, lines[0]["target"].(map[string]interface{})["ref"])
	require.Equal(t, 1, e.activityRequestIDs("req-revoke-1"))
	code, _ = e.rest(http.MethodDelete, "/api/v1/tokens/ci-bot", "req-revoke-2", nil)
	require.Equal(t, http.StatusNoContent, code)
	require.Empty(t, e.adminLines(&seen), "an already revoked token writes no line")

	// Regenerate a revoked token: 409, no line.
	code, _ = e.rest(http.MethodPost, "/api/v1/tokens/ci-bot/regenerate", "", nil)
	require.Equal(t, http.StatusConflict, code)
	require.Empty(t, e.adminLines(&seen))

	// DELETE /tokens/{name}/permanent → token.delete; again → 404, no line.
	code, _ = e.rest(http.MethodDelete, "/api/v1/tokens/ci-bot/permanent", "req-delete-1", nil)
	require.Equal(t, http.StatusNoContent, code)
	lines = e.adminLines(&seen)
	require.Len(t, lines, 1)
	require.Equal(t, "token.delete", lines[0]["action"])
	require.Equal(t, ref, lines[0]["target"].(map[string]interface{})["ref"])
	require.Equal(t, "req-delete-1", lines[0]["request_id"])
	require.Equal(t, "agent_token", lines[0]["target"].(map[string]interface{})["kind"])
	require.Equal(t, "api", lines[0]["source"])
	require.Equal(t, "local", lines[0]["origin"])
	require.Equal(t, "cli", lines[0]["client"].(map[string]interface{})["surface"])
	require.Equal(t, "ci-bot", lines[0]["target"].(map[string]interface{})["name"])
	require.Equal(t, map[string]interface{}{"kind": "api_key"}, lines[0]["caller"])
	code, _ = e.rest(http.MethodDelete, "/api/v1/tokens/ci-bot/permanent", "", nil)
	require.Equal(t, http.StatusNotFound, code)
	require.Empty(t, e.adminLines(&seen))

	// A refused create (invalid name) writes nothing.
	code, _ = e.rest(http.MethodPost, "/api/v1/tokens", "", map[string]any{"name": "bad name"})
	require.Equal(t, http.StatusBadRequest, code)
	require.Empty(t, e.adminLines(&seen))

	// A credential-shaped X-Request-Id is rejected by the REST id rule (it
	// carries no '_'-free shape issue) or masked: either way never verbatim.
	pat := "github_pat_11ABCDEFGHIJKLMNOPQRSTUV_0123456789"
	code, body = e.rest(http.MethodPost, "/api/v1/tokens", pat, map[string]any{"name": "masked-id", "expires_in": "1h"})
	require.Equal(t, http.StatusCreated, code, string(body))
	lines = e.adminLines(&seen)
	require.Len(t, lines, 1)
	raw, _ = json.Marshal(lines[0])
	require.NotContains(t, string(raw), pat)
	require.Equal(t, true, lines[0]["request_id_masked"])
	// The masked id keeps the mask's fixed shape (first four and last two
	// characters), and the activity row keeps the original: only this
	// line's join is lost.
	require.Equal(t, pat[:4]+"***"+pat[len(pat)-2:], lines[0]["request_id"])
	require.Equal(t, 1, e.activityRequestIDs(pat), "the activity row carries the original id")

}

func TestAdminActionDoors_MCPCredentialsTool(t *testing.T) {
	e := newAdminE2E(t)
	seen := 0
	tr, err := transport.NewStreamableHTTP(e.base+"/mcp", transport.WithHTTPHeaders(map[string]string{"X-API-Key": adminE2EAPIKey}))
	require.NoError(t, err)
	c := client.NewClient(tr)
	e.env.ConnectClient(c)
	t.Cleanup(func() { _ = c.Close() })

	res, err := c.CallTool(context.Background(), callReq("credentials", map[string]any{
		"operation": "create_token", "name": "worker", "profile": "ro", "expires_in": "1h",
	}))
	require.NoError(t, err)
	require.False(t, res.IsError, getToolResultText(res))
	lines := e.adminLines(&seen)
	require.Len(t, lines, 1)
	l := lines[0]
	require.Equal(t, "token.create", l["action"])
	require.Equal(t, "mcp", l["source"])
	require.Equal(t, "mcp", l["client"].(map[string]interface{})["surface"])
	require.NotEmpty(t, l["client"].(map[string]interface{})["name"], "the MCP session's clientInfo name, as on authz lines")
	rid, _ := l["request_id"].(string)
	require.NotEmpty(t, rid, "an MCP admin line carries the wrapper's request id")
	// The wrapper row and the profile_change row both carry the same id.
	sm := e.env.proxyServer.runtime.StorageManager()
	// The wrapper row is written asynchronously; the profile_change row
	// synchronously. Wait for both.
	require.Eventually(t, func() bool {
		rows, _, err := sm.ListActivities(storage.ActivityFilter{RequestID: rid, Limit: 100})
		if err != nil {
			return false
		}
		types := map[string]int{}
		wrapper := 0
		for _, r := range rows {
			types[string(r.Type)]++
			if r.Type == storage.ActivityTypeInternalToolCall && r.ToolName == "credentials" {
				wrapper++
			}
		}
		// The second row is the credentials wrapper row itself, not any
		// other row that happens to carry the id (round 25).
		return types[string(storage.ActivityTypeProfileChange)] == 1 && wrapper == 1
	}, 10*time.Second, 50*time.Millisecond, "wrapper row and profile_change row share the line's request id")

	require.Equal(t, "worker", l["target"].(map[string]interface{})["name"])
	require.Equal(t, "local", l["origin"], "a TCP MCP mint enters locally, not over the socket")
	// The prefix on the line is the display prefix of the credential the
	// tool returned.
	var minted map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(getToolResultText(res)), &minted))
	secret, _ := minted["credential"].(string)
	require.NotEmpty(t, secret, "create_token returns the credential once")
	wantPrefix := auth.TokenPrefix(secret)
	prefixOf := func(line map[string]interface{}, side string) interface{} {
		for _, c := range line["changes"].([]interface{}) {
			if m := c.(map[string]interface{}); m["field"] == "/token_prefix" {
				return m[side]
			}
		}
		return nil
	}
	require.Equal(t, wantPrefix, prefixOf(l, "after"))
	createRef := l["target"].(map[string]interface{})["ref"]
	require.Equal(t, map[string]interface{}{"kind": "api_key"}, l["caller"])

	res, err = c.CallTool(context.Background(), callReq("credentials", map[string]any{"operation": "revoke", "token": "worker"}))
	require.NoError(t, err)
	require.False(t, res.IsError, getToolResultText(res))
	lines = e.adminLines(&seen)
	require.Len(t, lines, 1)
	r := lines[0]
	require.Equal(t, "token.revoke", r["action"])
	require.Equal(t, map[string]interface{}{"kind": "api_key"}, r["caller"])
	require.Equal(t, "mcp", r["source"])
	require.Equal(t, "local", r["origin"])
	require.Equal(t, "mcp", r["client"].(map[string]interface{})["surface"])
	require.Equal(t, createRef, r["target"].(map[string]interface{})["ref"], "same token, same ref")
	require.Equal(t, "worker", r["target"].(map[string]interface{})["name"])
	require.Equal(t, wantPrefix, prefixOf(r, "before"), "the revoked key is the one minted")
	revokeID, _ := r["request_id"].(string)
	require.NotEmpty(t, revokeID)
	require.NotEqual(t, rid, revokeID, "each MCP call has its own request id")
	require.Eventually(t, func() bool {
		rows, _, err := sm.ListActivities(storage.ActivityFilter{RequestID: revokeID, Limit: 100})
		if err != nil {
			return false
		}
		types := map[string]int{}
		wrapper := 0
		for _, r := range rows {
			types[string(r.Type)]++
			if r.Type == storage.ActivityTypeInternalToolCall && r.ToolName == "credentials" {
				wrapper++
			}
		}
		// The second row is the credentials wrapper row itself, not any
		// other row that happens to carry the id (round 25).
		return types[string(storage.ActivityTypeProfileChange)] == 1 && wrapper == 1
	}, 10*time.Second, 50*time.Millisecond, "the revoke line joins its wrapper and profile_change rows")

	// A retried revoke of the already-revoked token writes nothing.
	res, err = c.CallTool(context.Background(), callReq("credentials", map[string]any{"operation": "revoke", "token": "worker"}))
	require.NoError(t, err)
	require.Empty(t, e.adminLines(&seen), "a no-op revoke writes no line (error=%v)", res.IsError)

	// A refused operation (unknown token) writes nothing.
	res, err = c.CallTool(context.Background(), callReq("credentials", map[string]any{"operation": "revoke", "token": "nope"}))
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Empty(t, e.adminLines(&seen))
}

// TestAdminActionDoors_SaturatedBus pins SC-003 at the token-revocation scale
// this slice owns: with the event bus saturated (nobody draining it), every
// committed revocation still writes its line — the line never goes through
// the bus.
func TestAdminActionDoors_SaturatedBus(t *testing.T) {
	if testing.Short() {
		t.Skip("bus saturation is slow")
	}
	e := newAdminE2E(t)
	rt := e.env.proxyServer.runtime
	// Subscribe and never read: the subscriber's buffer fills and the bus
	// drops for it, exactly as a stuck consumer would.
	_ = rt.SubscribeEvents()
	cs := rt.CredentialsService()
	ctx := adminTestContext()
	seen := len(e.sink.snapshot())
	a := runtime.Actor{Kind: "api_key", Surface: profile.SurfaceAPI}
	// The token cap (auth.MaxTokens) bounds how many exist at once, so the
	// revocations are create → revoke → delete cycles. 300 cycles publish
	// 900 credentials.changed events into a 256-slot subscriber buffer that
	// nobody drains — well past saturation. (SC-003 names 2,000; each cycle
	// is three fsynced bbolt transactions, ~100 ms, so 2,000 would add over
	// three minutes to every CI run for no extra coverage: the line path
	// never touches the bus whatever the count.)
	const n = 300
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("sat-%d", i)
		_, err := cs.IssueToken(ctx, a, runtime.IssueTokenRequest{Name: name, ExpiresIn: "1h", Expiry: runtime.ExpiryTokenDefault})
		require.NoError(t, err)
		before := len(e.sink.snapshot())
		_, err = cs.Revoke(ctx, a, runtime.CredentialRef{Token: name}, true)
		require.NoError(t, err)
		// FR-018: the line is on the sink when Revoke returns — written
		// synchronously, by no subscriber of the (saturated) bus.
		require.Equal(t, before+1, len(e.sink.snapshot()), "revoke %d: the line is written before Revoke returns", i)
		_, err = cs.DeleteToken(ctx, a, name)
		require.NoError(t, err)
	}
	revokes := 0
	perToken := map[string]int{}
	for _, l := range e.adminLines(&seen) {
		if l["action"] == audit.ActionTokenRevoke {
			revokes++
			perToken[l["target"].(map[string]interface{})["name"].(string)]++
		}
	}
	require.Equal(t, n, revokes, "every committed revocation writes one line with the bus saturated")
	require.Len(t, perToken, n, "one line per revoked token, none dropped and none doubled")
	for name, c := range perToken {
		require.Equal(t, 1, c, name)
	}
}

// adminTestContext is an administrator REST request context (API key over
// TCP, API mount), as the REST middleware chain builds it.
func adminTestContext() context.Context {
	ctx := auth.WithAuthContext(context.Background(), auth.AdminContext())
	ctx = reqcontext.WithRequestMeta(ctx, reqcontext.RequestMeta{Mount: reqcontext.MountAPI, Surface: "api"})
	return reqcontext.WithRequestID(ctx, "req-saturated")
}

// failingAuditSink fails every write, as a full disk would.
type failingAuditSink struct{ failures atomic.Uint64 }

func (s *failingAuditSink) Write([]byte) error {
	s.failures.Add(1)
	return errors.New("no space left on device")
}
func (s *failingAuditSink) WriteFailures() uint64 { return s.failures.Load() }
func (s *failingAuditSink) SanitizerHits() uint64 { return 0 }
func (s *failingAuditSink) Close() error          { return nil }

// US1 scenario 4 / FR-017: with the audit disk full the change still
// succeeds, and the failure is counted (doctor reports the counter).
func TestAdminActionDoors_WriteFailureNeverRefusesTheChange(t *testing.T) {
	sink := &failingAuditSink{}
	env := NewTestEnvironmentWithOptions(t, TestEnvironmentOptions{
		Mutate:        func(cfg *config.Config, _ string) { cfg.APIKey = adminE2EAPIKey },
		ServerOptions: []ServerOption{WithAuditSink(sink)},
	})
	t.Cleanup(env.Cleanup)
	e := &adminE2E{t: t, env: env, sink: &recordingAuditSink{}, base: strings.TrimSuffix(env.proxyAddr, "/mcp")}
	code, body := e.rest(http.MethodPost, "/api/v1/tokens", "", map[string]any{"name": "full-disk", "expires_in": "1h"})
	require.Equal(t, http.StatusCreated, code, string(body))
	code, _ = e.rest(http.MethodDelete, "/api/v1/tokens/full-disk", "", nil)
	require.Equal(t, http.StatusNoContent, code, "the revoke succeeds with every audit write failing")
	stored, _ := env.proxyServer.runtime.StorageManager().GetAgentTokenByName("full-disk")
	require.True(t, stored.Revoked)
	require.GreaterOrEqual(t, sink.WriteFailures(), uint64(2), "both attempts were made and counted")
	code, diag := e.rest(http.MethodGet, "/api/v1/diagnostics", "", nil)
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, string(diag), "audit_log: ", "doctor reports the audit write failures")
	require.Contains(t, string(diag), "write failures since start")
}

// US1 scenario 1: the real CLI HTTP client over the tray socket — caller
// socket, origin socket, client.surface cli, the CLI's X-MCPProxy-Client.
func TestAdminActionDoors_CLIOverTraySocket(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("named-pipe transport; the socket path is the Unix tray transport")
	}
	// A Unix socket path is limited to ~104 bytes; a long TMPDIR (macOS)
	// would make the bind fail and the daemon fall back to TCP. Use a short
	// data directory under /tmp.
	shortDir, err := os.MkdirTemp("/tmp", "mx")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(shortDir) })
	require.NoError(t, os.Chmod(shortDir, 0o700))
	e := newAdminE2E(t, func(cfg *config.Config) { cfg.EnableSocket = true; cfg.DataDir = shortDir })
	endpoint := socket.GetDefaultSocketPath(e.env.proxyServer.runtime.Config().DataDir)
	socketPath := strings.TrimPrefix(endpoint, "unix://")
	require.Eventually(t, func() bool { _, err := os.Stat(socketPath); return err == nil }, 10*time.Second, 50*time.Millisecond, "tray socket")
	cli := cliclient.NewClient(endpoint, nil)
	seen := len(e.sink.snapshot())

	resp, err := cli.DoRaw(context.Background(), http.MethodPost, "/api/v1/tokens", []byte(`{"name":"sock-bot","expires_in":"1h"}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp, err = cli.DoRaw(context.Background(), http.MethodPost, "/api/v1/tokens/sock-bot/regenerate", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	ingressID := resp.Header.Get("X-Request-Id")
	require.NotEmpty(t, ingressID, "the rotation response names its ingress id")

	lines := e.adminLines(&seen)
	require.Len(t, lines, 2)
	rot := lines[1]
	require.Equal(t, "token.rotate", rot["action"])
	require.Equal(t, map[string]interface{}{"kind": "socket"}, rot["caller"])
	require.Equal(t, "socket", rot["origin"])
	require.Equal(t, "api", rot["source"])
	client := rot["client"].(map[string]interface{})
	require.Equal(t, "cli", client["surface"])
	require.True(t, strings.HasPrefix(client["name"].(string), "cli/"), "the CLI's own X-MCPProxy-Client header")
	rid, _ := rot["request_id"].(string)
	require.NotEmpty(t, rid)
	require.Equal(t, ingressID, rid, "the line carries the ingress id the response returned (round 25)")
	require.Equal(t, 1, e.activityRequestIDs(rid))
}

// The daemon installs runtime-backed diagnostics fixers at startup
// (registerDiagnosticFixers); every fixer registered after a real start has
// its own registry row under POST /diagnostics/fix.
func TestAdminDoorRegistry_ListsEveryFixerAfterStartup(t *testing.T) {
	newAdminE2E(t)
	rows := map[string]bool{}
	for _, d := range audit.AdminDoors {
		if d.Door == "REST POST /api/v1/diagnostics/fix" {
			rows[d.Branch] = true
		}
	}
	for _, k := range diagnostics.FixerKeys() {
		require.True(t, rows["fixer "+k], "diagnostics fixer %q registered at startup has no registry row", k)
	}
	live := map[string]bool{}
	for _, k := range diagnostics.FixerKeys() {
		live["fixer "+k] = true
	}
	for branch := range rows {
		require.True(t, live[branch], "registry row %q names a fixer that is no longer registered", branch)
	}
}

// The Web UI's own header classifies the surface as web.
func TestAdminActionDoors_WebUISurface(t *testing.T) {
	e := newAdminE2E(t)
	seen := 0
	code, body := e.restAs("webui/1.0", http.MethodPost, "/api/v1/tokens", "req-web-1", map[string]any{"name": "web-bot", "expires_in": "1h"})
	require.Equal(t, http.StatusCreated, code, string(body))
	check := func(l map[string]interface{}, action, rid string) {
		require.Equal(t, action, l["action"])
		require.Equal(t, map[string]interface{}{"kind": "api_key"}, l["caller"], action)
		require.Equal(t, "api", l["source"], action)
		require.Equal(t, "local", l["origin"], action)
		require.Equal(t, "web-bot", l["target"].(map[string]interface{})["name"], action)
		require.Equal(t, rid, l["request_id"], action)
		require.Equal(t, "web", l["client"].(map[string]interface{})["surface"], action)
		require.Equal(t, "webui/1.0", l["client"].(map[string]interface{})["name"], action)
		require.Equal(t, "agent_token", l["target"].(map[string]interface{})["kind"], action)
		if action != "token.delete" { // a permanent delete writes no activity row
			require.Equal(t, 1, e.activityRequestIDs(rid), "%s joins its activity row", action)
		}
	}
	lines := e.adminLines(&seen)
	require.Len(t, lines, 1)
	check(lines[0], "token.create", "req-web-1")
	for i, step := range []struct{ method, path, action string }{
		{http.MethodPost, "/api/v1/tokens/web-bot/regenerate", "token.rotate"},
		{http.MethodDelete, "/api/v1/tokens/web-bot", "token.revoke"},
		{http.MethodDelete, "/api/v1/tokens/web-bot/permanent", "token.delete"},
	} {
		rid := fmt.Sprintf("req-web-%d", i+2)
		code, body := e.restAs("webui/1.0", step.method, step.path, rid, nil)
		require.Less(t, code, 300, "%s %s: %s", step.method, step.path, body)
		lines := e.adminLines(&seen)
		require.Len(t, lines, 1, step.action)
		check(lines[0], step.action, rid)
	}
}

// A scoped agent token is refused every token mutation (403) and its refused
// attempt writes no admin_action line (FR-013: nothing committed).
func TestAdminActionDoors_ScopedAgentTokenRefusedWritesNothing(t *testing.T) {
	e := newAdminE2E(t)
	code, body := e.rest(http.MethodPost, "/api/v1/tokens", "", map[string]any{"name": "scoped-bot", "expires_in": "1h"})
	require.Equal(t, http.StatusCreated, code, string(body))
	var created struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	require.NotEmpty(t, created.Data.Token)
	seen := len(e.sink.snapshot())
	for _, step := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/tokens"},
		{http.MethodPost, "/api/v1/tokens/scoped-bot/regenerate"},
		{http.MethodDelete, "/api/v1/tokens/scoped-bot"},
		{http.MethodDelete, "/api/v1/tokens/scoped-bot/permanent"},
	} {
		req, err := http.NewRequest(step.method, e.base+step.path, strings.NewReader(`{"name":"other","expires_in":"1h"}`))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+created.Data.Token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode, "%s %s", step.method, step.path)
	}
	require.Empty(t, e.adminLines(&seen), "a refused mutation writes no admin_action line")
	stored, _ := e.env.proxyServer.runtime.StorageManager().GetAgentTokenByName("scoped-bot")
	require.False(t, stored.Revoked, "nothing was committed")
}
