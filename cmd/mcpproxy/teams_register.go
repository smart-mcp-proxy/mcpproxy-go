//go:build teams

package main

// Teams features are registered via init() functions in their respective
// packages. The actual setup happens when the server calls teams.SetupAll()
// during HTTP server initialization (see internal/server/teams_wire.go).
//
// This file imports the teams package for its init() side effects,
// which register feature modules in the teams registry.
import _ "github.com/smart-mcp-proxy/mcpproxy-go/internal/teams"
