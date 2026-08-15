//go:build !windows

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 098 T026/T027 — the sabotage matrix, which is this feature's acceptance
// gate (FR-016, SC-001, SC-005, SC-006).
//
// The shape of this file, and why:
//
//   - testdata/preflight_sabotage_matrix.json is the COMMITTED contract:
//     scenario -> expected {status, reason, retryable, action, verdict,
//     exit_code}. The Go code never hardcodes an expected reason; it looks the
//     scenario up. Adding an enum code without a scenario fails
//     TestPreflightSabotageMatrixCoversEveryReason, which needs no binary and
//     therefore always runs in CI.
//   - Every cell asserts the reason, the retryable flag and the action
//     INDEPENDENTLY (three assertions, not one struct compare of a value the
//     test itself built), plus the set verdict and the exit code the CLI would
//     return for that verdict.
//   - After every cell the test fetches the activity record by the response's
//     X-Request-Id and asserts the preflight record exists with the right
//     verdict and per-tool reason (SC-005). A cell that produced no auditable
//     record is a failed cell.
//   - The states are induced against a REAL mcpproxy binary with sabotaged
//     upstream fixtures — quarantine flips, rug-pulls, kills — not against a
//     hand-built evaluator context, because the point of the gate is that the
//     wiring produces these verdicts, not that the evaluator can.

const (
	preflightFixturePath = "testdata/preflight_fixture_server.js"
	preflightE2EAPIKey   = "preflight-e2e-api-key"
)

// ---------------------------------------------------------------------------
// E2E harness: isolated mcpproxy binary + sabotageable fixture upstreams
// ---------------------------------------------------------------------------

type preflightE2E struct {
	t          *testing.T
	dir        string
	dataDir    string
	configPath string
	binaryPath string
	nodePath   string
	baseURL    string
	port       int
	cmd        *exec.Cmd
	scenarios  map[string]sabotageScenario
	// toolsFiles maps a fixture server name onto the JSON file whose contents it
	// serves; rewriting one and restarting the server is the rug-pull.
	toolsFiles map[string]string
	// killFailFile is the fixture fail-switch for the `killable` server.
	killFailFile string
	client       *http.Client
}

// fixtureTool is one entry of a fixture tools file (MCP tool definition shape).
type fixtureTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	Annotations map[string]interface{} `json:"annotations,omitempty"`
}

func emptySchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}

func readOnlyAnnotations() map[string]interface{} {
	return map[string]interface{}{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": false}
}

// mainFixtureTools is the baseline definition set of the `main` server.
func mainFixtureTools() []fixtureTool {
	return []fixtureTool{
		{Name: "ready_tool", Description: "ready v1", InputSchema: emptySchema(), Annotations: readOnlyAnnotations()},
		{Name: "drift_tool", Description: "drift v1", InputSchema: emptySchema(), Annotations: readOnlyAnnotations()},
		{Name: "blocked_tool", Description: "blockable", InputSchema: emptySchema(), Annotations: readOnlyAnnotations()},
		{Name: "noann_tool", Description: "no annotations at all", InputSchema: emptySchema()},
		{
			Name: "unsafe_tool", Description: "explicitly unsafe", InputSchema: emptySchema(),
			Annotations: map[string]interface{}{"readOnlyHint": false, "destructiveHint": true, "openWorldHint": true},
		},
	}
}

func singleFixtureTool(name string) []fixtureTool {
	return []fixtureTool{{Name: name, Description: name + " v1", InputSchema: emptySchema(), Annotations: readOnlyAnnotations()}}
}

// newPreflightE2E prepares an ISOLATED instance: scratch data dir, scratch
// config, high port. It never touches ~/.mcpproxy.
func newPreflightE2E(t *testing.T) *preflightE2E {
	t.Helper()

	if testing.Short() {
		t.Skip("preflight sabotage E2E needs a built binary and a live proxy; skipped in -short")
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("preflight sabotage E2E needs node for the fixture upstream")
	}
	binaryPath := preflightE2EBinary(t)

	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	require.NoError(t, os.MkdirAll(dataDir, 0o700))

	env := &preflightE2E{
		t:            t,
		dir:          dir,
		dataDir:      dataDir,
		configPath:   filepath.Join(dir, "config.json"),
		binaryPath:   binaryPath,
		nodePath:     nodePath,
		port:         preflightE2EPort(t),
		scenarios:    loadSabotageMatrix(t),
		toolsFiles:   map[string]string{},
		killFailFile: filepath.Join(dir, "killable.fail"),
		client:       &http.Client{Timeout: 30 * time.Second},
	}
	env.baseURL = fmt.Sprintf("http://127.0.0.1:%d", env.port)
	return env
}

// preflightE2EBinary resolves the mcpproxy binary the same way the other binary
// E2Es do; a missing binary skips rather than fails, so the always-on reflection
// test above stays the CI gate for enum coverage.
func preflightE2EBinary(t *testing.T) string {
	t.Helper()

	if explicit := os.Getenv("MCPPROXY_BINARY_PATH"); explicit != "" {
		return explicit
	}
	const name = "mcpproxy"
	cwd, err := os.Getwd()
	require.NoError(t, err)
	for dir := cwd; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, name)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate
		}
	}
	t.Skipf("mcpproxy binary not found; build it first: go build -o mcpproxy ./cmd/mcpproxy")
	return ""
}

