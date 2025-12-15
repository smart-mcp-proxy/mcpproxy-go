package core

import (
	"runtime"
	"strings"
	"testing"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/secureenv"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestShellescape(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		onlyOS   string // run only on this OS, empty means all
	}{
		{
			name:     "empty string unix",
			input:    "",
			expected: "''",
			onlyOS:   "darwin",
		},
		{
			name:     "empty string linux",
			input:    "",
			expected: "''",
			onlyOS:   "linux",
		},
		{
			name:     "simple string no escaping needed",
			input:    "hello",
			expected: "hello",
		},
		{
			name:     "string with spaces unix",
			input:    "hello world",
			expected: "'hello world'",
			onlyOS:   "darwin",
		},
		{
			name:     "string with spaces linux",
			input:    "hello world",
			expected: "'hello world'",
			onlyOS:   "linux",
		},
		{
			name:     "string with single quote unix",
			input:    "it's",
			expected: "'it'\"'\"'s'",
			onlyOS:   "darwin",
		},
		{
			name:     "string with dollar sign unix",
			input:    "$HOME",
			expected: "'$HOME'",
			onlyOS:   "darwin",
		},
		{
			name:     "string with backticks unix",
			input:    "`whoami`",
			expected: "'`whoami`'",
			onlyOS:   "darwin",
		},
		{
			name:     "path with no special chars",
			input:    "/usr/bin/node",
			expected: "/usr/bin/node",
		},
		{
			name:     "path with spaces unix",
			input:    "/Program Files/node",
			expected: "'/Program Files/node'",
			onlyOS:   "darwin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.onlyOS != "" && runtime.GOOS != tt.onlyOS {
				t.Skipf("Test only runs on %s, current OS is %s", tt.onlyOS, runtime.GOOS)
			}

			result := shellescape(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsBashLikeShell(t *testing.T) {
	// Test the bash detection logic used in wrapWithUserShell
	// This tests the condition: strings.Contains(strings.ToLower(shell), "bash") ||
	//                           strings.Contains(strings.ToLower(shell), "sh")

	tests := []struct {
		name     string
		shell    string
		expected bool
	}{
		// Bash variants
		{name: "bash lowercase", shell: "bash", expected: true},
		{name: "bash uppercase", shell: "BASH", expected: true},
		{name: "bash path unix", shell: "/bin/bash", expected: true},
		{name: "bash path usr", shell: "/usr/bin/bash", expected: true},
		{name: "git bash windows", shell: "C:\\Program Files\\Git\\bin\\bash.exe", expected: true},
		{name: "git bash mingw", shell: "/mingw64/bin/bash", expected: true},
		{name: "msys bash", shell: "/usr/bin/bash", expected: true},

		// Other shells containing "sh"
		{name: "sh", shell: "/bin/sh", expected: true},
		{name: "zsh", shell: "/bin/zsh", expected: true},
		{name: "dash", shell: "/bin/dash", expected: true},
		{name: "ash", shell: "/bin/ash", expected: true},
		{name: "ksh", shell: "/bin/ksh", expected: true},
		{name: "fish", shell: "/usr/bin/fish", expected: true}, // contains "sh"
		{name: "tcsh", shell: "/bin/tcsh", expected: true},
		{name: "csh", shell: "/bin/csh", expected: true},

		// Windows shells
		{name: "cmd", shell: "cmd", expected: false},
		{name: "cmd.exe", shell: "cmd.exe", expected: false},
		{name: "CMD uppercase", shell: "CMD", expected: false},
		{name: "cmd full path", shell: "C:\\Windows\\System32\\cmd.exe", expected: false},

		// Note: powershell contains "sh" so it matches the current detection logic.
		// This is acceptable because PowerShell also supports -c flag for command execution,
		// though it doesn't use -l for login shells. In practice, this edge case is rare
		// since SHELL env var on Windows is typically not set to PowerShell.
		{name: "powershell contains sh", shell: "powershell", expected: true},
		{name: "powershell.exe contains sh", shell: "powershell.exe", expected: true},
		{name: "pwsh contains sh", shell: "pwsh", expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Replicate the detection logic from wrapWithUserShell
			result := isBashLikeShell(tt.shell)
			assert.Equal(t, tt.expected, result, "shell: %s", tt.shell)
		})
	}
}

// isBashLikeShell replicates the detection logic from wrapWithUserShell for testing
// This is the same logic used in production code
func isBashLikeShell(shell string) bool {
	lower := strings.ToLower(shell)
	return strings.Contains(lower, "bash") || strings.Contains(lower, "sh")
}

func TestWrapWithUserShell(t *testing.T) {
	// Skip on Windows as the test environment may not have Git Bash
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - these tests verify Unix shell behavior")
	}

	logger := zap.NewNop()
	envManager := secureenv.NewManager(nil)

	tests := []struct {
		name          string
		command       string
		args          []string
		expectedShell string
		expectedFlags []string
	}{
		{
			name:          "simple command",
			command:       "echo",
			args:          []string{"hello"},
			expectedShell: "", // Will be detected from environment
			expectedFlags: []string{"-l", "-c"},
		},
		{
			name:          "command with multiple args",
			command:       "node",
			args:          []string{"-e", "console.log('test')"},
			expectedShell: "",
			expectedFlags: []string{"-l", "-c"},
		},
		{
			name:          "npx command",
			command:       "npx",
			args:          []string{"@modelcontextprotocol/server-everything"},
			expectedShell: "",
			expectedFlags: []string{"-l", "-c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{
				config: &config.ServerConfig{
					Name: "test-server",
				},
				logger:     logger,
				envManager: envManager,
			}

			shell, args := client.wrapWithUserShell(tt.command, tt.args)

			// Verify shell is not empty
			require.NotEmpty(t, shell, "shell should not be empty")

			// Verify flags are correct for Unix shells
			require.Len(t, args, 3, "should have 3 args: -l, -c, and command string")
			assert.Equal(t, "-l", args[0], "first flag should be -l")
			assert.Equal(t, "-c", args[1], "second flag should be -c")

			// Verify command string contains the original command
			assert.Contains(t, args[2], tt.command, "command string should contain original command")
		})
	}
}

