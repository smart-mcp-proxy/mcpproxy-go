package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/gatereport"
)

// Matrix cell names (FR-006). Server names get a "gate-" prefix so pgrep /
// docker-name-prefix cleanup can never touch anything the user runs.
var allCells = []string{"stdio", "http", "sse", "docker", "oauth"}

const (
	gateServerPrefix   = "gate-"
	dockerFixtureImage = "mcpfixture:gate"
	dockerNamePrefix   = "mcpproxy-gate-docker"
)

type matrixOpts struct {
	Binary      string // candidate ./mcpproxy
	Fixture     string // ./mcpfixture (stage-1 binary)
	OAuthBinary string // tests/oauthserver/cmd/server binary
	ReportDir   string
	StateFile   string
	WorkDir     string
	Cells       []string
	CellTimeout time.Duration
	Verbose     bool
}

// matrixRun owns the candidate core + fixture lifecycle for one gate run.
type matrixRun struct {
	opts    matrixOpts
	workDir string
	client  *Client
	core    *coreProc

	fixtures     map[string]*fixtureProc // driver-owned: http, sse, oauth (IdP+MCP)
	killPatterns map[string]string       // cell -> unique pgrep -f pattern (stdio)
	issued       []issuedCall
	before       *counterSnapshot

	oauthServer string // current oauth upstream name (changes after IdP restart re-add)
	oauthPort   int
	authDoneAt  time.Time // when the initial oauth authorization completed
}

// runMatrix executes the server-type matrix (US1, FR-006..FR-010) and writes
// one fragment per cell. Returns a non-nil error only for harness-level
// failures; cell failures are reported through fragments + exit code.
func runMatrix(ctx context.Context, opts matrixOpts) (bool, error) {
	if len(opts.Cells) == 0 {
		opts.Cells = allCells
	}
	workDir, err := mkWorkDir(opts.WorkDir, "gate-matrix-")
	if err != nil {
		return false, err
	}
	m := &matrixRun{
		opts:         opts,
		workDir:      workDir,
		fixtures:     make(map[string]*fixtureProc),
		killPatterns: make(map[string]string),
		oauthServer:  gateServerPrefix + "oauth",
	}
	logf("matrix: work dir %s", workDir)

	selected := make(map[string]bool)
	for _, c := range opts.Cells {
		selected[c] = true
	}

	// FR-009 preflight: docker cell fails (infrastructure) when Docker is
	// unusable — never skipped, never un-isolated.
	dockerErr := error(nil)
	if selected["docker"] {
		dockerErr = dockerPreflight(ctx, dockerFixtureImage)
		if dockerErr != nil {
			logf("matrix: docker preflight failed: %v", dockerErr)
		}
	}

	servers, err := m.startFixturesAndBuildServers(ctx, selected, dockerErr == nil)
	if err != nil {
		m.teardown(ctx)
		return false, err
	}

	// Global isolation flag drives the docker cell (host-run stdio fixtures
	// carry explicit per-server opt-outs).
	var dockerIsolation map[string]any
	if selected["docker"] && dockerErr == nil {
		dockerIsolation = map[string]any{"enabled": true}
	}

	if err := m.bootCore(ctx, servers, dockerIsolation); err != nil {
		m.teardown(ctx)
		return false, err
	}

	// FR-012 baseline: snapshot counters as early as possible, before the
	// upstream servers finish connecting/indexing and before any traffic.
	before, err := takeCounterSnapshot(ctx, m.client)
	if err != nil {
		logf("matrix: WARNING: baseline counter snapshot failed: %v", err)
		before = &counterSnapshot{TakenAt: time.Now().UTC()}
	}
	m.before = before

	allGreen := true
	for _, cell := range allCells {
		var frag gatereport.Fragment
		switch {
		case !selected[cell]:
			frag = gatereport.Fragment{
				Name:   "matrix/" + cell,
				Status: gatereport.StatusSkipped,
				Reason: "cell not selected for this run (--cells)",
			}
		case cell == "docker" && dockerErr != nil:
			frag = gatereport.Fragment{
				Name:           "matrix/docker",
				Status:         gatereport.StatusFail,
				Reason:         dockerErr.Error(),
				Classification: gatereport.ClassificationInfrastructure,
			}
		default:
			frag = m.runCell(ctx, cell)
		}
		if frag.Status != gatereport.StatusPass && frag.Status != gatereport.StatusFlaky {
			allGreen = false
		}
		if err := gatereport.WriteFragment(opts.ReportDir, &frag); err != nil {
			return false, err
		}
		logf("matrix: %s -> %s %s", frag.Name, frag.Status, frag.Reason)
	}

	if opts.StateFile != "" {
		if err := writeState(opts.StateFile, m.buildState()); err != nil {
			return false, err
		}
		logf("matrix: core left running (pid %d) for invariants; state: %s", m.core.PID, opts.StateFile)
	} else {
		m.teardown(ctx)
	}
	return allGreen, nil
}