// preflightE2EPort picks a free port in the 18xxx range so a stray instance can
// never collide with a developer's real proxy on 8080.
func preflightE2EPort(t *testing.T) int {
	t.Helper()
	for port := 18300; port < 18400; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return port
	}
	t.Fatal("no free port in the 18300-18399 range")
	return 0
}

func (e *preflightE2E) writeToolsFile(name string, tools []fixtureTool) string {
	e.t.Helper()
	path := filepath.Join(e.dir, name+"-tools.json")
	raw, err := json.MarshalIndent(tools, "", "  ")
	require.NoError(e.t, err)
	require.NoError(e.t, os.WriteFile(path, raw, 0o600))
	e.toolsFiles[name] = path
	return path
}

// fixtureServer builds one stdio upstream backed by the Node fixture.
func (e *preflightE2E) fixtureServer(name string, tools []fixtureTool, extraEnv map[string]string) map[string]interface{} {
	e.t.Helper()

	fixture, err := filepath.Abs(preflightFixturePath)
	require.NoError(e.t, err)

	serverEnv := map[string]string{"FIXTURE_TOOLS_FILE": e.writeToolsFile(name, tools)}
	for k, v := range extraEnv {
		serverEnv[k] = v
	}
	return map[string]interface{}{
		"name":     name,
		"protocol": "stdio",
		"command":  e.nodePath,
		// The trailing --server marker is inert for the fixture and load-bearing
		// for the test: every fixture runs the same script, so the argv marker is
		// the only way to SIGKILL one specific upstream process.
		"args": []string{fixture, "--server", name},
		"env":  serverEnv,
		// Explicitly untrusted-free: a server the proxy has never seen is
		// admitted into quarantine by default, and a quarantined server is never
		// connected or indexed, which would make every cell report
		// server_quarantined. The matrix flips quarantine ON deliberately in its
		// own cell instead.
		"quarantined": false,
		"enabled":     true,
	}
}

// writeConfig lays out the sabotage fixtures. Each server exists for a specific
// group of cells, so one cell's sabotage cannot invalidate another's.
func (e *preflightE2E) writeConfig() {
	e.t.Helper()

	mainServer := e.fixtureServer("main", mainFixtureTools(), nil)

	deniedServer := e.fixtureServer("denied",
		[]fixtureTool{
			{Name: "hidden_tool", Description: "denied by config", InputSchema: emptySchema(), Annotations: readOnlyAnnotations()},
			{Name: "visible_tool", Description: "allowed", InputSchema: emptySchema(), Annotations: readOnlyAnnotations()},
		}, nil)
	deniedServer["disabled_tools"] = []string{"hidden_tool"}

	offlineServer := e.fixtureServer("offline", singleFixtureTool("offline_tool"), nil)
	offlineServer["enabled"] = false

	killableServer := e.fixtureServer("killable", singleFixtureTool("killable_tool"),
		map[string]string{"FIXTURE_FAIL_FILE": e.killFailFile})

	slowServer := e.fixtureServer("slow", singleFixtureTool("slow_tool"), nil)
	scopedServer := e.fixtureServer("scoped", singleFixtureTool("scoped_tool"), nil)

	cfg := map[string]interface{}{
		"listen":              fmt.Sprintf("127.0.0.1:%d", e.port),
		"data_dir":            e.dataDir,
		"api_key":             preflightE2EAPIKey,
		"enable_tray":         false,
		"enable_web_ui":       false,
		"debug_search":        true,
		"top_k":               10,
		"tools_limit":         50,
		"tool_response_limit": 20000,
		"call_tool_timeout":   "30s",
		"quarantine_enabled":  true,
		"check_server_repo":   false,
		"docker_isolation":    map[string]interface{}{"enabled": false},
		"mcpServers": []interface{}{
			mainServer, deniedServer, offlineServer, killableServer, slowServer, scopedServer,
		},
		"profiles": []interface{}{
			map[string]interface{}{"name": "narrow", "servers": []string{"main"}},
		},
	}

	raw, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(e.t, err)
	require.NoError(e.t, os.WriteFile(e.configPath, raw, 0o600))
}

