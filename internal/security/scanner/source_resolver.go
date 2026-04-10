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
		sourceDir, cleanup, err := r.extractFromContainer(ctx, containerID, info)
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

	// For package-runner commands (npx, uvx, pipx, bunx, pnpm dlx, yarn dlx),
	// ALWAYS try the package cache FIRST before arg scanning. This prevents
	// a server like `npx @modelcontextprotocol/server-filesystem /tmp/data`
	// from picking up the user's data dir (`/tmp/data`) as the server
	// source — the arg is the filesystem server's allowed root, not code.
	if info.Command != "" && isPackageRunnerCommand(info.Command) {
		if resolved, err := r.resolveFromPackageCache(ctx, info); err == nil {
			return resolved, nil
		} else {
			r.logger.Debug("Package cache lookup failed, falling through",
				zap.String("server", info.Name),
				zap.String("command", info.Command),
				zap.Error(err),
			)
		}
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

	// Fallback: use directory of the command itself.
	// When we fall back to this arg-scan heuristic we require the candidate
	// directory to look like source code. Otherwise, a generic "path argument"
	// (e.g. a data directory passed to a filesystem server) would be
	// misclassified as code and fed to scanners.
	if info.Command != "" {
		for _, arg := range info.Args {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			absPath := arg
			if !filepath.IsAbs(arg) && info.WorkingDir != "" {
				absPath = filepath.Join(info.WorkingDir, arg)
			}
			stat, err := os.Stat(absPath)
			if err != nil {
				continue
			}
			dir := absPath
			if !stat.IsDir() {
				// For a concrete file arg (e.g. `python server.py`), the parent
				// directory is the source tree by convention. If the file is
				// a source file (.py/.js/.ts/.go/.rs) we accept it directly to
				// preserve the existing behavior for interpreter servers.
				if isSourceFile(absPath) {
					return &ResolvedSource{
						SourceDir: filepath.Dir(absPath),
						Method:    "working_dir",
						Cleanup:   func() {},
					}, nil
				}
				dir = filepath.Dir(absPath)
			}
			if !dirLooksLikeSource(dir) {
				r.logger.Debug("Arg path does not look like source tree, skipping",
					zap.String("server", info.Name),
					zap.String("arg", arg),
					zap.String("path", dir),
				)
				continue
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

	// Last resort: try the package cache for any other command (e.g. the user
	// has an absolute path to node_modules that we didn't match above).
	if info.Command != "" && !isPackageRunnerCommand(info.Command) {
		if resolved, err := r.resolveFromPackageCache(ctx, info); err == nil {
			return resolved, nil
		}
	}

	return nil, fmt.Errorf("could not resolve source for server %s: no Docker container found, no working_dir configured, and no local file paths in command args", info.Name)
}

// isPackageRunnerCommand returns true for commands that execute a package from
// a remote registry rather than running local source code. For these, the
// server source lives in the package manager's cache, not in any positional
// argument.
func isPackageRunnerCommand(command string) bool {
	base := strings.ToLower(filepath.Base(command))
	switch base {
	case "npx", "uvx", "pipx", "bunx":
		return true
	}
	return false
}

// isSourceFile returns true if the path looks like a source-code file by
// extension. Used so interpreter servers (`python server.py`) still resolve
// cleanly to the script's parent directory.
func isSourceFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".py", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".go", ".rs", ".rb", ".php", ".sh":
		return true
	}
	return false
}

// dirLooksLikeSource heuristically decides whether a directory is a source
// tree vs. e.g. a data directory passed as a filesystem-server root.
// A directory qualifies if it contains a known manifest file or at least one
// source file within two directory levels.
func dirLooksLikeSource(dir string) bool {
	markers := []string{
		"package.json", "pyproject.toml", "setup.py", "Cargo.toml",
		"go.mod", "composer.json", "Gemfile",
	}
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	// Walk up to depth 2 looking for at least one source file.
	found := false
	const maxDepth = 2
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil //nolint:nilerr // ignore walk errors, treat as "no source"
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return nil
		}
		depth := 0
		if rel != "." {
			depth = strings.Count(rel, string(filepath.Separator)) + 1
		}
		if info.IsDir() {
			if depth > maxDepth {
				return filepath.SkipDir
			}
			// Skip noisy dirs
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == "__pycache__" || name == ".venv" {
				return filepath.SkipDir
			}
			return nil
		}
		if depth > maxDepth {
			return nil
		}
		if isSourceFile(path) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
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

// extractFromContainer extracts changed files from a running container.
// Two resolution strategies are combined:
//
//  1. For package-runner servers (npx, uvx) the target package is located
//     directly via `docker exec` and copied out. This is necessary because the
//     target lives inside a Docker volume mount (e.g. /root/.npm for the
//     shared npx cache) and volume contents never appear in `docker diff`.
//
//  2. `docker diff` is used to find any additional user-added app source in
//     the container's writable layer (e.g. /app, /src) and copy those too.
//
// The npx diff path is filtered by target package name so sibling packages
// hoisted into the same shared cache cannot leak into the scan.
func (r *SourceResolver) extractFromContainer(ctx context.Context, containerID string, info ServerInfo) (string, func(), error) {
	serverName := info.Name
	// Create temp directory for extracted source
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("mcpproxy-scan-%s-", serverName))
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tempDir) }

	extracted := false

	// Strategy 1: direct target package lookup for npx/uvx servers.
	if targetDir := r.findContainerTargetDir(ctx, containerID, info); targetDir != "" {
		destDir := filepath.Join(tempDir, "target")
		_ = os.MkdirAll(destDir, 0755)
		cpCmd := exec.CommandContext(ctx, "docker", "cp", containerID+":"+targetDir+"/.", destDir)
		if err := cpCmd.Run(); err == nil {
			r.logger.Info("Extracted target package from container",
				zap.String("server", serverName),
				zap.String("target_dir", targetDir),
			)
			extracted = true
		} else {
			r.logger.Debug("docker cp of target package failed",
				zap.String("server", serverName),
				zap.String("target_dir", targetDir),
				zap.Error(err),
			)
		}
	}

	// Strategy 2: docker diff for any user-added app source.
	cmd := exec.CommandContext(ctx, "docker", "diff", containerID)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	diffErr := cmd.Run()

	var appDirs []string
	if diffErr == nil {
		// Identify app-relevant directories from the diff, scoped to the target npx package.
		// If the server is not an npx command, targetNpxPkg is empty and no npx-cache paths
		// are accepted at all (they would otherwise pollute the scan with sibling packages).
		targetNpxPkg := npxTargetPackage(info)
		appDirs = r.findAppDirectories(stdout.String(), targetNpxPkg)
	} else if !extracted {
		cleanup()
		return "", nil, fmt.Errorf("docker diff failed: %w", diffErr)
	}

	if len(appDirs) == 0 && !extracted {
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
// and any user-added source files. When targetNpxPkg is non-empty, paths in an
// npx cache that belong to a different package are filtered out — this prevents
// sibling packages (hoisted into the same /.npm/_npx/<hash>/node_modules) from
// leaking into scans of a specific target package.
func (r *SourceResolver) findAppDirectories(diffOutput, targetNpxPkg string) []string {
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
		dir := r.extractAppRoot(path, targetNpxPkg)
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
// targetNpxPkg, when non-empty, restricts npx cache matches to a specific package
// (e.g. "@modelcontextprotocol/server-everything") so unrelated sibling packages
// hoisted into the same /.npm/_npx/<hash>/node_modules bucket are excluded.
func (r *SourceResolver) extractAppRoot(path, targetNpxPkg string) string {
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
	// directory. Without this isolation, the shared bucket directory is returned and
	// `docker cp` copies ALL peer packages, causing findings to reference unrelated
	// code (e.g. scanning everything-server surfaces @just-every/mcp-screenshot-website-fast).
	if strings.Contains(path, "/.npm/_npx/") && strings.Contains(path, "/node_modules/") {
		return extractNpxPackageDir(path, targetNpxPkg)
	}

	// Common app directories
	appRoots := []string{"/app", "/src", "/opt/app", "/home"}
	for _, root := range appRoots {
		if strings.HasPrefix(path, root+"/") || path == root {
			return root
		}
	}

	// Root-level user files. Deliberately reject hidden (dot) directories —
	// those are package manager caches, config, and volume mounts (.npm, .cache,
	// .local, .config, .venv, ...) which are either already handled by the
	// specific matchers above, or would pull in tens of thousands of unrelated
	// files (e.g. /root/.npm is the shared Docker volume containing every
	// package ever used by any container that mounts it).
	if strings.HasPrefix(path, "/root/") {
		parts := strings.SplitN(path[6:], "/", 2) // after "/root/"
		if len(parts) > 0 && parts[0] != "" && !strings.HasPrefix(parts[0], ".") {
			return "/root/" + parts[0]
		}
	}

	return ""
}

// extractNpxPackageDir returns the directory of the specific package a file
// belongs to inside an npx cache. Scoped packages (@scope/name) consume two
// path segments; unscoped packages consume one. The caller MUST provide the
// target package name; when targetPkg is empty the function returns "" to
// avoid arbitrarily picking a package from a shared bucket (which is exactly
// the sibling-leak bug this helper exists to prevent). npx hoists ALL
// transitive dependencies into the same /_npx/<hash>/node_modules/ bucket
// alongside the requested package, so without a known target we cannot safely
// attribute a path to "the server's own code".
func extractNpxPackageDir(path, targetPkg string) string {
	if targetPkg == "" {
		return ""
	}
	const marker = "/node_modules/"
	idx := strings.Index(path, marker)
	if idx == -1 {
		return ""
	}
	rest := path[idx+len(marker):]
	if rest == "" {
		return ""
	}
	parts := strings.SplitN(rest, "/", 3)
	var pkgName, pkgDir string
	if strings.HasPrefix(parts[0], "@") {
		// Scoped package requires two segments: @scope/name
		if len(parts) < 2 || parts[1] == "" {
			return ""
		}
		pkgName = parts[0] + "/" + parts[1]
		pkgDir = path[:idx+len(marker)] + parts[0] + "/" + parts[1]
	} else {
		if parts[0] == "" {
			return ""
		}
		pkgName = parts[0]
		pkgDir = path[:idx+len(marker)] + parts[0]
	}
	if pkgName != targetPkg {
		return ""
	}
	return pkgDir
}

// npxTargetPackage returns the canonical package name (with version specifier
// stripped) that an npx-based server launches. Returns "" if the command is
// not npx or no package name can be determined.
func npxTargetPackage(info ServerInfo) string {
	if info.Command == "" || filepath.Base(info.Command) != "npx" {
		return ""
	}
	for _, arg := range info.Args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		pkg := arg
		// Strip version: @scope/name@1.0.0 → @scope/name, pkg@1.0.0 → pkg
		if idx := strings.LastIndex(pkg, "@"); idx > 0 {
			pkg = pkg[:idx]
		}
		return pkg
	}
	return ""
}

// uvxTargetPackage returns the Python package name a uvx-based server launches.
// Supports `uvx <pkg>`, `uvx --from <pkg> <cmd>`, and `uvx <pkg>@<version>`.
// Git URLs (git+https://...) are reduced to the repo name. Returns "" if the
// command is not uvx or no package name can be determined.
func uvxTargetPackage(info ServerInfo) string {
	if info.Command == "" || filepath.Base(info.Command) != "uvx" {
		return ""
	}
	var raw string
	for i, arg := range info.Args {
		if arg == "--from" && i+1 < len(info.Args) {
			raw = info.Args[i+1]
			break
		}
		if !strings.HasPrefix(arg, "-") {
			raw = arg
			break
		}
	}
	if raw == "" {
		return ""
	}
	// Git URL: extract the repo name.
	if strings.HasPrefix(raw, "git+") {
		url := strings.TrimPrefix(raw, "git+")
		url = strings.TrimSuffix(url, ".git")
		if idx := strings.LastIndex(url, "/"); idx != -1 && idx+1 < len(url) {
			return url[idx+1:]
		}
		return ""
	}
	// Strip version specifier: pkg@1.0 or pkg==1.0.
	if idx := strings.LastIndex(raw, "@"); idx > 0 {
		raw = raw[:idx]
	}
	if idx := strings.Index(raw, "=="); idx > 0 {
		raw = raw[:idx]
	}
	return raw
}

// findContainerTargetDir locates the target package's directory inside a
// running Docker container using `docker exec`. This is the resolution of
// choice for package-runner servers (npx, uvx) whose target lives inside a
// mounted cache volume — volume contents never appear in `docker diff`, so
// the diff-based scanners would otherwise either miss the target entirely or
// (worse) fall back to copying the whole volume and dragging sibling packages
// into the scan. Returns "" if the server is not a package-runner or the
// target cannot be located.
func (r *SourceResolver) findContainerTargetDir(ctx context.Context, containerID string, info ServerInfo) string {
	if pkg := npxTargetPackage(info); pkg != "" {
		// Shell-escape single quotes in the package name and glob for it under
		// every npx cache bucket inside the container.
		escaped := strings.ReplaceAll(pkg, "'", `'\''`)
		script := fmt.Sprintf(
			"ls -d /root/.npm/_npx/*/node_modules/'%s' 2>/dev/null | head -n 1",
			escaped,
		)
		if out, ok := dockerExecCapture(ctx, containerID, script); ok {
			if path := strings.TrimSpace(out); path != "" {
				return path
			}
		}
		return ""
	}
	if pkg := uvxTargetPackage(info); pkg != "" {
		// uv/uvx install locations (from most- to least-specific):
		//   1. /root/.local/share/uv/tools/<pkg>/                              (persistent `uv tool install`)
		//   2. /root/.cache/uv/archive-v0/<hash>/lib/pythonX.Y/site-packages/<pkg>/  (ephemeral uvx env)
		//   3. /usr/local/lib/pythonX.Y/site-packages/<pkg>/                   (system pip install)
		//
		// Wheel-normalised names use underscores, PEP 503 names use hyphens, so
		// try both variants. We search in priority order and stop at the first hit.
		escaped := strings.ReplaceAll(pkg, "'", `'\''`)
		lower := strings.ToLower(escaped)
		underscore := strings.ReplaceAll(lower, "-", "_")
		hyphen := strings.ReplaceAll(lower, "_", "-")
		// Build a deduped list of candidate leaf names.
		seen := map[string]bool{}
		var names []string
		for _, n := range []string{escaped, lower, underscore, hyphen} {
			if n != "" && !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
		var globs []string
		for _, n := range names {
			globs = append(globs,
				"/root/.local/share/uv/tools/'"+n+"'",
				"/root/.cache/uv/archive-v0/*/lib/python*/site-packages/'"+n+"'",
				"/usr/local/lib/python*/site-packages/'"+n+"'",
				"/usr/lib/python*/site-packages/'"+n+"'",
				"/usr/lib/python*/dist-packages/'"+n+"'",
			)
		}
		script := "for p in " + strings.Join(globs, " ") + "; do for d in $p; do [ -d \"$d\" ] && { echo \"$d\"; exit 0; }; done; done; exit 1"
		if out, ok := dockerExecCapture(ctx, containerID, script); ok {
			if path := strings.TrimSpace(out); path != "" {
				return path
			}
		}
	}
	return ""
}

// dockerExecCapture runs a shell command inside a container and returns its
// stdout. Returns ok=false if the command fails or exits non-zero.
func dockerExecCapture(ctx context.Context, containerID, script string) (string, bool) {
	cmd := exec.CommandContext(ctx, "docker", "exec", containerID, "sh", "-c", script)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return stdout.String(), true
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
		sourceDir, cleanup, err := r.extractFullFromContainer(ctx, containerID, info)
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
// that belong to the target server's source and its dependency trees. Used for
// Pass 2 supply chain audit. Unlike Pass 1, this intentionally INCLUDES
// installed dependencies (site-packages, node_modules) so CVE scanners can see
// the full supply chain — but it does NOT include unrelated system files such
// as the Python standard library, which would otherwise flood the scan with
// false positives (e.g. flagging shutil.py or tempfile.py as "malicious").
func (r *SourceResolver) extractFullFromContainer(ctx context.Context, containerID string, info ServerInfo) (string, func(), error) {
	serverName := info.Name
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("mcpproxy-scan-full-%s-", serverName))
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tempDir) }

	extracted := false

	// Strategy 1: direct target package lookup for npx/uvx servers. The target
	// (and, because npm/uv hoist dependencies into the same tree, also its deps)
	// lives inside a Docker volume mount invisible to `docker diff`, so we must
	// locate it via `docker exec` instead.
	if targetDir := r.findContainerTargetDir(ctx, containerID, info); targetDir != "" {
		destDir := filepath.Join(tempDir, "target")
		_ = os.MkdirAll(destDir, 0755)
		cpCmd := exec.CommandContext(ctx, "docker", "cp", containerID+":"+targetDir+"/.", destDir)
		if err := cpCmd.Run(); err == nil {
			r.logger.Info("Extracted target package from container (Pass 2)",
				zap.String("server", serverName),
				zap.String("target_dir", targetDir),
			)
			extracted = true
		} else {
			r.logger.Debug("docker cp of target package failed (Pass 2)",
				zap.String("server", serverName),
				zap.String("target_dir", targetDir),
				zap.Error(err),
			)
		}
	}

	// Strategy 2: docker diff for additional dependency subtrees that the
	// server touched (e.g. anything at /app, /src, or installed site-packages).
	cmd := exec.CommandContext(ctx, "docker", "diff", containerID)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	diffErr := cmd.Run()

	var dirs []string
	if diffErr == nil {
		dirs = r.findAllChangedDirectories(stdout.String(), npxTargetPackage(info))
	} else if !extracted {
		cleanup()
		return "", nil, fmt.Errorf("docker diff failed: %w", diffErr)
	}

	if len(dirs) == 0 && !extracted {
		// Narrow fallback: only try conventional app source dirs. Do NOT fall back
		// to /root or /usr — they contain the npm cache and Python stdlib which
		// would produce huge, noisy scans and cause stdlib false positives.
		for _, dir := range []string{"/app", "/src", "/opt/app"} {
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

	// Extract each directory. Use a content hash of the path for the destination
	// name so that multiple site-packages / archive subtrees under the same parent
	// don't collide when filepath.Base returns the same leaf.
	for i, dir := range dirs {
		destName := fmt.Sprintf("%d-%s", i, filepath.Base(dir))
		destDir := filepath.Join(tempDir, destName)
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

// findAllChangedDirectories analyzes docker diff output and returns specific
// dependency/source subtrees that belong to the target server. It deliberately
// filters out unrelated system files (Python stdlib, /usr/lib, OS pseudo-fs)
// while still including installed dependencies for supply chain auditing.
func (r *SourceResolver) findAllChangedDirectories(diffOutput, targetNpxPkg string) []string {
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

		if r.isHardSystemPath(path) {
			continue
		}

		dir := r.extractPass2Dir(path, targetNpxPkg)
		if dir != "" && !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}

	return dirs
}

// isHardSystemPath returns true for paths that are never useful for scanning.
// This includes OS pseudo-filesystems (proc, sys, dev), system config, and the
// Python standard library (which lives under /usr/local/lib/pythonX/ directly
// but NOT in site-packages/dist-packages — those are installed dependencies
// and ARE worth scanning for supply chain audit).
func (r *SourceResolver) isHardSystemPath(path string) bool {
	hardSystemPrefixes := []string{
		"/proc/", "/sys/", "/dev/",
		"/etc/", "/var/run/", "/var/lock/",
		"/usr/lib/", "/usr/bin/", "/usr/sbin/",
		"/lib/", "/bin/", "/sbin/",
	}
	for _, prefix := range hardSystemPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	// Python stdlib: anything under /.../lib/pythonX.Y/ that is NOT inside
	// site-packages or dist-packages is stdlib shipped with the base image.
	// Scanning it produces false positives (shutil flagged as "shell command
	// execution", tempfile flagged as "obfuscated payload", etc.).
	if isPythonStdlibPath(path) {
		return true
	}
	return false
}

// isPythonStdlibPath reports whether a path is inside the Python standard
// library tree (not in site-packages/dist-packages).
func isPythonStdlibPath(path string) bool {
	if !strings.Contains(path, "/python") {
		return false
	}
	// Look for a segment like "pythonX" or "pythonX.Y" preceded by "lib/".
	parts := strings.Split(path, "/")
	for i := 1; i < len(parts); i++ {
		if parts[i-1] != "lib" {
			continue
		}
		seg := parts[i]
		if !strings.HasPrefix(seg, "python") {
			continue
		}
		rest := seg[len("python"):]
		if rest == "" {
			continue
		}
		// Expect a digit after "python" (python3, python3.11, python313, ...)
		if rest[0] < '0' || rest[0] > '9' {
			continue
		}
		// Everything after .../lib/pythonX[.Y]/ is stdlib unless it then
		// descends into site-packages or dist-packages.
		tail := strings.Join(parts[i+1:], "/")
		if tail == "" {
			return true
		}
		if strings.HasPrefix(tail, "site-packages/") || tail == "site-packages" ||
			strings.HasPrefix(tail, "dist-packages/") || tail == "dist-packages" {
			return false
		}
		return true
	}
	return false
}

// extractPass2Dir returns the most specific dependency/source subtree a path
// belongs to, for Pass 2 supply chain scanning. Returns "" when the path does
// not belong to any recognised app or dependency location — in particular it
// NEVER returns broad roots like /usr or /root, which would cause `docker cp`
// to copy thousands of unrelated files (including Python stdlib) into the scan.
func (r *SourceResolver) extractPass2Dir(path, targetNpxPkg string) string {
	// Application source dirs — narrow, safe to copy whole.
	for _, d := range []string{"/app", "/src", "/opt/app"} {
		if strings.HasPrefix(path, d+"/") || path == d {
			return d
		}
	}

	// Python installed deps: return the full site-packages / dist-packages dir.
	// This is the root of the supply chain for uvx/pip-installed servers.
	for _, marker := range []string{"/site-packages/", "/dist-packages/"} {
		if idx := strings.Index(path, marker); idx != -1 {
			return path[:idx+len(marker)-1] // drop trailing slash
		}
	}

	// npm npx cache: isolate to the specific target package (prevents sibling
	// packages hoisted into the same npx bucket from leaking into the scan).
	if strings.Contains(path, "/.npm/_npx/") && strings.Contains(path, "/node_modules/") {
		return extractNpxPackageDir(path, targetNpxPkg)
	}

	// Regular node_modules (npm install, not npx): return the node_modules root
	// — supply chain audit wants to see all npm dependencies together.
	if strings.Contains(path, "/node_modules/") {
		idx := strings.Index(path, "/node_modules/")
		return path[:idx+len("/node_modules")]
	}

	// UV git checkouts — actual source code of a git-installed package.
	if strings.Contains(path, "/.cache/uv/git-v0/checkouts/") {
		parts := strings.Split(path, "/")
		for i, p := range parts {
			if p == "checkouts" && i+2 < len(parts) {
				return strings.Join(parts[:i+3], "/")
			}
		}
	}

	// UV archive cache (wheel cache): /.cache/uv/archive-v0/<hash>/<pkg>/
	if idx := strings.Index(path, "/.cache/uv/archive-v0/"); idx != -1 {
		rest := path[idx+len("/.cache/uv/archive-v0/"):]
		parts := strings.SplitN(rest, "/", 2)
		if parts[0] != "" {
			return path[:idx+len("/.cache/uv/archive-v0/")] + parts[0]
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

// resolveFromPackageCache attempts to find server source in local package manager caches.
// Supports npx (npm), uvx (uv/pip), and pipx.
func (r *SourceResolver) resolveFromPackageCache(ctx context.Context, info ServerInfo) (*ResolvedSource, error) {
	cmdBase := filepath.Base(info.Command)

	switch cmdBase {
	case "npx":
		return r.resolveNpxCache(info)
	case "uvx":
		return r.resolveUvxCache(info)
	}

	return nil, fmt.Errorf("unsupported command %q for package cache resolution", cmdBase)
}

// resolveNpxCache finds an npx package's source in ~/.npm/_npx/*/node_modules/<package>/
func (r *SourceResolver) resolveNpxCache(info ServerInfo) (*ResolvedSource, error) {
	// Extract package name from args (first non-flag arg)
	pkgName := ""
	for _, arg := range info.Args {
		if !strings.HasPrefix(arg, "-") {
			pkgName = arg
			break
		}
	}
	if pkgName == "" {
		return nil, fmt.Errorf("no package name found in npx args")
	}

	// Strip version specifier: @modelcontextprotocol/server-everything@1.0.0 → @modelcontextprotocol/server-everything
	if idx := strings.LastIndex(pkgName, "@"); idx > 0 {
		pkgName = pkgName[:idx]
	}

	// Find npm cache directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}

	npxCacheDir := filepath.Join(homeDir, ".npm", "_npx")
	if _, err := os.Stat(npxCacheDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("npx cache directory not found: %s", npxCacheDir)
	}

	// Search for the package in npx cache: ~/.npm/_npx/<hash>/node_modules/<package>/
	var bestMatch string
	var bestModTime int64

	entries, err := os.ReadDir(npxCacheDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read npx cache: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidatePath := filepath.Join(npxCacheDir, entry.Name(), "node_modules", pkgName)
		stat, err := os.Stat(candidatePath)
		if err != nil {
			continue
		}
		if stat.IsDir() {
			modTime := stat.ModTime().Unix()
			if modTime > bestModTime {
				bestModTime = modTime
				bestMatch = candidatePath
			}
		}
	}

	if bestMatch == "" {
		return nil, fmt.Errorf("package %q not found in npx cache (%s)", pkgName, npxCacheDir)
	}

	r.logger.Info("Resolved source from npx cache",
		zap.String("server", info.Name),
		zap.String("package", pkgName),
		zap.String("path", bestMatch),
	)

	return &ResolvedSource{
		SourceDir: bestMatch,
		Method:    "npx_cache",
		Cleanup:   func() {},
	}, nil
}

// resolveUvxCache finds a uvx package's source in ~/.cache/uv/ or ~/.local/share/uv/tools/
func (r *SourceResolver) resolveUvxCache(info ServerInfo) (*ResolvedSource, error) {
	// Extract package name from args
	// uvx supports: uvx <package>, uvx --from <package> <command>, uvx git+<url>
	pkgName := ""
	isGitURL := false
	for i, arg := range info.Args {
		if arg == "--from" && i+1 < len(info.Args) {
			pkgName = info.Args[i+1]
			break
		}
		if !strings.HasPrefix(arg, "-") {
			pkgName = arg
			break
		}
	}
	if pkgName == "" {
		return nil, fmt.Errorf("no package name found in uvx args")
	}

	// Check if it's a git URL: git+https://github.com/...
	if strings.HasPrefix(pkgName, "git+") {
		isGitURL = true
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}

	// Strategy 1: UV git checkouts (for git+URL packages)
	// The cache structure is: ~/.cache/uv/git-v0/checkouts/<hash>/<rev>/
	// We must match the checkout to the actual git URL by checking for repo-specific files,
	// not just picking the newest subdirectory (which could be from a different package).
	if isGitURL {
		gitCheckoutsDir := filepath.Join(homeDir, ".cache", "uv", "git-v0", "checkouts")
		// Extract repo name from git URL for matching: git+https://github.com/org/repo → repo
		repoName := ""
		gitURL := strings.TrimPrefix(pkgName, "git+")
		if parts := strings.Split(strings.TrimSuffix(gitURL, ".git"), "/"); len(parts) > 0 {
			repoName = parts[len(parts)-1]
		}
		if dir, err := r.findGitCheckoutByRepo(gitCheckoutsDir, repoName, pkgName); err == nil {
			r.logger.Info("Resolved source from UV git checkout",
				zap.String("server", info.Name),
				zap.String("repo", repoName),
				zap.String("path", dir),
			)
			return &ResolvedSource{
				SourceDir: dir,
				Method:    "uvx_cache",
				Cleanup:   func() {},
			}, nil
		}
	}

	// Strategy 2: UV tools directory (for regular packages)
	// Strip version: package@version → package
	cleanPkg := pkgName
	if idx := strings.LastIndex(cleanPkg, "@"); idx > 0 {
		cleanPkg = cleanPkg[:idx]
	}
	// Also strip git+ prefix and URL
	if strings.HasPrefix(cleanPkg, "git+") {
		// Extract package name from URL: git+https://github.com/org/repo → repo
		parts := strings.Split(cleanPkg, "/")
		if len(parts) > 0 {
			cleanPkg = parts[len(parts)-1]
		}
	}

	toolsDir := filepath.Join(homeDir, ".local", "share", "uv", "tools", cleanPkg)
	if stat, err := os.Stat(toolsDir); err == nil && stat.IsDir() {
		r.logger.Info("Resolved source from UV tools directory",
			zap.String("server", info.Name),
			zap.String("package", cleanPkg),
			zap.String("path", toolsDir),
		)
		return &ResolvedSource{
			SourceDir: toolsDir,
			Method:    "uvx_cache",
			Cleanup:   func() {},
		}, nil
	}

	return nil, fmt.Errorf("package %q not found in UV cache", pkgName)
}

// findGitCheckoutByRepo searches UV git checkouts for a directory that matches the given repo.
// It walks checkouts/<hash>/<rev>/ directories and checks for repo-identifying markers
// (pyproject.toml with matching name, or .git/config with matching URL).
func (r *SourceResolver) findGitCheckoutByRepo(checkoutsDir, repoName, gitURL string) (string, error) {
	if _, err := os.Stat(checkoutsDir); os.IsNotExist(err) {
		return "", err
	}

	gitURL = strings.TrimPrefix(gitURL, "git+")

	hashDirs, err := os.ReadDir(checkoutsDir)
	if err != nil {
		return "", err
	}

	var bestPath string
	var bestModTime int64

	for _, hashDir := range hashDirs {
		if !hashDir.IsDir() {
			continue
		}
		hashPath := filepath.Join(checkoutsDir, hashDir.Name())
		revDirs, err := os.ReadDir(hashPath)
		if err != nil {
			continue
		}
		for _, revDir := range revDirs {
			if !revDir.IsDir() {
				continue
			}
			candidate := filepath.Join(hashPath, revDir.Name())

			// Check pyproject.toml for package name match
			pyproject, err := os.ReadFile(filepath.Join(candidate, "pyproject.toml"))
			if err == nil {
				content := string(pyproject)
				// Match repo name in project name (handles hyphens/underscores)
				normalizedRepo := strings.ReplaceAll(strings.ToLower(repoName), "-", "[_-]")
				if matched, _ := filepath.Match("*"+strings.ToLower(repoName)+"*", strings.ToLower(content)); matched ||
					strings.Contains(strings.ToLower(content), strings.ToLower(repoName)) ||
					strings.Contains(strings.ToLower(content), strings.ReplaceAll(strings.ToLower(repoName), "-", "_")) {
					_ = normalizedRepo
					info, err := revDir.Info()
					if err == nil && info.ModTime().Unix() > bestModTime {
						bestModTime = info.ModTime().Unix()
						bestPath = candidate
					}
					continue
				}
			}

			// Fallback: check .git/config for URL match
			gitConfig, err := os.ReadFile(filepath.Join(candidate, ".git", "config"))
			if err == nil && strings.Contains(string(gitConfig), gitURL) {
				info, err := revDir.Info()
				if err == nil && info.ModTime().Unix() > bestModTime {
					bestModTime = info.ModTime().Unix()
					bestPath = candidate
				}
			}
		}
	}

	if bestPath == "" {
		return "", fmt.Errorf("no git checkout found matching repo %q", repoName)
	}
	return bestPath, nil
}

// sanitizeForDocker removes characters invalid in Docker container names
func sanitizeForDocker(name string) string {
	return strings.NewReplacer("/", "-", ":", "-", ".", "-", " ", "-").Replace(name)
}
