//go:build server

package httpapi

import "github.com/go-chi/chi/v5"

// clientRoutesSupported is false in the server edition: there is no per-client
// credential model, so client= parameters answer 404 "client not found".
const clientRoutesSupported = false

// The server edition deliberately has no fleet-wide local client presence API.
func (s *Server) registerClientRoutes(_ chi.Router) {}
