package oauthserver

import (
	"fmt"
	"net/http"
)

// handleProtected returns 401 with WWW-Authenticate header for OAuth detection testing.
func (s *OAuthTestServer) handleProtected(w http.ResponseWriter, r *http.Request) {
	// Build WWW-Authenticate header per RFC 9728
	wwwAuth := fmt.Sprintf(
		`Bearer realm="test", authorization_uri="%s/authorize", resource_metadata="%s/.well-known/oauth-protected-resource"`,
		s.issuerURL,
		s.issuerURL,
	)

	w.Header().Set("WWW-Authenticate", wwwAuth)
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte("Unauthorized"))
}
