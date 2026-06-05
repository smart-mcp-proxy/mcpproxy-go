//go:build !server

package server

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
)

// wireServerEditionOAuth is a no-op in the personal edition.
func wireServerEditionOAuth(_ *Server, _ *httpapi.Server) {}
