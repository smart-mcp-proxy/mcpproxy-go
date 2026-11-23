//go:build server

package httpapi

func editionGetRouteScopes() map[string]routeScope { return nil }

func editionRouteDenialMarkers() map[string]string { return nil }

func editionAbsentGetRoutes() map[string]string {
	return map[string]string{
		"/api/v1/clients":                   "server edition does not expose fleet-wide local client presence",
		"/api/v1/clients/{client}":          "server edition does not expose fleet-wide local client presence",
		"/api/v1/oauth/authorizations":      "server edition does not serve browser login",
		"/api/v1/oauth/authorizations/{id}": "server edition does not serve browser login",
	}
}
