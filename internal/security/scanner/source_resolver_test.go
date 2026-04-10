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
A /root/.npm/_npx/abc123/node_modules/@other/sibling/index.js
A /root/.npm/_npx/abc123/node_modules/unscoped-dep/index.js
C /tmp
A /tmp/some-cache
A /app/server.py
A /app/tools/search.py
C /etc/hostname
A /usr/local/lib/python3.11/site-packages/mcp_server/main.py
A /root/.cache/uv/git-v0/checkouts/abc123/def456/gcore_mcp_server/server.py
A /root/.cache/uv/archive-v0/xxx/click/__init__.py`

	// Target: @mcp/server — sibling @other/sibling and unscoped-dep must be filtered out.
	dirs := r.findAppDirectories(diffOutput, "@mcp/server")

	found := make(map[string]bool)
	for _, d := range dirs {
		found[d] = true
	}

	// Should find: the SPECIFIC target package dir, /app, and the UV git checkout.
	if !found["/root/.npm/_npx/abc123/node_modules/@mcp/server"] {
		t.Errorf("expected target npx package dir, got %v", dirs)
	}
	if !found["/app"] {
		t.Errorf("expected /app dir, got %v", dirs)
	}
	if !found["/root/.cache/uv/git-v0/checkouts/abc123/def456"] {
		t.Errorf("expected UV git checkout dir, got %v", dirs)
	}

	// Should NOT find: the shared node_modules bucket, sibling packages,
	// site-packages, UV archives (all excluded to prevent cross-package leakage).
	for _, d := range dirs {
		if d == "/root/.npm/_npx/abc123/node_modules" {
			t.Errorf("shared node_modules bucket must not be returned: %s", d)
		}
		if strings.Contains(d, "/@other/") || strings.Contains(d, "/unscoped-dep") {
			t.Errorf("sibling npx package must be excluded: %s", d)
		}
		if strings.Contains(d, "site-packages") {
			t.Errorf("site-packages should be excluded from Pass 1: %s", d)
		}
		if strings.Contains(d, "archive-v0") {
			t.Errorf("UV archive should be excluded: %s", d)
		}
	}
}

// TestFindAppDirectoriesNpxNoTarget guards against regressions where we would
// accept npx cache paths without knowing the target package — accepting any
// package in the bucket is exactly the sibling-leak bug we are fixing.
func TestFindAppDirectoriesNpxNoTarget(t *testing.T) {
	r := NewSourceResolver(zap.NewNop())
	diffOutput := `A /root/.npm/_npx/abc/node_modules/@any/pkg/index.js
