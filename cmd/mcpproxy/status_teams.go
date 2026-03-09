//go:build server

package main

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

func collectTeamsInfo(cfg *config.Config) *TeamsStatusInfo {
	if cfg.Teams == nil || !cfg.Teams.Enabled {
		return nil
	}
	info := &TeamsStatusInfo{
		AdminEmails: cfg.Teams.AdminEmails,
	}
	if cfg.Teams.OAuth != nil {
		info.OAuthProvider = cfg.Teams.OAuth.Provider
	}
	return info
}
