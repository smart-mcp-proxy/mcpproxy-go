//go:build !server

package httpapi

func editionGetRouteScopes() map[string]routeScope {
	return map[string]routeScope{
		"/api/v1/clients":          {scopeRefused, "MCP client/session presence; TestClientsPresence_AdministratorOnly"},
		"/api/v1/clients/{client}": {scopeRefused, "MCP client/session presence detail; TestClientsPresence_AdministratorOnly"},
	}
}

func editionRouteDenialMarkers() map[string]string {
	return map[string]string{
		"/api/v1/clients":          "Admin credentials required to read clients",
		"/api/v1/clients/{client}": "Admin credentials required to read clients",
	}
}

func editionAbsentGetRoutes() map[string]string { return nil }
