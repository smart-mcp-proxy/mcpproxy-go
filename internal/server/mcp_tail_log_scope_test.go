package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/dockernaming"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// tailLogCanary is written into every fixture server's log through the REAL
// stamped per-server writer. Its presence in a tool response proves the log
// was disclosed.
const tailLogCanary = "CANARY-upstream-log-line-7f3a"

// tailLogLegacyLine is appended to every fixture server's log WITHOUT a
// writer stamp (a pre-105 record). Spec 105 FR-007: a record with no
// attribution is withheld from scoped callers and kept for administrators —
// pre-105 this fixture's canary was itself an unstamped line served to the
// scoped token, which the attributed reader now (correctly) withholds.
const tailLogLegacyLine = "LEGACY-unstamped-log-line-2b61"

// newTailLogScopeProxy builds a proxy with two upstreams, "github" and
// "secret", each with a per-server log file AND a registered (never
// connected) upstream client so a served response carries connection_status,
// plus a profile "gh" that contains only "github". Spec 104 FR-016h: an agent
// token scoped away from "secret" (by AllowedServers or by a profile pin)
// must not learn anything about it through `upstream_servers` `tail_log`.
func newTailLogScopeProxy(t *testing.T) *MCPProxyServer {
	t.Helper()
	proxy := createTestMCPProxyServer(t)

	logDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.Logging.LogDir = logDir
	// The main server's config must carry both names so profile "gh" expands
	// (ProfileConfig.EffectiveServers keeps only configured servers), but the
	// entries are DISABLED: an enabled entry makes the runtime connect in a
	// background goroutine that keeps writing server-<name>.log after Shutdown
	// returns, and on Linux CI that write lands after t.TempDir's RemoveAll
	// ("directory not empty"). The enabled flag a served response reports comes
	// from proxy.storage below, not from this config.
	cfg.Servers = []*config.ServerConfig{
		{Name: "github", Protocol: "http", Enabled: false},
		{Name: "secret", Protocol: "http", Enabled: false},
	}
	cfg.Profiles = []config.ProfileConfig{{Name: "gh", Servers: []string{"github"}}}
	mainSrv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mainSrv.Shutdown() })
	proxy.mainServer = mainSrv

	for _, name := range []string{"github", "secret"} {
		sc := &config.ServerConfig{Name: name, Protocol: "http", URL: "http://127.0.0.1:1/mcp", Enabled: true}
		require.NoError(t, proxy.storage.SaveUpstreamServer(sc))
		// Register a client without connecting so GetClient resolves and the
		// served response includes connection_status (otherwise the
		// "connection status not disclosed" assertions would pass vacuously).
		require.NoError(t, proxy.upstreamManager.AddServerConfig(name, sc))
		// A pre-105 unstamped record first, then the canary through the real
		// stamped writer (the one internal/upstream/core installs).
		require.NoError(t, os.WriteFile(filepath.Join(logDir, "server-"+name+".log"),
			[]byte(tailLogLegacyLine+" "+name+"\n"), 0o600))
		writer, closer, err := logs.NewUpstreamServerLogger(cfg.Logging, name)
		require.NoError(t, err)
		t.Cleanup(func() { _ = closer.Close() })
		writer.Info(tailLogCanary + " " + name)
		_ = writer.Sync()
	}
	return proxy
}

// tailLogVia drives the real `upstream_servers` dispatcher — the path an
// agent token reaches on /mcp in retrieve mode — with operation=tail_log.
func tailLogVia(t *testing.T, proxy *MCPProxyServer, ctx context.Context, name string) (*mcp.CallToolResult, string) {
	t.Helper()
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{"operation": "tail_log", "name": name}
	result, err := proxy.handleUpstreamServers(ctx, request)
	require.NoError(t, err)
	return result, toolResultText(t, result)
}

