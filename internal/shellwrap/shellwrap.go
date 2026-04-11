// Package shellwrap provides platform-level helpers for wrapping commands in
// the user's login shell and for resolving tool binaries (e.g. docker) with
// PATH caching.
//
// It exists so that both the upstream proxy code (internal/upstream/core) and
// the security scanner (internal/security/scanner) can share a single,
// well-tested implementation of shell quoting + login-shell wrapping instead
// of each rolling their own.
package shellwrap

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	osWindows = "windows"
	// defaultUnixShell is used when $SHELL is unset on a Unix-like system.
	defaultUnixShell = "/bin/bash"
	// defaultWindowsShell is used when neither $ComSpec nor $SHELL is set.
	defaultWindowsShell = "cmd"
)

// Shellescape escapes a single argument for safe inclusion in a shell command
// string. On Unix it uses POSIX single-quoting; on Windows it performs a
// best-effort cmd.exe quoting.
//
// This mirrors the implementation in internal/upstream/core so both code paths
// can converge on one function.
func Shellescape(s string) string {
	if s == "" {
		if runtime.GOOS == osWindows {
			return `""`
		}
		return "''"
	}

	if runtime.GOOS == osWindows {
		// Windows cmd.exe special characters.
		if !strings.ContainsAny(s, " \t\n\r\"&|<>()^%") {
			return s
		}
		// cmd.exe does not use backslash as an escape character. If the
		// caller supplied embedded double quotes we strip them — this is
		// the same behaviour the upstream helper has used since PR #195.
		cleaned := strings.Trim(s, `"`)
		return `"` + cleaned + `"`
	}

	// Unix shell special characters: if none present, return as-is.
	if !strings.ContainsAny(s, " \t\n\r\"'\\$`;&|<>(){}[]?*~") {
		return s
	}
	// Use single quotes and escape any embedded single quotes.
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// isBashLikeShell mirrors the detection logic in connection_stdio.go so that
// Git Bash / MSYS on Windows uses the Unix-style -l -c flags.
func isBashLikeShell(shell string) bool {
	lower := strings.ToLower(shell)
	return strings.Contains(lower, "bash") || strings.Contains(lower, "sh")
}

// resolveLoginShell returns the user's preferred login shell, respecting the
// $SHELL environment variable and falling back to platform defaults.
func resolveLoginShell() string {
	shell := os.Getenv("SHELL")
	if shell != "" {
		return shell
	}
	if runtime.GOOS == osWindows {
		if cs := os.Getenv("ComSpec"); cs != "" {
			return cs
		}
		return defaultWindowsShell
	}
	return defaultUnixShell
}

// WrapWithUserShell wraps a command and its arguments in the user's login
// shell so the child process inherits the interactive PATH (important when
// mcpproxy is launched from a GUI / LaunchAgent on macOS).
//
// It returns the shell to exec and the shell arguments (e.g. ["-l", "-c",
// "docker run ..."] on Unix, ["/c", "docker run ..."] on Windows cmd).
//
// logger may be nil; when non-nil a debug line is emitted mirroring the
// existing upstream/core helper.
func WrapWithUserShell(logger *zap.Logger, command string, args []string) (shell string, shellArgs []string) {
	shell = resolveLoginShell()

	parts := make([]string, 0, len(args)+1)
	parts = append(parts, Shellescape(command))
	for _, a := range args {
		parts = append(parts, Shellescape(a))
	}
	commandString := strings.Join(parts, " ")

	if logger != nil {
		logger.Debug("shellwrap: wrapping command with user login shell",
			zap.String("original_command", command),
			zap.Strings("original_args", args),
			zap.String("shell", shell),
			zap.String("wrapped_command", commandString))
	}

	isBash := isBashLikeShell(shell)
	if runtime.GOOS == osWindows && !isBash {
		// Windows cmd.exe: /c to execute a command string.
		return shell, []string{"/c", commandString}
	}
	// Unix shells and Git Bash on Windows: -l for login env, -c for command.
	return shell, []string{"-l", "-c", commandString}
}

// --- Docker path resolution ---------------------------------------------

var (
	dockerPathOnce sync.Once
	dockerPath     string
	dockerPathErr  error
)

// ResolveDockerPath returns the absolute path to the `docker` binary. The
// result is cached for the process lifetime so that repeated calls from hot
// paths (health checks, connection diagnostics) do not re-spawn a login shell
// on every invocation.
//
// Resolution order:
//  1. exec.LookPath("docker") — cheap, works when mcpproxy was started from
//     a terminal or when the LaunchAgent PATH already contains docker.
//  2. Fallback: ask the user's login shell `command -v docker` so we pick up
//     Homebrew / Colima / Docker Desktop installs that only exist in the
//     interactive PATH. This fallback is only run once.
func ResolveDockerPath(logger *zap.Logger) (string, error) {
	dockerPathOnce.Do(func() {
		// Fast path: ask Go's standard PATH lookup first.
		if p, err := exec.LookPath("docker"); err == nil && p != "" {
			dockerPath = p
			if logger != nil {
				logger.Debug("shellwrap: resolved docker via PATH", zap.String("path", p))
			}
			return
		}

		// Slow path: shell out once via the user's login shell.
		if runtime.GOOS == osWindows {
			dockerPathErr = fmt.Errorf("docker not found in PATH")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		shell, shellArgs := WrapWithUserShell(logger, "command", []string{"-v", "docker"})
		cmd := exec.CommandContext(ctx, shell, shellArgs...)
		out, err := cmd.Output()
		if err != nil {
			dockerPathErr = fmt.Errorf("login-shell docker lookup failed: %w", err)
			return
		}
		resolved := strings.TrimSpace(string(out))
		if resolved == "" {
			dockerPathErr = fmt.Errorf("docker not found in login shell PATH")
			return
		}
		dockerPath = resolved
		if logger != nil {
			logger.Debug("shellwrap: resolved docker via login shell",
				zap.String("path", resolved))
		}
	})
	return dockerPath, dockerPathErr
}

// resetDockerPathCacheForTest is used by tests to probe the sync.Once
// behaviour. It is intentionally unexported and only referenced from
// shellwrap_test.go.
func resetDockerPathCacheForTest() {
	dockerPathOnce = sync.Once{}
	dockerPath = ""
	dockerPathErr = nil
}

// --- Login-shell PATH capture --------------------------------------------

var (
	loginShellPathOnce sync.Once
	loginShellPathVal  string
)

// LoginShellPATH returns the PATH value emitted by the user's login shell.
// It is captured exactly once per process via
// `<shell> -l -c 'printf %s "$PATH"'` and cached for the rest of the
// process lifetime.
//
// Why this exists: when mcpproxy runs as a macOS App Bundle or LaunchAgent,
// os.Getenv("PATH") is often `/usr/bin:/bin`. That is enough for Go's
// exec.LookPath to find a docker binary once shellwrap.ResolveDockerPath
// has cached its absolute path, but it is NOT enough for the docker CLI
// itself, which re-execs credential helpers like `docker-credential-desktop`
// via its own $PATH lookup. Those helpers typically live in
// /usr/local/bin or /opt/homebrew/bin — directories that only exist in
// the interactive login PATH.
//
// On Windows, this function returns "" (credential-helper PATH drift is
// not the same problem there, and interactive-shell PATH capture would
// require cmd.exe or PowerShell gymnastics we explicitly avoid).
//
// Callers should treat an empty return value as "no override available"
// and fall back to os.Getenv("PATH").
func LoginShellPATH(logger *zap.Logger) string {
	loginShellPathOnce.Do(func() {
		if runtime.GOOS == osWindows {
			return
		}
		shell := resolveLoginShell()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// `-l -c 'printf %s "$PATH"'` works on bash, zsh, dash, sh.
		// We deliberately build the argv ourselves rather than going
		// through WrapWithUserShell because shellescape would quote
		// the `$PATH` and suppress expansion.
		cmd := exec.CommandContext(ctx, shell, "-l", "-c", `printf %s "$PATH"`)
		out, err := cmd.Output()
		if err != nil {
			if logger != nil {
				logger.Debug("shellwrap: login-shell PATH capture failed",
					zap.String("shell", shell),
					zap.Error(err))
			}
			return
		}
		captured := strings.TrimSpace(string(out))
		if captured == "" {
			return
		}
		loginShellPathVal = captured
		if logger != nil {
			logger.Debug("shellwrap: captured login-shell PATH",
				zap.String("shell", shell),
				zap.Int("path_length", len(captured)))
		}
	})
	return loginShellPathVal
}

// mergePathUnique joins two PATH-style strings into one, preserving the
// order of `primary` (highest priority) followed by any entries of
// `secondary` that were not already present. Empty segments are dropped.
func mergePathUnique(primary, secondary, sep string) string {
	if primary == "" {
		return secondary
	}
	if secondary == "" {
		return primary
	}
	seen := make(map[string]struct{}, 16)
	parts := make([]string, 0, 16)
	add := func(list string) {
		for _, p := range strings.Split(list, sep) {
			if p == "" {
				continue
			}
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			parts = append(parts, p)
		}
	}
	add(primary)
	add(secondary)
	return strings.Join(parts, sep)
}

// resetLoginShellPathCacheForTest is only referenced from shellwrap_test.go.
func resetLoginShellPathCacheForTest() {
	loginShellPathOnce = sync.Once{}
	loginShellPathVal = ""
}

// --- Minimal environment for scanner subprocesses ------------------------

// MinimalEnv returns a minimal, allow-listed environment suitable for
// subprocesses that must NOT inherit the user's ambient credentials (e.g.
// AWS_ACCESS_KEY_ID, GITHUB_TOKEN, etc). It includes PATH + HOME on Unix and
// PATH + USERPROFILE on Windows so that `docker` itself still functions.
//
// Callers that need TLS or Docker-specific variables (DOCKER_HOST,
// DOCKER_CONFIG, …) should append them explicitly.
//
// On Unix, PATH is built by merging the user's login-shell PATH
// (captured once via LoginShellPATH) with the process's ambient PATH.
// Login-shell entries come first so that docker's own credential-helper
// lookups can find binaries installed in /opt/homebrew/bin or
// /usr/local/bin even when mcpproxy was started from a LaunchAgent with
// a minimal inherited PATH. See issue #381.
func MinimalEnv() []string {
	return minimalEnvWithLogger(nil)
}

// MinimalEnvWithLogger is MinimalEnv with an optional logger used while
// capturing the login-shell PATH on the first call. Subsequent calls
// return the cached value without logging.
func MinimalEnvWithLogger(logger *zap.Logger) []string {
	return minimalEnvWithLogger(logger)
}

func minimalEnvWithLogger(logger *zap.Logger) []string {
	env := make([]string, 0, 8)
	if path := buildMinimalPath(logger); path != "" {
		env = append(env, "PATH="+path)
	}
	if runtime.GOOS == osWindows {
		if v := os.Getenv("USERPROFILE"); v != "" {
			env = append(env, "USERPROFILE="+v)
		}
		if v := os.Getenv("SystemRoot"); v != "" {
			env = append(env, "SystemRoot="+v)
		}
		if v := os.Getenv("ComSpec"); v != "" {
			env = append(env, "ComSpec="+v)
		}
	} else {
		if v := os.Getenv("HOME"); v != "" {
			env = append(env, "HOME="+v)
		}
	}
	return env
}

// buildMinimalPath returns the PATH value that MinimalEnv should set on
// child processes. On Unix it merges the login-shell PATH with ambient
// PATH so that docker credential helpers (e.g. docker-credential-desktop)
// installed in /opt/homebrew/bin or /usr/local/bin are resolvable — see
// issue #381. On Windows it returns the ambient PATH unchanged.
func buildMinimalPath(logger *zap.Logger) string {
	ambient := os.Getenv("PATH")
	if runtime.GOOS == osWindows {
		return ambient
	}
	login := LoginShellPATH(logger)
	if login == "" {
		return ambient
	}
	return mergePathUnique(login, ambient, string(os.PathListSeparator))
}