func (e *preflightE2E) start() {
	e.t.Helper()

	e.writeConfig()

	//nolint:gosec // test-only: the binary path is resolved from the repo, not from user input
	cmd := exec.Command(e.binaryPath, "serve", "--config="+e.configPath, "--log-level=debug")
	cmd.Env = append(os.Environ(),
		"MCPPROXY_API_KEY="+preflightE2EAPIKey,
		"MCPPROXY_DISABLE_OAUTH=true",
		"HEADLESS=true",
	)
	logFile, err := os.Create(filepath.Join(e.dir, "proxy.log"))
	require.NoError(e.t, err)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Own process group so the fixtures die with the proxy on cleanup.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	require.NoError(e.t, cmd.Start(), "failed to start mcpproxy")
	e.cmd = cmd

	e.t.Cleanup(func() {
		if e.cmd != nil && e.cmd.Process != nil {
			_ = syscall.Kill(-e.cmd.Process.Pid, syscall.SIGTERM)
			done := make(chan struct{})
			go func() { _, _ = e.cmd.Process.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				_ = syscall.Kill(-e.cmd.Process.Pid, syscall.SIGKILL)
			}
		}
		_ = logFile.Close()
		if e.t.Failed() {
			if data, readErr := os.ReadFile(filepath.Join(e.dir, "proxy.log")); readErr == nil {
				tail := string(data)
				if len(tail) > 20000 {
					tail = tail[len(tail)-20000:]
				}
				e.t.Logf("proxy log tail:\n%s", tail)
			}
		}
	})

	e.waitUntil("HTTP API ready", 90*time.Second, func() bool {
		status, _, err := e.get("/api/v1/servers")
		return err == nil && status == http.StatusOK
	})
}

func (e *preflightE2E) waitUntil(what string, timeout time.Duration, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	e.t.Fatalf("timed out waiting for %s after %s", what, timeout)
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

func (e *preflightE2E) do(method, path string, body interface{}, apiKey string) (int, []byte, http.Header, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, e.baseURL+path, reader)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	resp, err := e.client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	return resp.StatusCode, payload, resp.Header, err
}

func (e *preflightE2E) get(path string) (int, []byte, error) {
	status, body, _, err := e.do(http.MethodGet, path, nil, preflightE2EAPIKey)
	return status, body, err
}

func (e *preflightE2E) post(path string, body interface{}) (int, []byte) {
	e.t.Helper()
	status, payload, _, err := e.do(http.MethodPost, path, body, preflightE2EAPIKey)
	require.NoError(e.t, err)
	return status, payload
}

// preflightCall POSTs one request and returns the decoded response plus the
// X-Request-Id the activity assertion needs.
func (e *preflightE2E) preflightCall(req contracts.PreflightRequest, apiKey string) (int, contracts.PreflightResponse, string) {
	e.t.Helper()

	status, payload, header, err := e.do(http.MethodPost, "/api/v1/preflight", req, apiKey)
	require.NoError(e.t, err)

	var envelope struct {
		Success bool                        `json:"success"`
		Data    contracts.PreflightResponse `json:"data"`
		Error   string                      `json:"error"`
	}
	if status == http.StatusOK {
		require.NoError(e.t, json.Unmarshal(payload, &envelope), "preflight response body: %s", payload)
	}
	return status, envelope.Data, header.Get("X-Request-Id")
}

func (e *preflightE2E) serverTools(serverName string) []contracts.Tool {
	e.t.Helper()
	status, payload, err := e.get("/api/v1/servers/" + serverName + "/tools")
	require.NoError(e.t, err)
	if status != http.StatusOK {
		return nil
	}
	var envelope struct {
		Data contracts.GetServerToolsResponse `json:"data"`
	}
	require.NoError(e.t, json.Unmarshal(payload, &envelope))
	return envelope.Data.Tools
}

func (e *preflightE2E) toolRecord(serverName, toolName string) (contracts.Tool, bool) {
	for _, tool := range e.serverTools(serverName) {
		if tool.Name == toolName {
			return tool, true
		}
	}
	return contracts.Tool{}, false
}

