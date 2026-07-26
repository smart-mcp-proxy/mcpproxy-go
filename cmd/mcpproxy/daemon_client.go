package main

// Shared daemon detection for CLI commands (v0.51.0-rc.1 QA finding CLI-SOCKET).
//
// Historically every CLI command detected a running daemon with a bare stat()
// on the Unix-socket path. When the daemon could not bind its socket (macOS
// 104-byte sun_path limit, enable_socket:false) it keeps serving over TCP,
// but the stat-only check reported "daemon not running" and commands silently
// fell back to standalone/config-only mode. daemonEndpoint adds a TCP
// fallback: socket first (unchanged semantics), then the config's listen
// address probed with the config's API key.

import (
	"context"
	"net"
	"os"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"

	"go.uber.org/zap"
)

// daemonProbeTimeout caps the HTTP probe used to verify a TCP endpoint.
// On localhost a dead port fails in <1ms (connection refused), so commands
// stay fast when no daemon is running.
const daemonProbeTimeout = 2 * time.Second

// resolveAPIKey returns the API key the daemon would accept, mirroring
// Config.EnsureAPIKey precedence (env > config file) WITHOUT ever generating
// a new key — a fabricated key cannot match a running daemon's.
func resolveAPIKey(cfg *config.Config) string {
	if envAPIKey, exists := os.LookupEnv("MCPPROXY_API_KEY"); exists && envAPIKey != "" {
		return envAPIKey
	}
	if cfg != nil {
		return cfg.APIKey
	}
	return ""
}

// tcpFallbackEndpoint converts a config listen address into a dialable
// http:// URL. Wildcard hosts ("", "0.0.0.0", "::") are normalized to
// loopback; other hosts come from the user's own config and are kept
// verbatim. ok=false when the address cannot be parsed.
func tcpFallbackEndpoint(listen string) (endpoint string, ok bool) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return "", false
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port), true
}

// probeDaemon verifies a daemon is answering at endpoint by hitting
// GET /api/v1/status with the given API key.
func probeDaemon(endpoint, apiKey string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), daemonProbeTimeout)
	defer cancel()
	client := cliclient.NewClientWithAPIKey(endpoint, apiKey, nil)
	return client.Ping(ctx) == nil
}

// daemonEndpoint resolves how to reach a running daemon.
// Order:
//  1. explicit http(s) MCPPROXY_TRAY_ENDPOINT (probed with the API key);
//  2. unix socket / named pipe — stat-only, preserves existing semantics;
//     socket connections bypass API-key auth so apiKey is "";
//  3. TCP fallback: cfg.Listen + API key (MCPPROXY_API_KEY env overrides
//     cfg.APIKey), verified by a short GET /api/v1/status probe. No key ⇒
//     no TCP attempt (the REST API always requires a key over TCP).
//
// ok=false means no daemon is reachable and the caller should use its
// standalone/config-only path (or error out for daemon-only commands).
func daemonEndpoint(cfg *config.Config) (endpoint, apiKey string, ok bool) {
	if env := os.Getenv("MCPPROXY_TRAY_ENDPOINT"); strings.HasPrefix(env, "http://") || strings.HasPrefix(env, "https://") {
		key := resolveAPIKey(cfg)
		if probeDaemon(env, key) {
			return env, key, true
		}
		return "", "", false
	}

	var dataDir string
	if cfg != nil {
		dataDir = cfg.DataDir
	}
	socketPath := socket.DetectSocketPath(dataDir)
	if socket.IsSocketAvailable(socketPath) {
		return socketPath, "", true
	}

	key := resolveAPIKey(cfg)
	if key == "" || cfg == nil {
		return "", "", false
	}
	tcpEndpoint, valid := tcpFallbackEndpoint(cfg.Listen)
	if !valid || !probeDaemon(tcpEndpoint, key) {
		return "", "", false
	}
	return tcpEndpoint, key, true
}

// newDaemonClient returns a client connected to the running daemon, or
// ok=false when no daemon is reachable (caller falls back to standalone mode).
func newDaemonClient(cfg *config.Config, logger *zap.SugaredLogger) (client *cliclient.Client, ok bool) {
	endpoint, apiKey, ok := daemonEndpoint(cfg)
	if !ok {
		return nil, false
	}
	return cliclient.NewClientWithAPIKey(endpoint, apiKey, logger), true
}