// adminTailLogNotFound returns the response an unrestricted caller gets for a
// server that genuinely does not exist — the storage-not-found path, not the
// scope-refusal path — so the scoped comparison below is against the real
// "no such server" shape rather than against itself.
func adminTailLogNotFound(t *testing.T, proxy *MCPProxyServer, name string) string {
	t.Helper()
	result, body := tailLogVia(t, proxy, context.Background(), name)
	require.True(t, result.IsError, "baseline: a nonexistent server must be an error")
	require.Contains(t, body, "not found")
	return body
}

// assertTailLogHidden asserts the response for an existing but out-of-scope
// server is byte-identical (modulo the echoed name) to the unrestricted
// response for a server that does not exist: no existence, no status, no logs.
func assertTailLogHidden(t *testing.T, proxy *MCPProxyServer, ctx context.Context, name string) {
	t.Helper()
	ghostBody := adminTailLogNotFound(t, proxy, "ghost")

	result, body := tailLogVia(t, proxy, ctx, name)
	assert.True(t, result.IsError, "out-of-scope server must be refused")
	assert.Equal(t, strings.ReplaceAll(ghostBody, "ghost", name), body,
		"refusal must have the same shape as a nonexistent server (no existence oracle)")
	assert.NotContains(t, body, tailLogCanary, "log lines disclosed")
	assert.NotContains(t, body, "server_status", "server status disclosed")
	assert.NotContains(t, body, "connection_status", "connection status disclosed")
}

// assertTailLogServed asserts the in-scope / admin path returns the stamped
// log record, the stored flags and the live connection status, and returns
// the body for the caller's attribution assertions.
func assertTailLogServed(t *testing.T, proxy *MCPProxyServer, ctx context.Context, name string) string {
	t.Helper()
	result, body := tailLogVia(t, proxy, ctx, name)
	assert.False(t, result.IsError, "in-scope tail_log must succeed: %s", body)
	assert.Contains(t, body, tailLogCanary+" "+name)
	assert.Contains(t, body, "server_status")
	assert.Contains(t, body, "connection_status", "fixture must register a client, or the non-disclosure assertions prove nothing")
	return body
}

// assertTailLogServedScoped is assertTailLogServed for a scoped caller: the
// stamped record is served, the unstamped legacy record is withheld
// (Spec 105 FR-007 — attribution is uniform, co-owner or not).
func assertTailLogServedScoped(t *testing.T, proxy *MCPProxyServer, ctx context.Context, name string) {
	t.Helper()
	body := assertTailLogServed(t, proxy, ctx, name)
	assert.NotContains(t, body, tailLogLegacyLine, "unattributed legacy record served to a scoped caller")
}

// assertTailLogServedWholeFile is assertTailLogServed for an administrator:
// the whole file, legacy record included (SC-005).
func assertTailLogServedWholeFile(t *testing.T, proxy *MCPProxyServer, ctx context.Context, name string) {
	t.Helper()
	body := assertTailLogServed(t, proxy, ctx, name)
	assert.Contains(t, body, tailLogLegacyLine+" "+name, "administrators keep the whole file")
}

func TestTailLog_ServerRestrictedToken_HidesOutOfScopeServer(t *testing.T) {
	proxy := newTailLogScopeProxy(t)
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "reader",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	})

	assertTailLogHidden(t, proxy, ctx, "secret")
	assertTailLogServedScoped(t, proxy, ctx, "github")
}

func TestTailLog_ProfilePinnedToken_HidesServerOutsideProfile(t *testing.T) {
	proxy := newTailLogScopeProxy(t)
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "pinned",
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead},
		ProfilePin:     "gh",
	})

	assertTailLogHidden(t, proxy, ctx, "secret")
	assertTailLogServedScoped(t, proxy, ctx, "github")
}

// A pin whose profile no longer exists resolves to a deny-all scope (see
// resolveActiveProfile); tail_log must honour that rather than widen to the
// token's own server list.
func TestTailLog_StaleProfilePin_DeniesAll(t *testing.T) {
	proxy := newTailLogScopeProxy(t)
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "stale",
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead},
		ProfilePin:     "removed-profile",
	})

	assertTailLogHidden(t, proxy, ctx, "secret")
	assertTailLogHidden(t, proxy, ctx, "github")
}

