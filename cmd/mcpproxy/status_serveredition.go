//go:build server

package main

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

func collectServerEditionInfo(cfg *config.Config) *ServerEditionStatusInfo {
	if cfg.ServerEdition == nil || !cfg.ServerEdition.Enabled {
		return nil
	}
	info := &ServerEditionStatusInfo{
		AdminEmails: cfg.ServerEdition.AdminEmails,
	}
	if cfg.ServerEdition.OAuth != nil {
		info.OAuthProvider = cfg.ServerEdition.OAuth.Provider
	}
	return info
}