A /app/server.py`
	dirs := r.findAppDirectories(diffOutput, "")
	for _, d := range dirs {
		if strings.Contains(d, "_npx") {
			t.Errorf("npx cache paths must be rejected when target is unknown, got: %s", d)
		}
	}
	// /app is still legitimate.
	var foundApp bool
	for _, d := range dirs {
		if d == "/app" {
			foundApp = true
		}
	}
	if !foundApp {
		t.Errorf("expected /app, got %v", dirs)
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
		name      string
		path      string
		targetPkg string
		want      string
	}{
		// npx isolation: scoped target package matches, unscoped sibling is rejected.
		{
			name:      "scoped target package matches",
			path:      "/root/.npm/_npx/abc/node_modules/@mcp/server/index.js",
			targetPkg: "@mcp/server",
			want:      "/root/.npm/_npx/abc/node_modules/@mcp/server",
		},
		{
			name:      "unscoped sibling rejected when target is scoped",
			path:      "/root/.npm/_npx/abc/node_modules/left-pad/index.js",
			targetPkg: "@mcp/server",
			want:      "",
		},
		{
			name:      "scoped sibling rejected when target is a different scope",
			path:      "/root/.npm/_npx/abc/node_modules/@just-every/mcp-screenshot-website-fast/dist/internal/screenshotCapture.js",
			targetPkg: "@modelcontextprotocol/server-everything",
			want:      "",
		},
		{
			name:      "unscoped target package matches",
			path:      "/root/.npm/_npx/xyz/node_modules/some-pkg/index.js",
			targetPkg: "some-pkg",
			want:      "/root/.npm/_npx/xyz/node_modules/some-pkg",
		},
		{
			name:      "npx path with empty target is rejected (no leakage)",
			path:      "/root/.npm/_npx/abc/node_modules/@mcp/server/index.js",
			targetPkg: "",
			want:      "",
		},
		// Non-npx paths don't depend on target.
		{name: "app", path: "/app/server.py", want: "/app"},
		{name: "src", path: "/src/main.go", want: "/src"},
		{name: "opt-app", path: "/opt/app/config.yaml", want: "/opt/app"},
		{
			name: "uv git checkout",
			path: "/root/.cache/uv/git-v0/checkouts/abc123/def456/server.py",
			want: "/root/.cache/uv/git-v0/checkouts/abc123/def456",
		},
		{
			name: "uv git checkout nested",
			path: "/root/.cache/uv/git-v0/checkouts/abc123/def456/pkg/main.py",
			want: "/root/.cache/uv/git-v0/checkouts/abc123/def456",
		},
		{name: "root non-cache file accepted", path: "/root/script.py", want: "/root/script.py"},
		{name: "root dot-dir rejected (npm volume)", path: "/root/.npm", want: ""},
		{name: "root dot-dir rejected (cache)", path: "/root/.cache", want: ""},
		{name: "root dot-dir rejected (local)", path: "/root/.local/share/uv/tools/foo", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.extractAppRoot(tt.path, tt.targetPkg); got != tt.want {
				t.Errorf("extractAppRoot(%q, %q) = %q, want %q", tt.path, tt.targetPkg, got, tt.want)
			}
		})
	}
}

func TestNpxTargetPackage(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    string
	}{
		{"scoped with version", "npx", []string{"-y", "@modelcontextprotocol/server-everything@1.2.3"}, "@modelcontextprotocol/server-everything"},
		{"scoped without version", "npx", []string{"-y", "@modelcontextprotocol/server-everything"}, "@modelcontextprotocol/server-everything"},
		{"unscoped with version", "npx", []string{"pkg@1.0.0"}, "pkg"},
		{"unscoped without version", "npx", []string{"pkg"}, "pkg"},
		{"flags skipped", "npx", []string{"-y", "--quiet", "@scope/pkg"}, "@scope/pkg"},
		{"non-npx returns empty", "node", []string{"server.js"}, ""},
		{"no args returns empty", "npx", []string{"-y"}, ""},
		{"npx with path", "/usr/local/bin/npx", []string{"@scope/pkg"}, "@scope/pkg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := npxTargetPackage(ServerInfo{Command: tt.command, Args: tt.args})
			if got != tt.want {
				t.Errorf("npxTargetPackage(%q, %v) = %q, want %q", tt.command, tt.args, got, tt.want)
			}
		})
	}
}

func TestIsPythonStdlibPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/usr/local/lib/python3.13/shutil.py", true},
		{"/usr/local/lib/python3.13/tempfile.py", true},
		{"/usr/local/lib/python3.13/__pycache__/shutil.cpython-313.pyc", true},
		{"/usr/local/lib/python3.11/site-packages/mcp_server/main.py", false},
		{"/usr/lib/python3/dist-packages/click/__init__.py", false},
		{"/opt/python/lib/python3.12/os.py", true},
		{"/app/server.py", false},
		{"/app/python/venv/lib/foo.py", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isPythonStdlibPath(tt.path); got != tt.want {
				t.Errorf("isPythonStdlibPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestExtractPass2Dir(t *testing.T) {
	r := NewSourceResolver(zap.NewNop())

	tests := []struct {
		name      string
		path      string
		targetPkg string
		want      string
	}{
		{"app source", "/app/server.py", "", "/app"},
		{"site-packages returns root", "/usr/local/lib/python3.13/site-packages/click/__init__.py", "", "/usr/local/lib/python3.13/site-packages"},
		{"dist-packages returns root", "/usr/lib/python3/dist-packages/mcp/server.py", "", "/usr/lib/python3/dist-packages"},
		{"npx isolates to target", "/root/.npm/_npx/abc/node_modules/@mcp/server/index.js", "@mcp/server", "/root/.npm/_npx/abc/node_modules/@mcp/server"},
		{"npx sibling rejected", "/root/.npm/_npx/abc/node_modules/@other/sibling/index.js", "@mcp/server", ""},
		{"plain node_modules returns root", "/home/user/proj/node_modules/click/index.js", "", "/home/user/proj/node_modules"},
		{"uv git checkout", "/root/.cache/uv/git-v0/checkouts/abc/def/server.py", "", "/root/.cache/uv/git-v0/checkouts/abc/def"},
		{"uv archive", "/root/.cache/uv/archive-v0/xyz/click/__init__.py", "", "/root/.cache/uv/archive-v0/xyz"},
		{"stdlib path returns empty (caller already skipped)", "/usr/local/lib/python3.13/shutil.py", "", ""},
		{"random /usr path rejected", "/usr/share/man/man1/ls.1", "", ""},
		{"random /root path rejected", "/root/.cache/pip/wheels/foo.whl", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.extractPass2Dir(tt.path, tt.targetPkg); got != tt.want {
				t.Errorf("extractPass2Dir(%q, %q) = %q, want %q", tt.path, tt.targetPkg, got, tt.want)
			}
		})
	}
}

func TestFindAllChangedDirectoriesStdlibFilter(t *testing.T) {
	r := NewSourceResolver(zap.NewNop())
	diffOutput := `A /usr/local/lib/python3.13/shutil.py
C /usr/local/lib/python3.13/__pycache__/shutil.cpython-313.pyc
A /usr/local/lib/python3.13/tempfile.py
A /usr/local/lib/python3.13/site-packages/obsidian_pilot/main.py
A /usr/local/lib/python3.13/site-packages/click/__init__.py
A /root/.cache/uv/archive-v0/hash1/click/__init__.py
A /app/server.py`

	dirs := r.findAllChangedDirectories(diffOutput, "")

	for _, d := range dirs {
		if isPythonStdlibPath(d + "/anything.py") {
			t.Errorf("stdlib subtree leaked into Pass 2 dirs: %s", d)
		}
		if d == "/usr" || d == "/root" {
			t.Errorf("broad root %q must not be returned", d)
		}
	}

	found := make(map[string]bool)
	for _, d := range dirs {
		found[d] = true
	}
	if !found["/usr/local/lib/python3.13/site-packages"] {
		t.Errorf("expected site-packages root, got %v", dirs)
	}
	if !found["/app"] {
		t.Errorf("expected /app, got %v", dirs)
	}
	if !found["/root/.cache/uv/archive-v0/hash1"] {
		t.Errorf("expected uv archive dir, got %v", dirs)
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