func TestTailLog_AdminUnchanged(t *testing.T) {
	proxy := newTailLogScopeProxy(t)

	adminCtx := auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAdmin})
	assertTailLogServedWholeFile(t, proxy, adminCtx, "secret")
	assertTailLogServedWholeFile(t, proxy, adminCtx, "github")

	// No AuthContext at all (in-process / stdio caller) is treated as admin by
	// the shared server-op policy; unchanged here.
	assertTailLogServedWholeFile(t, proxy, context.Background(), "secret")

	// An admin's AllowedServers is never consulted, even when populated.
	narrowAdmin := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAdmin, AllowedServers: []string{"github"},
	})
	assertTailLogServedWholeFile(t, proxy, narrowAdmin, "secret")
}

// An explicit URL profile (/mcp/p/<slug>) bounds tail_log for every caller,
// admin included — the same rule `list` and call_tool_* already apply
// (Spec 057 FR-004: profile filtering is independent of agent scope).
func TestTailLog_URLProfileScope_AppliesToAllCallers(t *testing.T) {
	proxy := newTailLogScopeProxy(t)
	scope := profile.NewProfileScope("gh", []string{"github"})

	// A profile bounds WHICH server an administrator may name, not which
	// records of it they see: still the whole file.
	adminInProfile := profile.WithProfileScope(
		auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAdmin}), scope)
	assertTailLogHidden(t, proxy, adminInProfile, "secret")
	assertTailLogServedWholeFile(t, proxy, adminInProfile, "github")

	anonInProfile := profile.WithProfileScope(context.Background(), scope)
	assertTailLogHidden(t, proxy, anonInProfile, "secret")
	assertTailLogServedWholeFile(t, proxy, anonInProfile, "github")
}

// ---------------------------------------------------------------------------
// Spec 105 FR-007 (gaps FR007-G1, G3): colliding log files. `a/b` and `a_b`
// both sanitise to server-a_b.log, so an `a_b`-only token must receive only
// the records `a_b` wrote (filtered BEFORE the tail limit, lines_returned =
// filtered length) and administrators must keep the whole file byte-for-byte.
// ---------------------------------------------------------------------------

// tailLogCollidingFixture is a proxy whose storage knows `a/b` and `a_b` and
// whose log directory holds their SHARED file, plus the two real stamped
// writers (logs.NewUpstreamServerLogger — the writer internal/upstream/core
// installs) so every record carries the `server=<raw>` stamp.
type tailLogCollidingFixture struct {
	proxy   *MCPProxyServer
	logCfg  *config.LogConfig
	writers map[string]*zap.Logger
}

const (
	collidingHidden = "a/b"
	collidingOwn    = "a_b"
)

// newTailLogCollidingProxy builds the fixture. Every returned io.Closer is
// closed at cleanup (CI "directory not empty" otherwise). The shared file is
// pre-created so both lumberjack sinks open it O_APPEND — lumberjack creates
// a NEW file O_TRUNC without O_APPEND, and two writers on one fresh file
// overwrite each other (the torn-fragment corruption gap-map FR007-G3 probed,
// a retained effect that is not what these tests are about).
func newTailLogCollidingProxy(t *testing.T) *tailLogCollidingFixture {
	t.Helper()
	return newTailLogProxyWithServers(t, collidingHidden, collidingOwn)
}

