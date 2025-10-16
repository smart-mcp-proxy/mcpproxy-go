package core

import "testing"

func TestIsLocalFilePath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		// Windows absolute paths
		{"Windows C drive backslash", `C:\Users\file.py`, true},
		{"Windows D drive backslash", `D:\path\to\script.js`, true},
		{"Windows C drive forward slash", "C:/Users/file.py", true},
		{"Windows E drive mixed", `E:\path/to/file.ts`, true},

		// Windows relative paths
		{"Windows current dir", `.\\script.py`, true},
		{"Windows parent dir", `..\\file.js`, true},

		// Windows UNC paths
		{"Windows UNC", `\\\\server\\share\\file.py`, true},

		// Unix absolute paths
		{"Unix absolute", "/usr/local/bin/script.py", true},
		{"Unix home", "~/file.py", true},

		// Unix relative paths
		{"Unix current dir", "./script.py", true},
		{"Unix parent dir", "../file.js", true},

		// File extensions (no path prefix)
		{"Python file", "script.py", true},
		{"JavaScript file", "index.js", true},
		{"TypeScript file", "app.ts", true},
		{"Shell script", "run.sh", true},
		{"Ruby file", "app.rb", true},
		{"PHP file", "index.php", true},

		// Non-local paths (should return false)
		{"Git URL", "git+https://github.com/user/repo", false},
		{"HTTP URL", "https://example.com/file.py", false},
		{"NPM package with scope", "@scope/package", false},
		{"NPM package simple", "some-package", false},
		{"PyPI package", "requests", false},
		{"Empty string", "", false},
		{"Just a name", "myapp", false},

		// Edge cases
		{"Windows drive only", "C:", false}, // Too short
		{"Windows drive forward", "C:/", true},
		{"Windows drive back", `C:\`, true},
		{"Relative binary path", "./myapp", true}, // Relative paths are local files
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLocalFilePath(tt.path)
			if got != tt.expected {
				t.Errorf("isLocalFilePath(%q) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}
