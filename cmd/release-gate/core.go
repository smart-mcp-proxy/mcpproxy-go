package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// coreProc is a candidate mcpproxy core instance owned by the gate driver.
type coreProc struct {
	Cmd        *exec.Cmd
	PID        int
	BaseURL    string
	APIKey     string
	DataDir    string
	ConfigPath string
	LogPath    string
	logFile    *os.File
}

// gateServerConfig is one mcpServers entry in the generated gate config.
type gateServerConfig map[string]any

// buildGateConfig renders a minimal mcpproxy config for a gate run.
//
// Deliberate choices:
//   - explicit api_key so the driver can authenticate without scraping;
//   - enable_socket=false: scratch dirs exceed the 104-char Unix socket path
//     limit on macOS, and the gate drives REST/MCP over TCP only;
//   - localhost listen on a driver-chosen free port (never the user's 8080);
//   - dockerIsolation (optional) becomes the global docker_isolation section.
//     The docker cell needs global enabled=true: per-server opt-ins (both the
//     legacy bool and the mode enum) do not reliably engage when the global
//     flag is off (the per-server mode override is dropped on the
//     contracts round-trip and the client-side isolation manager swallows
//     the opt-in warning), so host-run stdio fixtures carry an explicit
//     per-server opt-out instead.
func buildGateConfig(listen, dataDir, apiKey string, servers []gateServerConfig, dockerIsolation map[string]any) map[string]any {
	cfg := map[string]any{
		"listen":              listen,
		"data_dir":            dataDir,
		"api_key":             apiKey,
		"enable_tray":         false,
		"enable_socket":       false,
		"check_server_repo":   false,
		"call_tool_timeout":   "30s",
		"tool_response_limit": 20000,
		"mcpServers":          servers,
	}
	if dockerIsolation != nil {
		cfg["docker_isolation"] = dockerIsolation
	}
	return cfg
}

func writeConfig(path string, cfg map[string]any) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// newAPIKey generates the gate-run API key.
func newAPIKey() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("gate-%d", time.Now().UnixNano())
	}
	return "gate-" + hex.EncodeToString(buf)
}

// freePort asks the kernel for an unused TCP port on 127.0.0.1.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// startCore launches the candidate binary with `serve` against the given
// config and waits until the REST API responds (or the timeout elapses).
//
// Environment:
//   - HEADLESS=1 keeps OAuth flows from touching a browser.
//   - DO_NOT_TRACK=1 suppresses telemetry heartbeat SENDING. This is
//     load-bearing for the upgrade check (FR-014), which runs a REAL
//     downloaded release binary (semver version → would otherwise send a
//     heartbeat): a QA gate must never emit production telemetry, and CI=true
//     only covers GitHub runners, not local/dry-run (FR-001a) invocations.
//     The env-disable only early-returns from the send loop
//     (internal/telemetry/telemetry.go) — the /telemetry/payload provider and
//     in-process builtin_tool_calls counters the FR-012 invariant asserts stay
//     fully live.
func startCore(ctx context.Context, binary, configPath, dataDir, listen, logPath string) (*coreProc, error) {
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("create core log: %w", err)
	}
	cmd := exec.Command(binary, "serve", "--config="+configPath, "--data-dir="+dataDir, "--log-level=info")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "HEADLESS=1", "DO_NOT_TRACK=1")
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("start core: %w", err)
	}
	return &coreProc{
		Cmd:        cmd,
		PID:        cmd.Process.Pid,
		BaseURL:    "http://" + listen,
		DataDir:    dataDir,
		ConfigPath: configPath,
		LogPath:    logPath,
		logFile:    logFile,
	}, nil
}

// waitCoreReady polls /api/v1/status until the core answers.
func waitCoreReady(ctx context.Context, c *Client, proc *coreProc, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Bail out early if the core died (config error, port conflict...).
		if proc != nil && proc.Cmd != nil && proc.Cmd.ProcessState != nil {
			return fmt.Errorf("core exited before becoming ready: %s", proc.Cmd.ProcessState)
		}
		if err := c.statusOK(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("core not ready after %s: %v", timeout, lastErr)
}

// stopGraceful SIGTERMs the core we own and WAITS on the PID so the BBolt
// lock is fully released before a successor starts (exit code 3 = raced).
func (p *coreProc) stopGraceful(timeout time.Duration) error {
	if p.Cmd == nil || p.Cmd.Process == nil {
		return nil
	}
	defer func() {
		if p.logFile != nil {
			p.logFile.Close()
		}
	}()
	_ = p.Cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- p.Cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("core exited with error after SIGTERM: %w", err)
		}
		return nil
	case <-time.After(timeout):
		_ = p.Cmd.Process.Kill()
		<-done
		return fmt.Errorf("core did not exit within %s after SIGTERM; killed", timeout)
	}
}

// stopPID terminates a core we did not spawn in this process (attach mode):
// SIGTERM, then poll until the process is gone, escalating to SIGKILL.
func stopPID(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return nil // already gone
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = proc.Kill()
	// Give the kernel a beat to reap via init.
	for i := 0; i < 25; i++ {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("pid %d still alive after SIGKILL", pid)
}

// mkWorkDir creates a scratch directory under base (or os.TempDir).
func mkWorkDir(base, prefix string) (string, error) {
	if base == "" {
		return os.MkdirTemp("", prefix)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(base, prefix)
}

// absPath resolves a possibly-relative path against the current directory.
func absPath(p string) (string, error) {
	return filepath.Abs(p)
}

// copyFile copies a binary preserving exec permissions; used to give each
// stdio-spawned fixture a unique path so pkill patterns cannot cross cells.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}