// newTailLogProxyWithServers is newTailLogCollidingProxy for an explicit set
// of registered servers, so a differential can run a TRUE absent-co-owner
// arm (only `a_b` configured, no `a/b` anywhere: not in storage, not in the
// upstream manager, no writer) against the colliding one.
func newTailLogProxyWithServers(t *testing.T, names ...string) *tailLogCollidingFixture {
	t.Helper()
	require.Equal(t, logs.ServerLogFilename(collidingHidden), logs.ServerLogFilename(collidingOwn),
		"fixture premise: the two raw names must share one log file")

	proxy := createTestMCPProxyServer(t)

	logDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.Logging.LogDir = logDir
	cfg.Logging.EnableFile = true
	cfg.Logging.EnableConsole = false
	cfg.Logging.Compress = false
	for _, name := range names {
		cfg.Servers = append(cfg.Servers, &config.ServerConfig{Name: name, Protocol: "http", Enabled: false})
	}
	mainSrv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mainSrv.Shutdown() })
	proxy.mainServer = mainSrv

	require.NoError(t, os.WriteFile(filepath.Join(logDir, logs.ServerLogFilename(collidingOwn)), nil, 0o600))

	f := &tailLogCollidingFixture{proxy: proxy, logCfg: cfg.Logging, writers: map[string]*zap.Logger{}}
	for _, name := range names {
		sc := &config.ServerConfig{Name: name, Protocol: "http", URL: "http://127.0.0.1:1/mcp", Enabled: true}
		require.NoError(t, proxy.storage.SaveUpstreamServer(sc))
		require.NoError(t, proxy.upstreamManager.AddServerConfig(name, sc))

		writer, closer, err := logs.NewUpstreamServerLogger(cfg.Logging, name)
		require.NoError(t, err)
		t.Cleanup(func() { _ = closer.Close() })
		f.writers[name] = writer
	}
	return f
}

// write emits one record through name's real stamped writer. The writer
// records its CALLER's caller (NewUpstreamServerLogger adds one frame of
// skip), i.e. the test line that called write, so two fixtures' lines differ
// by timestamp and caller segment — both stripped by tailLogLineSignature.
func (f *tailLogCollidingFixture) write(name, msg string) {
	f.writers[name].Info(msg)
	_ = f.writers[name].Sync()
}

// tailLogResponse is the parsed tail_log payload.
type tailLogResponse struct {
	ServerName     string   `json:"server_name"`
	LinesRequested int      `json:"lines_requested"`
	LinesReturned  int      `json:"lines_returned"`
	LogLines       []string `json:"log_lines"`
}

// tailLogLinesVia drives the real dispatcher with an explicit `lines` and
// parses the payload.
func tailLogLinesVia(t *testing.T, proxy *MCPProxyServer, ctx context.Context, name string, lines int) (tailLogResponse, string) {
	t.Helper()
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{"operation": "tail_log", "name": name, "lines": float64(lines)}
	result, err := proxy.handleUpstreamServers(ctx, request)
	require.NoError(t, err)
	body := toolResultText(t, result)
	require.False(t, result.IsError, "tail_log must succeed for the in-scope server: %s", body)
	var parsed tailLogResponse
	require.NoError(t, json.Unmarshal([]byte(body), &parsed), body)
	return parsed, body
}

// tailLogLineSignature strips the timestamp and caller segments of a console
// record (`ts | LEVEL | caller | msg | {fields}`) so records written by two
// fixtures compare on level, message and fields only.
func tailLogLineSignature(line string) string {
	parts := strings.SplitN(line, " | ", 4)
	if len(parts) < 4 {
		return line
	}
	return parts[1] + " | " + parts[3]
}

func tailLogSignatures(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = tailLogLineSignature(l)
	}
	return out
}

// FR007-G1 + G3 at the tool surface: interleaved own/foreign records, an
// `a_b`-only token asks for the last 2 → exactly [own1, own2],
// lines_returned == 2, nothing from `a/b`.
func TestTailLog_CollidingLogFile_ScopedTokenGetsOnlyOwnRecords(t *testing.T) {
	f := newTailLogCollidingProxy(t)
	const sentinel = "SENTINEL-a-slash-b-only-4e2d"
	f.write(collidingOwn, "own1")
	f.write(collidingHidden, sentinel+"-1")
	f.write(collidingOwn, "own2")
	f.write(collidingHidden, sentinel+"-2")

	ctx := agentCtx([]string{collidingOwn}, []string{auth.PermRead}, "")
	resp, body := tailLogLinesVia(t, f.proxy, ctx, collidingOwn, 2)

	assert.NotContains(t, body, sentinel, "a/b's records disclosed to an a_b-only token")
	assert.Equal(t, 2, resp.LinesReturned, "lines_returned must count the authorized tail")
	require.Len(t, resp.LogLines, 2, "the window must hold the two OWN records, got: %v", resp.LogLines)
	assert.Contains(t, resp.LogLines[0], "own1", "foreign line displaced own1 from the window")
	assert.Contains(t, resp.LogLines[1], "own2")
	assert.Equal(t, len(resp.LogLines), resp.LinesReturned)

	// The default window (50) has the same property: no foreign record at all.
	resp, body = tailLogLinesVia(t, f.proxy, ctx, collidingOwn, 50)
	assert.NotContains(t, body, sentinel)
	assert.Len(t, resp.LogLines, 2)
	assert.Equal(t, 2, resp.LinesReturned)
}

