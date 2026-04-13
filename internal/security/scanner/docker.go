package scanner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/shellwrap"
	"go.uber.org/zap"
)

// DockerRunner executes Docker operations for scanner containers.
//
// Security note: the scanner runs untrusted container images (vulnerability
// scanners, SBOM generators, etc). It MUST NOT inherit the user's ambient
// credentials (AWS, GitHub, Anthropic tokens, …). We therefore:
//  1. Resolve the docker binary once via shellwrap.ResolveDockerPath — this
//     picks up Homebrew / Docker Desktop / Colima installs even when mcpproxy
//     was launched from a GUI with a minimal PATH, WITHOUT re-spawning a
//     login shell on every invocation (important for hot paths like the
//     ~2s health check loop).
//  2. Invoke docker directly with cmd.Env set to a minimal PATH+HOME
//     allowlist via shellwrap.MinimalEnv, so environment-based secrets do
//     not leak into the docker CLI's subprocess tree.
type DockerRunner struct {
	logger *zap.Logger
}

// NewDockerRunner creates a new DockerRunner
func NewDockerRunner(logger *zap.Logger) *DockerRunner {
	return &DockerRunner{logger: logger}
}

// getDockerCmd creates an exec.Cmd for Docker with a minimal, allow-listed
// environment so ambient secrets (AWS creds, GitHub tokens, …) cannot leak
// into the security scanner's subprocess. The docker binary is resolved once
// via shellwrap and then cached for the process lifetime.
func (d *DockerRunner) getDockerCmd(ctx context.Context, args ...string) *exec.Cmd {
	dockerBin, err := shellwrap.ResolveDockerPath(d.logger)
	if err != nil || dockerBin == "" {
		// Fall back to plain "docker" so exec returns a clear ENOENT the
		// caller can surface. We still set the minimal env to avoid leaking
		// secrets even in the error path.
		dockerBin = "docker"
		if d.logger != nil {
			d.logger.Debug("scanner docker: falling back to bare 'docker' lookup",
				zap.Error(err))
		}
	}
	cmd := exec.CommandContext(ctx, dockerBin, args...)
	cmd.Env = shellwrap.MinimalEnv()
	return cmd
}

