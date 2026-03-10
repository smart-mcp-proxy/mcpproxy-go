//go:build server

package main

// Server edition features are registered via init() functions in their
// respective packages. The actual setup happens when the server calls
// teams.SetupAll() during HTTP server initialization (see internal/server/teams_wire.go).
//
// This file imports the teams package for its init() side effects,
// which register feature modules in the server registry.
import _ "github.com/smart-mcp-proxy/mcpproxy-go/internal/teams"
