package mcplogin

// ProtectedResourceMetadata is the RFC 9728 document for one OAuth resource
// path: the generic address when clientID is empty, else that client's.
func ProtectedResourceMetadata(origin, clientID string) map[string]any {
	return map[string]any{
		"resource":                 origin + ResourcePath(clientID),
		"authorization_servers":    []string{origin},
		"bearer_methods_supported": []string{"header"},
	}
}

// AuthorizationServerMetadata is the RFC 8414 document for this instance.
func AuthorizationServerMetadata(origin string) map[string]any {
	return map[string]any{
		"issuer":                                origin,
		"authorization_endpoint":                origin + "/oauth/authorize",
		"token_endpoint":                        origin + "/oauth/token",
		"registration_endpoint":                 origin + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	}
}

// Challenge is the WWW-Authenticate value that points an unauthenticated
// client at the protected-resource metadata of its address.
func Challenge(origin, clientID string) string {
	return `Bearer resource_metadata="` + origin + "/.well-known/oauth-protected-resource" + ResourcePath(clientID) + `"`
}
