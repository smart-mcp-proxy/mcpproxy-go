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

	"go.uber.org/zap"
)

// DockerRunner executes Docker operations for scanner containers
type DockerRunner struct {
	logger *zap.Logger
}

// NewDockerRunner creates a new DockerRunner
func NewDockerRunner(logger *zap.Logger) *DockerRunner {
	return &DockerRunner{logger: logger}
}

// IsDockerAvailable checks if Docker daemon is running
func (d *DockerRunner) IsDockerAvailable(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// PullImage pulls a Docker image with progress logging
func (d *DockerRunner) PullImage(ctx context.Context, image string) error {
	d.logger.Info("Pulling Docker image", zap.String("image", image))
	cmd := exec.CommandContext(ctx, "docker", "pull", image)
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
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", image)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// RemoveImage removes a Docker image
func (d *DockerRunner) RemoveImage(ctx context.Context, image string) error {
	cmd := exec.CommandContext(ctx, "docker", "rmi", "-f", image)
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
	NetworkMode   string            // "none", "bridge", or custom network name
	Timeout       time.Duration     // Container execution timeout
	ReadOnly      bool              // Read-only root filesystem
	MemoryLimit   string            // e.g., "512m"
}

// RunScanner runs a scanner container and returns the exit code and stdout/stderr
func (d *DockerRunner) RunScanner(ctx context.Context, cfg ScannerRunConfig) (stdout, stderr string, exitCode int, err error) {
	args := []string{"run", "--rm"}

	// Container name
	if cfg.ContainerName != "" {
		args = append(args, "--name", cfg.ContainerName)
	}

	// Read-only root filesystem
	if cfg.ReadOnly {
		args = append(args, "--read-only")
	}

	// Network mode
	if cfg.NetworkMode != "" {
		args = append(args, "--network", cfg.NetworkMode)
	}

	// Memory limit
	if cfg.MemoryLimit != "" {
		args = append(args, "--memory", cfg.MemoryLimit)
	}

	// Security: no new privileges
	args = append(args, "--security-opt", "no-new-privileges")

	// Tmpfs for temp files
	args = append(args, "--tmpfs", "/tmp:rw,noexec,nosuid,size=100m")

	// Mount source directory read-only
	if cfg.SourceDir != "" {
		args = append(args, "-v", cfg.SourceDir+":/scan/source:ro")
	}

	// Mount report directory writable
	if cfg.ReportDir != "" {
		args = append(args, "-v", cfg.ReportDir+":/scan/report:rw")
	}

	// Environment variables
	for k, v := range cfg.Env {
		args = append(args, "-e", k+"="+v)
	}

	// Image
	args = append(args, cfg.Image)

	// Command
	args = append(args, cfg.Command...)

	d.logger.Info("Running scanner container",
		zap.String("name", cfg.ContainerName),
		zap.String("image", cfg.Image),
		zap.Strings("command", cfg.Command),
	)

	// Create context with timeout
	runCtx := ctx
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, "docker", args...)
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
	cmd := exec.CommandContext(ctx, "docker", "kill", name)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// StopContainer gracefully stops a container with timeout
func (d *DockerRunner) StopContainer(ctx context.Context, name string, timeout int) error {
	if name == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "docker", "stop", "-t", fmt.Sprintf("%d", timeout), name)
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
	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.Id}}", image)
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
