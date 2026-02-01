package security

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExpandPath(t *testing.T) {
	homeDir, _ := os.UserHomeDir()

	tests := []struct {
		name     string
		input    string
		contains string // Check if result contains this substring
		exact    string // Check for exact match if non-empty
	}{
		{
			name:     "empty path",
			input:    "",
			exact:    "",
		},
		{
			name:     "tilde expands to home",
			input:    "~",
			exact:    homeDir,
		},
		{
			name:     "tilde with path",
			input:    "~/.ssh/id_rsa",
			contains: homeDir,
		},
		{
			name:     "tilde in middle (no expansion)",
			input:    "/path/to/~something",
			contains: "/path/to/~",
		},
		{
			name:     "no expansion needed",
			input:    "/usr/local/bin",
			exact:    "/usr/local/bin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExpandPath(tt.input)
			if tt.exact != "" {
				assert.Equal(t, tt.exact, result)
			} else if tt.contains != "" {
				assert.Contains(t, result, tt.contains)
			}
		})
	}
}

func TestExpandPath_EnvVars(t *testing.T) {
	// Set a test environment variable
	os.Setenv("TEST_VAR", "/test/value")
	defer os.Unsetenv("TEST_VAR")

	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "Unix style $VAR",
			input:    "$TEST_VAR/subpath",
			contains: "/test/value",
		},
		{
			name:     "Unix style ${VAR}",
			input:    "${TEST_VAR}/subpath",
			contains: "/test/value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExpandPath(tt.input)
			assert.Contains(t, result, tt.contains)
		})
	}
}

func TestExpandPath_WindowsEnvVars(t *testing.T) {
	// Set a test environment variable
	os.Setenv("TESTVAR", "/test/value")
	defer os.Unsetenv("TESTVAR")

	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "Windows style %VAR%",
			input:    "%TESTVAR%/subpath",
			contains: "/test/value",
		},
		{
			name:     "Windows style unset variable",
			input:    "%NONEXISTENT%/subpath",
			contains: "%NONEXISTENT%", // Should remain unchanged
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExpandPath(tt.input)
			assert.Contains(t, result, tt.contains)
		})
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantWindows string // Expected on Windows
		wantUnix    string // Expected on Linux/macOS
	}{
		{
			name:        "unix path",
			input:       "/usr/local/bin",
			wantWindows: "\\usr\\local\\bin",
			wantUnix:    "/usr/local/bin",
		},
		{
			name:        "windows path with backslashes",
			input:       "C:\\Users\\test",
			wantWindows: "c:\\users\\test", // Lowercase on Windows
			wantUnix:    "C:/Users/test",
		},
		{
			name:        "mixed slashes",
			input:       "/usr\\local/bin",
			wantWindows: "\\usr\\local\\bin",
			wantUnix:    "/usr/local/bin",
		},
		{
			name:        "path with dots",
			input:       "/usr/./local/../bin",
			wantWindows: "\\usr\\bin",
			wantUnix:    "/usr/bin",
		},
		{
			name:        "empty path",
			input:       "",
			wantWindows: ".",
			wantUnix:    ".",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizePath(tt.input)
			if runtime.GOOS == "windows" {
				assert.Equal(t, tt.wantWindows, result)
			} else {
				assert.Equal(t, tt.wantUnix, result)
			}
		})
	}
}

func TestExtractPaths(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantPaths  []string
		dontWant   []string
	}{
		{
			name:      "unix absolute path",
			content:   `file: /etc/passwd`,
			wantPaths: []string{"/etc/passwd"},
		},
		{
			name:      "unix home path",
			content:   `path: ~/.ssh/id_rsa`,
			wantPaths: []string{"~/.ssh/id_rsa"},
		},
		{
			name:      "multiple paths",
			content:   `files: /etc/passwd and ~/.ssh/config`,
			wantPaths: []string{"/etc/passwd", "~/.ssh/config"},
		},
		{
			name:      "quoted paths",
			content:   `path="/etc/passwd"`,
			wantPaths: []string{"/etc/passwd"},
		},
		{
			name:      "path in JSON",
			content:   `{"file": "/home/user/.aws/credentials"}`,
			wantPaths: []string{"/home/user/.aws/credentials"},
		},
		{
			name:      "sensitive relative path",
			content:   `config: .aws/credentials`,
			wantPaths: []string{".aws/credentials"},
		},
		{
			name:      "sensitive file extension",
			content:   `key file: server.pem`,
			wantPaths: []string{"server.pem"},
		},
		{
			name:      "env file with extension",
			content:   `using config.env for secrets`,
			wantPaths: []string{"config.env"},
		},
		{
			name:     "empty content",
			content:  "",
			wantPaths: nil,
		},
		{
			name:      "no paths",
			content:   "just some regular text without paths",
			wantPaths: nil,
		},
		{
			name:      "docker config path",
			content:   `{"path": ".docker/config.json"}`,
			wantPaths: []string{".docker/config.json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := ExtractPaths(tt.content)

			if tt.wantPaths == nil {
				assert.Empty(t, paths)
			} else {
				for _, want := range tt.wantPaths {
					found := false
					for _, got := range paths {
						if got == want {
							found = true
							break
						}
					}
					assert.True(t, found, "expected to find path %s, got: %v", want, paths)
				}
			}

			for _, dontWant := range tt.dontWant {
				for _, got := range paths {
					assert.NotEqual(t, dontWant, got, "should not extract %s", dontWant)
				}
			}
		})
	}
}