// startFixturesAndBuildServers launches driver-owned fixtures for the
// selected cells and returns the mcpServers entries for the gate config.
func (m *matrixRun) startFixturesAndBuildServers(ctx context.Context, selected map[string]bool, dockerOK bool) ([]gateServerConfig, error) {
	var servers []gateServerConfig

	if selected["stdio"] {
		// Unique per-cell binary path => unique, user-safe pgrep pattern.
		stdioBin := filepath.Join(m.workDir, "bin", "mcpfixture-stdio")
		if err := copyFile(m.opts.Fixture, stdioBin); err != nil {
			return nil, fmt.Errorf("copy stdio fixture: %w", err)
		}
		m.killPatterns["stdio"] = stdioBin
		servers = append(servers, gateServerConfig{
			"name": gateServerPrefix + "stdio", "command": stdioBin,
			"args": []string{"--transport", "stdio"}, "protocol": "stdio",
			"enabled": true, "quarantined": false,
			// Explicit opt-out: this fixture runs the host binary and must
			// stay native when the docker cell turns global isolation on.
			"isolation": map[string]any{"enabled": false},
		})
	}

	if selected["http"] {
		port, err := freePort()
		if err != nil {
			return nil, err
		}
		proc, err := startFixture("http", m.opts.Fixture,
			[]string{"--transport", "http", "--port", fmt.Sprint(port)},
			port, filepath.Join(m.workDir, "fixture-http.log"))
		if err != nil {
			return nil, err
		}
		m.fixtures["http"] = proc
		if err := waitTCP(fmt.Sprintf("127.0.0.1:%d", port), 15*time.Second); err != nil {
			return nil, fmt.Errorf("http fixture: %w", err)
		}
		servers = append(servers, gateServerConfig{
			"name": gateServerPrefix + "http", "url": fmt.Sprintf("http://127.0.0.1:%d/mcp", port),
			"protocol": "http", "enabled": true, "quarantined": false,
		})
	}

	if selected["sse"] {
		port, err := freePort()
		if err != nil {
			return nil, err
		}
		proc, err := startFixture("sse", m.opts.Fixture,
			[]string{"--transport", "sse", "--port", fmt.Sprint(port)},
			port, filepath.Join(m.workDir, "fixture-sse.log"))
		if err != nil {
			return nil, err
		}
		m.fixtures["sse"] = proc
		if err := waitTCP(fmt.Sprintf("127.0.0.1:%d", port), 15*time.Second); err != nil {
			return nil, fmt.Errorf("sse fixture: %w", err)
		}
		servers = append(servers, gateServerConfig{
			"name": gateServerPrefix + "sse", "url": fmt.Sprintf("http://127.0.0.1:%d/sse", port),
			"protocol": "sse", "enabled": true, "quarantined": false,
		})
	}

	if selected["docker"] && dockerOK {
		// The docker cell inherits the GLOBAL docker_isolation.enabled=true
		// this run sets (see buildGateConfig) and overrides only the image.
		// Per-server opt-ins alone (legacy enabled bool AND the mode enum)
		// were verified NOT to engage isolation when the global flag is off
		// — the command would silently run un-isolated on the host, exactly
		// what FR-009 forbids — so the gate drives isolation from the global
		// flag and opts the host-run stdio fixtures out per-server.
		servers = append(servers, gateServerConfig{
			"name": gateServerPrefix + "docker", "command": "/mcpfixture",
			"args": []string{"--transport", "stdio"}, "protocol": "stdio",
			"enabled": true, "quarantined": false,
			"isolation": map[string]any{"image": dockerFixtureImage},
		})
	}

	if selected["oauth"] {
		if m.opts.OAuthBinary == "" {
			return nil, fmt.Errorf("--oauth-server binary is required for the oauth cell")
		}
		port, err := freePort()
		if err != nil {
			return nil, err
		}
		m.oauthPort = port
		proc, err := m.startOAuthIdP(port)
		if err != nil {
			return nil, err
		}
		m.fixtures["oauth"] = proc
		if err := waitTCP(fmt.Sprintf("127.0.0.1:%d", port), 15*time.Second); err != nil {
			return nil, fmt.Errorf("oauth fixture: %w", err)
		}
		servers = append(servers, gateServerConfig{
			"name": m.oauthServer, "url": fmt.Sprintf("http://127.0.0.1:%d/mcp", port),
			"protocol": "http", "enabled": true, "quarantined": false,
		})
	}
	return servers, nil
}

