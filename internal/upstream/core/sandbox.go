package core

import (
	"os"
	"path/filepath"
	"runtime"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/sandbox"
)

// wrapWithSandbox wraps an already-prepared (command, args) — typically the
// login-shell-wrapped stdio command — with the native sandbox re-exec wrapper
// (MCP-34.3). It returns the wrapped command/args plus extra env entries that
// must be appended to the child's environment.
//
// Graceful fallback: when sandboxing is unsupported on this OS, the binary path
// can't be resolved, or encoding fails, it logs a diagnostic and returns the
// inputs unchanged so the server still launches unconfined — a documented
// degrade to "none" rather than a hard failure. On Linux kernels that lack
// Landlock, the wrapper itself still runs but Apply degrades inside the child
// (Spec.BestEffort is set), which is logged from the child's stderr.
func (c *Client) wrapWithSandbox(command string, args []string) (wrappedCommand string, wrappedArgs []string, extraEnv []string) {
	if runtime.GOOS != "linux" {
		c.logger.Warn("sandbox isolation requested but unsupported on this OS; running unconfined (none)",
			zap.String("server", c.config.Name),
			zap.String("os", runtime.GOOS))
		return command, args, nil
	}

	self, err := os.Executable()
	if err != nil {
		c.logger.Warn("sandbox isolation requested but executable path unresolved; running unconfined (none)",
			zap.String("server", c.config.Name),
			zap.Error(err))
		return command, args, nil
	}

	spec := buildSandboxSpec(c.config)
	wrappedCommand, wrappedArgs, extraEnv, err = sandbox.WrapCommand(self, spec, command, args)
	if err != nil {
		c.logger.Warn("sandbox wrap failed; running unconfined (none)",
			zap.String("server", c.config.Name),
			zap.Error(err))
		return command, args, nil
	}

	if !sandbox.Available() {
		// Wrapper still runs (Spec.BestEffort downgrades inside the child), but
		// be honest in the host log that confinement won't actually take effect.
		c.logger.Warn("sandbox isolation requested but kernel lacks Landlock; server will run DEGRADED/unconfined",
			zap.String("server", c.config.Name))
	} else {
		c.logger.Info("sandbox isolation enabled for server (Landlock + rlimits)",
			zap.String("server", c.config.Name),
			zap.Strings("read_write", spec.ReadWritePaths))
	}
	return wrappedCommand, wrappedArgs, extraEnv
}

// buildSandboxSpec derives the default confinement for a server.
//
// It implements a filesystem WRITE allowlist, which is what MCP-34.3 scopes:
// reads stay broad (read-only "/") so package-manager runtimes can load
// interpreters, node_modules, and site-packages from anywhere on the host, while
// WRITES are denied outside a small allowlist — the server's working directory,
// the OS temp dir, and the common package caches the runtimes need. Tightening
// reads is deliberately deferred: a read allowlist breaks tool discovery and
// belongs to a future per-server explicit allowlist.
//
// BestEffort is set so a kernel without Landlock degrades to unconfined-with-
// diagnostic instead of failing the connection outright.
func buildSandboxSpec(cfg *config.ServerConfig) sandbox.Spec {
	rw := []string{os.TempDir()}
	if cfg != nil && cfg.WorkingDir != "" {
		rw = append(rw, cfg.WorkingDir)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		// Caches the npx/uvx/pip runtimes write to during a cold start. Paths
		// that don't exist are skipped best-effort by Apply.
		rw = append(rw,
			filepath.Join(home, ".npm"),
			filepath.Join(home, ".cache"),
			filepath.Join(home, ".local", "share"),
		)
	}
	return sandbox.Spec{
		ReadOnlyPaths:  []string{"/"},
		ReadWritePaths: rw,
		Rlimits:        defaultSandboxRlimits(),
		BestEffort:     true,
	}
}
