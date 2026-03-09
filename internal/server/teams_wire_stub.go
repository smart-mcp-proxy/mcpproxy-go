//go:build !server

package server

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
)

// wireTeamsOAuth is a no-op in the personal edition.
func wireTeamsOAuth(_ *Server, _ *httpapi.Server) {}
