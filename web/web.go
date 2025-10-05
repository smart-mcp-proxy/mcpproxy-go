package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"go.uber.org/zap"
)

//go:embed index.html assets/*
var FS embed.FS

// NewHandler creates a new HTTP handler for serving the embedded web UI
func NewHandler(logger *zap.SugaredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the path
		p := r.URL.Path
		if p == "" || p == "/" {
			p = "index.html"
		}

		// Try to read the file
		content, err := fs.ReadFile(FS, strings.TrimPrefix(p, "/"))
		if err != nil {
			// If file not found, serve index.html (for SPA routing)
			content, err = fs.ReadFile(FS, "index.html")
			if err != nil {
				logger.Errorw("Failed to read index.html", "error", err)
				http.Error(w, "Not found", http.StatusNotFound)
				return
			}
			p = "index.html"
		}

		// Set content type based on file extension
		ext := path.Ext(p)
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