// FR007-G1 SC-001 differential: the `a_b`-only token's view must be the same
// whether or not hidden `a/b` shares the file (uniform and independent of
// hidden co-owners — never a whole-file refusal that depends on a co-owner).
// Three arms (critique round 1, finding C2.6): co-owner present and writing;
// co-owner configured but silent; co-owner ABSENT (not configured at all) —
// the last is the spec's literal "without hidden a/b present", and it is the
// arm that catches an implementation refusing the whole file whenever a
// config co-owner exists.
func TestTailLog_CollidingLogFile_DifferentialWithHiddenCoOwner(t *testing.T) {
	ctx := agentCtx([]string{collidingOwn}, []string{auth.PermRead}, "")

	with := newTailLogCollidingProxy(t)
	with.write(collidingOwn, "own1")
	with.write(collidingHidden, "foreign1")
	with.write(collidingOwn, "own2")
	with.write(collidingHidden, "foreign2")
	withResp, _ := tailLogLinesVia(t, with.proxy, ctx, collidingOwn, 50)

	silent := newTailLogCollidingProxy(t)
	silent.write(collidingOwn, "own1")
	silent.write(collidingOwn, "own2")
	silentResp, _ := tailLogLinesVia(t, silent.proxy, ctx, collidingOwn, 50)

	absent := newTailLogProxyWithServers(t, collidingOwn)
	absent.write(collidingOwn, "own1")
	absent.write(collidingOwn, "own2")
	absentResp, _ := tailLogLinesVia(t, absent.proxy, ctx, collidingOwn, 50)

	assert.Equal(t, tailLogSignatures(absentResp.LogLines), tailLogSignatures(withResp.LogLines),
		"scoped view must not depend on whether a hidden co-owner shares the file")
	assert.Equal(t, tailLogSignatures(absentResp.LogLines), tailLogSignatures(silentResp.LogLines),
		"scoped view must not depend on whether a hidden co-owner is configured")
	assert.Equal(t, absentResp.LinesReturned, withResp.LinesReturned)
	assert.Equal(t, absentResp.LinesReturned, silentResp.LinesReturned)
	assert.Equal(t, 2, absentResp.LinesReturned)
}

// SC-005 administrator control: the administrator payload is the whole-file
// tail exactly as before the feature — log_lines byte-equal to the scrubbed
// whole-file reader, lines_returned its length, co-owner records included.
// Expected green on HEAD; it pins the whole-file path for the fix. The
// oracle is HEAD's logs.ReadUpstreamServerLogTail rather than a frozen
// capture (tasks.md T048): that is valid because internal/logs/logger.go is
// untouched by the Spec 105 PR E diff — if a later change edits the
// whole-file reader, this oracle moves with it and must be re-justified.
func TestTailLog_CollidingLogFile_AdminWholeFileUnchanged(t *testing.T) {
	f := newTailLogCollidingProxy(t)
	f.write(collidingOwn, "own1")
	f.write(collidingHidden, "foreign1")
	f.write(collidingOwn, "own2")
	f.write(collidingHidden, "foreign2")

	whole, err := logs.ReadUpstreamServerLogTail(f.logCfg, collidingOwn, 2)
	require.NoError(t, err)
	require.Len(t, whole, 2)

	for name, ctx := range map[string]context.Context{
		"api-key admin": adminCtx(),
		"no auth ctx":   context.Background(),
	} {
		t.Run(name, func(t *testing.T) {
			resp, body := tailLogLinesVia(t, f.proxy, ctx, collidingOwn, 2)
			assert.Equal(t, scrubUpstreamLines(whole), resp.LogLines, "administrator log_lines must be the raw whole-file tail")
			assert.Equal(t, 2, resp.LinesReturned)
			assert.Contains(t, body, "foreign2", "administrators keep co-owner records")
			assert.Contains(t, body, "own2")
			for _, key := range []string{"server_name", "lines_requested", "lines_returned", "log_lines", "server_status", "connection_status"} {
				assert.Contains(t, body, `"`+key+`"`, "administrator payload shape unchanged")
			}
		})
	}
}