// startOAuthIdP starts tests/oauthserver's standalone binary with a short
// access-token TTL so the cell provably exercises a refresh (FR-008).
func (m *matrixRun) startOAuthIdP(port int) (*fixtureProc, error) {
	return startFixture("oauth", m.opts.OAuthBinary,
		[]string{"-port", fmt.Sprint(port), "-access-token-ttl", "30s"},
		port, filepath.Join(m.workDir, "fixture-oauth.log"))
}

func (m *matrixRun) bootCore(ctx context.Context, servers []gateServerConfig, dockerIsolation map[string]any) error {
	port, err := freePort()
	if err != nil {
		return err
	}
	listen := fmt.Sprintf("127.0.0.1:%d", port)
	apiKey := newAPIKey()
	dataDir := filepath.Join(m.workDir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	configPath := filepath.Join(m.workDir, "gate-config.json")
	if err := writeConfig(configPath, buildGateConfig(listen, dataDir, apiKey, servers, dockerIsolation)); err != nil {
		return err
	}
	core, err := startCore(ctx, m.opts.Binary, configPath, dataDir, listen, filepath.Join(m.workDir, "core.log"))
	if err != nil {
		return err
	}
	m.core = core
	m.core.APIKey = apiKey
	m.client = newClient(core.BaseURL, apiKey)
	if err := waitCoreReady(ctx, m.client, core, 60*time.Second); err != nil {
		return fmt.Errorf("candidate core did not become ready (log: %s): %w", core.LogPath, err)
	}
	logf("matrix: core ready at %s (pid %d)", core.BaseURL, core.PID)
	return nil
}

func (m *matrixRun) buildState() *gateState {
	st := &gateState{
		BaseURL:       m.core.BaseURL,
		APIKey:        m.core.APIKey,
		CorePID:       m.core.PID,
		CoreBinary:    m.opts.Binary,
		FixtureBinary: m.opts.Fixture,
		OAuthBinary:   m.opts.OAuthBinary,
		DataDir:       m.core.DataDir,
		ConfigPath:    m.core.ConfigPath,
		WorkDir:       m.workDir,
		Cells:         m.opts.Cells,
		IssuedCalls:   m.issued,
		Before:        m.before,
	}
	for _, pattern := range m.killPatterns {
		st.StdioKillPatterns = append(st.StdioKillPatterns, pattern)
	}
	for _, f := range m.fixtures {
		if f.alive() {
			st.Fixtures = append(st.Fixtures, stateFixture{
				Name: f.Name, PID: f.Cmd.Process.Pid, Port: f.Port, Binary: f.Binary, Args: f.Args,
			})
		}
	}
	if containsStr(m.opts.Cells, "docker") {
		st.DockerNamePrefix = dockerNamePrefix
	}
	return st
}

// teardown kills everything this run owns (core, fixtures, stdio children,
// docker containers).
func (m *matrixRun) teardown(ctx context.Context) {
	if m.core != nil {
		if err := m.core.stopGraceful(20 * time.Second); err != nil {
			logf("matrix: teardown core: %v", err)
		}
	}
	for _, f := range m.fixtures {
		f.kill()
	}
	for _, pattern := range m.killPatterns {
		if n, err := killByPattern(pattern); err == nil && n > 0 {
			logf("matrix: teardown killed %d stdio fixture(s) for %s", n, pattern)
		}
	}
	if _, err := killDockerByNamePrefix(ctx, dockerNamePrefix); err != nil {
		logf("matrix: teardown docker: %v", err)
	}
}

// --- cell execution ----------------------------------------------------------

// infraError marks infrastructure-classified failures (FR-009).
type infraError struct{ err error }

func (e *infraError) Error() string { return e.err.Error() }
func (e *infraError) Unwrap() error { return e.err }

func isInfraErr(err error) bool {
	var ie *infraError
	return errors.As(err, &ie)
}

const maxCellAttempts = 3 // 1 + at most 2 retries (FR-010)

func (m *matrixRun) runCell(ctx context.Context, cell string) gatereport.Fragment {
	frag := gatereport.Fragment{Name: "matrix/" + cell, StartedAt: time.Now().UTC()}
	var steps []gatereport.Step
	var lastErr error
	for attempt := 1; attempt <= maxCellAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, m.opts.CellTimeout)
		steps, lastErr = m.cellOnce(attemptCtx, cell)
		cancel()
		frag.Retries = attempt - 1
		if lastErr == nil {
			if attempt == 1 {
				frag.Status = gatereport.StatusPass
			} else {
				frag.Status = gatereport.StatusFlaky
				frag.Reason = fmt.Sprintf("passed on attempt %d of %d", attempt, maxCellAttempts)
			}
			break
		}
		logf("matrix: cell %s attempt %d failed: %v", cell, attempt, lastErr)
		if attempt < maxCellAttempts {
			m.prepareRetry(ctx, cell)
		}
	}
	if lastErr != nil {
		frag.Status = gatereport.StatusFail
		frag.Reason = lastErr.Error()
		var ie *infraError
		if errors.As(lastErr, &ie) {
			frag.Classification = gatereport.ClassificationInfrastructure
		} else {
			frag.Classification = gatereport.ClassificationProduct
		}
	}
	frag.Steps = steps
	frag.FinishedAt = time.Now().UTC()
	frag.DurationMS = frag.FinishedAt.Sub(frag.StartedAt).Milliseconds()
	return frag
}

