//go:build !server

package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// clientRoutesSupported is true in the personal edition.
const clientRoutesSupported = true

// registerClientRoutes is personal-edition only. Client presence exposes local
// configuration paths, so it has the same administrator-only boundary as
// configuration reads.
func (s *Server) registerClientRoutes(r chi.Router) {
	adminRead := s.requireAdminReadMiddleware("Admin credentials required to read clients")
	r.With(adminRead).Get("/clients", s.handleGetClients)
	r.With(adminRead).Get("/clients/{client}", s.handleGetClient)
	admin := func(h http.HandlerFunc) http.HandlerFunc { return s.requireServerOp(auth.ServerOpConfigWrite, h) }
	// guarded: BindingGuardDelta (FR-008a) — every write below that can change a
	// binding's reach goes through the clients service, which runs the guard
	// before anything is written.
	r.Post("/clients", admin(s.handleCreateClient))                                     // guarded: ClientsService.Add
	r.Put("/clients/{client}/binding", admin(s.handlePutClientBinding))                 // guarded: ClientsService.SetBinding
	r.Post("/clients/bulk-assign", admin(s.handleBulkAssignClients))                    // guarded: ClientsService.BulkAssign (per client)
	r.Post("/clients/upgrade-admin-key-holders", admin(s.handleUpgradeAdminKeyHolders)) // guarded: whole-request guard + per-mint guard
	// exempt: a rotation, a finalize and a forget never change a binding's reach
	// (they keep it, promote a secret, or remove the credential).
	r.Post("/clients/{client}/rotate", admin(s.handleRotateClient))
	r.Post("/clients/{client}/rotate/finalize", admin(s.handleFinalizeClientRotation))
	r.Delete("/clients/{client}", admin(s.handleForgetClient))
}
