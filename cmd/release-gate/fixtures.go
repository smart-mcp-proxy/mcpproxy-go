package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// fixtureProc is a fixture process the gate driver owns directly (http/sse
// mcpfixture transports and the OAuth IdP). stdio and docker fixtures are
// spawned by mcpproxy itself and are killed via pkill pattern / docker kill.
type fixtureProc struct {
	Name    string
	Binary  string
	Args    []string
	Port    int
	Cmd     *exec.Cmd
	LogPath string
	logFile *os.File
}

func startFixture(name, binary string, args []string, port int, logPath string) (*fixtureProc, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open fixture log: %w", err)
	}
	cmd := exec.Command(binary, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("start fixture %s: %w", name, err)
	}
	return &fixtureProc{Name: name, Binary: binary, Args: args, Port: port, Cmd: cmd, LogPath: logPath, logFile: logFile}, nil
}

// kill force-terminates the fixture (SIGKILL — FR-007d requires forcible
// termination) and reaps it.
func (f *fixtureProc) kill() {
	if f == nil || f.Cmd == nil || f.Cmd.Process == nil {
		return
	}
	_ = f.Cmd.Process.Kill()
	_, _ = f.Cmd.Process.Wait()
	if f.logFile != nil {
		f.logFile.Close()
		f.logFile = nil
	}
}

// restart kills the fixture and starts a fresh instance with the same argv
// and port.
func (f *fixtureProc) restart() (*fixtureProc, error) {
	f.kill()
	// The kernel may need a beat to release the port.
	time.Sleep(200 * time.Millisecond)
	return startFixture(f.Name, f.Binary, f.Args, f.Port, f.LogPath)
}

// alive reports whether the fixture process is still running.
func (f *fixtureProc) alive() bool {
	return f != nil && f.Cmd != nil && f.Cmd.ProcessState == nil
}

// waitTCP polls until the address accepts connections.
func waitTCP(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("tcp %s not reachable after %s: %v", addr, timeout, lastErr)
}

// --- processes mcpproxy spawns (stdio cells) --------------------------------

// pgrepPIDs returns PIDs whose full command line matches pattern. The
// pattern is always a unique per-cell fixture binary path inside the gate's
// scratch dir, so it can never match the user's processes.
func pgrepPIDs(pattern string) ([]int, error) {
	out, err := exec.Command("pgrep", "-f", pattern).Output()
	if err != nil {
		// exit status 1 = no matches
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("pgrep -f %q: %w", pattern, err)
	}
	var pids []int
	for _, line := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(line); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// killByPattern SIGKILLs every process whose command line matches the unique
// scratch-dir pattern. Returns the number of processes killed.
func killByPattern(pattern string) (int, error) {
	pids, err := pgrepPIDs(pattern)
	if err != nil {
		return 0, err
	}
	for _, pid := range pids {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Kill()
		}
	}
	return len(pids), nil
}

// --- docker helpers (FR-009) -------------------------------------------------

// dockerPreflight verifies the Docker CLI and daemon are usable and the gate
// fixture image exists locally. Failures are infrastructure-classified: the
// docker cell must FAIL (never skip, never fall back to un-isolated).
func dockerPreflight(ctx context.Context, image string) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker CLI not found on PATH: %w", err)
	}
	infoCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(infoCtx, "docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		return fmt.Errorf("docker daemon unreachable (docker info failed): %v: %s", err, truncateStr(string(out), 200))
	}
	if out, err := exec.CommandContext(ctx, "docker", "image", "inspect", image, "--format", "{{.Id}}").CombinedOutput(); err != nil {
		return fmt.Errorf("fixture image %s not present locally (run scripts/gate/build-fixture-image.sh): %v: %s",
			image, err, truncateStr(string(out), 200))
	}
	return nil
}

// killDockerByNamePrefix force-kills containers whose name starts with the
// given prefix (mcpproxy names isolation containers mcpproxy-<server>-<rand>,
// and gate server names are gate-* so the prefix cannot match user containers).
func killDockerByNamePrefix(ctx context.Context, prefix string) (int, error) {
	out, err := exec.CommandContext(ctx, "docker", "ps", "-q", "--filter", "name="+prefix).Output()
	if err != nil {
		return 0, fmt.Errorf("docker ps: %w", err)
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return 0, nil
	}
	args := append([]string{"kill"}, ids...)
	if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
		return 0, fmt.Errorf("docker kill: %v: %s", err, truncateStr(string(out), 200))
	}
	return len(ids), nil
}

// --- OAuth flow completion (FR-008) -----------------------------------------

// completeOAuthFlow drives the tests/oauthserver login form headlessly: it
// takes the auth_url mcpproxy produced (HEADLESS=1 keeps the browser closed),
// POSTs the fixture IdP's known test credentials with consent, and follows
// the redirect chain into mcpproxy's loopback callback server, which
// completes the token exchange.
func completeOAuthFlow(ctx context.Context, authURL string) error {
	u, err := url.Parse(authURL)
	if err != nil {
		return fmt.Errorf("parse auth_url: %w", err)
	}
	form := url.Values{}
	for k, vs := range u.Query() {
		if len(vs) > 0 {
			form.Set(k, vs[0])
		}
	}
	if form.Get("response_type") == "" {
		form.Set("response_type", "code")
	}
	form.Set("username", "testuser")
	form.Set("password", "testpass")
	form.Set("consent", "on")
	form.Set("action", "approve")

	endpoint := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Follow redirects (IdP → mcpproxy loopback callback).
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("submit authorization form: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("authorization flow ended with status %d at %s", resp.StatusCode, resp.Request.URL)
	}
	// The final URL must be the loopback callback, not an IdP error page.
	if q := resp.Request.URL.Query(); q.Get("error") != "" {
		return fmt.Errorf("authorization error: %s (%s)", q.Get("error"), q.Get("error_description"))
	}
	return nil
}