// prepareRetry restores preconditions before a cell retry: driver-owned
// fixtures must be running again.
func (m *matrixRun) prepareRetry(ctx context.Context, cell string) {
	if f, ok := m.fixtures[cell]; ok && !f.alive() {
		if nf, err := f.restart(); err == nil {
			m.fixtures[cell] = nf
			_ = waitTCP(fmt.Sprintf("127.0.0.1:%d", nf.Port), 15*time.Second)
		} else {
			logf("matrix: retry prep for %s: %v", cell, err)
		}
	}
	if cell == "oauth" {
		if err := m.ensureOAuthServer(ctx); err != nil {
			logf("matrix: retry prep oauth: %v", err)
		}
	}
}

// cellTools describes the deterministic tool surface of a cell's fixture.
// Four cells run cmd/mcpfixture (echo(text)+ping); the oauth cell runs
// tests/oauthserver's MCP server (echo(message)+get_time, no ping).
type cellTools struct {
	EchoArg string // argument key the echo tool round-trips
	HasPing bool   // ping carries instance_id for restart detection
}

func toolsForCell(cell string) cellTools {
	if cell == "oauth" {
		return cellTools{EchoArg: "message", HasPing: false}
	}
	return cellTools{EchoArg: "text", HasPing: true}
}

// cellOnce runs FR-007 (a)-(d) for one cell.
func (m *matrixRun) cellOnce(ctx context.Context, cell string) ([]gatereport.Step, error) {
	serverName := gateServerPrefix + cell
	if cell == "oauth" {
		serverName = m.oauthServer
	}
	tools := toolsForCell(cell)
	var steps []gatereport.Step
	step := func(name string, fn func() error) error {
		start := time.Now()
		err := fn()
		s := gatereport.Step{Name: name, Status: gatereport.StatusPass, DurationMS: time.Since(start).Milliseconds()}
		if err != nil {
			s.Status = gatereport.StatusFail
			s.Reason = err.Error()
		}
		steps = append(steps, s)
		if err != nil {
			return fmt.Errorf("step %s: %w", name, err)
		}
		return nil
	}

	// (a) Ready.
	if err := step("ready", func() error {
		if cell == "oauth" {
			if err := m.oauthAuthorize(ctx, serverName, 60*time.Second); err != nil {
				return err
			}
		}
		_, err := m.waitServerReady(ctx, serverName, 2, 90*time.Second)
		return err
	}); err != nil {
		return steps, err
	}

	// MCP session for (b)+(c), carrying a probe X-Request-Id.
	mcpReqID := "gate-mcp-" + cell + "-" + randomNonce()
	sess, err := newMCPSession(ctx, m.core.BaseURL, mcpReqID)
	if err != nil {
		steps = append(steps, gatereport.Step{Name: "mcp-session", Status: gatereport.StatusFail, Reason: err.Error()})
		return steps, fmt.Errorf("open MCP session: %w", err)
	}
	defer sess.close()

	// (b) Tools listed AND discoverable via retrieve_tools.
	if err := step("tools-discoverable", func() error {
		return m.waitToolDiscoverable(ctx, sess, serverName+":echo", 60*time.Second)
	}); err != nil {
		return steps, err
	}

	// (c) Tool call round-trips through the MCP proxy (native MCP path).
	if err := step("call-mcp", func() error {
		nonce := "gate-" + cell + "-mcp-" + randomNonce()
		text, err := sess.callUpstreamRead(ctx, serverName+":echo", map[string]any{tools.EchoArg: nonce})
		if err != nil {
			return err
		}
		if !strings.Contains(text, nonce) {
			return fmt.Errorf("echo response did not round-trip nonce: %s", truncateStr(text, 200))
		}
		m.issued = append(m.issued, issuedCall{Cell: cell, Via: "mcp", Tool: serverName + ":echo", Nonce: nonce, HeaderRequestID: mcpReqID})
		return nil
	}); err != nil {
		return steps, err
	}

	// (c') Same call via POST /api/v1/tools/call with a caller-chosen
	// X-Request-Id — the REST path demonstrably passes RequestIDMiddleware,
	// giving FR-011 a correlated call per cell without middleware scope creep.
	if err := step("call-rest", func() error {
		nonce := "gate-" + cell + "-rest-" + randomNonce()
		rid := "gate-rest-" + cell + "-" + randomNonce()
		text, err := m.client.callToolREST(ctx, serverName+":echo", map[string]any{tools.EchoArg: nonce}, rid)
		if err != nil {
			return err
		}
		if !strings.Contains(text, nonce) {
			return fmt.Errorf("REST echo response did not round-trip nonce: %s", truncateStr(text, 200))
		}
		m.issued = append(m.issued, issuedCall{Cell: cell, Via: "rest", Tool: serverName + ":echo", Nonce: nonce, HeaderRequestID: rid})
		return nil
	}); err != nil {
		return steps, err
	}

	// FR-008: the oauth cell must survive at least one token refresh: the
	// IdP issues 30s access tokens; hold the cell past expiry and prove a
	// post-expiry call still round-trips.
	if cell == "oauth" {
		if err := step("token-refresh", func() error {
			return m.oauthRefreshCheck(ctx, serverName)
		}); err != nil {
			return steps, err
		}
	}

	// (d) Force-kill the fixture, restart it, and prove the reconnected
	// upstream serves calls again — instance_id must change (new process)
	// where the fixture exposes one.
	if err := step("reconnect", func() error {
		return m.reconnectCheck(ctx, cell, tools, &serverName)
	}); err != nil {
		return steps, err
	}

	return steps, nil
}

