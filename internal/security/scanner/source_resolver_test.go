package scanner

import (
	"context"
	"os"
	"path/filepath"
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
A /root/.npm
A /root/.npm/_npx/abc123/node_modules/@mcp/server/index.js
A /root/.npm/_npx/abc123/node_modules/@mcp/server/package.json
C /tmp
A /tmp/some-cache
A /app/server.py
A /app/tools/search.py
C /etc/hostname
A /usr/local/lib/python3.11/site-packages/mcp_server/main.py`

	dirs := r.findAppDirectories(diffOutput)

	// Should find: /root/.npm, /app, and site-packages
	found := make(map[string]bool)
	for _, d := range dirs {
		found[d] = true
	}

	if !found["/root/.npm/_npx/abc123/node_modules"] {
		t.Errorf("expected node_modules dir, got %v", dirs)
	}
	if !found["/app"] {
		t.Errorf("expected /app dir, got %v", dirs)
	}
	if !found["/usr/local/lib/python3.11/site-packages"] {
		t.Errorf("expected site-packages dir, got %v", dirs)
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
		{"/usr/local/lib/python3.11/site-packages/mcp_server/main.py", "/usr/local/lib/python3.11/site-packages"},
		{"/app/server.py", "/app"},
		{"/src/main.go", "/src"},
		{"/root/.cache/data", "/root/.cache"},
		{"/opt/app/config.yaml", "/opt/app"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := r.extractAppRoot(tt.path); got != tt.want {
				t.Errorf("extractAppRoot(%q) = %q, want %q", tt.path, got, tt.want)
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
