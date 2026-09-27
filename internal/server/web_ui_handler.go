package server

import (
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/web"
	"go.uber.org/zap"
)

// newWebUIHandler composes the startup-pinned UI bootstrap from the same
// configuration snapshot used to register the HTTP routes. The edition hint
// is intentionally derived only through the build-tagged config accessor so
// personal builds keep their server_edition carrier opaque.
func newWebUIHandler(cfg *config.Config, logger *zap.SugaredLogger, onIndexServe func()) http.Handler {
	return web.NewHandlerWithOptions(logger, web.HandlerOptions{
		ServerEditionEnabled: config.ServerEditionEnabled(cfg),
		OnIndexServe:         onIndexServe,
	})
}