// waitServerReady polls until the named server is connected with at least
// minTools tools.
func (m *matrixRun) waitServerReady(ctx context.Context, name string, minTools int, timeout time.Duration) (*serverInfo, error) {
	deadline := time.Now().Add(timeout)
	var last *serverInfo
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		srv, err := m.client.server(ctx, name)
		if err == nil && srv != nil {
			last = srv
			if srv.Connected && srv.ToolCount >= minTools {
				return srv, nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	if last == nil {
		return nil, fmt.Errorf("server %s never appeared in /api/v1/servers", name)
	}
	return nil, fmt.Errorf("server %s not ready after %s (connected=%v tools=%d status=%s last_error=%s)",
		name, timeout, last.Connected, last.ToolCount, last.Status, last.LastError)
}

// waitToolDiscoverable polls retrieve_tools until the qualified tool shows
// up in the BM25 results (indexing is asynchronous).
func (m *matrixRun) waitToolDiscoverable(ctx context.Context, sess *mcpSession, qualifiedTool string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastText string
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		text, err := sess.retrieveTools(ctx, "echo deterministic fixture "+qualifiedTool)
		if err == nil && strings.Contains(text, qualifiedTool) {
			return nil
		}
		lastText, lastErr = text, err
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("tool %s not discoverable via retrieve_tools after %s (last err: %v, last result: %s)",
		qualifiedTool, timeout, lastErr, truncateStr(lastText, 300))
}

// pingPayload mirrors the fixture ping tool result.
type pingPayload struct {
	Message    string `json:"message"`
	Counter    int64  `json:"counter"`
	InstanceID string `json:"instance_id"`
}

func (m *matrixRun) pingREST(ctx context.Context, serverName string) (*pingPayload, error) {
	text, err := m.client.callToolREST(ctx, serverName+":ping", map[string]any{}, "")
	if err != nil {
		return nil, err
	}
	var p pingPayload
	if err := json.Unmarshal([]byte(text), &p); err != nil {
		return nil, fmt.Errorf("parse ping payload %q: %w", truncateStr(text, 200), err)
	}
	if p.Message != "pong" || p.InstanceID == "" {
		return nil, fmt.Errorf("unexpected ping payload: %s", truncateStr(text, 200))
	}
	return &p, nil
}

// reconnectCheck implements FR-007(d) per cell kind. serverName is a pointer
// because the oauth cell re-adds the upstream under a fresh name after the
// IdP restart (see oauthReconnect).
func (m *matrixRun) reconnectCheck(ctx context.Context, cell string, tools cellTools, serverName *string) error {
	var pre *pingPayload
	if tools.HasPing {
		var err error
		pre, err = m.pingREST(ctx, *serverName)
		if err != nil {
			return fmt.Errorf("pre-restart ping: %w", err)
		}
	}

	nudgeRestart := false
	switch cell {
	case "stdio":
		n, err := killByPattern(m.killPatterns["stdio"])
		if err != nil {
			return &infraError{fmt.Errorf("kill stdio fixture: %w", err)}
		}
		if n == 0 {
			return fmt.Errorf("no stdio fixture process matched pattern %s", m.killPatterns["stdio"])
		}
		nudgeRestart = true // mcpproxy respawns; nudge via restart API if slow
	case "docker":
		n, err := killDockerByNamePrefix(ctx, dockerNamePrefix)
		if err != nil {
			return &infraError{err}
		}
		if n == 0 {
			return fmt.Errorf("no running container matched name prefix %s", dockerNamePrefix)
		}
		nudgeRestart = true
	case "http", "sse":
		nf, err := m.fixtures[cell].restart()
		if err != nil {
			return fmt.Errorf("restart %s fixture: %w", cell, err)
		}
		m.fixtures[cell] = nf
		if err := waitTCP(fmt.Sprintf("127.0.0.1:%d", nf.Port), 15*time.Second); err != nil {
			return err
		}
		if err := m.client.restartServer(ctx, *serverName); err != nil {
			return fmt.Errorf("restart server %s: %w", *serverName, err)
		}
	case "oauth":
		if err := m.oauthReconnect(ctx, serverName); err != nil {
			return err
		}
	}

	// Poll until a call proves the restarted fixture is serving again; where
	// the fixture exposes ping, additionally require a NEW instance_id.
	deadline := time.Now().Add(90 * time.Second)
	nudged := false
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if tools.HasPing {
			post, err := m.pingREST(ctx, *serverName)
			if err == nil && post.InstanceID != pre.InstanceID {
				logf("matrix: %s reconnected: instance %s -> %s (counter %d -> %d)",
					cell, pre.InstanceID, post.InstanceID, pre.Counter, post.Counter)
				return nil
			}
			if err == nil {
				lastErr = fmt.Errorf("ping still served by pre-restart instance %s", pre.InstanceID)
			} else {
				lastErr = err
			}
		} else {
			nonce := "gate-" + cell + "-reconnect-" + randomNonce()
			text, err := m.client.callToolREST(ctx, *serverName+":echo", map[string]any{tools.EchoArg: nonce}, "")
			if err == nil && strings.Contains(text, nonce) {
				logf("matrix: %s reconnected: post-restart echo round-tripped", cell)
				return nil
			}
			if err != nil {
				lastErr = err
			} else {
				lastErr = fmt.Errorf("post-restart echo missing nonce: %s", truncateStr(text, 200))
			}
		}
		// If auto-reconnect is slow, nudge once via the restart API after
		// ~30s and keep polling (recorded implicitly in the log).
		if nudgeRestart && !nudged && time.Until(deadline) < 60*time.Second {
			nudged = true
			_ = m.client.restartServer(ctx, *serverName)
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("server did not reconnect with a fresh fixture instance within 90s: %v", lastErr)
}

// oauthAuthorize completes the headless authorization-code + PKCE dance:
// POST /servers/{id}/login yields the auth_url (browser suppressed by
// HEADLESS=1), the driver submits the IdP's login form, and the redirect
// chain lands on mcpproxy's loopback callback.
func (m *matrixRun) oauthAuthorize(ctx context.Context, serverName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if srv, err := m.client.server(ctx, serverName); err == nil && srv != nil && srv.Connected {
			m.authDoneAt = time.Now()
			return nil
		}
		resp, err := m.client.serverLogin(ctx, serverName)
		if err != nil || resp.AuthURL == "" {
			lastErr = fmt.Errorf("login trigger: err=%v auth_url_empty=%v", err, resp == nil || resp.AuthURL == "")
			time.Sleep(2 * time.Second)
			continue
		}
		if err := completeOAuthFlow(ctx, resp.AuthURL); err != nil {
			lastErr = err
			time.Sleep(2 * time.Second)
			continue
		}
		m.authDoneAt = time.Now()
		return nil
	}
	return fmt.Errorf("oauth authorization did not complete within %s: %v", timeout, lastErr)
}

// oauthRefreshCheck holds the cell alive past the 30s access-token TTL and
// asserts a post-expiry call round-trips (FR-008: refresh exercised).
func (m *matrixRun) oauthRefreshCheck(ctx context.Context, serverName string) error {
	const holdFor = 36 * time.Second // TTL 30s + margin
	elapsed := time.Since(m.authDoneAt)
	if wait := holdFor - elapsed; wait > 0 {
		logf("matrix: oauth refresh: waiting %s for the 30s access token to expire", wait.Round(time.Second))
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	nonce := "gate-oauth-refresh-" + randomNonce()
	text, err := m.client.callToolREST(ctx, serverName+":echo", map[string]any{"message": nonce}, "")
	if err != nil {
		return fmt.Errorf("post-expiry call failed (token refresh regression?): %w", err)
	}
	if !strings.Contains(text, nonce) {
		return fmt.Errorf("post-expiry echo did not round-trip: %s", truncateStr(text, 200))
	}
	logf("matrix: oauth refresh OK")
	return nil
}

// oauthReconnect implements FR-007(d) for the oauth cell. Restarting the IdP
// invalidates its in-memory DCR client registry and signing keys, so the
// persisted DCR credentials cannot be reused; the driver re-adds the
// upstream under a fresh name (fresh DCR registration) and re-authorizes.
// This limitation is deterministic fixture behavior, not a product bug, and
// is recorded in the gate report details.
func (m *matrixRun) oauthReconnect(ctx context.Context, serverName *string) error {
	nf, err := m.fixtures["oauth"].restart()
	if err != nil {
		return fmt.Errorf("restart oauth IdP fixture: %w", err)
	}
	m.fixtures["oauth"] = nf
	if err := waitTCP(fmt.Sprintf("127.0.0.1:%d", nf.Port), 15*time.Second); err != nil {
		return err
	}

	// Remove the old upstream and add a fresh one (new serverKey => fresh DCR).
	if err := m.client.removeServer(ctx, *serverName); err != nil {
		logf("matrix: oauth reconnect: remove %s: %v", *serverName, err)
	}
	newName := gateServerPrefix + "oauth-r" + randomNonce()[:4]
	if err := m.addOAuthServer(ctx, newName); err != nil {
		return err
	}
	m.oauthServer = newName
	*serverName = newName

	if err := m.oauthAuthorize(ctx, newName, 60*time.Second); err != nil {
		return err
	}
	if _, err := m.waitServerReady(ctx, newName, 2, 60*time.Second); err != nil {
		return err
	}
	return nil
}

func (m *matrixRun) addOAuthServer(ctx context.Context, name string) error {
	t := true
	return m.client.addServer(ctx, addServerRequest{
		Name:        name,
		URL:         fmt.Sprintf("http://127.0.0.1:%d/mcp", m.oauthPort),
		Protocol:    "http",
		Enabled:     &t,
		Quarantined: boolPtr(false),
	})
}

// ensureOAuthServer re-adds the oauth upstream if a failed attempt left it
// deleted.
func (m *matrixRun) ensureOAuthServer(ctx context.Context) error {
	if f := m.fixtures["oauth"]; f != nil && !f.alive() {
		nf, err := f.restart()
		if err != nil {
			return err
		}
		m.fixtures["oauth"] = nf
	}
	srv, err := m.client.server(ctx, m.oauthServer)
	if err != nil {
		return err
	}
	if srv == nil {
		return m.addOAuthServer(ctx, m.oauthServer)
	}
	return nil
}

func boolPtr(b bool) *bool { return &b }

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// randomNonce returns 12 hex chars.
func randomNonce() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%012d", time.Now().UnixNano()%1e12)
	}
	return hex.EncodeToString(buf)
}
