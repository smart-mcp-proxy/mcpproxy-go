//go:build teams

package main

import (
	"log"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams"
)

func init() {
	// Initialize all registered teams features.
	// Individual feature packages (auth, workspace, etc.) register
	// themselves via their own init() functions.
	if err := teams.SetupAll(teams.Dependencies{}); err != nil {
		log.Fatalf("failed to initialize teams features: %v", err)
	}
}
