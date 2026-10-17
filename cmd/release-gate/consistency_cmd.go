package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/gatereport"
)

// Mixed-state servers the consistency check adds on top of the matrix fleet
// (FR-018 "fixtures in a known mixed state"). The matrix servers are all
// connected; these add the other admin states and an unhealthy server.
const (
	consServerLive        = "gate-cons-live"        // healthy, then disabled live (no page reload)
	consServerQuarantined = "gate-cons-quarantined" // admin_state quarantined
	consServerDisabled    = "gate-cons-disabled"    // admin_state disabled
	consServerBroken      = "gate-cons-broken"      // enabled, endpoint refuses → unhealthy
)

type consistencyOpts struct {
	StateFile     string
	ReportDir     string
	CLIBinary     string
	WebUIScript   string
	NoWebUI       bool
	SetupMixed    bool
	SettleTimeout time.Duration
	Seed          *seedMismatch
	KeepCore      bool
}

func cmdConsistency(ctx context.Context, args []string) (bool, error) {
	fs := flag.NewFlagSet("consistency", flag.ExitOnError)
	stateFile := fs.String("state-file", "", "state file written by `matrix --state-file` (required)")
	reportDir := fs.String("report-dir", "", "directory for report fragments (required)")
	cliBinary := fs.String("cli-binary", "", "mcpproxy binary used for the CLI surface (default: the matrix core binary)")
	webuiScript := fs.String("webui-script", "", "path to e2e/web-ui-sweep/surface-snapshot.mjs (required unless --no-webui)")
	noWebUI := fs.Bool("no-webui", false, "do not compare the Web UI (recorded as not-compared; local runs without Node only)")
	setupMixed := fs.Bool("setup-mixed-state", true, "add quarantined/disabled/unhealthy/live servers before comparing")
	settle := fs.Duration("settle-timeout", 60*time.Second, "per-phase budget for the surfaces to converge")
	seed := fs.String("seed-mismatch", "", "test the negative path: override surface:server:field=value on every capture")
	keepCore := fs.Bool("keep-core", false, "leave the matrix core running afterwards")
	if err := fs.Parse(args); err != nil {
		return false, err
	}
	if *stateFile == "" || *reportDir == "" {
		return false, fmt.Errorf("--state-file and --report-dir are required")
	}
	if *webuiScript == "" && !*noWebUI {
		return false, fmt.Errorf("--webui-script is required (or pass --no-webui to record the Web UI as not-compared)")
	}
	opts := consistencyOpts{
		StateFile: *stateFile, ReportDir: *reportDir, CLIBinary: absOrEmpty(*cliBinary),
		WebUIScript: absOrEmpty(*webuiScript), NoWebUI: *noWebUI, SetupMixed: *setupMixed,
		SettleTimeout: *settle, KeepCore: *keepCore,
	}
	if *seed != "" {
		s, err := parseSeedMismatch(*seed)
		if err != nil {
			return false, err
		}
		opts.Seed = s
	}
	return runConsistency(ctx, opts)
}

func runConsistency(ctx context.Context, opts consistencyOpts) (bool, error) {
	st, err := readState(opts.StateFile)
	if err != nil {
		return false, err
	}
	if opts.CLIBinary == "" {
		opts.CLIBinary = st.CoreBinary
	}
	frag := runTimed(gatereport.EntrySurfaceConsistency, func(frag *gatereport.Fragment) error {
		return checkSurfaceConsistency(ctx, st, opts, frag)
	})
	if !opts.KeepCore {
		teardownState(ctx, st)
	}
	if err := gatereport.WriteFragment(opts.ReportDir, frag); err != nil {
		return false, fmt.Errorf("write fragment: %w", err)
	}
	logf("consistency: %s -> %s %s", frag.Name, frag.Status, frag.Reason)
	return frag.Status == gatereport.StatusPass, nil
}