// Codex round 2 (PR E), docker finding 1, at the tool surface: `a/b` and
// hidden `a-b` both generate mcpproxy-a-b-<suffix>, and on a suffix
// collision Docker's own `docker run` failure names the FOREIGN container's
// name and full id. That text reaches a/b's per-server log as child output
// — through the stdio child's stderr (monitoring.go) and the launcher pump
// (connection_launcher.go), both of which write it as the `message` field
// of a record stamped child_output=true (logs.ChildOutputField). tail_log
// for an a/b-scoped token must never show the foreign id or name; the
// administrator keeps the whole file (SC-005). The records are written here
// through the real stamped writer in exactly the producers' shape
// (internal/upstream/core/docker_collision_output_test.go drives the real
// producers against a fake docker and the same reader).
func TestTailLog_DockerCollisionChildOutput_ForeignContainerWithheldFromScopedCaller(t *testing.T) {
	const foreignID = "f0e1d2c3b4a5968778695a4b3c2d1e0ff0e1d2c3b4a5968778695a4b3c2d1e0f"
	const foreignName = "mcpproxy-a-b-wxyz"
	collision := `docker: Error response from daemon: Conflict. The container name "/` + foreignName +
		`" is already in use by container "` + foreignID + `". You have to remove (or rename) that container to be able to reuse that name.`

	const hiddenOwner = "a-b" // the container's owner, a co-tenant of the container namespace only
	require.Equal(t, dockernaming.SanitizeServerName(collidingHidden), dockernaming.SanitizeServerName(hiddenOwner),
		"fixture premise: a/b and a-b generate the same container-name stem")
	require.Equal(t, "mcpproxy-"+dockernaming.SanitizeServerName(hiddenOwner)+"-wxyz", foreignName)

	f := newTailLogProxyWithServers(t, collidingHidden, hiddenOwner) // "a/b" reads; "a-b" is registered and silent
	w := f.writers[collidingHidden]
	w.Info("own-ordinary-record")
	w.Info("stderr", zap.String("message", collision), logs.ChildOutputField())
	w.Info("launcher", zap.String("message", "[launcher stderr] "+collision), logs.ChildOutputField())
	w.Info("stderr", zap.String("message", "listening on 127.0.0.1:9331"), logs.ChildOutputField())
	_ = w.Sync()

	scoped := agentCtx([]string{collidingHidden}, []string{auth.PermRead}, "")
	resp, body := tailLogLinesVia(t, f.proxy, scoped, collidingHidden, 50)
	assert.NotContains(t, body, foreignID, "foreign container id disclosed to an a/b-scoped token")
	assert.NotContains(t, body, foreignName, "foreign container name disclosed to an a/b-scoped token")
	assert.NotContains(t, body, "already in use by container")
	assert.Contains(t, body, "own-ordinary-record")
	assert.Contains(t, body, "listening on 127.0.0.1:9331", "ordinary child output stays served")
	assert.Equal(t, 2, resp.LinesReturned, "lines_returned counts the authorized tail: %s", body)

	for name, ctx := range map[string]context.Context{
		"api-key admin": adminCtx(),
		"no auth ctx":   context.Background(),
	} {
		t.Run(name, func(t *testing.T) {
			resp, body := tailLogLinesVia(t, f.proxy, ctx, collidingHidden, 50)
			assert.Contains(t, body, foreignID, "administrators keep Docker's output (SC-005)")
			assert.Equal(t, 4, resp.LinesReturned)
		})
	}
}
