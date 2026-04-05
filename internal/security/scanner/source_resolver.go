package scanner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// SourceResolver automatically determines the source directory for scanning
// a server. It resolves source based on server type:
//   - Docker-isolated stdio servers: extracts changed files from running container
//   - HTTP/SSE servers: no source needed (scanners use mcp_connection)
//   - Local stdio servers: uses working_dir or command directory
type SourceResolver struct {
	logger *zap.Logger
}

// NewSourceResolver creates a new SourceResolver
func NewSourceResolver(logger *zap.Logger) *SourceResolver {
	return &SourceResolver{logger: logger}
}

// ServerInfo contains the information needed to resolve a server's source
type ServerInfo struct {
	Name       string // Server name
	Protocol   string // "stdio", "http", "sse", "streamable-http"
	Command    string // Command used to start the server (stdio only)
	Args       []string
	WorkingDir string            // Configured working directory
	URL        string            // Server URL (HTTP/SSE only)
	Env        map[string]string // Environment variables
}

// ResolvedSource contains the resolved source information for scanning
type ResolvedSource struct {
	SourceDir   string   // Host directory containing source files
	ContainerID string   // Docker container ID (if applicable)
	ServerURL   string   // URL for mcp_connection input (HTTP/SSE servers)
	Method      string   // How source was resolved: "docker_extract", "working_dir", "local_path", "url", "manual"
	Cleanup     func()   // Cleanup function (removes temp dirs)
	Files       []string // List of files found in source dir (capped)
	TotalFiles  int      // Total file count
	TotalSize   int64    // Total size in bytes
}