// IsDockerAvailable checks if Docker daemon is running
func (d *DockerRunner) IsDockerAvailable(ctx context.Context) bool {
	cmd := d.getDockerCmd(ctx, "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// PullImage pulls a Docker image with progress logging
func (d *DockerRunner) PullImage(ctx context.Context, image string) error {
	d.logger.Info("Pulling Docker image", zap.String("image", image))
	cmd := d.getDockerCmd(ctx, "pull", image)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker pull %s failed: %s: %w", image, stderr.String(), err)
	}
	d.logger.Info("Docker image pulled successfully", zap.String("image", image))
	return nil
}

// ImageExists checks if a Docker image exists locally
func (d *DockerRunner) ImageExists(ctx context.Context, image string) bool {
	cmd := d.getDockerCmd(ctx, "image", "inspect", image)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// RemoveImage removes a Docker image
func (d *DockerRunner) RemoveImage(ctx context.Context, image string) error {
	cmd := d.getDockerCmd(ctx, "rmi", "-f", image)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker rmi %s failed: %s: %w", image, stderr.String(), err)
	}
	return nil
}

// ScannerRunConfig defines how to run a scanner container
type ScannerRunConfig struct {
	ContainerName string            // e.g., "mcpproxy-scanner-mcp-scan-abc123"
	Image         string            // Docker image to use
	Command       []string          // Command to run inside container
	Env           map[string]string // Environment variables
	SourceDir     string            // Host directory to mount at /scan/source (read-only)
	ReportDir     string            // Host directory to mount at /scan/report (writable)
	CacheDir      string            // Host directory for scanner cache (persists between runs)
	NetworkMode   string            // "none", "bridge", or custom network name
	Timeout       time.Duration     // Container execution timeout
	ReadOnly      bool              // Read-only root filesystem
	MemoryLimit   string            // e.g., "512m"
	ExtraMounts   []string          // Additional -v mounts (e.g., "~/.claude:/app/.claude:ro")

	// DisableNoNewPrivileges, when true, omits the
	// `--security-opt no-new-privileges` flag. Required on hosts running
	// snap-installed Docker where the AppArmor profile transition would
	// otherwise fail with "operation not permitted". See
	// SecurityConfig.ScannerDisableNoNewPrivileges in internal/config.
	DisableNoNewPrivileges bool
}

// buildRunArgs assembles the `docker run ...` argument list for a scanner
// container. Extracted from RunScanner so the argument logic is unit-testable
// without invoking the docker binary.
func buildRunArgs(cfg ScannerRunConfig) []string {
	args := []string{"run", "--rm"}

	if cfg.ContainerName != "" {
		args = append(args, "--name", cfg.ContainerName)
	}
	if cfg.ReadOnly {
		args = append(args, "--read-only")
	}
	if cfg.NetworkMode != "" {
		args = append(args, "--network", cfg.NetworkMode)
	}
	if cfg.MemoryLimit != "" {
		args = append(args, "--memory", cfg.MemoryLimit)
	}

	// Security: no new privileges. Intentionally omitted on hosts where
	// snap-docker + AppArmor would otherwise deny the container entrypoint
	// exec (EPERM). The scanner container still benefits from read-only fs,
	// tmpfs /tmp, no-net default, and read-only source mounts, so dropping
	// this single flag is an acceptable tradeoff for the affected users.
	if !cfg.DisableNoNewPrivileges {
		args = append(args, "--security-opt", "no-new-privileges")
	}

	// Tmpfs for temp files (500MB to accommodate scanner DB downloads like Trivy ~90MB)
	args = append(args, "--tmpfs", "/tmp:rw,nosuid,size=500m")

	if cfg.SourceDir != "" {
		args = append(args, "-v", cfg.SourceDir+":/scan/source:ro")
		// Also mount at /src for scanners that expect it (e.g., Semgrep Docker image)
		args = append(args, "-v", cfg.SourceDir+":/src:ro")
	}
	if cfg.ReportDir != "" {
		args = append(args, "-v", cfg.ReportDir+":/scan/report:rw")
	}
	if cfg.CacheDir != "" {
		args = append(args, "-v", cfg.CacheDir+":/root/.cache:rw")
	}
	for _, mount := range cfg.ExtraMounts {
		args = append(args, "-v", mount)
	}
	for k, v := range cfg.Env {
		args = append(args, "-e", k+"="+v)
	}

	args = append(args, cfg.Image)
	args = append(args, cfg.Command...)
	return args
}

// ClassifyScannerExecFailure inspects a scanner's stderr and exit code and
// returns a human-readable remediation hint when the failure matches a
// well-known environment incompatibility. Returns an empty string when the
// stderr does not match a known pattern.
//
// Currently recognised:
//   - snap-docker × AppArmor × no-new-privileges: runc reports
//     "exec /usr/local/bin/...: operation not permitted" (exit 255) when
//     the dockerd snap profile blocks the inner profile transition.
func ClassifyScannerExecFailure(stderr string, exitCode int) string {
	if exitCode != 255 {
		return ""
	}
	// runc prints "exec <path>: operation not permitted" when AppArmor
	// denies the entrypoint exec. Match loosely so we catch variants like
	// "exec /app/run.sh: operation not permitted" too.
	if !strings.Contains(stderr, "operation not permitted") {
		return ""
	}
	if !strings.Contains(stderr, "exec ") {
		return ""
	}
	return "container exec was denied by the host (likely AppArmor on " +
		"snap-installed Docker). Fix options: (1) replace snap docker with " +
		"your distro's docker package (e.g. `sudo snap remove docker && " +
		"sudo apt install docker.io`), or (2) set " +
		"`security.scanner_disable_no_new_privileges: true` in your " +
		"mcpproxy config to drop the `--security-opt no-new-privileges` " +
		"flag, which restores the AppArmor profile transition at the cost " +
		"of a small isolation reduction."
}

// RunScanner runs a scanner container and returns the exit code and stdout/stderr
func (d *DockerRunner) RunScanner(ctx context.Context, cfg ScannerRunConfig) (stdout, stderr string, exitCode int, err error) {
	args := buildRunArgs(cfg)

	d.logger.Info("Running scanner container",
		zap.String("name", cfg.ContainerName),
		zap.String("image", cfg.Image),
		zap.Strings("command", cfg.Command),
		zap.Bool("no_new_privileges_disabled", cfg.DisableNoNewPrivileges),
	)

	// Create context with timeout
	runCtx := ctx
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	cmd := d.getDockerCmd(runCtx, args...)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()
	stdout = stdoutBuf.String()
	stderr = stderrBuf.String()

	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			// Kill the container on timeout
			d.KillContainer(context.Background(), cfg.ContainerName)
			return stdout, stderr, -1, fmt.Errorf("scanner timed out after %v", cfg.Timeout)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			return stdout, stderr, exitCode, nil // Non-zero exit is not necessarily an error for scanners
		}
		return stdout, stderr, -1, fmt.Errorf("docker run failed: %w", err)
	}

	return stdout, stderr, 0, nil
}

// KillContainer forcefully kills a running container
func (d *DockerRunner) KillContainer(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}
	cmd := d.getDockerCmd(ctx, "kill", name)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// StopContainer gracefully stops a container with timeout
func (d *DockerRunner) StopContainer(ctx context.Context, name string, timeout int) error {
	if name == "" {
		return nil
	}
	cmd := d.getDockerCmd(ctx, "stop", "-t", fmt.Sprintf("%d", timeout), name)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// ReadReportFile reads the SARIF report from the report directory
func (d *DockerRunner) ReadReportFile(reportDir string) ([]byte, error) {
	path := filepath.Join(reportDir, "results.sarif")
	data, err := os.ReadFile(path)
	if err != nil {
		// Try alternate names
		alternates := []string{"report.sarif", "results.json", "report.json"}
		for _, name := range alternates {
			altPath := filepath.Join(reportDir, name)
			data, err = os.ReadFile(altPath)
			if err == nil {
				return data, nil
			}
		}
		return nil, fmt.Errorf("no report file found in %s", reportDir)
	}
	return data, nil
}

// GetImageDigest returns the Docker image digest
func (d *DockerRunner) GetImageDigest(ctx context.Context, image string) (string, error) {
	cmd := d.getDockerCmd(ctx, "inspect", "--format", "{{.Id}}", image)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker inspect failed for %s: %w", image, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// GenerateContainerName creates a unique container name for a scanner run
func GenerateContainerName(scannerID, serverName string) string {
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%10000)
	// Sanitize names for Docker
	sanitized := strings.NewReplacer("/", "-", ":", "-", ".", "-").Replace(scannerID + "-" + serverName)
	return "mcpproxy-scanner-" + sanitized + "-" + suffix
}

// PrepareReportDir creates a temporary directory for scanner output
func PrepareReportDir(baseDir, jobID, scannerID string) (string, error) {
	dir := filepath.Join(baseDir, "scanner-reports", jobID, scannerID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create report directory: %w", err)
	}
	return dir, nil
}
