package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestSourceResolverHTTPServer(t *testing.T) {
	r := NewSourceResolver(zap.NewNop())
	result, err := r.Resolve(context.Background(), ServerInfo{
		Name:     "test-http",
		Protocol: "http",
		URL:      "https://api.example.com/mcp",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.ServerURL != "https://api.example.com/mcp" {
		t.Errorf("expected URL, got %q", result.ServerURL)
	}
	if result.Method != "url" {
		t.Errorf("expected method 'url', got %q", result.Method)
	}
	result.Cleanup()
}

func TestSourceResolverHTTPServerNoURL(t *testing.T) {
	r := NewSourceResolver(zap.NewNop())
	_, err := r.Resolve(context.Background(), ServerInfo{
		Name:     "test-http",
		Protocol: "http",
	})
	if err == nil {
		t.Error("expected error for HTTP server without URL")
	}
}

func TestSourceResolverSSEServer(t *testing.T) {
	r := NewSourceResolver(zap.NewNop())
	result, err := r.Resolve(context.Background(), ServerInfo{
		Name:     "test-sse",
		Protocol: "sse",
		URL:      "http://localhost:3000/sse",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.Method != "url" {
		t.Errorf("expected method 'url', got %q", result.Method)
	}
	result.Cleanup()
}

func TestSourceResolverWorkingDir(t *testing.T) {
	dir := t.TempDir()
	// Write a file so the dir is non-empty
	os.WriteFile(filepath.Join(dir, "server.py"), []byte("# test"), 0644)

	r := NewSourceResolver(zap.NewNop())
	result, err := r.Resolve(context.Background(), ServerInfo{
		Name:       "test-stdio",
		Protocol:   "stdio",
		Command:    "python",
		Args:       []string{"server.py"},
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.SourceDir != dir {
		t.Errorf("expected source_dir %q, got %q", dir, result.SourceDir)
	}
	if result.Method != "working_dir" {
		t.Errorf("expected method 'working_dir', got %q", result.Method)
	}
	result.Cleanup()
}

func TestSourceResolverCommandArgs(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "server.py")
	os.WriteFile(scriptPath, []byte("# test"), 0644)

	r := NewSourceResolver(zap.NewNop())
	result, err := r.Resolve(context.Background(), ServerInfo{
		Name:     "test-stdio",
		Protocol: "stdio",
		Command:  "python",
		Args:     []string{scriptPath},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.SourceDir != dir {
		t.Errorf("expected source_dir %q, got %q", dir, result.SourceDir)
	}
	result.Cleanup()
}

func TestSourceResolverNoSource(t *testing.T) {
	r := NewSourceResolver(zap.NewNop())
	_, err := r.Resolve(context.Background(), ServerInfo{
		Name:     "test-stdio",
		Protocol: "stdio",
		Command:  "npx",
		Args:     []string{"-y", "@modelcontextprotocol/server-everything"},
	})
	// Without Docker and without a local path, this should fail
	if err == nil {
		// Only expected to succeed if Docker is running with a matching container
		t.Log("Resolve succeeded (Docker container may exist)")
	}
}

func TestFindAppDirectories(t *testing.T) {
	r := NewSourceResolver(zap.NewNop())

	diffOutput := `C /root
A /root/.npm/_npx/abc123/node_modules/@mcp/server/index.js
A /root/.npm/_npx/abc123/node_modules/@mcp/server/package.json
C /tmp
A /tmp/some-cache
A /app/server.py
A /app/tools/search.py
C /etc/hostname
A /usr/local/lib/python3.11/site-packages/mcp_server/main.py
A /root/.cache/uv/git-v0/checkouts/abc123/def456/gcore_mcp_server/server.py
A /root/.cache/uv/archive-v0/xxx/click/__init__.py`

	dirs := r.findAppDirectories(diffOutput)

	found := make(map[string]bool)
	for _, d := range dirs {
		found[d] = true
	}

	// Should find: npm node_modules, /app, and UV git checkout
	if !found["/root/.npm/_npx/abc123/node_modules"] {
		t.Errorf("expected npm node_modules dir, got %v", dirs)
	}
	if !found["/app"] {
		t.Errorf("expected /app dir, got %v", dirs)
	}
	if !found["/root/.cache/uv/git-v0/checkouts/abc123/def456"] {
		t.Errorf("expected UV git checkout dir, got %v", dirs)
	}

	// Should NOT find: site-packages, UV archive (dependencies)
	for _, d := range dirs {
		if strings.Contains(d, "site-packages") {
			t.Errorf("site-packages should be excluded: %s", d)
		}
		if strings.Contains(d, "archive-v0") {
			t.Errorf("UV archive should be excluded: %s", d)
		}
	}
}

func TestIsSystemPath(t *testing.T) {
	r := NewSourceResolver(zap.NewNop())

	tests := []struct {
		path   string
		system bool
	}{
		{"/etc/hostname", true},
		{"/var/log/syslog", true},
		{"/usr/bin/python3", true},
		{"/app/server.py", false},
		{"/root/.npm/cache", false},
		{"/opt/myapp/main.go", false},
		{"/home/user/code.py", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := r.isSystemPath(tt.path); got != tt.system {
				t.Errorf("isSystemPath(%q) = %v, want %v", tt.path, got, tt.system)
			}
		})
	}
}

func TestExtractAppRoot(t *testing.T) {
	r := NewSourceResolver(zap.NewNop())

	tests := []struct {
		path string
		want string
	}{
		{"/root/.npm/_npx/abc/node_modules/@mcp/server/index.js", "/root/.npm/_npx/abc/node_modules"},
		{"/app/server.py", "/app"},
		{"/src/main.go", "/src"},
		{"/opt/app/config.yaml", "/opt/app"},
		// UV git checkouts — actual server source
		{"/root/.cache/uv/git-v0/checkouts/abc123/def456/server.py", "/root/.cache/uv/git-v0/checkouts/abc123/def456"},
		{"/root/.cache/uv/git-v0/checkouts/abc123/def456/pkg/main.py", "/root/.cache/uv/git-v0/checkouts/abc123/def456"},
		// /root non-cache files
		{"/root/script.py", "/root/script.py"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := r.extractAppRoot(tt.path); got != tt.want {
				t.Errorf("extractAppRoot(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestResolveNpxCache(t *testing.T) {
	// Create a fake npx cache structure
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir) // Windows uses USERPROFILE

	npxDir := filepath.Join(homeDir, ".npm", "_npx", "abc123hash", "node_modules", "@modelcontextprotocol", "server-everything")
	os.MkdirAll(npxDir, 0755)
	os.WriteFile(filepath.Join(npxDir, "index.js"), []byte("// server"), 0644)
	os.WriteFile(filepath.Join(npxDir, "package.json"), []byte(`{"name": "test"}`), 0644)

	r := NewSourceResolver(zap.NewNop())
	result, err := r.resolveNpxCache(ServerInfo{
		Name:    "everything-server",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-everything"},
	})
	if err != nil {
		t.Fatalf("resolveNpxCache: %v", err)
	}
	if result.SourceDir != npxDir {
		t.Errorf("expected source_dir %q, got %q", npxDir, result.SourceDir)
	}
	if result.Method != "npx_cache" {
		t.Errorf("expected method 'npx_cache', got %q", result.Method)
	}
}

func TestResolveNpxCacheNotFound(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir) // Windows uses USERPROFILE

	// Create npx dir but no matching package
	os.MkdirAll(filepath.Join(homeDir, ".npm", "_npx"), 0755)

	r := NewSourceResolver(zap.NewNop())
	_, err := r.resolveNpxCache(ServerInfo{
		Name:    "test",
		Command: "npx",
		Args:    []string{"nonexistent-package"},
	})
	if err == nil {
		t.Error("expected error for missing npx package")
	}
}

func TestResolveUvxCacheToolsDir(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir) // Windows uses USERPROFILE

	// Create a fake UV tools directory
	toolDir := filepath.Join(homeDir, ".local", "share", "uv", "tools", "my-tool")
	os.MkdirAll(toolDir, 0755)
	os.WriteFile(filepath.Join(toolDir, "main.py"), []byte("# test"), 0644)

	r := NewSourceResolver(zap.NewNop())
	result, err := r.resolveUvxCache(ServerInfo{
		Name:    "my-tool",
		Command: "uvx",
		Args:    []string{"my-tool"},
	})
	if err != nil {
		t.Fatalf("resolveUvxCache: %v", err)
	}
	if result.SourceDir != toolDir {
		t.Errorf("expected source_dir %q, got %q", toolDir, result.SourceDir)
	}
	if result.Method != "uvx_cache" {
		t.Errorf("expected method 'uvx_cache', got %q", result.Method)
	}
}

func TestResolveUvxCacheGitCheckout(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir) // Windows uses USERPROFILE

	// Create a fake UV git checkout with pyproject.toml identifying the repo
	checkoutDir := filepath.Join(homeDir, ".cache", "uv", "git-v0", "checkouts", "abc123", "def456")
	os.MkdirAll(checkoutDir, 0755)
	os.WriteFile(filepath.Join(checkoutDir, "server.py"), []byte("# test"), 0644)
	os.WriteFile(filepath.Join(checkoutDir, "pyproject.toml"), []byte("[project]\nname = \"repo\"\nversion = \"0.1.0\"\n"), 0644)

	r := NewSourceResolver(zap.NewNop())
	result, err := r.resolveUvxCache(ServerInfo{
		Name:    "malicious-demo",
		Command: "uvx",
		Args:    []string{"git+https://github.com/org/repo"},
	})
	if err != nil {
		t.Fatalf("resolveUvxCache: %v", err)
	}
	if result.SourceDir != checkoutDir {
		t.Errorf("expected source_dir %q, got %q", checkoutDir, result.SourceDir)
	}
	if result.Method != "uvx_cache" {
		t.Errorf("expected method 'uvx_cache', got %q", result.Method)
	}
}

// TestSourceResolverNpxFilesystemArgNotPicked verifies that a filesystem-style
// positional argument (e.g. `/tmp/mydata` for @modelcontextprotocol/server-filesystem)
// is NOT picked up as the server's source directory. Package-runner commands
// must resolve via the package cache; if the cache has the package, we use it.
func TestSourceResolverNpxFilesystemArgNotPicked(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	// Seed the npx cache with a fake filesystem server package
	pkgDir := filepath.Join(homeDir, ".npm", "_npx", "deadbeef", "node_modules", "@modelcontextprotocol", "server-filesystem")
	os.MkdirAll(pkgDir, 0755)
	os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"name":"@modelcontextprotocol/server-filesystem"}`), 0644)
	os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte("// server"), 0644)

	// Create a bogus "data dir" (the arg the user passes — NOT source code).
	dataDir := t.TempDir()
	os.WriteFile(filepath.Join(dataDir, "notes.txt"), []byte("my private notes"), 0644)

	r := NewSourceResolver(zap.NewNop())
	result, err := r.Resolve(context.Background(), ServerInfo{
		Name:     "fs-server",
		Protocol: "stdio",
		Command:  "npx",
		Args:     []string{"-y", "@modelcontextprotocol/server-filesystem", dataDir},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.SourceDir != pkgDir {
		t.Errorf("expected source_dir %q (package cache), got %q", pkgDir, result.SourceDir)
	}
	if result.SourceDir == dataDir {
		t.Errorf("regression: resolver picked the data directory %q", dataDir)
	}
	if result.Method != "npx_cache" {
		t.Errorf("expected method 'npx_cache', got %q", result.Method)
	}
	result.Cleanup()
}

// TestSourceResolverNpxDataDirFallsThroughWhenNoCache verifies that when
// the package cache is EMPTY and the only arg is a plain data directory
// (no source markers), the resolver reports no source rather than wrongly
// picking the data dir.
func TestSourceResolverNpxDataDirFallsThroughWhenNoCache(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	// Data directory without any source markers
	dataDir := t.TempDir()
	os.WriteFile(filepath.Join(dataDir, "notes.txt"), []byte("notes"), 0644)
	os.WriteFile(filepath.Join(dataDir, "data.csv"), []byte("a,b,c"), 0644)

	r := NewSourceResolver(zap.NewNop())
	_, err := r.Resolve(context.Background(), ServerInfo{
		Name:     "fs-server",
		Protocol: "stdio",
		Command:  "npx",
		Args:     []string{"-y", "@modelcontextprotocol/server-filesystem", dataDir},
	})
	if err == nil {
		t.Fatal("expected error when no cache and no source markers, got nil")
	}
}

// TestSourceResolverPythonScriptArgPreserved verifies that the existing
// "python server.py" behavior still resolves to the script's parent directory.
func TestSourceResolverPythonScriptArgPreserved(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "server.py")
	os.WriteFile(scriptPath, []byte("# test"), 0644)

	r := NewSourceResolver(zap.NewNop())
	result, err := r.Resolve(context.Background(), ServerInfo{
		Name:     "py-stdio",
		Protocol: "stdio",
		Command:  "python",
		Args:     []string{scriptPath},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.SourceDir != dir {
		t.Errorf("expected source_dir %q, got %q", dir, result.SourceDir)
	}
	result.Cleanup()
}

// TestSourceResolverStdioNoSource verifies that a stdio server with no
// resolvable source (no container, no working_dir, no package cache, no
// source-like args) returns an error cleanly.
func TestSourceResolverStdioNoSource(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	r := NewSourceResolver(zap.NewNop())
	_, err := r.Resolve(context.Background(), ServerInfo{
		Name:     "ghost",
		Protocol: "stdio",
		Command:  "/usr/bin/does-not-exist",
		Args:     []string{"--flag"},
	})
	if err == nil {
		t.Fatal("expected error for stdio server with no resolvable source")
	}
}

func TestDirLooksLikeSource(t *testing.T) {
	// Dir with package.json → source
	sourceDir := t.TempDir()
	os.WriteFile(filepath.Join(sourceDir, "package.json"), []byte("{}"), 0644)
	if !dirLooksLikeSource(sourceDir) {
		t.Error("expected dir with package.json to look like source")
	}

	// Dir with only a .py file → source
	pyDir := t.TempDir()
	os.WriteFile(filepath.Join(pyDir, "main.py"), []byte("# test"), 0644)
	if !dirLooksLikeSource(pyDir) {
		t.Error("expected dir with .py file to look like source")
	}

	// Empty dir → not source
	emptyDir := t.TempDir()
	if dirLooksLikeSource(emptyDir) {
		t.Error("expected empty dir to NOT look like source")
	}

	// Data dir → not source
	dataDir := t.TempDir()
	os.WriteFile(filepath.Join(dataDir, "notes.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dataDir, "image.png"), []byte{0}, 0644)
	if dirLooksLikeSource(dataDir) {
		t.Error("expected plain data dir to NOT look like source")
	}
}

func TestIsPackageRunnerCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"npx", true},
		{"/usr/local/bin/npx", true},
		{"uvx", true},
		{"pipx", true},
		{"bunx", true},
		{"python", false},
		{"node", false},
		{"./server", false},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			if got := isPackageRunnerCommand(tt.cmd); got != tt.want {
				t.Errorf("isPackageRunnerCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestSanitizeForDocker(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"my-server", "my-server"},
		{"org/repo", "org-repo"},
		{"host:port", "host-port"},
		{"with.dots", "with-dots"},
		{"with spaces", "with-spaces"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := sanitizeForDocker(tt.input); got != tt.want {
				t.Errorf("sanitizeForDocker(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
