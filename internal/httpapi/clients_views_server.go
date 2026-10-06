//go:build server

package httpapi

import (
	"context"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// The server edition has no per-client credential model: every client view
// answers "client not found" (Spec 108-h, H12).

// ClientsListData answers "client not found" in the server edition.
func (s *Server) ClientsListData(context.Context, string, string) (map[string]any, error) {
	return nil, profile.ErrUnknownClient
}

// ClientBindingData answers "client not found" in the server edition.
func (s *Server) ClientBindingData(context.Context, string) (map[string]any, error) {
	return nil, profile.ErrUnknownClient
}
