package oas

import swagv1 "github.com/swaggo/swag"

// register generated spec with swag v1 so http-swagger (which depends on v1 path) can serve doc.json
func init() {
	// swagger UI handler reads from swag v1 registry; we reuse the v2-generated spec since it implements ReadDoc.
	swagv1.Register(SwaggerInfo.InstanceName(), SwaggerInfo)
}
