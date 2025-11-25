package httpapi

import (
	"net/http"

	"go.uber.org/zap"

	httpSwagger "github.com/swaggo/http-swagger"
	_ "mcpproxy-go/docs" // Import generated docs
)

// SetupSwaggerHandler returns a handler for Swagger UI
// This is exported so it can be mounted on the main mux
func SetupSwaggerHandler(logger *zap.SugaredLogger) http.Handler {
	logger.Debug("Setting up Swagger UI handler")
	return httpSwagger.WrapHandler
}
