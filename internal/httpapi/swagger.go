package httpapi

import (
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
	path := strings.TrimPrefix(r.URL.Path, "/swagger/")

	switch path {
	case "", "/":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, swaggerHTML)
	case "doc.json":
		doc, err := swagv1.ReadDoc()
		if err != nil {
			h.logger.Errorf("failed to read swagger doc: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(doc))
	default:
		http.NotFound(w, r)
	}
}

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
