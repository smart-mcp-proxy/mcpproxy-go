package server

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// scannerIsolationModeFor resolves the isolation mode that governs whether the
// security scanner may run DOCKER-BASED scanner plugins against serverName.
//
// This is deliberately NOT the server's process-isolation mode. Scanner plugins
// run in their own short-lived containers over tool definitions already captured
// over MCP; they never wrap, spawn or touch the target server's own process. The
// process-spawn resolver's structural gates ("no local command, so there is
// nothing to wrap"; "the command already invokes docker, so wrapping it would
// break its socket") therefore have no bearing here — routing them into the
// scanner made every remote HTTP/SSE server skip every Docker scanner, and made
// the skip message's own remedy a dead end (GH #1303).
//
// Returns "" when the server is unknown or the config carries no isolation
// block; the scanner service then falls back to the engine-wide default set by
// Service.SetIsolationMode, which is the same global mode.
func scannerIsolationModeFor(liveCfg *config.Config, serverName string) string {
	if liveCfg == nil || liveCfg.DockerIsolation == nil {
		return ""
	}
	for _, candidate := range liveCfg.Servers {
		if candidate != nil && candidate.Name == serverName {
			mode, _ := config.ResolveScannerIsolationMode(liveCfg.DockerIsolation, candidate)
			return string(mode)
		}
	}
	return "" // unknown server → fall back to the engine-wide default
}
