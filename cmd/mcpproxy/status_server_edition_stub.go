//go:build !server

package main

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

func collectServerEditionInfo(_ *config.Config) *ServerEditionStatusInfo {
	return nil
}
