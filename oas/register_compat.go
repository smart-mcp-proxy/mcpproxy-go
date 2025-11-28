package oas

import swagv1 "github.com/swaggo/swag"

// register generated spec with swag v1 so our doc.json handler can read from the v1 registry
func init() {
	// swagger UI handler reads from swag v1 registry; we reuse the v2-generated spec since it implements ReadDoc.
	swagv1.Register(SwaggerInfo.InstanceName(), SwaggerInfo)
}
