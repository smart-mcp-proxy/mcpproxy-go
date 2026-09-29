//go:build !server

package httpapi

import "github.com/go-chi/chi/v5"

// registerClientRoutes is personal-edition only. Client presence exposes local
// configuration paths, so it has the same administrator-only boundary as
// configuration reads.
func (s *Server) registerClientRoutes(r chi.Router) {
	adminRead := s.requireAdminReadMiddleware("Admin credentials required to read clients")
	r.With(adminRead).Get("/clients", s.handleGetClients)
	r.With(adminRead).Get("/clients/{client}", s.handleGetClient)
}
