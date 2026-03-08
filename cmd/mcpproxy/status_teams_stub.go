//go:build !teams

package main

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

func collectTeamsInfo(_ *config.Config) *TeamsStatusInfo {
	return nil
}
