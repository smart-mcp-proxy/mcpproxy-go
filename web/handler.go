package web

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"go.uber.org/zap"
)

// Handler provides HTTP handlers for the web UI
type Handler struct {
	logger *zap.SugaredLogger
	fs     fs.FS
}

// NewHandler creates a new web UI handler
func NewHandler(logger *zap.SugaredLogger) *Handler {
	return &Handler{
		logger: logger,
		fs:     GetDistFS(),
	}
}

// ServeHTTP handles HTTP requests for the web UI
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Clean the path and remove leading slash
	uiPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/ui")
	if uiPath == "" || uiPath == "/" {
		uiPath = "index.html"
	} else {
		uiPath = strings.TrimPrefix(uiPath, "/")
	}

	// Try to open the file
	file, err := h.fs.Open(uiPath)
	if err != nil {
		// If file not found, serve index.html (SPA routing)
		if uiPath != "index.html" {
			file, err = h.fs.Open("index.html")
			if err != nil {
				h.logger.Error("Failed to open index.html", zap.Error(err))
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
		} else {
			h.logger.Error("Failed to open file", zap.String("path", uiPath), zap.Error(err))
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
	}
	defer file.Close()

	// Get file info for content type detection
	stat, err := file.Stat()
	if err != nil {
		h.logger.Error("Failed to get file stat", zap.String("path", uiPath), zap.Error(err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Set appropriate content type
	contentType := getContentType(uiPath)
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}

	// Set caching headers for static assets
	if strings.Contains(uiPath, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000") // 1 year
	} else {
		w.Header().Set("Cache-Control", "no-cache") // No cache for HTML files
	}

	// Serve the file
	http.ServeContent(w, r, stat.Name(), stat.ModTime(), file.(io.ReadSeeker))
}

// getContentType returns the appropriate content type for a file
func getContentType(filename string) string {
	ext := path.Ext(filename)
	switch ext {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	default:
		return ""
	}
}