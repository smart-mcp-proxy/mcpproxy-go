package server

import (
	"errors"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// ResolveViewAs implements the optional view-as capability of the REST
// controller (GET /api/v1/tools?client= / ?profile=, Spec 108 FR-032): it
// resolves the subject once and hands back the access-chain evaluator.
func (s *Server) ResolveViewAs(subject profile.AccessSubject) (httpapi.ViewAsEvaluator, error) {
	if s.mcpProxy == nil {
		return nil, errors.New("view-as is unavailable: the MCP proxy is not running")
	}
	return s.mcpProxy.NewAccessEvaluator(subject)
}
