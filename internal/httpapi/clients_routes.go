//go:build !server

package httpapi

import (
	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// registerClientRoutes is personal-edition only. Client presence exposes local
// configuration paths, so it has the same administrator-only boundary as
// configuration reads.
func (s *Server) registerClientRoutes(r chi.Router) {
	adminRead := s.requireAdminReadMiddleware("Admin credentials required to read clients")
	r.With(adminRead).Get("/clients", s.handleGetClients)
	r.With(adminRead).Get("/clients/{client}", s.handleGetClient)
	// guarded: BindingGuardDelta (FR-008a) — every write here goes through the
	// clients service, which runs the guard before anything is written.
	r.Put("/clients/{client}/binding", s.requireServerOp(auth.ServerOpConfigWrite, s.handlePutClientBinding))
}
