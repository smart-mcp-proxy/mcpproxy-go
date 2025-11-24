package httpapi

import (
	"net/http"

	httpSwagger "github.com/swaggo/http-swagger"
)

// setupSwaggerRoutes mounts the Swagger UI handler at /swagger/
func (s *Server) setupSwaggerRoutes() http.Handler {
	// The httpSwagger.WrapHandler will serve the Swagger UI
	// It expects the docs to be generated in docs/docs.go by swag init
	return httpSwagger.WrapHandler
}