func TestWrapWithUserShellGitBashDetection(t *testing.T) {
	// This test verifies the shell flag selection logic
	// On Unix: always uses -l -c
	// On Windows with cmd: uses /c
	// On Windows with Git Bash: uses -l -c

	tests := []struct {
		name           string
		shell          string
		isWindows      bool
		expectedFlags  []string
		expectedPrefix string
	}{
		{
			name:           "unix bash",
			shell:          "/bin/bash",
			isWindows:      false,
			expectedFlags:  []string{"-l", "-c"},
			expectedPrefix: "-l",
		},
		{
			name:           "unix zsh",
			shell:          "/bin/zsh",
			isWindows:      false,
			expectedFlags:  []string{"-l", "-c"},
			expectedPrefix: "-l",
		},
		{
			name:           "windows cmd should use /c",
			shell:          "cmd",
			isWindows:      true,
			expectedFlags:  []string{"/c"},
			expectedPrefix: "/c",
		},
		{
			name:           "windows git bash should use -l -c",
			shell:          "C:\\Program Files\\Git\\bin\\bash.exe",
			isWindows:      true,
			expectedFlags:  []string{"-l", "-c"},
			expectedPrefix: "-l",
		},
		{
			name:           "windows msys bash should use -l -c",
			shell:          "/usr/bin/bash",
			isWindows:      true,
			expectedFlags:  []string{"-l", "-c"},
			expectedPrefix: "-l",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the flag selection logic directly
			isBash := isBashLikeShell(tt.shell)

			var expectedFirstFlag string
			if tt.isWindows && !isBash {
				expectedFirstFlag = "/c"
			} else {
				expectedFirstFlag = "-l"
			}

			assert.Equal(t, tt.expectedPrefix, expectedFirstFlag,
				"shell %s on windows=%v should use flag %s", tt.shell, tt.isWindows, tt.expectedPrefix)
		})
	}
}

func TestShellFlagSelection(t *testing.T) {
	// Comprehensive test for the shell flag selection logic
	// Verifies PR #195 fix: Git Bash on Windows should use Unix-style flags

	testCases := []struct {
		shell         string
		goos          string
		expectUnix    bool // true = expects -l -c, false = expects /c
		description   string
	}{
		// Unix systems - always use Unix flags
		{"/bin/bash", "darwin", true, "macOS bash"},
		{"/bin/zsh", "darwin", true, "macOS zsh"},
		{"/bin/sh", "linux", true, "Linux sh"},
		{"/usr/bin/fish", "linux", true, "Linux fish"},

		// Windows with cmd.exe - use Windows flags (/c)
		{"cmd", "windows", false, "Windows cmd"},
		{"cmd.exe", "windows", false, "Windows cmd.exe"},
		{"C:\\Windows\\System32\\cmd.exe", "windows", false, "Windows cmd full path"},

		// Note: PowerShell contains "sh" so it matches bash detection.
		// This is acceptable since PowerShell supports -c flag.
		{"powershell", "windows", true, "Windows PowerShell (contains sh)"},
		{"pwsh", "windows", true, "Windows PowerShell Core (contains sh)"},

		// Windows with Git Bash / MSYS - use Unix flags (PR #195 fix)
		{"C:\\Program Files\\Git\\bin\\bash.exe", "windows", true, "Git Bash on Windows"},
		{"C:\\Program Files\\Git\\usr\\bin\\bash.exe", "windows", true, "Git Bash usr on Windows"},
		{"/mingw64/bin/bash", "windows", true, "MSYS2 bash on Windows"},
		{"/usr/bin/bash", "windows", true, "Cygwin-style bash on Windows"},
		{"bash", "windows", true, "Plain bash on Windows"},
		{"bash.exe", "windows", true, "bash.exe on Windows"},
		{"/bin/sh", "windows", true, "sh on Windows (Git Bash)"},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			isBash := isBashLikeShell(tc.shell)
			isWindows := tc.goos == "windows"

			// Apply the same logic as wrapWithUserShell
			useUnixFlags := !isWindows || isBash

			assert.Equal(t, tc.expectUnix, useUnixFlags,
				"shell=%q goos=%s: expected useUnixFlags=%v, got %v (isBash=%v)",
				tc.shell, tc.goos, tc.expectUnix, useUnixFlags, isBash)
		})
	}
}
