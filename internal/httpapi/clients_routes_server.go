//go:build server

package httpapi

import "github.com/go-chi/chi/v5"

// The server edition deliberately has no fleet-wide local client presence API.
func (s *Server) registerClientRoutes(_ chi.Router) {}