// Resolve determines the source directory for scanning a server.
// It tries these strategies in order:
//  1. Find running Docker container for the server (mcpproxy-<name>-*)
//  2. Use working_dir from server config
//  3. Use directory containing the server command
//  4. For HTTP servers, return URL for mcp_connection scanners
func (r *SourceResolver) Resolve(ctx context.Context, info ServerInfo) (*ResolvedSource, error) {
	// HTTP/SSE servers: scanners connect via URL
	if info.Protocol == "http" || info.Protocol == "sse" || info.Protocol == "streamable-http" {
		if info.URL != "" {
			return &ResolvedSource{
				ServerURL: info.URL,
				Method:    "url",
				Cleanup:   func() {},
			}, nil
		}
		return nil, fmt.Errorf("HTTP server %s has no URL configured", info.Name)
	}

	// Stdio servers: try Docker container first
	containerID, err := r.findServerContainer(ctx, info.Name)
	if err == nil && containerID != "" {
		sourceDir, cleanup, err := r.extractFromContainer(ctx, containerID, info.Name)
		if err == nil {
			r.logger.Info("Resolved source from Docker container",
				zap.String("server", info.Name),
				zap.String("container", containerID),
				zap.String("source_dir", sourceDir),
			)
			return &ResolvedSource{
				SourceDir:   sourceDir,
				ContainerID: containerID,
				Method:      "docker_extract",
				Cleanup:     cleanup,
			}, nil
		}
		r.logger.Warn("Failed to extract from container, trying fallback",
			zap.String("server", info.Name),
			zap.Error(err),
		)
	}

	// Fallback: use working_dir
	if info.WorkingDir != "" {
		if stat, err := os.Stat(info.WorkingDir); err == nil && stat.IsDir() {
			r.logger.Info("Resolved source from working_dir",
				zap.String("server", info.Name),
				zap.String("working_dir", info.WorkingDir),
			)
			return &ResolvedSource{
				SourceDir: info.WorkingDir,
				Method:    "working_dir",
				Cleanup:   func() {},
			}, nil
		}
	}

	// Fallback: use directory of the command itself
	if info.Command != "" {
		// Check if any args reference a local file/directory
		for _, arg := range info.Args {
			if !strings.HasPrefix(arg, "-") {
				absPath := arg
				if !filepath.IsAbs(arg) && info.WorkingDir != "" {
					absPath = filepath.Join(info.WorkingDir, arg)
				}
				if stat, err := os.Stat(absPath); err == nil {
					dir := absPath
					if !stat.IsDir() {
						dir = filepath.Dir(absPath)
					}
					r.logger.Info("Resolved source from command argument",
						zap.String("server", info.Name),
						zap.String("path", dir),
					)
					return &ResolvedSource{
						SourceDir: dir,
						Method:    "working_dir",
						Cleanup:   func() {},
					}, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("could not resolve source for server %s: no Docker container found, no working_dir configured, and no local file paths in command args", info.Name)
}

// findServerContainer finds the running Docker container for a server
// MCPProxy names containers as: mcpproxy-<sanitized-server-name>-<suffix>
func (r *SourceResolver) findServerContainer(ctx context.Context, serverName string) (string, error) {
	// Use docker ps with filter to find matching containers
	cmd := exec.CommandContext(ctx, "docker", "ps",
		"--filter", fmt.Sprintf("name=mcpproxy-%s-", sanitizeForDocker(serverName)),
		"--format", "{{.ID}}",
		"--no-trunc",
	)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker ps failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", fmt.Errorf("no running container found for server %s", serverName)
	}

	// Return first match
	return lines[0], nil
}

// extractFromContainer extracts changed files from a running container
// Uses `docker diff` to find added/changed files, then `docker cp` to extract them
func (r *SourceResolver) extractFromContainer(ctx context.Context, containerID, serverName string) (string, func(), error) {
	// Create temp directory for extracted source
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("mcpproxy-scan-%s-", serverName))
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tempDir) }

	// Get docker diff to find app-relevant directories
	cmd := exec.CommandContext(ctx, "docker", "diff", containerID)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("docker diff failed: %w", err)
	}

	// Identify app-relevant directories from the diff
	appDirs := r.findAppDirectories(stdout.String())

	if len(appDirs) == 0 {
		// Fallback: try UV git checkouts directly, then common app dirs
		// Do NOT copy /root entirely — it may contain 10K+ dependency files
		r.logger.Info("No specific app directories found in docker diff, trying direct paths",
			zap.String("container", containerID),
		)
		// Try UV git checkouts first (uvx --from pkg@git+URL)
		uvCheckoutCmd := exec.CommandContext(ctx, "docker", "exec", containerID, "find", "/root/.cache/uv/git-v0/checkouts", "-maxdepth", "2", "-mindepth", "2", "-type", "d")
		var uvOut bytes.Buffer
		uvCheckoutCmd.Stdout = &uvOut
		if uvCheckoutCmd.Run() == nil {
			for _, dir := range strings.Split(strings.TrimSpace(uvOut.String()), "\n") {
				if dir == "" {
					continue
				}
				destDir := filepath.Join(tempDir, "source")
				os.MkdirAll(destDir, 0755)
				cpCmd := exec.CommandContext(ctx, "docker", "cp", containerID+":"+dir+"/.", destDir)
				if cpCmd.Run() == nil {
					r.logger.Info("Extracted UV git checkout", zap.String("dir", dir))
					return tempDir, cleanup, nil
				}
			}
		}
		// Try common app dirs (NOT /root — too broad)
		for _, dir := range []string{"/app", "/src", "/opt/app"} {
			cpCmd := exec.CommandContext(ctx, "docker", "cp", containerID+":"+dir+"/.", filepath.Join(tempDir, filepath.Base(dir)))
			if cpCmd.Run() == nil {
				return tempDir, cleanup, nil
			}
		}
		cleanup()
		return "", nil, fmt.Errorf("no extractable source found in container %s", containerID)
	}

	// Extract each app directory
	for _, dir := range appDirs {
		destDir := filepath.Join(tempDir, filepath.Base(dir))
		os.MkdirAll(destDir, 0755)
		cpCmd := exec.CommandContext(ctx, "docker", "cp", containerID+":"+dir+"/.", destDir)
		if err := cpCmd.Run(); err != nil {
			r.logger.Debug("Failed to copy directory from container",
				zap.String("dir", dir),
				zap.Error(err),
			)
		}
	}

	return tempDir, cleanup, nil
}

// findAppDirectories analyzes docker diff output to find app-relevant directories.
// It looks for directories where packages were installed (node_modules, site-packages, etc.)
// and any user-added source files.
func (r *SourceResolver) findAppDirectories(diffOutput string) []string {
	seen := make(map[string]bool)
	var dirs []string

	for _, line := range strings.Split(diffOutput, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 3 {
			continue
		}
		action := line[0] // A=added, C=changed, D=deleted
		path := line[2:]

		if action == 'D' {
			continue // Skip deleted files
		}

		// Skip OS-level directories (not app code)
		if r.isSystemPath(path) {
			continue
		}

		// Find the top-level app directory
		dir := r.extractAppRoot(path)
		if dir != "" && !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}

	return dirs
}

