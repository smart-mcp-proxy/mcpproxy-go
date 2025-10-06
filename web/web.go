package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"go.uber.org/zap"
)

//go:embed frontend/dist
var frontendFS embed.FS

// NewHandler creates a new HTTP handler for serving the embedded web UI
func NewHandler(logger *zap.SugaredLogger) http.Handler {
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
			// If file not found, serve index.html (for SPA routing)
			content, err = fs.ReadFile(frontendFS, "frontend/dist/index.html")
			if err != nil {
				logger.Errorw("Failed to read index.html", "error", err)
				http.Error(w, "Not found", http.StatusNotFound)
				return
			}
			fullPath = "frontend/dist/index.html"
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

		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	})
}