func TestExtractPaths_WindowsPaths(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantPaths []string
	}{
		{
			name:      "windows drive path",
			content:   `path: C:\Users\test\secrets.txt`,
			wantPaths: []string{`C:\Users\test\secrets.txt`},
		},
		{
			name:      "windows env var path",
			content:   `file: %USERPROFILE%\.ssh\id_rsa`,
			wantPaths: []string{`%USERPROFILE%\.ssh\id_rsa`},
		},
		{
			name:      "windows appdata path",
			content:   `config: %APPDATA%\aws\credentials`,
			wantPaths: []string{`%APPDATA%\aws\credentials`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := ExtractPaths(tt.content)
			for _, want := range tt.wantPaths {
				found := false
				for _, got := range paths {
					if got == want {
						found = true
						break
					}
				}
				assert.True(t, found, "expected to find path %s, got: %v", want, paths)
			}
		})
	}
}

func TestMatchesPathPattern(t *testing.T) {
	tests := []struct {
		name    string
		content string
		pattern string
		want    bool
	}{
		{
			name:    "exact match",
			content: `file: /etc/passwd`,
			pattern: "/etc/passwd",
			want:    true,
		},
		{
			name:    "glob pattern",
			content: `file: ~/.ssh/id_rsa`,
			pattern: "~/.ssh/*",
			want:    true,
		},
		{
			name:    "partial match",
			content: `path: /home/user/.aws/credentials`,
			pattern: "*/.aws/credentials",
			want:    true,
		},
		{
			name:    "no match",
			content: `file: /etc/passwd`,
			pattern: "/etc/shadow",
			want:    false,
		},
		{
			name:    "empty content",
			content: "",
			pattern: "/etc/passwd",
			want:    false,
		},
		{
			name:    "empty pattern",
			content: `file: /etc/passwd`,
			pattern: "",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MatchesPathPattern(tt.content, tt.pattern)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestIsPlatformMatch(t *testing.T) {
	currentOS := runtime.GOOS

	tests := []struct {
		name     string
		platform string
		want     bool
	}{
		{
			name:     "empty platform matches all",
			platform: "",
			want:     true,
		},
		{
			name:     "all platform matches",
			platform: "all",
			want:     true,
		},
		{
			name:     "current platform matches",
			platform: currentOS,
			want:     true,
		},
	}

	// Add tests for non-current platforms
	otherPlatforms := []string{"linux", "darwin", "windows"}
	for _, p := range otherPlatforms {
		if p != currentOS {
			tests = append(tests, struct {
				name     string
				platform string
				want     bool
			}{
				name:     "other platform " + p,
				platform: p,
				want:     false,
			})
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsPlatformMatch(tt.platform)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestGetCurrentPlatform(t *testing.T) {
	platform := GetCurrentPlatform()
	assert.Equal(t, runtime.GOOS, platform)
}

func TestIsSensitiveRelPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "ssh directory",
			path: ".ssh/id_rsa",
			want: true,
		},
		{
			name: "aws directory",
			path: ".aws/credentials",
			want: true,
		},
		{
			name: "azure directory",
			path: ".azure/config",
			want: true,
		},
		{
			name: "kube directory",
			path: ".kube/config",
			want: true,
		},
		{
			name: "gcloud config",
			path: ".config/gcloud/credentials.json",
			want: true,
		},
		{
			name: "docker config",
			path: ".docker/config.json",
			want: true,
		},
		{
			name: "npmrc",
			path: ".npmrc",
			want: true,
		},
		{
			name: "pypirc",
			path: ".pypirc",
			want: true,
		},
		{
			name: "netrc",
			path: ".netrc",
			want: true,
		},
		{
			name: "git-credentials",
			path: ".git-credentials",
			want: true,
		},
		{
			name: "env file",
			path: ".env",
			want: true,
		},
		{
			name: "secrets folder",
			path: "secrets/api_key.txt",
			want: true,
		},
		{
			name: "credentials file",
			path: "credentials/db.json",
			want: true,
		},
		{
			name: "config.json",
			path: "app/config.json",
			want: true,
		},
		{
			name: "regular file - not sensitive",
			path: "src/main.go",
			want: false,
		},
		{
			name: "readme - not sensitive",
			path: "README.md",
			want: false,
		},
		{
			name: "case insensitive - uppercase",
			path: ".SSH/ID_RSA",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSensitiveRelPath(tt.path)
			assert.Equal(t, tt.want, result)
		})
	}
}

func BenchmarkExtractPaths(b *testing.B) {
	content := `{"files": ["/etc/passwd", "~/.ssh/id_rsa", "/home/user/.aws/credentials", "C:\\Users\\test\\secrets.txt"]}`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ExtractPaths(content)
	}
}

func BenchmarkMatchesPathPattern(b *testing.B) {
	content := `path: /home/user/.ssh/id_rsa`
	pattern := "*/.ssh/*"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MatchesPathPattern(content, pattern)
	}
}
