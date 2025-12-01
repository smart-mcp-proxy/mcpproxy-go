package oas

import (
	"strings"

	swagv1 "github.com/swaggo/swag"
)

// register generated spec with swag v1 so our doc.json handler can read from the v1 registry
func init() {
	// Use a relative server URL so Swagger UI executes against the same origin as the served UI.
	const defaultServer = "localhost:8080/api/v1"
	SwaggerInfo.SwaggerTemplate = strings.Replace(
		SwaggerInfo.SwaggerTemplate,
		defaultServer,
		"/api/v1",
		1,
	)

	// swagger UI handler reads from swag v1 registry; we reuse the v2-generated spec since it implements ReadDoc.
	swagv1.Register(SwaggerInfo.InstanceName(), SwaggerInfo)
}
