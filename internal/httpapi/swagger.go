package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"go.uber.org/zap"

	swagv1 "github.com/swaggo/swag"
	_ "mcpproxy-go/oas" // Import generated docs
)

// SetupSwaggerHandler returns a handler for Swagger UI
// This is exported so it can be mounted on the main mux
func SetupSwaggerHandler(logger *zap.SugaredLogger) http.Handler {
	logger.Debug("Setting up Swagger UI handler")
	return &swaggerHandler{logger: logger}
}

type swaggerHandler struct {
	logger *zap.SugaredLogger
}

func (h *swaggerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/swagger")
	path = strings.TrimPrefix(path, "/")

	switch path {
	case "", "/":
		fallthrough
	case "index.html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, swaggerHTML)
	case "doc.json":
		doc, err := swagv1.ReadDoc()
		if err != nil {
			h.logger.Errorf("failed to read swagger doc: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		doc, err = h.overrideServers(doc, r)
		if err != nil {
			h.logger.Warnf("failed to inject server URL into swagger doc: %v", err)
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(doc))
	default:
		http.NotFound(w, r)
	}
}

func (h *swaggerHandler) overrideServers(doc string, r *http.Request) (string, error) {
	// Decode to map to avoid depending on the generated struct format.
	var spec map[string]any
	if err := json.Unmarshal([]byte(doc), &spec); err != nil {
		return "", err
	}

	serverURL := h.buildServerURL(r)
	spec["servers"] = []map[string]string{
		{"url": serverURL},
	}

	// Force header-based API key as default while keeping query-based option available.
	components, _ := spec["components"].(map[string]any)
	if components == nil {
		components = make(map[string]any)
	}
	securitySchemes, _ := components["securitySchemes"].(map[string]any)
	if securitySchemes == nil {
		securitySchemes = make(map[string]any)
	}
	securitySchemes["ApiKeyAuth"] = map[string]any{
		"type":        "apiKey",
		"in":          "header",
		"name":        "X-API-Key",
		"description": "API key authentication via X-API-Key header",
	}
	securitySchemes["ApiKeyQuery"] = map[string]any{
		"type":        "apiKey",
		"in":          "query",
		"name":        "apikey",
		"description": "API key authentication via query parameter. Use ?apikey=your-key",
	}
	components["securitySchemes"] = securitySchemes
	spec["components"] = components

	// Default to header auth.
	spec["security"] = []map[string]any{
		{"ApiKeyAuth": []any{}},
	}

	out, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (h *swaggerHandler) buildServerURL(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}

	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}

	trimmedHost := strings.TrimSuffix(host, "/")
	return fmt.Sprintf("%s://%s%s", scheme, trimmedHost, apiBasePath)
}

const apiBasePath = "/api/v1"

const swaggerHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Swagger UI</title>
  <link rel="stylesheet" type="text/css" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css" />
  <link rel="icon" type="image/png" href="https://unpkg.com/swagger-ui-dist@5.17.14/favicon-32x32.png" sizes="32x32" />
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"></script>
  <script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-standalone-preset.js"></script>
  <script>
    window.onload = () => {
      window.ui = SwaggerUIBundle({
        url: "/swagger/doc.json",
        dom_id: '#swagger-ui',
        deepLinking: true,
        presets: [
          SwaggerUIBundle.presets.apis,
          SwaggerUIStandalonePreset
        ],
        layout: "StandaloneLayout"
      });
    };
  </script>
</body>
</html>`
