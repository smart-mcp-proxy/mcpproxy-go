package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"go.uber.org/zap"
)

// frontendFS embeds the Vite-built web UI from web/frontend/dist/. The
// `all:` prefix is required because a fresh module fetch (e.g.
// `go install …@latest`) contains only the tracked .gitkeep placeholder
// under that directory, and the default //go:embed pattern excludes
// dotfiles. With `all:`, the directive compiles even when no real UI
// has been produced yet; the handler below detects that case and falls
// back to fallbackFS.
//
//go:embed all:frontend/dist
var frontendFS embed.FS

// fallbackFS embeds a small stub UI shown when frontendFS contains only
// the .gitkeep placeholder (no real index.html). This is what bare
// `go build ./cmd/mcpproxy` and `go install …@latest` users see — it
// points them at the documented `make build` flow or release artifacts.
//
//go:embed embedded_fallback
var fallbackFS embed.FS

// NewHandler creates a new HTTP handler for serving the embedded web UI
func NewHandler(logger *zap.SugaredLogger) http.Handler {
	return NewHandlerWithIndexCallback(logger, nil)
}

// NewHandlerWithIndexCallback is NewHandler with an optional hook fired each
// time the UI entrypoint (index document) is served — i.e. a request for the
// root, index.html, or an extensionless SPA route that resolves to the index
// body. Asset requests never fire it, including missing assets that fall back
// to the index body (stale hashed bundles after an upgrade). Spec 080 FR-006
// wires this to the persistent web_ui_opened telemetry counter; it is
// deliberately independent of the X-MCPProxy-Client-header-based
// surface_requests.webui counting. onIndexServe may be nil.
func NewHandlerWithIndexCallback(logger *zap.SugaredLogger, onIndexServe func()) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The /ui prefix is already stripped by http.StripPrefix in server.go
		// So paths come in as: "/" for index, "/assets/file.js" for assets
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}

		// Build full path within embedded FS
		fullPath := "frontend/dist/" + p

		// Try to read the file
		content, err := fs.ReadFile(frontendFS, fullPath)
		if err != nil {
			// File not found in the real UI bundle. Fall back to the
			// real index.html (for SPA client-side routing), and if
			// that's missing too, serve the embedded fallback stub.
			content, err = fs.ReadFile(frontendFS, "frontend/dist/index.html")
			if err != nil {
				content, err = fs.ReadFile(fallbackFS, "embedded_fallback/index.html")
				if err != nil {
					logger.Errorw("Failed to read fallback index.html", "error", err)
					http.Error(w, "Not found", http.StatusNotFound)
					return
				}
				fullPath = "embedded_fallback/index.html"
			} else {
				fullPath = "frontend/dist/index.html"
			}
		}

		// Set content type based on file extension
		ext := path.Ext(fullPath)
		contentType := "text/html"
		switch ext {
		case ".js":
			contentType = "application/javascript"
		case ".css":
			contentType = "text/css"
		case ".png":
			contentType = "image/png"
		case ".jpg", ".jpeg":
			contentType = "image/jpeg"
		case ".svg":
			contentType = "image/svg+xml"
		case ".ico":
			contentType = "image/x-icon"
		}

		// Count UI-entrypoint serves: the request asked for the index (root or
		// index.html) or an extensionless SPA route, and the resolved file is
		// an index document. Requests with a file extension are asset fetches
		// — they never count, even when a missing asset falls back to the
		// index body above.
		if onIndexServe != nil &&
			strings.HasSuffix(fullPath, "index.html") &&
			(p == "index.html" || path.Ext(p) == "") {
			onIndexServe()
		}

		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	})
}