// isSystemPath returns true for OS-level paths or dependency dirs that aren't app source
func (r *SourceResolver) isSystemPath(path string) bool {
	systemPrefixes := []string{
		"/etc/", "/var/", "/tmp/", "/proc/", "/sys/", "/dev/",
		"/usr/lib/", "/usr/bin/", "/usr/sbin/",
		"/lib/", "/bin/", "/sbin/",
	}
	for _, prefix := range systemPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	// Skip dependency directories (too large, not user code)
	if strings.Contains(path, "/site-packages/") ||
		strings.Contains(path, "/dist-packages/") {
		return true
	}
	// Skip standalone node_modules (but NOT inside npx cache which is the server itself)
	if strings.Contains(path, "/node_modules/") && !strings.Contains(path, "/_npx/") {
		return true
	}
	// Skip UV/pip dependency archives (keep git checkouts which are actual source)
	if strings.Contains(path, "/.cache/uv/archive-v0/") ||
		strings.Contains(path, "/.cache/pip/") {
		return true
	}
	return false
}

// extractAppRoot extracts the top-level application directory from a path.
// Identifies actual server source vs dependency code for various package managers.
func (r *SourceResolver) extractAppRoot(path string) string {
	// UV git checkouts: /root/.cache/uv/git-v0/checkouts/<hash>/<rev>/ → extract that specific checkout
	// This is the ACTUAL source code of a git-installed package (e.g., uvx --from pkg@git+URL)
	if strings.Contains(path, "/.cache/uv/git-v0/checkouts/") {
		// Extract: /root/.cache/uv/git-v0/checkouts/<hash>/<rev>
		parts := strings.Split(path, "/")
		for i, p := range parts {
			if p == "checkouts" && i+2 < len(parts) {
				return strings.Join(parts[:i+3], "/")
			}
		}
	}

	// npm npx cache: /root/.npm/_npx/<hash>/node_modules/<pkg> → extract the specific package
	if strings.Contains(path, "/.npm/_npx/") && strings.Contains(path, "/node_modules/") {
		idx := strings.Index(path, "/node_modules/")
		return path[:idx+len("/node_modules")]
	}

	// Common app directories
	appRoots := []string{"/app", "/src", "/opt/app", "/home"}
	for _, root := range appRoots {
		if strings.HasPrefix(path, root+"/") || path == root {
			return root
		}
	}

	// Root-level user files (but NOT .cache or .local — too broad, contains deps)
	if strings.HasPrefix(path, "/root/") &&
		!strings.HasPrefix(path, "/root/.cache") &&
		!strings.HasPrefix(path, "/root/.local") {
		parts := strings.SplitN(path[6:], "/", 2) // after "/root/"
		if len(parts) > 0 {
			return "/root/" + parts[0]
		}
	}

	return ""
}

// ResolveFullSource resolves the FULL source directory for a server, including
// all dependencies (site-packages, node_modules, UV archives, etc.).
// This is used for Pass 2 (supply chain audit) to scan the complete filesystem.
func (r *SourceResolver) ResolveFullSource(ctx context.Context, info ServerInfo) (*ResolvedSource, error) {
	// HTTP/SSE servers: no filesystem to scan
	if info.Protocol == "http" || info.Protocol == "sse" || info.Protocol == "streamable-http" {
		if info.URL != "" {
			return &ResolvedSource{
				ServerURL: info.URL,
				Method:    "url",
				Cleanup:   func() {},
			}, nil
		}
		return nil, fmt.Errorf("HTTP server %s has no URL configured", info.Name)
	}

	// Stdio servers: try Docker container first — extract FULL container
	containerID, err := r.findServerContainer(ctx, info.Name)
	if err == nil && containerID != "" {
		sourceDir, cleanup, err := r.extractFullFromContainer(ctx, containerID, info.Name)
		if err == nil {
			r.logger.Info("Resolved full source from Docker container for Pass 2",
				zap.String("server", info.Name),
				zap.String("container", containerID),
				zap.String("source_dir", sourceDir),
			)
			return &ResolvedSource{
				SourceDir:   sourceDir,
				ContainerID: containerID,
				Method:      "docker_extract",
				Cleanup:     cleanup,
			}, nil
		}
		r.logger.Warn("Failed to extract full source from container, trying fallback",
			zap.String("server", info.Name),
			zap.Error(err),
		)
	}

	// Fallback: use working_dir (same as Pass 1 — no container means no deps to scan)
	if info.WorkingDir != "" {
		if stat, err := os.Stat(info.WorkingDir); err == nil && stat.IsDir() {
			return &ResolvedSource{
				SourceDir: info.WorkingDir,
				Method:    "working_dir",
				Cleanup:   func() {},
			}, nil
		}
	}

	return nil, fmt.Errorf("could not resolve full source for server %s", info.Name)
}