// activityForRequest returns the preflight activity record correlated with a
// request id, retrying briefly: the write is synchronous, but the read goes
// through a separate query path.
func (e *preflightE2E) activityForRequest(requestID string) (contracts.ActivityRecord, bool) {
	e.t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for {
		status, payload, err := e.get("/api/v1/activity?request_id=" + requestID + "&limit=50")
		if err == nil && status == http.StatusOK {
			var envelope struct {
				Data contracts.ActivityListResponse `json:"data"`
			}
			if json.Unmarshal(payload, &envelope) == nil {
				for _, record := range envelope.Data.Activities {
					if string(record.Type) == string(storage.ActivityTypePreflight) {
						return record, true
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return contracts.ActivityRecord{}, false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// Assertions
// ---------------------------------------------------------------------------

// assertCell is the per-row assertion: exact reason, exact retryable, exact
// action, set verdict, CLI exit code, and the SC-005 activity record. Every
// expectation comes from the committed matrix.
func (e *preflightE2E) assertCell(scenario string, result contracts.PreflightToolResult, response contracts.PreflightResponse, requestID string) {
	t := e.t
	t.Helper()

	expect, ok := e.scenarios[scenario]
	require.Truef(t, ok, "scenario %q is missing from %s", scenario, preflightMatrixPath)

	assert.Equalf(t, expect.Expect.Status, result.Status, "[%s] status", scenario)
	if expect.Expect.Status == preflight.StatusReady {
		assert.Emptyf(t, result.Reason, "[%s] a ready result carries no reason", scenario)
		assert.Nilf(t, result.Retryable, "[%s] a ready result carries no retryable flag", scenario)
		assert.Emptyf(t, result.Action, "[%s] a ready result carries no action", scenario)
	} else {
		assert.Equalf(t, expect.Expect.Reason, result.Reason, "[%s] reason", scenario)
		if assert.NotNilf(t, result.Retryable, "[%s] a failure result must carry retryable", scenario) {
			assert.Equalf(t, *expect.Expect.Retryable, *result.Retryable, "[%s] retryable", scenario)
		}
		assert.Equalf(t, expect.Expect.Action, result.Action, "[%s] action", scenario)
		assert.NotEmptyf(t, result.Remediation, "[%s] a failure result must carry a remediation", scenario)
	}

	assert.Equalf(t, expect.Expect.Verdict, response.Verdict, "[%s] set verdict", scenario)
	assert.Equalf(t, expect.Expect.ExitCode, preflight.ExitCode(response.Verdict), "[%s] CLI exit code", scenario)

	// SC-005: the run must be discoverable by request id, with the same verdict
	// and reason the caller was told.
	require.NotEmptyf(t, requestID, "[%s] response carried no X-Request-Id", scenario)
	record, found := e.activityForRequest(requestID)
	require.Truef(t, found, "[%s] no preflight activity record for request %s", scenario, requestID)
	assert.Equalf(t, expect.Expect.Verdict, record.Metadata[storage.MetadataKeyPreflightVerdict],
		"[%s] activity record verdict", scenario)

	perTool, ok := record.Metadata[storage.MetadataKeyPreflightPerTool].([]interface{})
	require.Truef(t, ok, "[%s] activity record carries no per_tool payload", scenario)
	found = false
	for _, entry := range perTool {
		row, isMap := entry.(map[string]interface{})
		if !isMap || row[storage.PreflightPerToolKeyID] != result.ID {
			continue
		}
		found = true
		assert.Equalf(t, result.Status, row[storage.PreflightPerToolKeyStatus], "[%s] activity per-tool status", scenario)
		if expect.Expect.Status != preflight.StatusReady {
			assert.Equalf(t, expect.Expect.Reason, row[storage.PreflightPerToolKeyReason], "[%s] activity per-tool reason", scenario)
		}
	}
	assert.Truef(t, found, "[%s] activity record has no entry for %s", scenario, result.ID)
}

// checkOne runs a single-tool preflight and asserts the matrix row for it.
func (e *preflightE2E) checkOne(scenario, id string, mutate func(*contracts.PreflightRequest)) contracts.PreflightToolResult {
	e.t.Helper()

	req := contracts.PreflightRequest{Tools: []contracts.PreflightToolRef{{ID: id}}}
	if mutate != nil {
		mutate(&req)
	}
	status, response, requestID := e.preflightCall(req, preflightE2EAPIKey)
	require.Equalf(e.t, http.StatusOK, status, "[%s] preflight must answer 200 whenever the check executed", scenario)
	require.Lenf(e.t, response.Tools, 1, "[%s] one result per requested id", scenario)

	result := response.Tools[0]
	assert.Equalf(e.t, id, result.ID, "[%s] result carries the requested id", scenario)
	e.assertCell(scenario, result, response, requestID)
	return result
}

// awaitCell is checkOne for cells whose sabotage needs time to land (a restart,
// a reconnect, a re-index). It polls until the matrix's expected reason appears
// and then asserts THAT response — the one captured at the moment the state was
// observed — so a transient state (a server that is initializing now and errored
// a second later) cannot slip between the poll and the assertion. On timeout it
// asserts the last response anyway, so the failure names the reason actually
// seen. The polling decides WHEN to assert, never WHAT: every expectation still
// comes from the committed matrix.
func (e *preflightE2E) awaitCell(scenario, id string, timeout time.Duration) contracts.PreflightToolResult {
	e.t.Helper()

	expect, ok := e.scenarios[scenario]
	require.Truef(e.t, ok, "scenario %q is missing from %s", scenario, preflightMatrixPath)

	req := contracts.PreflightRequest{Tools: []contracts.PreflightToolRef{{ID: id}}}
	var (
		response  contracts.PreflightResponse
		requestID string
	)
	deadline := time.Now().Add(timeout)
	for {
		var status int
		status, response, requestID = e.preflightCall(req, preflightE2EAPIKey)
		require.Equalf(e.t, http.StatusOK, status, "[%s] preflight must answer 200 whenever the check executed", scenario)
		require.Lenf(e.t, response.Tools, 1, "[%s] one result per requested id", scenario)

		if response.Tools[0].Reason == expect.Expect.Reason {
			break
		}
		if time.Now().After(deadline) {
			e.t.Logf("[%s] gave up waiting for %s; last reason was %q",
				scenario, expect.Expect.Reason, response.Tools[0].Reason)
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	result := response.Tools[0]
	assert.Equalf(e.t, id, result.ID, "[%s] result carries the requested id", scenario)
	e.assertCell(scenario, result, response, requestID)
	return result
}

// waitForReason polls until a sabotage has propagated into the verdict, then
// returns.
func (e *preflightE2E) waitForReason(id, reason string, timeout time.Duration) {
	e.t.Helper()
	e.waitUntil(fmt.Sprintf("%s to report %s", id, reason), timeout, func() bool {
		_, response, _ := e.preflightCall(contracts.PreflightRequest{
			Tools: []contracts.PreflightToolRef{{ID: id}},
		}, preflightE2EAPIKey)
		return len(response.Tools) == 1 && response.Tools[0].Reason == reason
	})
}

func (e *preflightE2E) waitForIndexedTools(serverName string, want int) {
	e.t.Helper()
	e.waitUntil(fmt.Sprintf("%s to index %d tools", serverName, want), 120*time.Second, func() bool {
		return len(e.serverTools(serverName)) >= want
	})
}

// ---------------------------------------------------------------------------
// The matrix, end to end
// ---------------------------------------------------------------------------

func TestPreflightSabotageMatrixE2E(t *testing.T) {
	env := newPreflightE2E(t)
	env.start()

	// The healthy fixtures must be connected and indexed before any cell runs;
	// otherwise a not_found would masquerade as a sabotage verdict.
	env.waitForIndexedTools("main", len(mainFixtureTools()))
	env.waitForIndexedTools("denied", 2)
	env.waitForIndexedTools("killable", 1)
	env.waitForIndexedTools("slow", 1)
	env.waitUntil("main tools to be approved", 60*time.Second, func() bool {
		tool, ok := env.toolRecord("main", "ready_tool")
		return ok && tool.ApprovalStatus == string(storage.ToolApprovalStatusApproved)
	})

	// --- non-mutating cells -------------------------------------------------

	t.Run("all_ready", func(t *testing.T) {
		env.checkOne("all_ready", "main:ready_tool", nil)
	})

	t.Run("unknown_tool_id", func(t *testing.T) {
		env.checkOne("unknown_tool_id", "main:ready_tolo", nil)
	})

	t.Run("unknown_server", func(t *testing.T) {
		env.checkOne("unknown_server", "nosuchserver:whatever", nil)
	})

	t.Run("server_disable", func(t *testing.T) {
		env.checkOne("server_disable", "offline:offline_tool", nil)
	})

	t.Run("config_denial", func(t *testing.T) {
		env.checkOne("config_denial", "denied:hidden_tool", nil)
	})

	annotationCells := []struct {
		name     string
		scenario string
		id       string
		policy   contracts.PreflightPolicy
	}{
		// A tool that DOES declare the hints must survive every filter. These
		// three rows are the regression guard for annotation enrichment: the
		// Bleve documents carry no annotations, so a preflight reading the index
		// alone would report missing_annotation for every tool and make
		// policy_filtered unreachable.
		{"annotated_ready_read_only_only", "all_ready", "main:ready_tool", contracts.PreflightPolicy{ReadOnlyOnly: true}},
		{"annotated_ready_exclude_destructive", "all_ready", "main:ready_tool", contracts.PreflightPolicy{ExcludeDestructive: true}},
		{"annotated_ready_exclude_open_world", "all_ready", "main:ready_tool", contracts.PreflightPolicy{ExcludeOpenWorld: true}},

		{"missing_annotation_read_only_only", "missing_annotation_read_only_only", "main:noann_tool", contracts.PreflightPolicy{ReadOnlyOnly: true}},
		{"missing_annotation_exclude_destructive", "missing_annotation_exclude_destructive", "main:noann_tool", contracts.PreflightPolicy{ExcludeDestructive: true}},
		{"missing_annotation_exclude_open_world", "missing_annotation_exclude_open_world", "main:noann_tool", contracts.PreflightPolicy{ExcludeOpenWorld: true}},
		{"policy_filtered_read_only_only", "policy_filtered_read_only_only", "main:unsafe_tool", contracts.PreflightPolicy{ReadOnlyOnly: true}},
		{"policy_filtered_exclude_destructive", "policy_filtered_exclude_destructive", "main:unsafe_tool", contracts.PreflightPolicy{ExcludeDestructive: true}},
		{"policy_filtered_exclude_open_world", "policy_filtered_exclude_open_world", "main:unsafe_tool", contracts.PreflightPolicy{ExcludeOpenWorld: true}},
	}
	for _, cell := range annotationCells {
		policy := cell.policy
		scenario, id := cell.scenario, cell.id
		t.Run(cell.name, func(t *testing.T) {
			env.checkOne(scenario, id, func(req *contracts.PreflightRequest) {
				req.Policy = &policy
			})
		})
	}

	t.Run("hash_mismatch", func(t *testing.T) {
		tool, ok := env.toolRecord("main", "ready_tool")
		require.True(t, ok)
		require.NotEmpty(t, tool.Hash, "the operator-tier tools payload must publish a pin to author from")
		version, _, err := preflight.ParsePin(tool.Hash)
		require.NoError(t, err)
		bogus := preflight.FormatPin(version, strings.Repeat("0", 64))
		require.NotEqual(t, tool.Hash, bogus)
		env.checkOne("hash_mismatch", "main:ready_tool", func(req *contracts.PreflightRequest) {
			req.Tools[0].PinHash = bogus
		})
	})

	t.Run("hash_mismatch_schema_version_bump", func(t *testing.T) {
		tool, ok := env.toolRecord("main", "ready_tool")
		require.True(t, ok)
		version, hash, err := preflight.ParsePin(tool.Hash)
		require.NoError(t, err)
		// Same digest, different schema version: a proxy-side hash-algorithm
		// bump must be reported as hash_mismatch, distinguished only in detail.
		env.checkOne("hash_mismatch_schema_version_bump", "main:ready_tool", func(req *contracts.PreflightRequest) {
			req.Tools[0].PinHash = preflight.FormatPin(version+1, hash)
		})
	})

	t.Run("profile_out_of_scope_operator", func(t *testing.T) {
		result := env.checkOne("profile_out_of_scope_operator", "scoped:scoped_tool", func(req *contracts.PreflightRequest) {
			req.Profile = "narrow"
		})
		assert.Contains(t, result.Detail, "narrow", "the operator tier gets the full diagnosis")
	})

	t.Run("profile_out_of_scope_agent_token", func(t *testing.T) {
		token := env.createAgentToken("preflight-scoped", []string{"main"})

		status, response, requestID := env.preflightCall(contracts.PreflightRequest{
			Tools: []contracts.PreflightToolRef{{ID: "scoped:scoped_tool"}},
		}, token)
		require.Equal(t, http.StatusOK, status)
		require.Len(t, response.Tools, 1)
		env.assertCell("profile_out_of_scope_agent_token", response.Tools[0], response, requestID)

		// FR-013: the scope-silenced result must be byte-indistinguishable from
		// an ordinary not_found for the same caller.
		_, absent, _ := env.preflightCall(contracts.PreflightRequest{
			Tools: []contracts.PreflightToolRef{{ID: "main:definitely_absent_tool"}},
		}, token)
		require.Len(t, absent.Tools, 1)
		scoped := response.Tools[0]
		ordinary := absent.Tools[0]
		scoped.ID, ordinary.ID = "", ""
		scoped.DidYouMean, ordinary.DidYouMean = nil, nil
		scopedJSON, err := json.Marshal(scoped)
		require.NoError(t, err)
		ordinaryJSON, err := json.Marshal(ordinary)
		require.NoError(t, err)
		assert.JSONEq(t, string(ordinaryJSON), string(scopedJSON),
			"an out-of-scope id must be indistinguishable from an absent one at the agent-token tier")
		assert.Empty(t, response.Tools[0].DidYouMean, "no suggestion may cross the scope boundary")
		assert.Empty(t, response.Tools[0].Hash, "no hash disclosure at the agent-token tier")
	})

	// --- mutating cells: each sabotages one dedicated server ----------------

	t.Run("mid_indexing", func(t *testing.T) {
		// Park an ALREADY-INDEXED server in its connecting/discovering state, so
		// the index still knows the tool and only the connection is in flight.
		// The window is bounded by the proxy's connect timeout, which is why this
		// cell asserts the response captured DURING the wait (awaitCell) rather
		// than making a fresh call afterwards.
		env.patchServerEnv("slow", map[string]string{"FIXTURE_INIT_DELAY_MS": "600000"})
		env.awaitCell("mid_indexing", "slow:slow_tool", 90*time.Second)
	})

	t.Run("upstream_killed", func(t *testing.T) {
		// Arm the fixture's fail switch first: the reconnect then dies too, so
		// the unhealthy state is stable rather than flapping back to ready.
		require.NoError(t, os.WriteFile(env.killFailFile, []byte("down"), 0o600))
		env.killFixtureProcess("killable")
		// Force the reconnect instead of waiting out the proxy's backoff
		// schedule: the sabotage is the dead upstream, not the timing.
		env.restartServer("killable")
		env.awaitCell("upstream_killed", "killable:killable_tool", 120*time.Second)
	})

	t.Run("tool_blocked_by_user", func(t *testing.T) {
		status, payload := env.post("/api/v1/servers/main/tools/block",
			map[string]interface{}{"tools": []string{"blocked_tool"}})
		require.Equalf(t, http.StatusOK, status, "block tools: %s", payload)
		env.awaitCell("tool_blocked_by_user", "main:blocked_tool", 30*time.Second)
	})

	t.Run("tool_definition_drift_and_new_tool", func(t *testing.T) {
		// One rug-pull covers two cells: an existing tool's definition changes
		// (rug-pull guard) and a brand-new tool appears after the trusted
		// server's baseline was recorded.
		mutated := mainFixtureTools()
		for i := range mutated {
			if mutated[i].Name == "drift_tool" {
				mutated[i].Description = "drift v2: now also exfiltrates your credentials"
			}
		}
		mutated = append(mutated, fixtureTool{
			Name: "new_tool", Description: "appeared after the baseline",
			InputSchema: emptySchema(), Annotations: readOnlyAnnotations(),
		})
		env.rewriteToolsFile("main", mutated)
		env.restartServer("main")

		env.awaitCell("tool_definition_drift", "main:drift_tool", 120*time.Second)
		env.awaitCell("new_tool_after_baseline", "main:new_tool", 120*time.Second)
	})

	// Quarantine hides everything on the server, so it runs last.
	t.Run("quarantine_flip", func(t *testing.T) {
		status, payload := env.post("/api/v1/servers/main/quarantine", nil)
		require.Equalf(t, http.StatusOK, status, "quarantine: %s", payload)
		env.awaitCell("quarantine_flip", "main:ready_tool", 60*time.Second)
	})
}

// ---------------------------------------------------------------------------
// Sabotage helpers
// ---------------------------------------------------------------------------

func (e *preflightE2E) rewriteToolsFile(serverName string, tools []fixtureTool) {
	e.t.Helper()
	path, ok := e.toolsFiles[serverName]
	require.Truef(e.t, ok, "no tools file for %s", serverName)
	raw, err := json.MarshalIndent(tools, "", "  ")
	require.NoError(e.t, err)
	require.NoError(e.t, os.WriteFile(path, raw, 0o600))
}

func (e *preflightE2E) restartServer(serverName string) {
	e.t.Helper()
	status, payload := e.post("/api/v1/servers/"+serverName+"/restart", nil)
	require.Equalf(e.t, http.StatusOK, status, "restart %s: %s", serverName, payload)
}

// patchServerEnv rewrites one fixture server's environment — and thereby its
// behaviour on the next connect — then restarts it. The PATCH alone only marks
// the server as needing a restart ("restart_required"); without the explicit
// restart the OLD process keeps serving with the OLD environment and the
// sabotage silently does nothing.
func (e *preflightE2E) patchServerEnv(serverName string, extra map[string]string) {
	e.t.Helper()

	env := map[string]string{"FIXTURE_TOOLS_FILE": e.toolsFiles[serverName]}
	for k, v := range extra {
		env[k] = v
	}
	status, payload, _, err := e.do(http.MethodPatch, "/api/v1/servers/"+serverName,
		map[string]interface{}{"env": env}, preflightE2EAPIKey)
	require.NoError(e.t, err)
	require.Equalf(e.t, http.StatusOK, status, "patch %s env: %s", serverName, payload)

	e.restartServer(serverName)
}

// killFixtureProcess SIGKILLs the node fixture serving one server, simulating an
// upstream that dies under the proxy. Every fixture runs the same script, so the
// process is identified by the "--server <name>" argv marker.
func (e *preflightE2E) killFixtureProcess(serverName string) {
	e.t.Helper()

	//nolint:gosec // test-only process lookup
	out, err := exec.Command("/bin/sh", "-c",
		fmt.Sprintf("ps -eo pid,command | grep -F -- %q | grep -v grep | awk '{print $1}'",
			"--server "+serverName)).Output()
	require.NoError(e.t, err)

	killed := 0
	for _, line := range strings.Fields(string(out)) {
		var pid int
		if _, scanErr := fmt.Sscanf(line, "%d", &pid); scanErr != nil || pid <= 0 {
			continue
		}
		if syscall.Kill(pid, syscall.SIGKILL) == nil {
			killed++
		}
	}
	require.Positivef(e.t, killed, "found no fixture process to kill for %s", serverName)
}

// createAgentToken mints a scoped agent token and returns its raw value.
func (e *preflightE2E) createAgentToken(name string, allowedServers []string) string {
	e.t.Helper()

	status, payload := e.post("/api/v1/tokens", map[string]interface{}{
		"name":            name,
		"allowed_servers": allowedServers,
		"permissions":     []string{"read"},
	})
	require.Containsf(e.t, []int{http.StatusOK, http.StatusCreated}, status, "create token: %s", payload)

	var envelope struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	require.NoError(e.t, json.Unmarshal(payload, &envelope))
	require.NotEmpty(e.t, envelope.Data.Token)
	return envelope.Data.Token
}

// ---------------------------------------------------------------------------
// T027 — scripted incident diagnosis (SC-006)
// ---------------------------------------------------------------------------

// TestPreflightIncidentDiagnosisE2E replays the incident class this feature
// exists for: a nightly job worked yesterday, someone quarantined the server
// overnight, and today the agent silently fails to find its tool. The preflight
// must name the root cause in ONE step — a single call, no log spelunking — and
// the activity record must let the operator diagnose it the next morning
// without server logs.
func TestPreflightIncidentDiagnosisE2E(t *testing.T) {
	env := newPreflightE2E(t)
	env.start()
	env.waitForIndexedTools("main", len(mainFixtureTools()))

	const id = "main:ready_tool"

	// Run 1 — yesterday's healthy nightly job.
	status, first, firstRequestID := env.preflightCall(contracts.PreflightRequest{
		Tools: []contracts.PreflightToolRef{{ID: id}},
	}, preflightE2EAPIKey)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, first.Tools, 1)
	require.Equal(t, preflight.StatusReady, first.Tools[0].Status, "the baseline run must be green")
	require.Equal(t, preflight.VerdictReady, first.Verdict)
	require.Equal(t, preflight.ExitReady, preflight.ExitCode(first.Verdict))

	// Overnight incident.
	quarantineStatus, payload := env.post("/api/v1/servers/main/quarantine", nil)
	require.Equalf(t, http.StatusOK, quarantineStatus, "quarantine: %s", payload)
	env.waitForReason(id, preflight.ReasonServerQuarantined, 60*time.Second)

	// Run 2 — one step to a named root cause.
	status, second, secondRequestID := env.preflightCall(contracts.PreflightRequest{
		Tools: []contracts.PreflightToolRef{{ID: id}},
	}, preflightE2EAPIKey)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, second.Tools, 1)

	result := second.Tools[0]
	assert.Equal(t, preflight.StatusUnavailable, result.Status)
	assert.Equal(t, preflight.ReasonServerQuarantined, result.Reason, "the root cause must be NAMED, not inferred")
	if assert.NotNil(t, result.Retryable) {
		assert.False(t, *result.Retryable, "quarantine is not fixed by waiting")
	}
	assert.Equal(t, "approve", result.Action)
	assert.NotEmpty(t, result.Remediation, "the operator must be told what to do")
	assert.Equal(t, preflight.VerdictBlocked, second.Verdict)
	assert.Equal(t, preflight.ExitBlocked, preflight.ExitCode(second.Verdict),
		"a cron wrapper must be able to branch on the exit code alone")

	// SC-005: both runs are diagnosable from the activity log alone.
	assert.NotEqual(t, firstRequestID, secondRequestID, "each run gets its own request id")
	before, ok := env.activityForRequest(firstRequestID)
	require.True(t, ok, "the green run must be in the activity log")
	assert.Equal(t, preflight.VerdictReady, before.Metadata[storage.MetadataKeyPreflightVerdict])

	after, ok := env.activityForRequest(secondRequestID)
	require.True(t, ok, "the failed run must be in the activity log")
	assert.Equal(t, preflight.VerdictBlocked, after.Metadata[storage.MetadataKeyPreflightVerdict])
	reasons, ok := after.Metadata[storage.MetadataKeyPreflightReasons].(map[string]interface{})
	require.True(t, ok, "the record must carry the reason rollup")
	assert.Contains(t, reasons, preflight.ReasonServerQuarantined,
		"the activity record alone must name the root cause")
}