// checkSurfaceConsistency runs the three phases:
//
//	settled  fixtures in the mixed state, Web UI freshly loaded
//	live     one server disabled through REST while the page stays open —
//	         the Web UI must follow via its SSE/poll path, not a reload
//	reload   the same state after a page reload (tells "stale live view"
//	         apart from "renders the state wrong")
func checkSurfaceConsistency(ctx context.Context, st *gateState, opts consistencyOpts, frag *gatereport.Fragment) error {
	client := newClient(st.BaseURL, st.APIKey)
	if err := client.statusOK(ctx); err != nil {
		return &infraError{fmt.Errorf("matrix core unreachable at %s: %w", st.BaseURL, err)}
	}
	details := map[string]any{
		"reference": "core: GET /api/v1/servers",
		"compared":  []string{surfaceCLI, surfaceMCP},
		"not_compared": map[string]string{
			surfaceTray: "macOS tray is Spec 081 T3 (macos-smoke, advisory) — not part of this Linux comparison",
		},
		"fields": surfaceFields,
	}
	frag.Details = details
	if opts.Seed != nil {
		details["seeded_mismatch"] = *opts.Seed
	}

	if opts.SetupMixed {
		step, err := timedStep("setup-mixed-state", func() error { return setupMixedState(ctx, client, st) })
		frag.Steps = append(frag.Steps, step)
		if err != nil {
			return err
		}
	}

	mcpSess, err := newMCPSession(ctx, st.BaseURL, "")
	if err != nil {
		return &infraError{fmt.Errorf("MCP session: %w", err)}
	}
	defer mcpSess.close()

	var web *webuiReader
	if opts.NoWebUI {
		details["not_compared"].(map[string]string)[surfaceWebUI] = "--no-webui (local run without Node/Playwright)"
	} else {
		details["compared"] = []string{surfaceCLI, surfaceMCP, surfaceWebUI}
		web, err = startWebUIReader(ctx, opts.WebUIScript, st.BaseURL, st.APIKey)
		if err != nil {
			return &infraError{fmt.Errorf("start Web UI reader: %w", err)}
		}
		defer web.close()
	}
	capt := &capturer{client: client, mcp: mcpSess, web: web, cliBinary: opts.CLIBinary,
		configPath: st.ConfigPath, seed: opts.Seed}

	var failures []string
	runPhase := func(name string, webCmd string) []fieldMismatch {
		var mm []fieldMismatch
		step, perr := timedStep(name, func() error {
			var err error
			mm, err = capt.converge(ctx, webCmd, opts.SettleTimeout)
			if err != nil {
				return err
			}
			if len(mm) > 0 {
				return fmt.Errorf("%d field(s) disagree with core after %s:\n%s", len(mm), opts.SettleTimeout, formatMismatches(mm))
			}
			return nil
		})
		frag.Steps = append(frag.Steps, step)
		details["mismatches_"+name] = mm
		details["captured_"+name] = map[string]any{surfaceCore: capt.lastCore, "surfaces": capt.lastSnaps}
		if perr != nil {
			failures = append(failures, name+": "+perr.Error())
		}
		return mm
	}

	if web != nil {
		if _, err := web.do(ctx, "load"); err != nil {
			return &infraError{fmt.Errorf("load /ui/servers: %w", err)}
		}
	}
	runPhase("settled", "snapshot")

	if opts.SetupMixed {
		if err := client.postJSON(ctx, "/api/v1/servers/"+url.PathEscape(consServerLive)+"/disable", map[string]any{}, nil); err != nil {
			failures = append(failures, "live: disable "+consServerLive+": "+err.Error())
		} else {
			liveMM := runPhase("live", "snapshot")
			reloadMM := runPhase("reload", "reload")
			if web != nil && hasSurface(liveMM, surfaceWebUI) && !hasSurface(reloadMM, surfaceWebUI) {
				failures = append(failures, "the Web UI only showed the new state after a page reload — its live update path is stale")
			}
		}
	}

	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "\n"))
	}
	return nil
}

func hasSurface(mm []fieldMismatch, surface string) bool {
	for _, m := range mm {
		if m.Surface == surface {
			return true
		}
	}
	return false
}

func timedStep(name string, fn func() error) (gatereport.Step, error) {
	start := time.Now()
	err := fn()
	step := gatereport.Step{Name: name, Status: gatereport.StatusPass, DurationMS: time.Since(start).Milliseconds()}
	if err != nil {
		step.Status = gatereport.StatusFail
		step.Reason = err.Error()
	}
	return step, err
}