// extractFullFromContainer extracts ALL changed files from a container
// WITHOUT filtering out dependency directories. Used for Pass 2 supply chain audit.
func (r *SourceResolver) extractFullFromContainer(ctx context.Context, containerID, serverName string) (string, func(), error) {
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("mcpproxy-scan-full-%s-", serverName))
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tempDir) }

	// Get docker diff to find ALL changed directories
	cmd := exec.CommandContext(ctx, "docker", "diff", containerID)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("docker diff failed: %w", err)
	}

	// Find all top-level changed directories (no filtering of deps)
	dirs := r.findAllChangedDirectories(stdout.String())

	if len(dirs) == 0 {
		// Try common directories including dependency paths
		for _, dir := range []string{"/app", "/src", "/opt/app", "/root"} {
			destDir := filepath.Join(tempDir, filepath.Base(dir))
			os.MkdirAll(destDir, 0755)
			cpCmd := exec.CommandContext(ctx, "docker", "cp", containerID+":"+dir+"/.", destDir)
			if cpCmd.Run() == nil {
				return tempDir, cleanup, nil
			}
		}
		cleanup()
		return "", nil, fmt.Errorf("no extractable source found in container %s", containerID)
	}

	// Extract each directory
	for _, dir := range dirs {
		destDir := filepath.Join(tempDir, filepath.Base(dir))
		os.MkdirAll(destDir, 0755)
		cpCmd := exec.CommandContext(ctx, "docker", "cp", containerID+":"+dir+"/.", destDir)
		if err := cpCmd.Run(); err != nil {
			r.logger.Debug("Failed to copy directory from container (Pass 2)",
				zap.String("dir", dir),
				zap.Error(err),
			)
		}
	}

	return tempDir, cleanup, nil
}

// findAllChangedDirectories analyzes docker diff output and returns ALL changed
// directories including dependency directories. Unlike findAppDirectories, this
// does NOT filter out site-packages, node_modules, UV archives, etc.
func (r *SourceResolver) findAllChangedDirectories(diffOutput string) []string {
	seen := make(map[string]bool)
	var dirs []string

	for _, line := range strings.Split(diffOutput, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 3 {
			continue
		}
		action := line[0]
		path := line[2:]

		if action == 'D' {
			continue
		}

		// Skip only OS-level system paths (proc, sys, dev, etc.)
		if r.isHardSystemPath(path) {
			continue
		}

		dir := r.extractTopLevelDir(path)
		if dir != "" && !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}

	return dirs
}

// isHardSystemPath returns true only for paths that are never useful for scanning
// (kernel, device, proc pseudo-filesystems). Does NOT filter dependency dirs.
func (r *SourceResolver) isHardSystemPath(path string) bool {
	hardSystemPrefixes := []string{
		"/proc/", "/sys/", "/dev/",
		"/etc/", "/var/run/", "/var/lock/",
	}
	for _, prefix := range hardSystemPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// extractTopLevelDir extracts the top-level directory from a path.
// For /root/.cache/uv/... it returns /root (broader than extractAppRoot).
func (r *SourceResolver) extractTopLevelDir(path string) string {
	// Known top-level directories
	topDirs := []string{"/app", "/src", "/opt", "/root", "/home", "/usr", "/var", "/tmp"}
	for _, dir := range topDirs {
		if strings.HasPrefix(path, dir+"/") || path == dir {
			return dir
		}
	}
	return ""
}

// CollectFileList walks a directory and returns a list of files (relative paths).
// Caps at MaxScannedFiles entries. Also returns total count and size.
func CollectFileList(dir string) (files []string, totalFiles int, totalSize int64) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			// Skip common uninteresting directories
			name := info.Name()
			if name == ".git" || name == "__pycache__" || name == ".npm" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		totalFiles++
		totalSize += info.Size()
		if len(files) < MaxScannedFiles {
			rel, _ := filepath.Rel(dir, path)
			if rel == "" {
				rel = path
			}
			files = append(files, rel)
		}
		return nil
	})
	return
}

// EnrichWithFileList populates the Files, TotalFiles, TotalSize fields on a ResolvedSource
func (r *SourceResolver) EnrichWithFileList(resolved *ResolvedSource) {
	if resolved.SourceDir == "" {
		return
	}
	resolved.Files, resolved.TotalFiles, resolved.TotalSize = CollectFileList(resolved.SourceDir)
}

// sanitizeForDocker removes characters invalid in Docker container names
func sanitizeForDocker(name string) string {
	return strings.NewReplacer("/", "-", ":", "-", ".", "-", " ", "-").Replace(name)
}
