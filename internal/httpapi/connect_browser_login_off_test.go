//go:build !server

package httpapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// With browser login off the REST responses name none of the new fields.
func TestConnectREST_FeatureOffResponsesCarryNoBrowserLoginFields(t *testing.T) {
	srv, _ := browserLoginServer(t, false)
	for _, path := range []string{
		"/api/v1/connect",
		"/api/v1/connect/claude-code",
		"/api/v1/connect/claude-code/preview",
	} {
		w := doConnect(srv, http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, w.Code, path)
		for _, field := range []string{"supports_browser_login", "browser_login_available", `"auth"`, "auth_note"} {
			require.NotContains(t, w.Body.String(), field, path)
		}
	}
}