// setupMixedState adds the four consistency servers and waits until each has
// reached the state it exists to show, so the comparison runs over a fleet
// covering every admin state plus an unhealthy server.
func setupMixedState(ctx context.Context, c *Client, st *gateState) error {
	yes, no := true, false
	isolateOff := &isolationRequest{EnabledOverride: &no}
	port, err := freePort() // nothing listens here: connection refused
	if err != nil {
		return &infraError{err}
	}
	reqs := []addServerRequest{
		{Name: consServerLive, Command: st.FixtureBinary, Args: []string{"--transport", "stdio"}, Protocol: "stdio",
			Enabled: &yes, Quarantined: &no, Isolation: isolateOff},
		{Name: consServerQuarantined, Command: st.FixtureBinary, Args: []string{"--transport", "stdio"}, Protocol: "stdio",
			Enabled: &yes, Quarantined: &yes, Isolation: isolateOff},
		{Name: consServerDisabled, Command: st.FixtureBinary, Args: []string{"--transport", "stdio"}, Protocol: "stdio",
			Enabled: &no, Quarantined: &no, Isolation: isolateOff},
		{Name: consServerBroken, URL: fmt.Sprintf("http://127.0.0.1:%d/mcp", port), Protocol: "http",
			Enabled: &yes, Quarantined: &no},
	}
	for _, r := range reqs {
		if err := c.addServer(ctx, r); err != nil {
			return fmt.Errorf("add %s: %w", r.Name, err)
		}
	}
	// REST add may still land a server in quarantine (security default);
	// the live/broken servers must be trusted for their state to be visible.
	for _, name := range []string{consServerLive, consServerBroken} {
		if s, err := c.server(ctx, name); err == nil && s != nil && s.Quarantined {
			if err := c.unquarantineServer(ctx, name); err != nil {
				return fmt.Errorf("unquarantine %s: %w", name, err)
			}
		}
	}
	want := map[string]func(*serverInfo) bool{
		consServerLive: func(s *serverInfo) bool {
			return s.Connected && s.ToolCount > 0 && s.Health != nil && s.Health.Level == "healthy"
		},
		consServerQuarantined: func(s *serverInfo) bool { return s.Health != nil && s.Health.AdminState == "quarantined" },
		consServerDisabled:    func(s *serverInfo) bool { return s.Health != nil && s.Health.AdminState == "disabled" },
		consServerBroken: func(s *serverInfo) bool {
			return !s.Connected && s.Health != nil && s.Health.AdminState == "enabled" && s.Health.Level != "healthy"
		},
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		servers, err := c.servers(ctx)
		pending := []string{}
		if err != nil {
			pending = append(pending, "list: "+err.Error())
		} else {
			for name, ok := range want {
				found := false
				for i := range servers {
					if servers[i].Name == name {
						found = true
						if !ok(&servers[i]) {
							pending = append(pending, fmt.Sprintf("%s (connected=%v tools=%d health=%+v)", name, servers[i].Connected, servers[i].ToolCount, servers[i].Health))
						}
					}
				}
				if !found {
					pending = append(pending, name+" (absent)")
				}
			}
		}
		if len(pending) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("mixed state not reached within 90s: %s", strings.Join(pending, "; "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// capturer reads every surface once per attempt.
type capturer struct {
	client     *Client
	mcp        *mcpSession
	web        *webuiReader
	cliBinary  string
	configPath string
	seed       *seedMismatch

	// lastCore/lastSnaps are the final attempt's reads, kept for the report.
	lastCore  map[string]surfaceRow
	lastSnaps []surfaceSnapshot
}

func (c *capturer) core(ctx context.Context) (map[string]surfaceRow, error) {
	var data json.RawMessage
	if err := c.client.getJSON(ctx, "/api/v1/servers", &data); err != nil {
		return nil, err
	}
	return coreRowsFromREST([]byte(`{"data":` + string(data) + `}`))
}

func (c *capturer) cli(ctx context.Context) surfaceSnapshot {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, c.cliBinary, "upstream", "list", "-o", "json", "--config", c.configPath)
	// Never let the caller's environment point the CLI at another daemon.
	cmd.Env = filteredEnv(os.Environ(), "MCPPROXY_TRAY_ENDPOINT", "MCPPROXY_API_KEY", "MCPPROXY_OUTPUT", "MCPPROXY_LISTEN")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return surfaceSnapshot{Surface: surfaceCLI, Err: fmt.Sprintf("%v: %s", err, truncateStr(stderr.String()+stdout.String(), 400))}
	}
	rows, err := cliRowsFromJSON(stdout.Bytes())
	if err != nil {
		return surfaceSnapshot{Surface: surfaceCLI, Err: err.Error()}
	}
	return surfaceSnapshot{Surface: surfaceCLI, Rows: rows}
}

func (c *capturer) mcpList(ctx context.Context) surfaceSnapshot {
	text, err := c.mcp.callTool(ctx, "upstream_servers", map[string]any{"operation": "list"})
	if err != nil {
		return surfaceSnapshot{Surface: surfaceMCP, Err: err.Error()}
	}
	rows, err := mcpRowsFromText(text)
	if err != nil {
		return surfaceSnapshot{Surface: surfaceMCP, Err: err.Error()}
	}
	return surfaceSnapshot{Surface: surfaceMCP, Rows: rows}
}

func (c *capturer) webui(ctx context.Context, cmd string) surfaceSnapshot {
	line, err := c.web.do(ctx, cmd)
	if err != nil {
		return surfaceSnapshot{Surface: surfaceWebUI, Err: err.Error()}
	}
	rows, err := webuiRowsFromJSON(line)
	if err != nil {
		return surfaceSnapshot{Surface: surfaceWebUI, Err: err.Error()}
	}
	return surfaceSnapshot{Surface: surfaceWebUI, Rows: rows}
}

// converge captures every surface until they agree with the core or the
// budget runs out. An attempt only counts when the core did not move while
// the surfaces were being read (core-before == core-after) — otherwise a
// legitimate transition would read as a disagreement. The first Web UI read
// uses webCmd ("snapshot" or "reload"); later attempts only re-read the page.
func (c *capturer) converge(ctx context.Context, webCmd string, budget time.Duration) ([]fieldMismatch, error) {
	deadline := time.Now().Add(budget)
	var last []fieldMismatch
	stableAttempts := 0
	for attempt := 1; ; attempt++ {
		before, err := c.core(ctx)
		if err != nil {
			return nil, &infraError{fmt.Errorf("read core: %w", err)}
		}
		snaps := []surfaceSnapshot{c.cli(ctx), c.mcpList(ctx)}
		if c.web != nil {
			snaps = append(snaps, c.webui(ctx, webCmd))
			webCmd = "snapshot"
		}
		after, err := c.core(ctx)
		if err != nil {
			return nil, &infraError{fmt.Errorf("read core: %w", err)}
		}
		if reflect.DeepEqual(before, after) {
			stableAttempts++
			applySeed(c.seed, snaps)
			c.lastCore, c.lastSnaps = after, snaps
			last = compareSurfaces(after, snaps)
			if len(last) == 0 {
				logf("consistency: surfaces agree with core (attempt %d, %d servers)", attempt, len(after))
				return nil, nil
			}
			logf("consistency: attempt %d: %d mismatch(es)\n%s", attempt, len(last), formatMismatches(last))
		} else {
			logf("consistency: attempt %d: core state moved during capture, retrying", attempt)
		}
		if time.Now().After(deadline) {
			if stableAttempts == 0 {
				return nil, fmt.Errorf("core state never held still for one capture within %s", budget)
			}
			return last, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func filteredEnv(env []string, drop ...string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		keep := true
		for _, d := range drop {
			if strings.HasPrefix(kv, d+"=") {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}

// webuiReader drives e2e/web-ui-sweep/surface-snapshot.mjs: one long-lived
// headless Chromium page, one JSON line per command on stdin/stdout. Keeping
// the page open is what lets the live phase test the Web UI's update path.
type webuiReader struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan []byte
	stderr *bytes.Buffer
}

func startWebUIReader(ctx context.Context, script, baseURL, apiKey string) (*webuiReader, error) {
	if _, err := os.Stat(script); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "node", script)
	cmd.Dir = filepath.Dir(script) // resolves @playwright/test from the sweep's node_modules
	cmd.Env = append(filteredEnv(os.Environ(), "MCPPROXY_API_KEY", "MCPPROXY_BASE_URL"),
		"MCPPROXY_BASE_URL="+baseURL, "MCPPROXY_API_KEY="+apiKey)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	r := &webuiReader{cmd: cmd, stdin: stdin, lines: make(chan []byte, 4), stderr: stderr}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			r.lines <- append([]byte(nil), sc.Bytes()...)
		}
		close(r.lines)
	}()
	return r, nil
}

func (r *webuiReader) do(ctx context.Context, command string) ([]byte, error) {
	if _, err := io.WriteString(r.stdin, command+"\n"); err != nil {
		return nil, fmt.Errorf("send %q: %w (stderr: %s)", command, err, truncateStr(r.stderr.String(), 400))
	}
	select {
	case line, ok := <-r.lines:
		if !ok {
			return nil, fmt.Errorf("web UI reader exited (stderr: %s)", truncateStr(r.stderr.String(), 600))
		}
		return line, nil
	case <-time.After(90 * time.Second):
		return nil, fmt.Errorf("web UI reader: %q timed out", command)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *webuiReader) close() {
	_, _ = io.WriteString(r.stdin, "quit\n")
	_ = r.stdin.Close()
	done := make(chan struct{})
	go func() { _ = r.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = r.cmd.Process.Kill()
	}
}
